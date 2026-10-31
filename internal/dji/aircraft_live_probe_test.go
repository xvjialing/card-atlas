//go:build dji_live_probe

package camera

import (
	"bufio"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/platform"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 仅在显式构建标签和环境变量同时设置时，录制本客户端的短时只读会话。
// 原始数据留在用户本机 work 目录，不进入普通诊断包。
func TestLiveAircraftCapture(t *testing.T) {
	if os.Getenv("CARDATLAS_LIVE_PROBE") != "1" {
		t.Skip("需显式授权的实机调试开关")
	}
	var identity struct{ Identity string }
	b, err := os.ReadFile(filepath.Join(diagnostics.DefaultBase(), "mini5pro-identity.json"))
	if err != nil || json.Unmarshal(b, &identity) != nil {
		t.Fatal("无法读取已有配对身份")
	}
	dir, err := os.MkdirTemp("../../work/mini5pro-live-check", "trace-")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "datagrams.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	defer writer.Flush()
	var mu sync.Mutex
	record := func(direction string, b []byte) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(writer).Encode(map[string]any{"direction": direction, "time": time.Now().UnixMicro(), "data": hex.EncodeToString(b)})
	}
	logs := diagnostics.New(dir)
	logs.RegisterSecrets(identity.Identity)
	defer func() {
		p, _ := logs.Read(diagnostics.Query{Limit: 40})
		for i := len(p.Entries) - 1; i >= 0; i-- {
			e := p.Entries[i]
			t.Log(e.Event, e.Message, e.Fields)
		}
		t.Log("本地记录目录", dir)
	}()
	d, err := platform.CameraWiFiDialer("udp4", 9003)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ctx = diagnostics.With(ctx, logs, nil)
	link, err := openAircraftTransportWithRecovery(ctx, "192.168.2.1:9003", identity.Identity, func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := d.DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &probeRecordingConn{Conn: c, record: record}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = link.announceAircraftEndpoint(); err != nil {
		link.cancel()
		link.conn.Close()
		t.Fatal(err)
	}
	go link.run()
	defer link.Close()
	if err = link.establish(ctx); err != nil {
		t.Errorf("会话未完成：%v", err)
		// 仅在剩余预算内记录一次目录是否可读；目录探测不能替代会话确认，也不能把测试标为通过。
		p, e := link.ListPage(ctx, 0, 0)
		t.Log("重连目录探测", len(p.Files), e)
		return
	}
	for _, storage := range []int{0, 1} {
		p, e := link.ListPage(ctx, storage, 0)
		t.Log("首批目录", storage, len(p.Files), e)
		if e != nil {
			t.Errorf("存储 %d 的目录读取未通过：%v", storage, e)
			return
		}
	}
}

type probeRecordingConn struct {
	net.Conn
	record func(string, []byte)
}

func (c *probeRecordingConn) Read(b []byte) (int, error) {
	n, e := c.Conn.Read(b)
	if n > 0 {
		c.record("in", b[:n])
	}
	return n, e
}
func (c *probeRecordingConn) Write(b []byte) (int, error) {
	n, e := c.Conn.Write(b)
	if n > 0 {
		c.record("out", b[:n])
	}
	return n, e
}
