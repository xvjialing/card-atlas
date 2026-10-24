package camera

import (
	"cardatlas/internal/diagnostics"
	"context"
	"net"
	"testing"
	"time"
)

func TestCameraSessionAndTwoStoreListing(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9004})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65535)
		for {
			n, remote, e := server.ReadFromUDP(buf)
			if e != nil {
				return
			}
			p := append([]byte{}, buf[:n]...)
			if n < 8 {
				continue
			}
			session := le.Uint16(p[2:])
			if p[6] == 0 {
				_, _ = server.WriteToUDP(Packet(0, session, 0, p[8:]), remote)
				continue
			}
			var reader Reader
			for _, m := range reader.Feed(p[8:]) {
				send := func(message Message) { _, _ = server.WriteToUDP(Packet(3, session, 8, message.Encode()), remote) }
				if m.Set == 2 && m.ID == 0x0c {
					state := make([]byte, 4)
					le.PutUint32(state, 0x40000000)
					send(Message{1, 2, m.Seq, 0, 2, 0x80, state})
				}
				if m.Set == 0 && m.ID == 0x26 && len(m.Payload) >= 38 {
					counter := le.Uint16(m.Payload[4:])
					store := 0
					if le.Uint32(m.Payload[10:])&0x40000000 != 0 {
						store = 1
					}
					raw := manifestRecord("FIXTURE.MP4", store, 321)
					payload := make([]byte, 10)
					payload[0] = 0x4a
					payload[1] = 1
					le.PutUint16(payload[2:], uint16(len(raw)+10)|0x1000)
					le.PutUint16(payload[4:], counter)
					payload = append(payload, raw...)
					send(Message{1, 2, m.Seq, 0, 0, 0x27, payload})
					end := make([]byte, 10)
					end[0], end[1] = 0x4a, 3
					le.PutUint16(end[4:], counter)
					send(Message{1, 2, m.Seq, 0, 0, 0x27, end})
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	logs := diagnostics.New(t.TempDir())
	logs.SaveSettings(diagnostics.Settings{RetentionDays: 7, Level: "debug"})
	ctx = diagnostics.With(ctx, logs, map[string]string{"connection_id": "test-connection"})
	link, err := OpenLink(ctx, "127.0.0.1", "00000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	files, warning, err := link.List(ctx)
	if err != nil || warning != "" || len(files) != 2 {
		t.Fatalf("双存储列表错误：%v %s %+v", err, warning, files)
	}
	if files[0].Storage != 1 || files[1].Storage != 0 || files[0].ID == files[1].ID {
		t.Fatal("存储识别错误")
	}
	page, err := logs.Read(diagnostics.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]bool{}
	for _, e := range page.Entries {
		events[e.Event] = true
		if e.Fields["connection_id"] != "test-connection" {
			t.Fatal("连接关联编号丢失")
		}
	}
	for _, event := range []string{"link.handshake_ready", "link.playback_ready", "media.page_ready"} {
		if !events[event] {
			t.Fatalf("关键诊断事件缺失 %s", event)
		}
	}
	link.Close()
	server.Close()
	<-done
}
