package camera

import (
	"cardatlas/internal/diagnostics"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

type Link struct {
	conn                 *net.UDPConn
	mu                   sync.Mutex
	session, seq, msgSeq uint16
	count                byte
	cursors              [3]uint16
	ctx                  context.Context
	cancel               context.CancelFunc
	done                 chan struct{}
	inbox                chan Message
	queryMu              sync.Mutex
	counter              uint16
	playback             bool
	playbackVersion      uint64
	lastSeen             time.Time
	trace                diagnostics.Trace
	lastSendError        time.Time
	unacked              []pendingCommand
	acksSeen             bool
}

type pendingCommand struct {
	seq             uint16
	packet          []byte
	firstSent, sent time.Time
}

func hx(s string) []byte { b, _ := hex.DecodeString(s); return b }

func OpenLink(ctx context.Context, host, identity string) (*Link, error) {
	trace := diagnostics.From(ctx)
	trace.Log("info", "datalink", "link.open", "开始建立相机数据链路", map[string]string{"port": "9004"})
	// HTTP 与 UDP 只访问用户直接连接的相机；不扫描其他网络设备。
	if c, err := net.DialTimeout("tcp", net.JoinHostPort(host, "7001"), 1200*time.Millisecond); err == nil {
		_ = c.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = c.Write(Pairing(identity).Encode())
		_ = c.Close()
		trace.Log("debug", "datalink", "link.poke", "已发送 TCP 会话唤醒", nil)
	} else {
		trace.Log("warn", "datalink", "link.poke_failed", "TCP 会话唤醒失败，将继续尝试 UDP", map[string]string{"error": err.Error()})
	}
	a, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, "9004"))
	if err != nil {
		return nil, err
	}
	c, err := net.DialUDP("udp", nil, a)
	if err != nil {
		return nil, err
	}
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	base := le.Uint16(seed[2:]) & 0xfff8
	l := &Link{conn: c, session: le.Uint16(seed[:2]), seq: base + 8, msgSeq: 0xa000, cursors: [3]uint16{0, base, base}, done: make(chan struct{}), inbox: make(chan Message, 512), lastSeen: time.Now(), trace: trace}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	body := hx("b88764006400c005140000640000019001c005140000640014006400c00514000064000101040102")
	le.PutUint16(body, base)
	buf := make([]byte, 65535)
	connected := false
	for attempt := 0; attempt < 15; attempt++ {
		trace.Log("debug", "datalink", "link.handshake_attempt", "发送 UDP 握手", map[string]string{"attempt": fmt.Sprint(attempt + 1)})
		if ctx.Err() != nil {
			break
		}
		_, err = c.Write(Packet(0, l.session, 0, body))
		if err != nil {
			break
		}
		_ = c.SetReadDeadline(time.Now().Add(350 * time.Millisecond))
		n, e := c.Read(buf)
		if e == nil && validPacket(buf[:n], l.session) && buf[6] == 0 {
			connected = true
			break
		}
	}
	if !connected {
		l.cancel()
		c.Close()
		return nil, errors.New("相机未响应。请确认 Mac 已加入相机 Wi-Fi，手机 Mimo 已断开，并允许CardAtlas访问本地网络")
	}
	trace.Log("info", "datalink", "link.handshake_ready", "UDP 握手完成", nil)
	go l.run()
	if err = l.send(Message{2, 0x48, 0, 0x80, 0, 0x81, appInfo()}); err != nil {
		l.Close()
		return nil, err
	}
	if err = l.command(0x28, 0, 0x88, hx("170008237b41505000000000000002")); err != nil {
		l.Close()
		return nil, err
	}
	_ = l.command(3, 3, 0xda, hx("05ffffffff"))
	if err = l.enterPlayback(ctx); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (l *Link) enterPlayback(ctx context.Context) error {
	// 回放模式必须由相机状态位确认，不能把发送成功当成连接完成。
	l.mu.Lock()
	version := l.playbackVersion
	l.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		l.trace.Log("debug", "datalink", "link.playback_attempt", "请求进入相机回放模式", map[string]string{"attempt": fmt.Sprint(attempt + 1)})
		if err := l.command(1, 2, 0x0c, []byte{1, 1, 0, 1}); err != nil {
			return err
		}
		timer := time.NewTimer(time.Second)
	waitPlayback:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				break waitPlayback
			case <-l.done:
				timer.Stop()
				return errors.New("相机连接已中断")
			case m := <-l.inbox:
				if m.Set != 2 || m.ID != 0x80 || len(m.Payload) < 4 {
					continue
				}
				l.mu.Lock()
				ok := l.playback && l.playbackVersion > version
				l.mu.Unlock()
				if ok {
					timer.Stop()
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-l.done:
						return errors.New("相机连接已中断")
					case <-time.After(1800 * time.Millisecond):
						l.trace.Log("info", "datalink", "link.playback_ready", "相机已确认进入回放模式", nil)
						return nil
					}
				}
			}
		}
	}
	return errors.New("尚未确认相机进入素材回放模式，请停止录制后重新连接")
}

func (l *Link) command(receiver, set, id byte, payload []byte) error {
	return l.send(Message{Sender: 2, Receiver: receiver, Flags: 0x40, Set: set, ID: id, Payload: payload})
}
func (l *Link) send(m Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m.Seq == 0 {
		m.Seq = l.msgSeq
		l.msgSeq++
	}
	b := make([]byte, 12)
	le.PutUint16(b, l.seq-8)
	le.PutUint16(b[2:], l.seq)
	b[8] = l.count
	b[9] = 1
	packet := Packet(5, l.session, l.seq, append(b, m.Encode()...))
	_ = l.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := l.conn.Write(packet)
	if err == nil {
		now := time.Now()
		l.unacked = append(l.unacked, pendingCommand{l.seq, packet, now, now})
		// 只保留有界的重传窗口；对端持续不确认时由清单超时提示重连。
		if len(l.unacked) > 256 {
			l.unacked = l.unacked[len(l.unacked)-256:]
		}
		l.seq += 8
		l.count++
	}
	if err != nil && time.Since(l.lastSendError) > 10*time.Second {
		l.lastSendError = time.Now()
		l.trace.Log("warn", "datalink", "link.command_failed", "相机命令发送失败", map[string]string{"command": fmt.Sprintf("%02x/%02x", m.Set, m.ID), "error": err.Error()})
	}
	return err
}

// 命令按前驱序号串联；一个 UDP 包丢失会使后续命令全部失效，必须原序重传。
func (l *Link) acknowledge(cursor uint16) {
	for i, pending := range l.unacked {
		if pending.seq == cursor {
			l.unacked = l.unacked[i+1:]
			l.acksSeen = true
			return
		}
	}
}

func (l *Link) resendPending() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.acksSeen || len(l.unacked) == 0 {
		return
	}
	now := time.Now()
	first := l.unacked[0]
	if now.Sub(first.sent) < 300*time.Millisecond || now.Sub(first.firstSent) > 5*time.Second {
		return
	}
	l.trace.Log("debug", "datalink", "link.commands_resent", "重传相机尚未确认的命令", map[string]string{"count": fmt.Sprint(len(l.unacked)), "sequence": fmt.Sprint(first.seq)})
	for i := range l.unacked {
		_ = l.conn.SetWriteDeadline(now.Add(time.Second))
		if _, err := l.conn.Write(l.unacked[i].packet); err != nil {
			return
		}
		l.unacked[i].sent = now
	}
}

func (l *Link) run() {
	defer close(l.done)
	defer l.conn.Close()
	defer l.trace.Log("info", "datalink", "link.closed", "相机数据链路已关闭", nil)
	ack := time.NewTicker(100 * time.Millisecond)
	defer ack.Stop()
	beat := time.NewTicker(time.Second)
	defer beat.Stop()
	buf := make([]byte, 65535)
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ack.C:
			b := make([]byte, 26)
			l.mu.Lock()
			for i, v := range l.cursors {
				le.PutUint16(b[i*8:], v)
				le.PutUint16(b[i*8+2:], v)
			}
			l.mu.Unlock()
			_, _ = l.conn.Write(Packet(4, l.session, 0, b))
			l.resendPending()
		case <-beat.C:
			_ = l.command(0x28, 0, 0x88, hx("170008237b41505000000000000002"))
		default:
		}
		_ = l.conn.SetReadDeadline(time.Now().Add(70 * time.Millisecond))
		n, err := l.conn.Read(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			if l.ctx.Err() == nil {
				l.trace.Log("error", "datalink", "link.read_failed", "读取相机数据失败", map[string]string{"error": err.Error()})
			}
			return
		}
		b := buf[:n]
		if !validPacket(b, l.session) {
			continue
		}
		l.mu.Lock()
		l.lastSeen = time.Now()
		if b[6] == 1 && n >= 34 {
			l.cursors = [3]uint16{le.Uint16(b[10:]), le.Uint16(b[18:]), le.Uint16(b[26:])}
			l.acknowledge(l.cursors[2])
		} else if b[6] == 3 {
			l.cursors[1] = le.Uint16(b[4:])
		}
		l.mu.Unlock()
		var r Reader
		for _, m := range r.Feed(b[8:]) {
			if m.Flags&0xc0 == 0x40 {
				p := []byte{0}
				if m.Set == 0 && m.ID == 0x81 {
					p = appInfo()
				}
				if m.Set == 0 && m.ID == 0x88 && len(m.Payload) > 0 && m.Payload[0] == 0x19 {
					p = hx("1a00000000")
				}
				_ = l.send(Message{2, m.Sender, m.Seq, 0x80, m.Set, m.ID, p})
			}
			if m.Set == 2 && m.ID == 0x80 && len(m.Payload) >= 4 {
				l.mu.Lock()
				l.playback = le.Uint32(m.Payload)&0x40000000 != 0
				l.playbackVersion++
				l.mu.Unlock()
			}
			select {
			case l.inbox <- m:
			default:
			}
		}
	}
}

func (l *Link) Alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Since(l.lastSeen) < 8*time.Second && l.ctx.Err() == nil
}
func (l *Link) Close() {
	if l == nil {
		return
	}
	_ = l.command(1, 2, 0x0c, []byte{1, 1, 0, 0})
	l.cancel()
	_ = l.conn.Close()
	<-l.done
}

func (l *Link) ListPage(ctx context.Context, storage int, cursor uint32) (Page, error) {
	l.queryMu.Lock()
	defer l.queryMu.Unlock()
	return l.listPage(ctx, storage, cursor)
}

func (l *Link) listPage(ctx context.Context, storage int, cursor uint32) (Page, error) {
	page := Page{Files: []Media{}, Storage: storage, Cursor: cursor}
	start := time.Now()
	trace := l.trace
	trace.Log("debug", "media", "media.page_start", "开始读取素材分页", map[string]string{"storage": fmt.Sprint(storage), "cursor": fmt.Sprint(cursor)})
	if storage != 0 && storage != 1 {
		return page, errors.New("无效存储")
	}
	if cursor == 0 {
		cursor = 1
		if storage == 1 {
			cursor |= 0x40000000
		}
	}
	if cursor != 1 && cursor != 0x40000001 {
		l.counter++
		first := uint32(1)
		if storage == 1 {
			first |= 0x40000000
		}
		if err := l.command(1, 0, 0x26, listPayload(l.counter, first)); err != nil {
			return page, err
		}
		trigger := hx("4a040e1001000000000001000000")
		le.PutUint32(trigger[4:], uint32(l.counter))
		le.PutUint32(trigger[10:], first)
		if err := l.command(1, 0, 0x26, trigger); err != nil {
			return page, err
		}
	}
	l.counter++
	counter := l.counter
	if err := l.command(1, 0, 0x26, listPayload(counter, cursor)); err != nil {
		return page, err
	}
	timer := time.NewTimer(9 * time.Second)
	defer timer.Stop()
	settle := time.NewTicker(150 * time.Millisecond)
	defer settle.Stop()
	chunks := map[uint16][]byte{}
	seen := map[uint16]bool{}
	last := -1
	ended := time.Time{}
	started := false
	assemble := func() []byte {
		keys := make([]int, 0, len(chunks))
		for k := range chunks {
			keys = append(keys, int(k))
		}
		sort.Ints(keys)
		raw := []byte{}
		for _, k := range keys {
			raw = append(raw, chunks[uint16(k)]...)
		}
		return raw
	}
	finish := func() (Page, error) {
		raw := assemble()
		if len(raw) < 12 {
			return page, errors.New("相机返回的素材列表头不完整，请刷新重试")
		}
		files, e := ParseManifest(raw, storage)
		if e != nil {
			return page, e
		}
		// 部分机型会提供本页记录数；不能把截断后仍能解析的前几条当成完整分页。
		if len(raw) >= 4 {
			declared := le.Uint32(raw)
			if declared > 0 && declared != uint32(len(files)) {
				return page, errors.New("素材列表记录数不完整，请刷新重试")
			}
		}
		if len(raw) > 16 && len(files) == 0 {
			return page, errors.New("相机返回了无法识别的素材列表，未将其当作空相册")
		}
		page.Files = files
		page.Complete = len(files) < 45
		page.Cursor = cursor
		if len(files) > 0 {
			page.Cursor = files[0].Handle
			for _, f := range files {
				if f.Handle < page.Cursor {
					page.Cursor = f.Handle
				}
			}
		}
		trace.Log("debug", "media", "media.page_ready", "素材分页接收完成", map[string]string{"storage": fmt.Sprint(storage), "records": fmt.Sprint(len(files)), "chunks": fmt.Sprint(len(chunks)), "elapsed_ms": fmt.Sprint(time.Since(start).Milliseconds())})
		return page, nil
	}
	contiguous := func() bool {
		if len(seen) == 0 {
			return false
		}
		end := len(seen) - 1
		if last >= 0 {
			end = last
		}
		for i := 0; i <= end; i++ {
			if !seen[uint16(i)] {
				return false
			}
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return page, ctx.Err()
		case <-l.done:
			return page, errors.New("相机连接已中断，请重新连接")
		case <-timer.C:
			trace.Log("warn", "media", "media.page_timeout", "等待素材分页超时", map[string]string{"storage": fmt.Sprint(storage), "chunks": fmt.Sprint(len(chunks)), "started": fmt.Sprint(started), "counter": fmt.Sprint(counter), "ended": fmt.Sprint(!ended.IsZero()), "last_sequence": fmt.Sprint(last)})
			if started && len(chunks) == 0 {
				return page, errors.New("该存储尚未就绪或未插入存储卡，请稍后刷新")
			}
			if !started && len(chunks) == 0 {
				return page, errors.New("相机未响应素材查询，请断开后重新连接")
			}
			return page, errors.New("素材列表接收不完整，请刷新重试")
		case <-settle.C:
			if !ended.IsZero() && time.Since(ended) > 600*time.Millisecond && contiguous() {
				// 未标记末片时，只接受明确的空页或记录数已齐的分页；不能仅凭短暂静默结束。
				raw := assemble()
				if len(raw) >= 12 && len(raw) <= 16 && le.Uint32(raw) == 0 {
					return finish()
				}
				if len(raw) < 4 {
					continue
				}
				declared := le.Uint32(raw)
				if declared > 0 {
					files, err := ParseManifest(raw, storage)
					if err == nil && uint32(len(files)) == declared {
						return finish()
					}
				}
			}
		case m := <-l.inbox:
			if m.Set != 0 || m.ID != 0x27 || len(m.Payload) < 10 {
				continue
			}
			p := m.Payload
			if p[0] != 0x4a || le.Uint16(p[4:]) != counter {
				continue
			}
			seq := le.Uint16(p[6:])
			switch p[1] {
			case 4:
				started = true
				seen[seq] = true
			case 1:
				seen[seq] = true
				chunks[seq] = append([]byte(nil), p[10:]...)
				if le.Uint16(p[2:])&0x1000 != 0 {
					last = int(seq)
				}
			case 3:
				// 结束帧的序号恒为 0，可能先于数据到达或重复推送，不能当成数据末序号。
				if ended.IsZero() {
					ended = time.Now()
				}
			}
			if !ended.IsZero() && last >= 0 && contiguous() {
				return finish()
			}
		}
	}
}

func (l *Link) List(ctx context.Context) ([]Media, string, error) {
	l.queryMu.Lock()
	defer l.queryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	// 刷新复用现有连接，但重新宣布应用身份并确认回放，避免长会话或模式变化后静默丢弃查询。
	if err := l.send(Message{2, 0x48, 0, 0x80, 0, 0x81, appInfo()}); err != nil {
		return nil, "", err
	}
	if err := l.command(0x28, 0, 0x88, hx("170008237b41505000000000000002")); err != nil {
		return nil, "", err
	}
	if err := l.enterPlayback(ctx); err != nil {
		return nil, "", err
	}
	files := []Media{}
	warnings := []string{}
	seen := map[string]bool{}
	for _, store := range []int{1, 0} {
		cursor := uint32(0)
		name := map[int]string{0: "SD 卡", 1: "内置存储"}[store]
		for pageIndex := 0; pageIndex < 200; pageIndex++ {
			p, err := l.listPage(ctx, store, cursor)
			if err != nil {
				l.trace.Log("warn", "media", "media.store_failed", "读取存储中的素材未完成", map[string]string{"storage": fmt.Sprint(store), "error": err.Error()})
				warnings = append(warnings, fmt.Sprintf("%s：%s", name, err))
				break
			}
			added := 0
			for _, m := range p.Files {
				if !seen[m.ID] {
					seen[m.ID] = true
					files = append(files, m)
					added++
				}
			}
			if p.Complete {
				break
			}
			if added == 0 || p.Cursor == cursor {
				warnings = append(warnings, name+"分页未前进，当前列表可能不完整，请刷新重试")
				break
			}
			cursor = p.Cursor
			if pageIndex == 199 {
				warnings = append(warnings, name+"素材数量超过单次读取上限，当前列表可能不完整")
			}
		}
	}
	if len(files) == 0 && len(warnings) == 2 {
		return files, "", errors.New(joinWarnings(warnings))
	}
	return files, joinWarnings(warnings), nil
}
func joinWarnings(w []string) string {
	s := ""
	for _, v := range w {
		if s != "" {
			s += "；"
		}
		s += v
	}
	return s
}
