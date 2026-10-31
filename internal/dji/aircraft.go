package camera

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"cardatlas/internal/diagnostics"
)

// AircraftLink 使用飞行器的 UDP 9003 / WLM 会话，不发送 Osmo 的唤醒和回放命令。
// 协议依据及未验证部分见 docs/device-import.md，不能仅凭 UDP 握手宣称素材连接成功。
type AircraftLink struct {
	conn                                     net.Conn
	mu, queryMu                              sync.Mutex
	closeOnce                                sync.Once
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	done                                     chan struct{}
	inbox                                    chan Message
	trace                                    diagnostics.Trace
	session, base, seq, messageSeq, querySeq uint16
	count                                    byte
	tailSeq, beaconSeq                       uint32
	app, peer                                [4]byte
	peerKnown                                bool
	appName                                  string
	started, lastSeen                        time.Time
	cursors                                  [2]uint16
	receivedPackets                          uint32
	wlmSeen                                  [256]uint32
	wlmOtherDestination                      [256]uint32
	stateChanged                             chan struct{}
	capabilities                             uint8
	sessionReady, playback                   bool
	playbackVersion                          uint64
	capabilityActive                         bool
	controlAck                               uint16
	beaconAnnounced                          bool
	capabilityShapes                         map[string]bool
}

// OpenAircraftLink 的 dialer 由适配器绑定实际 Wi-Fi 网卡和本地 9003 端口。
func OpenAircraftLink(ctx context.Context, host, identity string, dialer *net.Dialer) (*AircraftLink, error) {
	return openAircraftLink(ctx, net.JoinHostPort(host, "9003"), identity, dialer)
}

func openAircraftLink(ctx context.Context, address, identity string, dialer *net.Dialer) (*AircraftLink, error) {
	if len(identity) != 32 || dialer == nil {
		return nil, errors.New("无人机配对身份或 Wi-Fi 接口无效")
	}
	ctx, stop := context.WithTimeout(ctx, 14*time.Second)
	defer stop()
	l, err := openAircraftTransportWithRecovery(ctx, address, identity, dialer.DialContext)
	if err != nil {
		return nil, err
	}
	if err = l.announceAircraftEndpoint(); err != nil {
		l.cancel()
		l.conn.Close()
		return nil, err
	}
	go l.run()
	if err = l.establish(ctx); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

type aircraftDial func(context.Context, string, string) (net.Conn, error)

// macOS 更新本地网络策略时可能使第一个 UDP socket 永久 EPIPE。
// 等待策略刷新后只重建一次，仍由系统检查权限；不修改权限、不降级接口绑定。
func openAircraftTransportWithRecovery(ctx context.Context, address, identity string, dial aircraftDial) (*AircraftLink, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		link, err := openAircraftTransport(ctx, address, identity, dial)
		if err == nil {
			return link, nil
		}
		if !errors.Is(err, syscall.EPIPE) {
			return nil, err
		}
		if attempt == 1 {
			return nil, fmt.Errorf("macOS 本地网络通道被重置（broken pipe），重建连接后仍无法发送。请检查“系统设置 → 隐私与安全性 → 本地网络”中的 CardAtlas；若已允许，请退出并重新打开客户端后重试：%w", err)
		}
		diagnostics.From(ctx).Log("warn", "datalink", "aircraft.transport_reset", "本机 UDP 通道被系统重置，等待后重新建立一次连接", map[string]string{"attempt": "1", "reason": "EPIPE", "port": "9003"})
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("无人机网络通道未建立")
}

func openAircraftTransport(ctx context.Context, address, identity string, dial aircraftDial) (*AircraftLink, error) {
	c, err := dial(ctx, "udp4", address)
	if err != nil {
		return nil, fmt.Errorf("无法绑定无人机 Wi-Fi 素材端口：%w", err)
	}
	var seed [4]byte
	if _, err = rand.Read(seed[:]); err != nil {
		c.Close()
		return nil, err
	}
	l := &AircraftLink{conn: c, session: le.Uint16(seed[:2]), base: le.Uint16(seed[2:4]) & 0xfff8,
		messageSeq: 0xa000, querySeq: 0x0c, count: 1, done: make(chan struct{}), inbox: make(chan Message, 128), stateChanged: make(chan struct{}, 1),
		trace: diagnostics.From(ctx), started: time.Now(), lastSeen: time.Now()}
	l.appName, l.app = aircraftLocalIdentity(identity)
	l.seq = l.base + 8
	l.cursors = [2]uint16{l.base, l.base}
	l.controlAck = l.base
	l.ctx, l.cancel = context.WithCancel(context.Background())
	l.trace.Log("info", "datalink", "aircraft.open", "开始建立无人机快传链路", map[string]string{"port": "9003", "local_address": c.LocalAddr().String()})
	body := hx("000064006400c005140000640000019001c005140000640014006400c00514000064000101040102")
	le.PutUint16(body, l.base)
	buf := make([]byte, 65535)
	ready := false
	for attempt := 0; attempt < 6 && ctx.Err() == nil; attempt++ {
		_ = c.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err = c.Write(Packet(0, l.session, 0, body)); err != nil {
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(350 * time.Millisecond))
		for {
			var n int
			n, err = c.Read(buf)
			if err != nil {
				break
			}
			if validPacket(buf[:n], l.session) && buf[6] == 0 {
				ready = true
				break
			}
		}
		if ready {
			break
		}
		if e, ok := err.(net.Error); err != nil && (!ok || !e.Timeout()) {
			break
		}
	}
	if !ready {
		l.cancel()
		c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("无人机 UDP 9003 未完成握手，请检查热点连接和本地网络权限：%w", err)
	}
	l.trace.Log("info", "datalink", "aircraft.handshake_ready", "无人机网络握手成功，正在确认快传会话", nil)
	return l, nil
}

// 同一个已授权客户端在重连时保持同一个 WLM 身份和地址。
// 使用配对标识本身的前缀，不插入此前不存在的字符，也不借用手机的地址。
func aircraftLocalIdentity(identity string) (string, [4]byte) {
	h := sha256.Sum256([]byte("CardAtlas/WLM/" + identity))
	var address [4]byte
	copy(address[:], h[:4])
	if address == [4]byte{} || address == [4]byte{255, 255, 255, 255} {
		address[0] ^= 0x80
	}
	return identity[:19], address
}

func (l *AircraftLink) writeLocked(m Message) error {
	body := make([]byte, 12)
	le.PutUint16(body, l.controlAck)
	le.PutUint16(body[2:], l.seq)
	body[8], body[9] = l.count, 1
	_ = l.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := l.conn.Write(Packet(5, l.session, l.seq, append(body, m.Encode()...)))
	if err == nil {
		l.seq += 8
		l.count++
	}
	return err
}

// WLM 尾部使用本机稳定地址、设备广播地址和会话请求序号；身份广播不消耗请求序号。
func (l *AircraftLink) sendTunnel(inner Message, broadcast bool) error {
	flag := byte(0xe2)
	if broadcast {
		flag = 0x82
	}
	return l.sendTunnelWithFlag(inner, broadcast, flag)
}

func (l *AircraftLink) sendTunnelWithFlag(inner Message, broadcast bool, flag byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !broadcast && !l.peerKnown {
		return errors.New("尚未收到无人机的会话地址")
	}
	if l.tailSeq == 0 {
		l.tailSeq = 1
	}
	sequence := l.tailSeq
	if inner.Seq == 0 && inner.Flags&0x80 == 0 {
		l.messageSeq++
		inner.Seq = l.messageSeq
	}
	tail := make([]byte, 22)
	copy(tail, l.app[:])
	tail[4] = 2
	le.PutUint32(tail[5:], sequence)
	copy(tail[9:], l.peer[:])
	tail[13], tail[14] = 1, flag
	if broadcast {
		copy(tail[9:], []byte{255, 255, 255, 255})
	}
	err := l.writeLocked(Message{Sender: 0x3b, Receiver: 0xe9, Seq: uint16(sequence), Set: 0x51, ID: 1, Payload: append(inner.Encode(), tail...)})
	// #12367/#12392/#12404 的身份广播都使用1，#12421的首条请求仍使用1。
	// 广播只宣告下一条请求的序号，不能消耗它，否则接收端的可靠窗口会出现空洞。
	if err == nil && !(inner.Set == 0x51 && inner.ID == 0x13 && inner.Flags == 0) {
		l.tailSeq++
	}
	return err
}

func (l *AircraftLink) sendBeacon() error {
	l.mu.Lock()
	l.beaconSeq++
	p := make([]byte, 55)
	copy(p, []byte{5, 4, 2, 0})
	copy(p[24:27], []byte{1, 1, 1})
	p[33], p[36], p[37], p[38] = 1, 1, 1, 1
	// 成功抓包的兼容性字段；不能在未确定语义前把非零值直接抹为零。
	copy(p[49:53], hx("2f475400"))
	if l.sessionReady {
		copy(p[24:27], []byte{5, 1, 4})
		p[53] = 1
	}
	copy(p[4:23], l.appName)
	le.PutUint32(p[39:], l.beaconSeq)
	le.PutUint32(p[43:], uint32(time.Since(l.started).Milliseconds()))
	l.mu.Unlock()
	return l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Seq: 0x7c, Set: 0x51, ID: 0x13, Payload: p}, true)
}

func aircraftSerial(p []byte) ([]byte, byte) {
	var best []byte
	var tag byte
	char := func(b byte) bool { return b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }
	for i := 0; i < len(p); {
		if !char(p[i]) {
			i++
			continue
		}
		end := i
		for end < len(p) && char(p[end]) {
			end++
		}
		if i > 0 && end-i >= 12 && end-i <= 24 && end-i > len(best) {
			best = append([]byte(nil), p[i:end]...)
			tag = p[i-1]
		}
		i = end
	}
	return best, tag
}

func (l *AircraftLink) waitMessage(ctx context.Context, duration time.Duration, accept func(Message) bool) (Message, error) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case <-l.done:
			return Message{}, errors.New("无人机快传链路已断开")
		case <-timer.C:
			return Message{}, context.DeadlineExceeded
		case m := <-l.inbox:
			if accept(m) {
				return m, nil
			}
		}
	}
}

func (l *AircraftLink) establish(ctx context.Context) error {
	_, err := l.waitMessage(ctx, 5*time.Second, func(m Message) bool { return m.Set == 0x51 && m.ID == 0x13 })
	if err != nil {
		return fmt.Errorf("Wi-Fi 已连通，但未收到无人机快传身份广播：%w", err)
	}
	if err = l.negotiateCapabilities(ctx); err != nil {
		return err
	}
	if err = l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 2, Payload: aircraftOpenRequest()}, false); err != nil {
		return err
	}
	challenge, err := l.waitMessage(ctx, 2*time.Second, func(m Message) bool { return m.Set == 0x51 && m.ID == 8 && m.Flags&0xc0 == 0x40 })
	if err != nil {
		l.mu.Lock()
		fields := map[string]string{"udp_packets": fmt.Sprint(l.receivedPackets), "wlm_beacons": fmt.Sprint(l.wlmSeen[0x13]), "wlm_challenges": fmt.Sprint(l.wlmSeen[8]), "wlm_confirmations": fmt.Sprint(l.wlmSeen[6]), "peer_address_known": fmt.Sprint(l.peerKnown), "capability_other_destination": fmt.Sprint(l.wlmOtherDestination[0x41])}
		l.mu.Unlock()
		l.trace.Log("warn", "datalink", "aircraft.session_unanswered", "Wi-Fi 和 UDP 握手正常，无人机未确认素材会话", fields)
		return errors.New("Wi-Fi 已连通，UDP 9003 握手成功，但 Mini 5 Pro 未接受快传素材会话；当前固件协议尚未验证，请勿反复长时间重连。已停止本次连接的保活")
	}
	if err = l.confirmAircraftIdentity(ctx, challenge); err != nil {
		return err
	}
	if err = l.enterAircraftPlayback(ctx); err != nil {
		return err
	}
	l.trace.Log("info", "datalink", "aircraft.session_ready", "无人机已确认快传会话，等待验证素材清单", nil)
	return nil
}

func (l *AircraftLink) accept(m Message) {
	if m.Set == 0x51 && m.ID == 1 {
		if len(m.Payload) < 13 {
			return
		}
		n := int(le.Uint16(m.Payload[1:]) & 0x3ff)
		if n < 13 || n+22 > len(m.Payload) || m.Payload[n+4] != 2 {
			return
		}
		var reader Reader
		inner := reader.Feed(m.Payload[:n])
		if len(inner) != 1 {
			return
		}
		tail := m.Payload[n:]
		l.mu.Lock()
		if inner[0].Set == 0x51 {
			l.wlmSeen[inner[0].ID]++
		}
		valid := bytes.Equal(tail[9:13], l.app[:]) || bytes.Equal(tail[9:13], []byte{255, 255, 255, 255})
		// 其他端点的报文不能用于本机会话，但需要保留计数以区分“未收到”和“目的地址不匹配”。
		// 每个命令只记一次概要，不写设备地址、身份或原始载荷，也不根据这些报文自动改绑。
		firstOtherDestination := false
		if !valid && inner[0].Set == 0x51 {
			l.wlmOtherDestination[inner[0].ID]++
			firstOtherDestination = l.wlmOtherDestination[inner[0].ID] == 1
		}
		if valid && !l.peerKnown && inner[0].Set == 0x51 && inner[0].ID == 0x13 {
			copy(l.peer[:], tail[:4])
			l.peerKnown = true
		}
		valid = valid && l.peerKnown && bytes.Equal(tail[:4], l.peer[:])
		l.mu.Unlock()
		if firstOtherDestination {
			l.trace.Log("info", "datalink", "aircraft.other_destination", "收到发往其他会话地址的无人机消息，已忽略", map[string]string{
				"command": fmt.Sprintf("51/%02x", inner[0].ID), "content_bytes": fmt.Sprint(len(inner[0].Payload)), "duml_sequence": fmt.Sprint(inner[0].Seq),
			})
		}
		if !valid {
			return
		}
		m = inner[0]
	}
	if m.Set == 0x51 && m.ID == 0x13 {
		l.mu.Lock()
		if !l.peerKnown {
			l.mu.Unlock()
			return
		}
		already := l.beaconAnnounced
		l.beaconAnnounced = true
		l.mu.Unlock()
		if already {
			return
		}
	}
	if m.Set == 0x51 && m.ID == 0x41 && m.Flags&0xc0 == 0x40 {
		l.mu.Lock()
		reply, bit := aircraftCapabilityReply(m.Payload, l.app, l.peer)
		shape := "short"
		if len(m.Payload) >= 3 {
			shape = fmt.Sprintf("%02x/%04x/%d", m.Payload[0], le.Uint16(m.Payload[1:]), len(m.Payload))
		}
		if l.capabilityShapes == nil {
			l.capabilityShapes = map[string]bool{}
		}
		fresh := !l.capabilityShapes[shape] && len(l.capabilityShapes) < 16
		if fresh {
			l.capabilityShapes[shape] = true
		}
		l.mu.Unlock()
		if fresh {
			l.trace.Log("info", "datalink", "aircraft.capability_request", "收到无人机能力请求", map[string]string{"request_shape": shape, "reply_kind": fmt.Sprint(bit)})
		}
		if reply != nil && l.sendTunnelWithFlag(Message{Sender: 0xee, Receiver: m.Sender, Seq: m.Seq, Flags: 0xc0, Set: 0x51, ID: 0x41, Payload: reply}, false, 0x82) == nil {
			l.mu.Lock()
			l.capabilities |= bit
			l.mu.Unlock()
			l.notifyState()
		}
		return
	}
	if m.Set == 2 && m.ID == 0x80 && len(m.Payload) >= 4 {
		l.mu.Lock()
		l.playback = le.Uint32(m.Payload)&0x40000000 != 0
		l.playbackVersion++
		l.mu.Unlock()
		l.notifyState()
		return
	}
	if m.Set == 0 && m.ID == 0x27 || m.Set == 2 && m.ID == 0x0c || m.Set == 0x51 && (m.ID == 0x13 || m.ID == 8 || m.ID == 6) {
		select {
		case l.inbox <- m:
		default:
		}
	}
}

func (l *AircraftLink) run() {
	defer close(l.done)
	defer l.cancel()
	defer l.conn.Close()
	buf := make([]byte, 65535)
	var stream Reader
	var ackAt, beaconAt, capabilityAt time.Time
	seen := map[uint16]bool{}
	var recent []uint16
	for l.ctx.Err() == nil {
		now := time.Now()
		l.mu.Lock()
		stale := now.Sub(l.lastSeen) > 8*time.Second
		l.mu.Unlock()
		if stale {
			return
		}
		if now.Sub(ackAt) >= 100*time.Millisecond {
			l.mu.Lock()
			body := aircraftAckPayload(l.cursors[0], l.cursors[1], l.controlAck, l.seq-8)
			_ = l.conn.SetWriteDeadline(now.Add(time.Second))
			_, err := l.conn.Write(Packet(4, l.session, 0, body))
			l.mu.Unlock()
			if err != nil {
				return
			}
			ackAt = now
		}
		if now.Sub(beaconAt) >= 200*time.Millisecond {
			if l.sendBeacon() != nil {
				return
			}
			beaconAt = now
		}
		l.mu.Lock()
		capabilityActive := l.capabilityActive
		appAddress := l.app
		l.mu.Unlock()
		if capabilityActive && now.Sub(capabilityAt) >= 500*time.Millisecond {
			if l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 0x41, Payload: aircraftCapabilityOffer(appAddress)}, true) != nil {
				return
			}
			capabilityAt = now
		}
		_ = l.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, err := l.conn.Read(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return
		}
		b := buf[:n]
		if !validPacket(b, l.session) {
			continue
		}
		l.mu.Lock()
		l.lastSeen = time.Now()
		l.receivedPackets++
		if b[6] == 1 && n >= 34 {
			l.cursors = [2]uint16{le.Uint16(b[10:]), le.Uint16(b[18:])}
			l.controlAck = le.Uint16(b[26:])
		}
		l.mu.Unlock()
		var messages []Message
		if b[6] == 3 && n > 20 {
			seq := le.Uint16(b[4:])
			if seen[seq] {
				continue
			}
			seen[seq] = true
			recent = append(recent, seq)
			if len(recent) > 128 {
				delete(seen, recent[0])
				recent = recent[1:]
			}
			messages = stream.Feed(b[20:])
		} else {
			var r Reader
			messages = r.Feed(b[8:])
		}
		for _, m := range messages {
			l.accept(m)
		}
	}
}

func (l *AircraftLink) Alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ctx.Err() == nil && time.Since(l.lastSeen) < 8*time.Second
}
func (l *AircraftLink) Close() { l.closeOnce.Do(func() { l.cancel(); l.conn.Close(); <-l.done }) }

func aircraftListRequest(counter uint16, cursor uint32) []byte {
	p := hx("4a0021100c0000000000010000002d000d0100ffffffffffffffff000100000000")
	le.PutUint16(p[4:], counter)
	le.PutUint32(p[10:], cursor)
	return p
}

func (l *AircraftLink) ListPage(ctx context.Context, storage int, cursor uint32) (Page, error) {
	l.queryMu.Lock()
	defer l.queryMu.Unlock()
	page := Page{Storage: storage, Files: []Media{}}
	if storage != 0 && storage != 1 {
		return page, errors.New("无效存储")
	}
	if cursor == 0 {
		cursor = uint32(storage)<<30 | 1
	}
	l.querySeq++
	counter := l.querySeq
	send := func(p []byte) error {
		return l.sendTunnel(Message{Sender: 2, Receiver: 1, Flags: 0x40, Set: 0, ID: 0x26, Payload: p}, true)
	}
	release := hx("4a040e1000000000000001000000")
	le.PutUint16(release[4:], counter)
	defer func() { _ = send(release) }()
	if err := send(aircraftListRequest(counter, cursor)); err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	chunks := map[uint32][]byte{}
	last := -1
	total := 0
	proceeded := false
	for {
		m, err := l.waitMessage(ctx, 8*time.Second, func(m Message) bool {
			return m.Set == 0 && m.ID == 0x27 && len(m.Payload) >= 10 && m.Payload[0] == 0x4a && le.Uint16(m.Payload[4:]) == counter
		})
		if err != nil {
			return page, fmt.Errorf("无人机素材清单未完整返回（已收 %d 个分片）：%w", len(chunks), err)
		}
		p := m.Payload
		if int(le.Uint16(p[2:])&0xfff) != len(p) {
			return page, errors.New("无人机素材分片长度异常")
		}
		if p[1] == 3 && !proceeded {
			goOn := hx("4a020f100000000000000000000000")
			le.PutUint16(goOn[4:], counter)
			if err := send(goOn); err != nil {
				return page, err
			}
			proceeded = true
		}
		if p[1] != 1 {
			continue
		}
		index := le.Uint32(p[6:])
		if index > 1024 {
			return page, errors.New("无人机素材分片序号异常")
		}
		if _, ok := chunks[index]; !ok {
			chunks[index] = append([]byte(nil), p[10:]...)
			total += len(p) - 10
		}
		if total > 1<<20 {
			return page, errors.New("无人机素材分页超出大小限制")
		}
		if le.Uint16(p[2:])&0x1000 != 0 {
			last = int(index)
		}
		if last < 0 || len(chunks) != last+1 {
			continue
		}
		raw := make([]byte, 0, total)
		for i := 0; i <= last; i++ {
			part, ok := chunks[uint32(i)]
			if !ok {
				raw = nil
				break
			}
			raw = append(raw, part...)
		}
		if raw == nil {
			continue
		}
		files, err := ParseAircraftManifest(raw, storage)
		if err != nil {
			return page, err
		}
		page.Files, page.Complete, page.Cursor = files, len(files) < 45, cursor
		if len(files) > 0 {
			page.Cursor = files[0].Handle
		}
		for _, f := range files {
			if f.Handle < page.Cursor {
				page.Cursor = f.Handle
			}
		}
		return page, nil
	}
}
