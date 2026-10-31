package camera

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAircraftPairingUsesFlyToken(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, tc := range []struct {
		message Message
		token   string
	}{{Pairing(id), "osmo"}, {AircraftPairing(id), "DJI FLY"}} {
		var reader Reader
		m := reader.Feed(tc.message.Encode())
		if len(m) != 1 || m[0].Set != 7 || m[0].ID != 0x45 || !bytes.Equal(m[0].Payload, append(append([]byte{32}, []byte(id)...), append([]byte{byte(len(tc.token))}, []byte(tc.token)...)...)) {
			t.Fatal("配对身份或机型标识不正确")
		}
	}
}

func TestAircraftSerialUsesDeviceTag(t *testing.T) {
	serial, tag := aircraftSerial(append([]byte{0, 0, 0, 0x21}, []byte("TEST1234567890123456\x00")...))
	if string(serial) != "TEST1234567890123456" || tag != 0x21 {
		t.Fatal("未使用当前机型的序列号标记")
	}
	if s, _ := aircraftSerial([]byte("short123")); s != nil {
		t.Fatal("不应把短字符串当作序列号")
	}
}

// 模拟器只接受绑定当前设备 WLM 地址的请求，素材帧跨越 UDP 包，并重复发送第一片。
func TestAircraftSessionAndFragmentedMedia(t *testing.T) {
	for _, mode := range []string{"both", "basic-only", "none"} {
		t.Run(mode, func(t *testing.T) { testAircraftSessionAndFragmentedMedia(t, mode) })
	}
}

func testAircraftSessionAndFragmentedMedia(t *testing.T, capabilityMode string) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	capture := readCaptureSession(t)
	manifest, err := os.ReadFile("testdata/mini5pro-sd-manifest.bin")
	if err != nil {
		t.Fatal(err)
	}
	serverErrors := make(chan error, 1)
	released := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 65535)
		var session uint16
		var app [4]byte
		var peer [4]byte
		copy(peer[:], hx(capture.PeerAddress))
		capabilitiesSent, challengeAnswered, identityAnswered, playback := false, false, false, false
		var nextWLM uint32 = 1
		var counter uint16
		var remote *net.UDPAddr
		echo := capture.message(t, "challenge_response").Payload
		send := func(kind byte, seq uint16, p []byte) { _, _ = server.WriteToUDP(Packet(kind, session, seq, p), remote) }
		response := func(m Message) {
			tail := make([]byte, 22)
			copy(tail, peer[:])
			tail[4] = 2
			copy(tail[9:], app[:])
			tail[13] = 1
			outer := Message{Sender: 0xe9, Receiver: 0x3b, Seq: 1, Set: 0x51, ID: 1, Payload: append(m.Encode(), tail...)}
			send(1, 0, append(make([]byte, 26), outer.Encode()...))
		}
		for {
			n, addr, readErr := server.ReadFromUDP(buf)
			remote = addr
			if readErr != nil {
				return
			}
			b := append([]byte(nil), buf[:n]...)
			if b[6] == 0 {
				session = le.Uint16(b[2:])
				send(0, 0, []byte{1})
				continue
			}
			if b[6] != 5 || len(b) < 20 {
				continue
			}
			var reader Reader
			frames := reader.Feed(b[20:])
			if len(frames) != 1 {
				serverErrors <- errors.New("控制报文不完整")
				return
			}
			if frames[0].Set != 0x51 || frames[0].ID != 1 {
				m := frames[0]
				versionQuery := m.Set == 0 && m.ID == 1 && len(m.Payload) == 0 && (m.Receiver == 0x0e || m.Receiver == 0x1f || m.Receiver == 0xa2 || m.Receiver == 0)
				macQuery := m.Set == 0x12 && m.ID == 0x0c && m.Receiver == 7 && len(m.Payload) == 0
				valid := versionQuery || macQuery || m.Set == 7 && m.ID == 0x93 && bytes.Equal(m.Payload, capture.message(t, "endpoint_announcement").Payload) || m.Set == 0x51 && m.ID == 0x34 && bytes.Equal(m.Payload, capture.message(t, "identity_announcement").Payload)
				if !valid {
					serverErrors <- errors.New("收到未出现在连接序列中的裸控制命令")
					return
				}
				continue
			}
			p := frames[0].Payload
			nInner := int(le.Uint16(p[1:]) & 0x3ff)
			if len(p) != nInner+22 {
				serverErrors <- errors.New("会话地址尾部长度错误")
				return
			}
			var innerReader Reader
			m := innerReader.Feed(p[:nInner])[0]
			tail := p[nInner:]
			copy(app[:], tail[:4])
			next := le.Uint32(tail[5:])
			if next != nextWLM || frames[0].Seq != uint16(next) {
				serverErrors <- errors.New("WLM 请求序号出现空洞，或与外层编号不一致")
				return
			}
			if m.Set == 0x51 && m.ID == 0x13 {
				if !bytes.Equal(tail[9:13], []byte{255, 255, 255, 255}) || tail[14] != 0x82 {
					serverErrors <- errors.New("身份广播地址错误")
					return
				}
				response(Message{Sender: 0xe9, Receiver: 0xee, Set: 0x51, ID: 0x13, Payload: echo})
				continue
			}
			nextWLM++
			broadcast := m.Set != 0x51 || m.ID == 0x34 || m.ID == 0x41 && m.Flags == 0x40
			wantDestination := peer[:]
			wantFlag := byte(0xe2)
			if broadcast {
				wantDestination = []byte{255, 255, 255, 255}
				wantFlag = 0x82
			}
			if m.Set == 0x51 && m.ID == 0x41 {
				wantFlag = 0x82
			}
			if !bytes.Equal(tail[9:13], wantDestination) || tail[14] != wantFlag {
				serverErrors <- errors.New("会话单播或素材广播寻址未匹配抓包")
				return
			}
			if m.Set == 0x51 && m.ID == 0x41 && m.Flags == 0x40 && !capabilitiesSent {
				if capabilityMode != "none" {
					response(capture.message(t, "capability_request_basic"))
				}
				if capabilityMode == "both" {
					response(capture.message(t, "capability_request_full"))
				}
				capabilitiesSent = true
			}
			if m.Set == 0x51 && m.ID == 2 {
				if !bytes.Equal(m.Payload, capture.message(t, "open").Payload) {
					serverErrors <- errors.New("开启请求仍是其他机型的布局")
					return
				}
				response(capture.message(t, "challenge"))
			}
			if m.Set == 0x51 && m.ID == 8 && m.Flags == 0xc0 {
				challengeAnswered = bytes.Equal(m.Payload, echo)
			}
			if m.Set == 0x51 && m.ID == 6 && m.Flags == 0x40 {
				if !challengeAnswered || !bytes.Equal(m.Payload, capture.message(t, "identity_request").Payload) {
					serverErrors <- errors.New("身份请求未匹配抓包")
					return
				}
				ack := capture.message(t, "identity_ack")
				ack.Seq = m.Seq
				response(ack)
				response(capture.message(t, "peer_identity_request"))
			}
			if m.Set == 0x51 && m.ID == 6 && m.Flags == 0xc0 {
				identityAnswered = bytes.Equal(m.Payload, echo)
			}
			if m.Set == 2 && m.ID == 0x0c {
				if !identityAnswered || !bytes.Equal(m.Payload, []byte{1, 1, 0, 1}) {
					serverErrors <- errors.New("未完成双向确认就进入回放")
					return
				}
				response(Message{Sender: 1, Receiver: 2, Seq: m.Seq, Flags: 0xc0, Set: 2, ID: 0x0c, Payload: []byte{0}})
				state := make([]byte, 60)
				le.PutUint32(state, 0x40000001)
				response(Message{Sender: 1, Receiver: 2, Set: 2, ID: 0x80, Payload: state})
				playback = true
			}
			if m.Set != 0 || m.ID != 0x26 {
				continue
			}
			if len(m.Payload) < 10 {
				serverErrors <- errors.New("查询过短")
				return
			}
			switch m.Payload[1] {
			case 0:
				if !playback {
					serverErrors <- errors.New("回放未确认就读取目录")
					return
				}
				if len(m.Payload) != 33 || le.Uint32(m.Payload[10:]) != 1 {
					serverErrors <- errors.New("飞行器查询布局错误")
					return
				}
				counter = le.Uint16(m.Payload[4:])
				state := hx("4a030a00000000000000")
				le.PutUint16(state[4:], counter)
				response(Message{Sender: 1, Receiver: 2, Set: 0, ID: 0x27, Payload: state})
			case 2:
				var stream []byte
				for offset, index := 0, uint32(0); offset < len(manifest); index++ {
					end := min(offset+989, len(manifest))
					p := make([]byte, 10)
					p[0], p[1] = 0x4a, 1
					length := uint16(end - offset + 10)
					if end == len(manifest) {
						length |= 0x1000
					}
					le.PutUint16(p[2:], length)
					le.PutUint16(p[4:], counter)
					le.PutUint32(p[6:], index)
					frame := Message{Sender: 0xe1, Receiver: 2, Set: 0, ID: 0x27, Payload: append(p, manifest[offset:end]...)}.Encode()
					stream = append(stream, frame...)
					offset = end
				}
				for offset, seq := 0, uint16(8); offset < len(stream); seq += 8 {
					end := min(offset+1452, len(stream))
					body := append(make([]byte, 12), stream[offset:end]...)
					send(3, seq, body)
					if offset == 0 {
						send(3, seq, body)
					}
					offset = end
				}
			case 4:
				select {
				case released <- struct{}{}:
				default:
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link, err := openAircraftLink(ctx, server.LocalAddr().String(), capture.AppName+"0123456789abc", &net.Dialer{LocalAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}})
	if err != nil {
		select {
		case e := <-serverErrors:
			t.Fatal(e)
		default:
			t.Fatal(err)
		}
	}
	defer link.Close()
	page, err := link.ListPage(ctx, 0, 0)
	if err != nil || len(page.Files) != 45 || page.Complete {
		t.Fatalf("素材分片合并失败：%+v %v", page, err)
	}
	select {
	case <-released:
	case e := <-serverErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("未释放素材传输槽")
	}
	link.Close()
	if link.Alive() {
		t.Fatal("已关闭的链路仍为存活状态")
	}
}

func TestAircraftHandshakeAloneDoesNotMeanConnected(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, e := server.ReadFromUDP(buf)
			if e != nil {
				return
			}
			if n >= 8 && buf[6] == 0 {
				_, _ = server.WriteToUDP(Packet(0, le.Uint16(buf[2:]), 0, []byte{1}), addr)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	link, err := openAircraftLink(ctx, server.LocalAddr().String(), strings.Repeat("b", 32), &net.Dialer{})
	if link != nil || err == nil || !strings.Contains(err.Error(), "Wi-Fi 已连通") {
		t.Fatal(fmt.Sprint("网络握手被误认为素材会话成功：", err))
	}
}
