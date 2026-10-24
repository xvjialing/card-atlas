package camera

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// 模拟器只绑定回环地址，使用真实 UDP 编解码与收发循环，不访问相机。
func simulatedLink(t *testing.T, handle func([]byte, func([]byte))) *Link {
	t.Helper()
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	link := &Link{conn: conn, session: 1, seq: 8, msgSeq: 0xa000, done: make(chan struct{}), inbox: make(chan Message, 512), lastSeen: time.Now()}
	link.ctx, link.cancel = context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		buf := make([]byte, 65535)
		for {
			n, remote, err := server.ReadFromUDP(buf)
			if err != nil {
				return
			}
			packet := append([]byte(nil), buf[:n]...)
			handle(packet, func(p []byte) { _, _ = server.WriteToUDP(p, remote) })
		}
	}()
	go link.run()
	t.Cleanup(func() {
		link.Close()
		server.Close()
		<-finished
	})
	return link
}

func replyChunk(counter, seq uint16, kind byte, last bool, data []byte) Message {
	p := make([]byte, 10, 10+len(data))
	p[0], p[1] = 0x4a, kind
	length := uint16(10 + len(data))
	if last {
		length |= 0x1000
	}
	le.PutUint16(p[2:], length)
	le.PutUint16(p[4:], counter)
	le.PutUint16(p[6:], seq)
	return Message{Sender: 1, Receiver: 2, Flags: 0x80, Set: 0, ID: 0x27, Payload: append(p, data...)}
}

func TestListPageWaitsForEndAndMissingChunks(t *testing.T) {
	for _, endFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("结束帧提前到达=", endFirst), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var sentTail atomic.Bool
			link := simulatedLink(t, func(packet []byte, send func([]byte)) {
				if packet[6] != 5 {
					return
				}
				var r Reader
				for _, m := range r.Feed(packet[8:]) {
					if m.ID != 0x26 {
						continue
					}
					counter := le.Uint16(m.Payload[4:])
					reply := func(m Message) { send(Packet(3, 1, 8, m.Encode())) }
					if endFirst {
						reply(replyChunk(counter, 0, 3, false, nil))
					}
					reply(replyChunk(counter+1, 0, 1, true, manifestRecord("STALE.MP4", 1, 9)))
					reply(replyChunk(counter, 0, 4, false, nil))
					// 末片先到，旧计数器、重复片和已到达的结束帧都不能掩盖缺片。
					reply(replyChunk(counter, 2, 1, true, manifestRecord("TAIL.JPG", 1, 2)))
					reply(replyChunk(counter, 2, 1, true, manifestRecord("TAIL.JPG", 1, 2)))
					time.Sleep(80 * time.Millisecond)
					reply(replyChunk(counter, 1, 1, false, manifestRecord("HEAD.MP4", 1, 1)))
					if !endFirst {
						time.Sleep(80 * time.Millisecond)
					}
					sentTail.Store(true)
					reply(replyChunk(counter, 0, 3, false, nil))
				}
			})
			page, err := link.ListPage(ctx, 1, 0)
			if err != nil || (!endFirst && !sentTail.Load()) || len(page.Files) != 2 || page.Files[0].Name != "HEAD.MP4" || page.Files[1].Name != "TAIL.JPG" {
				t.Fatalf("未完整收集乱序分页：%+v %v", page, err)
			}
		})
	}
}

func TestListPageDoesNotAcceptMissingFinalChunk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			if m.ID != 0x26 {
				continue
			}
			counter := le.Uint16(m.Payload[4:])
			for _, chunk := range []Message{
				replyChunk(counter, 0, 4, false, nil),
				replyChunk(counter, 1, 1, false, manifestRecord("HEAD.MP4", 1, 1)),
				replyChunk(counter, 3, 1, true, manifestRecord("TAIL.MP4", 1, 3)),
				replyChunk(counter, 0, 3, false, nil),
			} {
				send(Packet(3, 1, 8, chunk.Encode()))
			}
		}
	})
	page, err := link.ListPage(ctx, 1, 0)
	if err == nil || len(page.Files) != 0 {
		t.Fatalf("缺片被误判成成功：%+v %v", page, err)
	}
}

func TestLostCommandIsResentBeforeFollowingCommands(t *testing.T) {
	var dropped []byte
	var resent atomic.Bool
	lastAccepted := uint16(0)
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		seq := le.Uint16(packet[4:])
		if seq == 16 && dropped == nil {
			dropped = append([]byte(nil), packet...)
			return
		}
		if le.Uint16(packet[8:]) == lastAccepted {
			lastAccepted = seq
			if seq == 16 {
				resent.Store(bytes.Equal(dropped, packet))
			}
		}
		ack := make([]byte, 26)
		le.PutUint16(ack[18:], lastAccepted)
		send(Packet(1, 1, 0, ack))
		if lastAccepted == 24 {
			send(Packet(3, 1, 8, Message{1, 2, 1, 0x80, 0, 0x27, []byte("已恢复")}.Encode()))
		}
	})
	for i := 0; i < 3; i++ {
		if err := link.command(1, 0, 0x26, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-link.inbox:
		if !resent.Load() {
			t.Fatal("重传改变了原报文或未恢复命令链")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("一个丢包导致后续命令持续失效")
	}
}

func TestTwoStorePaginationAndRepeatedRefresh(t *testing.T) {
	var registrations atomic.Int32
	registered, playback := false, false
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			reply := func(m Message) { send(Packet(3, 1, 8, m.Encode())) }
			if m.Set == 0 && m.ID == 0x81 {
				registered = true
				registrations.Add(1)
			}
			if m.Set == 2 && m.ID == 0x0c && registered {
				playback = len(m.Payload) == 4 && m.Payload[3] == 1
				p := make([]byte, 4)
				if playback {
					le.PutUint32(p, 0x40000000)
				}
				reply(Message{1, 2, m.Seq, 0x80, 2, 0x80, p})
			}
			if m.Set != 0 || m.ID != 0x26 || len(m.Payload) < 38 || !registered || !playback {
				continue
			}
			cursor := le.Uint32(m.Payload[10:])
			counter := le.Uint16(m.Payload[4:])
			store, total := 0, 45
			base := uint32(0x00100000)
			if cursor&0x40000000 != 0 {
				store, total, base = 1, 67, 0x40100000
			}
			start := 0
			if cursor != 1 && cursor != 0x40000001 {
				start = total - int((cursor-base)/0x40)
			}
			raw := []byte{}
			for i := start; i < total && i < start+45; i++ {
				record := manifestRecord(fmt.Sprintf("FILE_%03d.MP4", i), store, uint32(i+1))
				le.PutUint32(record[8:], base+uint32(total-i)*0x40)
				raw = append(raw, record...)
			}
			reply(replyChunk(counter, 0, 4, false, nil))
			seq := uint16(1)
			for len(raw) > 0 {
				n := min(len(raw), 700)
				reply(replyChunk(counter, seq, 1, n == len(raw), raw[:n]))
				raw = raw[n:]
				seq++
			}
			reply(replyChunk(counter, 0, 3, false, nil))
			// 模拟读完 SD 卡后身份/回放失效，下次刷新必须重新准备会话。
			if store == 0 && start > 0 {
				registered, playback = false, false
			}
		}
	})
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		files, warning, err := link.List(ctx)
		cancel()
		if err != nil || warning != "" || len(files) != 112 {
			t.Fatalf("第 %d 次刷新双存储分页不完整：数量=%d warning=%s err=%v", attempt+1, len(files), warning, err)
		}
		seen := map[string]bool{}
		for _, m := range files {
			if seen[m.ID] {
				t.Fatal("重叠页产生重复素材")
			}
			seen[m.ID] = true
		}
	}
	if registrations.Load() != 2 {
		t.Fatalf("连续刷新没有重新注册：%d", registrations.Load())
	}
}

func TestListPageRejectsTruncatedRecordCount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			if m.ID != 0x26 {
				continue
			}
			counter := le.Uint16(m.Payload[4:])
			raw := make([]byte, 12)
			le.PutUint32(raw, 2)
			raw = append(raw, manifestRecord("ONLY_ONE.MP4", 1, 1)...)
			for _, chunk := range []Message{
				replyChunk(counter, 0, 1, true, raw),
				replyChunk(counter, 0, 3, false, nil),
			} {
				send(Packet(3, 1, 8, chunk.Encode()))
			}
		}
	})
	page, err := link.ListPage(ctx, 1, 0)
	if err == nil || len(page.Files) > 0 {
		t.Fatalf("记录数不足仍发布了素材：%+v %v", page, err)
	}
}

func TestListPageWaitsForUnmarkedTailAfterEarlyEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			if m.ID != 0x26 {
				continue
			}
			counter := le.Uint16(m.Payload[4:])
			reply := func(m Message) { send(Packet(3, 1, 8, m.Encode())) }
			raw := make([]byte, 12)
			le.PutUint32(raw, 2)
			raw = append(raw, manifestRecord("HEAD.MP4", 1, 1)...)
			reply(replyChunk(counter, 0, 1, false, raw))
			reply(replyChunk(counter, 0, 3, false, nil))
			// 超过原来的静默窗口；结束帧重复到达不应掩盖或截断后续数据。
			time.Sleep(400 * time.Millisecond)
			reply(replyChunk(counter, 0, 3, false, nil))
			time.Sleep(400 * time.Millisecond)
			reply(replyChunk(counter, 1, 1, false, manifestRecord("TAIL.MP4", 1, 2)))
		}
	})
	page, err := link.ListPage(ctx, 1, 0)
	if err != nil || len(page.Files) != 2 {
		t.Fatalf("结束帧提前到达导致尾部记录丢失：%+v %v", page, err)
	}
}

func TestCommandAcknowledgementAcrossSequenceWrap(t *testing.T) {
	link := &Link{unacked: []pendingCommand{{seq: 0xfff8}, {seq: 0}, {seq: 8}}}
	link.acknowledge(0xfff0)
	if link.acksSeen || len(link.unacked) != 3 {
		t.Fatal("接受了不在发送窗口中的确认序号")
	}
	link.acknowledge(0)
	if !link.acksSeen || len(link.unacked) != 1 || link.unacked[0].seq != 8 {
		t.Fatal("序号回绕时错误地清除了未确认命令")
	}
	link.acknowledge(0xfff8)
	if len(link.unacked) != 1 {
		t.Fatal("旧确认改变了当前窗口")
	}
}

func TestListPageAcceptsExplicitEmptyManifest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			if m.ID != 0x26 {
				continue
			}
			counter := le.Uint16(m.Payload[4:])
			for _, chunk := range []Message{
				replyChunk(counter, 0, 4, false, nil),
				replyChunk(counter, 1, 1, false, make([]byte, 12)),
				replyChunk(counter, 0, 3, false, nil),
			} {
				send(Packet(3, 1, 8, chunk.Encode()))
			}
		}
	})
	page, err := link.ListPage(ctx, 0, 0)
	if err != nil || len(page.Files) != 0 || !page.Complete {
		t.Fatalf("未接受明确空页：%+v %v", page, err)
	}
}

func TestListPageDoesNotTreatEarlyEndAsEmptyAlbum(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	link := simulatedLink(t, func(packet []byte, send func([]byte)) {
		if packet[6] != 5 {
			return
		}
		var r Reader
		for _, m := range r.Feed(packet[8:]) {
			if m.ID != 0x26 {
				continue
			}
			counter := le.Uint16(m.Payload[4:])
			for _, chunk := range []Message{
				replyChunk(counter, 0, 4, false, nil),
				replyChunk(counter, 0, 3, false, nil),
			} {
				send(Packet(3, 1, 8, chunk.Encode()))
			}
		}
	})
	if _, err := link.ListPage(ctx, 0, 0); err == nil {
		t.Fatal("结束帧先到且没有清单数据时被误判成空相册")
	}
}
