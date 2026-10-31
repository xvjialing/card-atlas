package zve10

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cardatlas/internal/upnpav"

	"github.com/knallli/alpha2go/ptpip/ptpiptest"
)

func simulatedPTPLink(t *testing.T, model string, native ...bool) (*upnpLink, *ptpiptest.Server, []byte) {
	t.Helper()
	dir := t.TempDir()
	data := bytes.Repeat([]byte("PTP 原始字节核对"), 50000)
	for _, file := range []string{"DCIM/100MSDCF/DSC00001.JPG", "PRIVATE/M4ROOT/CLIP/C0001.MP4"} {
		p := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	options := ptpiptest.Options{Dir: dir, A6700: true, NoRemote: true, Model: model}
	if len(native) > 0 && native[0] {
		options.A6700 = false
		options.Sony = true
	}
	sim, err := ptpiptest.Start(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: time.Second}
	link, err := openPTPMedia(ctx, &upnpav.Server{Location: "http://127.0.0.1:64321/DmsDescPush.xml"}, func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "127.0.0.1:15740" {
			t.Errorf("PTP 离开了已发现相机：%s", addr)
		}
		return dialer.DialContext(ctx, network, sim.Addr())
	})
	if model != "ZV-E10" {
		if err == nil {
			link.Close()
			t.Fatal("PTP 真实型号不符时仍建立连接")
		}
		return nil, sim, data
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(link.Close)
	return link, sim, data
}

func TestPTPNativeMediaKeepsCameraMode(t *testing.T) {
	link, sim, _ := simulatedPTPLink(t, "ZV-E10", true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	media, _, err := link.List(ctx)
	if err != nil || len(media) != 2 {
		t.Fatalf("原生素材会话失败：%d %v", len(media), err)
	}
	link.Close()
	if sim.Xfer9212.Load() != 0 {
		t.Fatal("原生发送模式被不必要地切换")
	}
}

func TestPTPMediaListRangesAndEarlyClosePreserveSession(t *testing.T) {
	link, sim, data := simulatedPTPLink(t, "ZV-E10")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	media, warning, err := link.List(ctx)
	if err != nil || len(media) != 2 || warning != "" {
		t.Fatalf("PTP 清单不完整：数量=%d 警告=%q 错误=%v", len(media), warning, err)
	}
	if sim.Xfer9212.Load() != 1 {
		t.Fatal("没有建立素材读取模式")
	}
	for _, m := range media {
		if m.Size != int64(len(data)) || m.DateSource != "相机媒体服务" {
			t.Fatalf("PTP 大小或来源错误：%+v", m)
		}
		item, _ := link.Item(m.ID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, item.Res, nil)
		req.Header.Set("Range", "bytes=524300-524399")
		resp, err := link.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 206 || !bytes.Equal(got, data[524300:524400]) {
			t.Fatalf("Sony 分段字节不正确：HTTP %d，%d 字节，%v", resp.StatusCode, len(got), err)
		}
		full, _ := http.NewRequestWithContext(ctx, http.MethodGet, item.Res, nil)
		resp, err = link.client.Do(full)
		if err != nil {
			t.Fatal(err)
		}
		one := make([]byte, 1)
		if _, err := resp.Body.Read(one); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// 提前关闭只舍弃当前块；后续请求仍能在同一 PTP 会话正常读取。
		resp, err = link.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !bytes.Equal(got, data[524300:524400]) || !link.Alive() {
			t.Fatalf("关闭预览破坏了会话：%v", err)
		}
		foreign := httptest.NewRequest(http.MethodGet, strings.Replace(item.Res, "127.0.0.1", "192.0.2.1", 1), nil)
		if _, err := link.client.Transport.RoundTrip(foreign); err == nil {
			t.Fatal("允许任意地址访问 PTP 会话")
		}
	}
	link.Close()
	if link.Alive() || sim.Xfer9212.Load() != 2 {
		t.Fatal("关闭后仍占用相机素材模式")
	}
}

func TestPTPRejectsOtherModelAndCancelledQueue(t *testing.T) {
	simulatedPTPLink(t, "ZV-E10M2")
	link, _, _ := simulatedPTPLink(t, "ZV-E10")
	p := link.ptp
	if err := p.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.lock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("排队取消失败：%v", err)
	}
	p.unlock()
	if !p.alive() {
		t.Fatal("取消排队请求关闭了共享 PTP 会话")
	}
}

func TestPTPRangesKeep64BitSize(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		size, start, end int64
		status           int
	}{
		{"", 100, 0, 100, 200}, {"bytes=5-9", 100, 5, 10, 206}, {"bytes=-10", 100, 90, 100, 206},
		{"bytes=90-", 100, 90, 100, 206}, {"bytes=90-999", 100, 90, 100, 206},
		{"bytes=4294967296-4294967300", 1 << 33, 1 << 32, (1 << 32) + 5, 206},
	} {
		start, end, status, err := ptpRange(tc.raw, tc.size)
		if err != nil || start != tc.start || end != tc.end || status != tc.status {
			t.Fatalf("范围截断或解析错误：%+v，%d-%d HTTP %d %v", tc, start, end, status, err)
		}
	}
	for _, raw := range []string{"bytes=100-", "bytes=9-5", "bytes=-0", "bytes=0-5,8-9", "bytes=9223372036854775808-", "items=0-9"} {
		if _, _, _, err := ptpRange(raw, 100); err == nil {
			t.Errorf("接受了无效范围：%s", raw)
		}
	}

}
