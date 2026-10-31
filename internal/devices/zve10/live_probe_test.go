//go:build sony_live_probe && darwin && cgo

package zve10

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/platform"

	"github.com/knallli/alpha2go/ptpip"
)

func TestLiveSonyPTPCapabilities(t *testing.T) {
	if os.Getenv("CARDATLAS_SONY_LIVE") != "1" {
		t.Skip("需显式启用 Sony 真机调试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	snapshot := platform.CurrentWiFi()
	ip, _, err := platform.DeviceWiFiAddressForPeers(snapshot, os.Getenv("CARDATLAS_SONY_SSID"), sonyPeers...)
	if err != nil {
		t.Fatal(err)
	}
	control, err := platform.WiFiSocketControl(snapshot, ip)
	if err != nil {
		t.Fatal(err)
	}
	dialer := net.Dialer{Timeout: 4 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IP(ip.AsSlice())}, Control: control}
	options := ptpip.Options{Name: "CardAtlas", DialContext: dialer.DialContext, IOTimeout: 6 * time.Second}
	if os.Getenv("CARDATLAS_SONY_PTP_STANDARD") == "1" {
		options.GUID[8] = 0xff
		options.GUID[9] = 0xff
	}
	c, err := ptpip.Dial(ctx, "192.168.122.1:15740", options)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	di, err := c.GetDeviceInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, op := range di.Operations {
		ops = append(ops, fmt.Sprintf("%04x", op))
	}
	t.Logf("PTP 机型=%s 厂家=%s 版本=%s 操作=%v", di.Model, di.Manufacturer, di.DeviceVersion, ops)
	if !di.IsSony() || di.Model != "ZV-E10" {
		t.Fatal("真机型号不是用户指定的一代 ZV-E10")
	}
	if err := c.OpenSession(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CARDATLAS_SONY_PTP_STANDARD") != "1" {
		if _, err := c.SonyHandshake(ctx); err != nil {
			t.Fatal(err)
		}
		_, propErr := c.Transaction(ctx, ptpip.OpSonyGetAllExtDevicePropInfo, []uint32{0}, nil, io.Discard)
		t.Logf("Sony 会话状态同步：%v", propErr)
		if err := c.SonySetContentsTransferMode(ctx); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if os.Getenv("CARDATLAS_SONY_PTP_STANDARD") != "1" {
			_ = c.SonyDisableContentsTransferMode(closeCtx)
		}
		_ = c.CloseSession(closeCtx)
	}()
	storages, err := c.GetStorageIDs(ctx)
	for try := 0; err == nil && len(storages) == 0 && try < 30; try++ {
		time.Sleep(150 * time.Millisecond)
		storages, err = c.GetStorageIDs(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PTP 存储数量=%d", len(storages))
	if len(storages) == 0 {
		t.Fatal("相机尚未发布存储")
	}
	handles, err := c.GetObjectHandles(ctx, storages[0], 0, ptpip.ParentAll)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PTP 对象数量=%d", len(handles))
	// 固件 2.01 的全量 GetObjectPropList 会超时，日常真机探测不再发送该已知不兼容请求。
	t.Logf("PTP 前几个句柄=%x", handles[:min(8, len(handles))])
	for _, handle := range handles[:min(100, len(handles))] {
		info, err := c.GetObjectInfo(ctx, handle)
		if err != nil {
			if ptpip.IsCode(err, ptpip.RespInvalidObjectHandle) {
				t.Logf("跳过无效句柄 %x", handle)
				continue
			}
			t.Fatal(err)
		}
		if info.IsFolder() || classify("", info.Filename) != "photo" {
			continue
		}
		var thumb bytes.Buffer
		_, err = c.Transaction(ctx, ptpip.OpGetThumb, []uint32{handle}, nil, &thumb)
		if err != nil {
			t.Fatal(err)
		}
		cfg, kind, err := image.DecodeConfig(bytes.NewReader(thumb.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("PTP 照片缩略图=%s %d×%d，原片大小=%d", kind, cfg.Width, cfg.Height, info.CompressedSize)
		var part bytes.Buffer
		n, err := c.GetPartialObject(ctx, handle, 0, 65536, &part)
		t.Logf("PTP 分段读取：字节=%d 类型=%s 错误=%v", n, http.DetectContentType(part.Bytes()), err)
		if err != nil && di.Supports(ptpip.OpSonyGetPartialLargeObject) {
			part.Reset()
			n, err = c.SonyGetPartialLargeObject(ctx, handle, 0, 65536, &part)
			t.Logf("Sony 分段读取：字节=%d 类型=%s 错误=%v", n, http.DetectContentType(part.Bytes()), err)
		}
		if err != nil || n == 0 {
			t.Fatalf("真机分段读取未成功：%d %v", n, err)
		}
		return
	}
	t.Fatal("当前 PTP 对象中没有照片")
}

// 显式授权后使用已连接的相机真机验证生产发现、清单与受控预览，不扫描/切换 Wi-Fi 或读取钥匙串。
// 素材仅在内存中检查，不输出文件名、内容或保存相片；不会执行导入、删除或拍摄。
func TestLiveSonyLibraryAndPreview(t *testing.T) {
	if os.Getenv("CARDATLAS_SONY_LIVE") != "1" {
		t.Skip("需显式启用 Sony 真机调试")
	}
	selected := os.Getenv("CARDATLAS_SONY_SSID")
	if selected == "" {
		t.Fatal("需指定用户已选择的相机热点")
	}
	logs := diagnostics.New(t.TempDir())
	logs.RegisterSecrets(selected)
	s := &Adapter{logs: logs}
	s.host = wifihost.New(wifihost.Config{Name: "Sony ZV-E10", Timeout: 4 * time.Minute}, wifihost.Actions{
		// 命令行常受 macOS 的 SSID 隐私限制；显式参数仅充当界面已选目标，不更改系统网络。
		CurrentSSID: func() string { return selected },
		WaitReady: func(ctx context.Context, _ string) error {
			return platform.WaitDeviceWiFiForPeers(ctx, selected, sonyPeers...)
		},
	}, &memoryCredentials{values: map[string]string{}}, logs, s.openLink)
	t.Cleanup(func() { _ = s.Disconnect() })
	defer func() {
		page, _ := logs.Read(diagnostics.Query{Limit: 80})
		for i := len(page.Entries) - 1; i >= 0; i-- {
			e := page.Entries[i]
			t.Log(e.Event, e.Message, e.Fields)
		}
	}()
	if err := s.host.ManualConnect(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		state := s.Snapshot()
		if !state.Busy {
			if state.Phase != "connected" {
				t.Fatalf("真机素材连接失败，阶段=%s：%s", state.Phase, state.Message)
			}
			t.Logf("真机连接成功：素材数量=%d", len(state.Media))
			if len(state.Media) == 0 {
				t.Fatal("相机当前选择范围没有素材，无法检查预览")
			}
			photos, videos := 0, 0
			for _, media := range state.Media {
				if media.Kind == "photo" {
					photos++
				} else if media.Kind == "video" {
					videos++
				}
			}
			t.Logf("素材类型：照片=%d 视频=%d", photos, videos)
			checked := false
			for _, media := range state.Media {
				if media.Kind != "photo" {
					continue
				}
				thumb := requestPreview(t, s, media, false, "", state.SessionToken)
				if thumb.Code != http.StatusOK {
					t.Fatalf("真机缩略图返回 HTTP %d", thumb.Code)
				}
				cfg, kind, err := image.DecodeConfig(thumb.Body)
				if err != nil {
					t.Fatalf("真机缩略图解码失败：%v", err)
				}
				t.Logf("真机缩略图成功：%s，%d×%d", kind, cfg.Width, cfg.Height)
				if previewKind(media.Name) != "image/jpeg" {
					checked = true
					break
				}
				// 只读照片的前 64 KiB，防止相机忽略 Range 时把整张照片读入测试。
				req := httptest.NewRequest(http.MethodGet, "http://localhost/device?id="+media.ID+"&session="+state.SessionToken+"&preview=1", nil)
				req.Header.Set("Range", "bytes=0-65535")
				preview := &limitedPreviewWriter{ResponseRecorder: httptest.NewRecorder(), limit: 64 << 10}
				s.ServeHTTP(preview, req)
				if preview.Code != http.StatusOK && preview.Code != http.StatusPartialContent {
					t.Fatalf("真机照片预览返回 HTTP %d", preview.Code)
				}
				if !strings.HasPrefix(http.DetectContentType(preview.Body.Bytes()), "image/") {
					t.Fatal("照片预览没有返回可识别的图片字节")
				}
				t.Logf("真机照片预览成功：HTTP %d，已检查 %d 字节", preview.Code, preview.Body.Len())
				checked = true
				break
			}
			if !checked {
				t.Fatal("当前素材范围没有可验证的照片")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等待真机连接超时")
}

type limitedPreviewWriter struct {
	*httptest.ResponseRecorder
	limit int
}

func (w *limitedPreviewWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.Body.Len()
	if remaining <= 0 {
		return 0, io.ErrClosedPipe
	}
	if len(p) > remaining {
		n, _ := w.ResponseRecorder.Write(p[:remaining])
		return n, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}
