package camera

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

// 以下布局来自 2026-10-27 Mini 5 Pro 成功快传抓包，保留各阶段独立的双向确认。
func aircraftOpenRequest() []byte { return hx("05ff040000000201010000") }

// #12369 等 ACK 的控制窗口为“对端已确认 → 本机已发送”，不是冻结在握手基值。
func aircraftAckPayload(video, download, controlAck, lastSent uint16) []byte {
	p := make([]byte, 26)
	for i, v := range []uint16{video, download} {
		le.PutUint16(p[i*8:], v)
		le.PutUint16(p[i*8+2:], v)
	}
	le.PutUint16(p[16:], controlAck)
	le.PutUint16(p[18:], lastSent)
	return p
}

func aircraftAppIdentity(name string) []byte { return append([]byte{4, 2, 0}, []byte(name)...) }

func aircraftEndpointAnnouncement(name string) []byte {
	p := []byte{1, 0x2b, 0x23, 0}
	p = append(p, []byte(name)...)
	return append(p, 0)
}

func aircraftAbilityAnnouncement(name string, active bool) []byte {
	p := make([]byte, 38)
	copy(p, []byte{0, 4, 2, 0, 0})
	copy(p[5:24], name)
	if active {
		p[31] = 0x10
	}
	return p
}

// #12358/#12359：WLM 封装前先公告 UDP 端点和本机身份；不复用手机身份。
func (l *AircraftLink) announceAircraftEndpoint() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range []Message{
		{Sender: 2, Receiver: 0x0e, Flags: 0x40, Set: 0, ID: 1},
		{Sender: 2, Receiver: 0x1f, Flags: 0x40, Set: 0, ID: 1},
		{Sender: 2, Receiver: 0xa2, Flags: 0x40, Set: 0, ID: 1},
		{Sender: 2, Receiver: 0x00, Flags: 0x40, Set: 0, ID: 1},
		{Sender: 2, Receiver: 7, Flags: 0x40, Set: 7, ID: 0x93, Payload: aircraftEndpointAnnouncement(l.appName)},
		{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 0x34, Payload: aircraftAbilityAnnouncement(l.appName, false)},
		{Sender: 2, Receiver: 7, Flags: 0x40, Set: 0x12, ID: 0x0c},
	} {
		l.messageSeq++
		m.Seq = l.messageSeq
		if err := l.writeLocked(m); err != nil {
			return err
		}
	}
	l.trace.Log("info", "datalink", "aircraft.endpoint_announced", "已发送无人机 UDP 端点与客户端身份公告", nil)
	return nil
}

func aircraftIdentityRequest(app, peer []byte) []byte {
	p := append(append([]byte{}, app...), 0)
	p = append(p, peer...)
	return append(p, 0, 0, 0, 0, 0)
}

func aircraftCapabilityOffer(app [4]byte) []byte {
	p := hx("10010000000000260006112000000000000000001f000100000000000a112000000000000000007d07000000000000")
	copy(p[3:7], app[:])
	return p
}

// 0x51/0x41 请求有 basic/full 两种，尾部地址标志均为 0x82，包括单播回复。
func aircraftCapabilityReply(payload []byte, app, peer [4]byte) ([]byte, uint8) {
	if len(payload) < 9 || !bytes.Equal(payload[3:7], peer[:]) || int(le.Uint16(payload[7:])) != len(payload)-9 {
		return nil, 0
	}
	switch {
	case payload[0] == 0x20 && le.Uint16(payload[1:3]) == 1:
		// 版本字段来自请求；变化的事务序号是 DUML 头部 Seq，回包另行原样回显。
		p := append([]byte{0, 0x20, 0, payload[1], payload[2]}, aircraftCapabilityOffer(app)[3:]...)
		return p, 1
	case payload[0] == 0 && le.Uint16(payload[1:3]) == 2:
		p := hx("0000000200000000000a000b0006000a0000000000")
		copy(p[3:5], payload[1:3])
		copy(p[5:9], app[:])
		return p, 2
	}
	return nil, 0
}

func (l *AircraftLink) notifyState() {
	select {
	case l.stateChanged <- struct{}{}:
	default:
	}
}

func (l *AircraftLink) waitState(ctx context.Context, duration time.Duration, ready func() bool) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		l.mu.Lock()
		ok := ready()
		l.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.done:
			return errors.New("无人机链路已断开")
		case <-timer.C:
			return context.DeadlineExceeded
		case <-l.stateChanged:
		}
	}
}

func (l *AircraftLink) negotiateCapabilities(ctx context.Context) error {
	// 普通重连抓包中，设备先发能力请求，客户端先回复，主动通告在身份确认后才出现。
	// 首次发现未收到请求时再主动通告，兼容另一份抓包中的发现流程。
	waitErr := l.waitState(ctx, 500*time.Millisecond, func() bool { return l.capabilities != 0 })
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		return waitErr
	}
	if waitErr != nil {
		l.mu.Lock()
		l.capabilityActive = true
		appAddress := l.app
		l.mu.Unlock()
		if err := l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 0x41, Payload: aircraftCapabilityOffer(appAddress)}, true); err != nil {
			return err
		}
	}
	// 抓包只证明首次安装时两种能力请求先于开启，不能把它们当作每次重连的门槛。
	// 能力请求由接收循环持续应答；会话是否真正建立仍须通过挑战和双向身份确认。
	err := l.waitState(ctx, 3*time.Second, func() bool { return l.capabilities == 3 })
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	l.mu.Lock()
	fields := map[string]string{"received": fmt.Sprint(l.wlmSeen[0x41]), "answered_mask": fmt.Sprint(l.capabilities), "other_destination": fmt.Sprint(l.wlmOtherDestination[0x41]), "udp_packets": fmt.Sprint(l.receivedPackets), "control_ack": fmt.Sprint(l.controlAck), "last_sent": fmt.Sprint(l.seq - 8)}
	l.mu.Unlock()
	if err != nil {
		l.trace.Log("info", "datalink", "aircraft.capabilities_pending", "继续进行快传身份确认；后台仍应答能力请求", fields)
		return nil
	}
	l.trace.Log("info", "datalink", "aircraft.capabilities_answered", "已应答两类能力请求，继续确认快传身份", fields)
	return nil
}

func (l *AircraftLink) confirmAircraftIdentity(ctx context.Context, challenge Message) error {
	app := aircraftAppIdentity(l.appName)
	// #12824：设备身份23字节、客户端身份22字节、状态4字节。
	if len(challenge.Payload) != 49 || !bytes.Equal(challenge.Payload[23:45], app) {
		return errors.New("无人机会话挑战未匹配本机身份")
	}
	peer := append([]byte{}, challenge.Payload[:23]...)
	serial, _ := aircraftSerial(peer)
	if len(serial) != 20 || !bytes.Equal(serial, peer[3:]) {
		return errors.New("无人机挑战的设备身份格式不兼容")
	}
	echo := append(append([]byte{}, peer...), 0)
	if err := l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Seq: challenge.Seq, Flags: 0xc0, Set: 0x51, ID: 8, Payload: echo}, false); err != nil {
		return err
	}
	const identitySeq = 0x7d
	if err := l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Seq: identitySeq, Flags: 0x40, Set: 0x51, ID: 6, Payload: aircraftIdentityRequest(app, peer)}, false); err != nil {
		return err
	}
	l.mu.Lock()
	l.capabilityActive = true
	l.mu.Unlock()
	wait, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	acked, answered := false, false
	for !acked || !answered {
		m, err := l.waitMessage(wait, 2*time.Second, func(m Message) bool { return m.Set == 0x51 && m.ID == 6 })
		if err != nil {
			return errors.New("无人机快传会话尚未完成双向身份确认")
		}
		if m.Flags&0xc0 == 0xc0 && m.Seq == identitySeq {
			if len(m.Payload) != 24 || !bytes.Equal(m.Payload[:22], app) || !bytes.Equal(m.Payload[22:], []byte{0, 0}) {
				return errors.New("无人机未确认本机快传身份")
			}
			acked = true
		} else if m.Flags&0xc0 == 0x40 {
			if len(m.Payload) != 51 || !bytes.Equal(m.Payload[:23], peer) || !bytes.Equal(m.Payload[23:45], app) {
				return errors.New("无人机双向确认的身份不匹配")
			}
			if err := l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Seq: m.Seq, Flags: 0xc0, Set: 0x51, ID: 6, Payload: echo}, false); err != nil {
				return err
			}
			answered = true
		}
	}
	l.mu.Lock()
	l.sessionReady = true
	l.mu.Unlock()
	// #12843：仅通告本客户端的下载能力，不重放官方客户端的飞控初始化序列。
	ability := aircraftAbilityAnnouncement(l.appName, true)
	if err := l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 0x34, Payload: ability}, true); err != nil {
		return err
	}
	return l.sendBeacon()
}

func (l *AircraftLink) enterAircraftPlayback(ctx context.Context) error {
	l.mu.Lock()
	version := l.playbackVersion
	l.mu.Unlock()
	if err := l.sendTunnel(Message{Sender: 2, Receiver: 1, Flags: 0x40, Set: 2, ID: 0x0c, Payload: []byte{1, 1, 0, 1}}, true); err != nil {
		return err
	}
	ack, err := l.waitMessage(ctx, 2*time.Second, func(m Message) bool { return m.Set == 2 && m.ID == 0x0c && m.Flags&0x80 != 0 })
	if err != nil || len(ack.Payload) != 1 || ack.Payload[0] != 0 {
		return errors.New("无人机尚未确认进入素材回放模式")
	}
	if err = l.waitState(ctx, 2*time.Second, func() bool { return l.playback && l.playbackVersion > version }); err != nil {
		return errors.New("无人机回放状态尚未就绪")
	}
	l.trace.Log("info", "datalink", "aircraft.playback_ready", "无人机已确认素材回放模式", nil)
	return nil
}
