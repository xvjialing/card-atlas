package camera

import (
	"testing"

	"cardatlas/internal/diagnostics"
)

// 新的成功重连抓包包含发给旧 Mac 端点的 51/17，手机正常连接且从不回复这些消息。
// 即使通知中的名称碰巧等于本机旧名称，也不能跨目的地址迁移会话或发送未经证实的 ACK。
func TestStaleNotificationCannotRebindCurrentRoute(t *testing.T) {
	capture := readCaptureSession(t)
	conn := &capturedWriteConn{}
	var app, peer [4]byte
	copy(app[:], hx(capture.AppAddress))
	copy(peer[:], hx(capture.PeerAddress))
	link := &AircraftLink{conn: conn, app: app, appName: capture.AppName, inbox: make(chan Message, 4)}
	beaconTail := make([]byte, 22)
	copy(beaconTail, peer[:])
	beaconTail[4] = 2
	copy(beaconTail[9:], []byte{255, 255, 255, 255})
	beaconTail[13], beaconTail[14] = 1, 0x82
	link.accept(Message{Sender: 0x27, Receiver: 0xee, Set: 0x51, ID: 1, Payload: append(capture.message(t, "drone_beacon").Encode(), beaconTail...)})
	if !link.peerKnown || len(link.inbox) != 1 {
		t.Fatal("未建立真实设备广播上下文")
	}
	<-link.inbox
	inner := capture.message(t, "stale_notification")
	tail := hx(capture.Frames["stale_notification"].Tail)
	link.accept(Message{Sender: 0x27, Receiver: 0xee, Set: 0x51, ID: 1, Payload: append(inner.Encode(), tail...)})
	if link.app != app || len(conn.packet) != 0 || len(link.inbox) != 0 {
		t.Fatal("旧端点通知改变了当前路由或被错误应答")
	}
}

// 实机记录中 basic 发给本机、full 发给另一个端点。保留这个差异，不能把 full 当作本机已完成。
func TestCapabilityForAnotherEndpointIsCountedWithoutReply(t *testing.T) {
	capture := readCaptureSession(t)
	conn := &capturedWriteConn{}
	logs := diagnostics.New(t.TempDir())
	var app, peer [4]byte
	copy(app[:], hx(capture.AppAddress))
	copy(peer[:], hx(capture.PeerAddress))
	link := &AircraftLink{conn: conn, app: app, peer: peer, peerKnown: true, trace: diagnostics.Trace{Store: logs}, stateChanged: make(chan struct{}, 1)}
	send := func(name string, destination [4]byte) {
		inner := capture.message(t, "capability_request_"+name)
		tail := make([]byte, 22)
		copy(tail, peer[:])
		tail[4] = 2
		copy(tail[9:], destination[:])
		tail[13], tail[14] = 1, 0xa2
		link.accept(Message{Set: 0x51, ID: 1, Payload: append(inner.Encode(), tail...)})
	}
	send("basic", app)
	if link.capabilities != 1 || len(conn.packet) == 0 {
		t.Fatal("应答本机基础能力请求失败")
	}
	conn.packet = nil
	for i := 0; i < 20; i++ {
		send("full", [4]byte{0x91, 0x92, 0x93, 0x94})
	}
	if link.capabilities != 1 || len(conn.packet) != 0 || link.app != app || link.wlmOtherDestination[0x41] != 20 {
		t.Fatal("其他端点的完整能力请求被误应答、改变本机状态或未记录")
	}
	page, err := logs.Read(diagnostics.Query{Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range page.Entries {
		if event.Event == "aircraft.other_destination" {
			count++
			if len(event.Fields) != 3 || event.Fields["command"] != "51/41" || event.Fields["content_bytes"] != "72" {
				t.Fatal("路由诊断应只记录命令、长度和序号，不记录身份或原始载荷")
			}
		}
	}
	if count != 1 {
		t.Fatalf("重复目的地址不匹配只应写一次概要日志，实际 %d 次", count)
	}
	// 日志去重不能吞掉后续真正属于本机的请求。
	send("full", app)
	if link.capabilities != 3 || len(conn.packet) == 0 {
		t.Fatal("属于本机的完整能力请求未正常应答")
	}
}
