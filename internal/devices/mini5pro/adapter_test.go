package mini5pro

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/diagnostics"
	dji "cardatlas/internal/dji"
	"cardatlas/internal/platform"
)

func TestAircraftIdentitySurvivesRestart(t *testing.T) {
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	first := NewAdapter()
	if err := first.saveIdentity(); err != nil {
		t.Fatal(err)
	}
	second := NewAdapter()
	if first.identity != second.identity || len(second.identity) != 32 {
		t.Fatal("重启丢失配对身份")
	}
	info, err := os.Stat(first.configFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("配对身份文件权限应为 0600")
	}
}

func TestAircraftHTTPMatchesCapturedRelativePath(t *testing.T) {
	got := aircraftMediaURL(0, "DCIM/100MEDIA/a & b.MP4")
	if !strings.Contains(got, "path=DCIM/100MEDIA/a%20%26%20b.MP4") {
		t.Fatal(got)
	}
}

type fakeActions struct {
	joined  []string
	current string
}

func (f *fakeActions) Scan(context.Context) ([]platform.WiFiNetwork, error) {
	return []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", RSSI: -40, Security: "WPA2", Supported: true}}, nil
}
func (f *fakeActions) Join(ssid, password string) error {
	f.joined = append(f.joined, ssid+"\x00"+password)
	return nil
}
func (f *fakeActions) CurrentSSID() string                     { return f.current }
func (f *fakeActions) WaitReady(context.Context, string) error { return nil }
func (f *fakeActions) Restore(context.Context, string) error   { return nil }

type fakeSession struct {
	alive         bool
	closed        int
	pages         map[uint32]dji.Page
	internalPages map[uint32]dji.Page
}

func (f *fakeSession) Alive() bool { return f.alive }
func (f *fakeSession) Close()      { f.closed++ }
func (f *fakeSession) ListPage(_ context.Context, storage int, cursor uint32) (dji.Page, error) {
	if storage != 0 {
		if storage != 1 {
			return dji.Page{}, errors.New("无效存储")
		}
		if f.internalPages == nil {
			return dji.Page{Files: []dji.Media{}, Complete: true, Storage: 1}, nil
		}
		return f.internalPages[cursor], nil
	}
	page, ok := f.pages[cursor]
	if !ok {
		return dji.Page{}, errors.New("相机未响应素材查询，请断开后重新连接")
	}
	return page, nil
}

func newTestAdapter(t *testing.T, session *fakeSession) *Adapter {
	t.Helper()
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	s := NewAdapter()
	actions := &fakeActions{current: "HomeNet"}
	s.host = wifihost.New(
		wifihost.Config{Name: "DJI Mini 5 Pro", HotKeywords: []string{"dji", "mini"}, Timeout: 10 * time.Second},
		wifihost.Actions{Scan: actions.Scan, Join: actions.Join, CurrentSSID: actions.CurrentSSID, WaitReady: actions.WaitReady, Restore: actions.Restore},
		platform.Credentials{},
		s.logs,
		func(context.Context) (wifihost.MediaLink, error) { return &djiLink{link: session}, nil },
	)
	return s
}

func waitPhase(t *testing.T, s *Adapter, phase string) deviceimport.Session {
	t.Helper()
	for i := 0; i < 300; i++ {
		st := s.Snapshot()
		if st.Phase == phase && !st.Busy {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时，当前 %+v", phase, s.Snapshot())
	return deviceimport.Session{}
}

func TestDescriptorListsBothCapturedStorages(t *testing.T) {
	s := newTestAdapter(t, &fakeSession{})
	d := s.Descriptor()
	if d.ID != "dji-mini5pro" || d.Brand != "DJI" || !d.Available {
		t.Fatalf("描述错误：%+v", d)
	}
	if len(d.Storages) != 2 || d.Storages[0].ID != "0" || d.Storages[0].Name != "存储卡" || d.Storages[1].ID != "1" || d.Storages[1].Name != "机身存储" {
		t.Fatalf("未显示抓包验证的两个存储：%+v", d.Storages)
	}
}

func TestHostWithoutBluetoothHooksRejectsPairingAction(t *testing.T) {
	s := newTestAdapter(t, &fakeSession{})
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "prepare", DeviceID: "xx"}); err == nil {
		t.Fatal("蓝牙配对动作不应被接受")
	}
	if _, err := s.Discover(); err == nil {
		t.Fatal("未配置蓝牙钩子的测试会话不应扫描蓝牙")
	}
}

func TestLinkListPagesCardAndInternalStorage(t *testing.T) {
	session := &fakeSession{alive: true, pages: map[uint32]dji.Page{}}
	session.pages[0] = dji.Page{Files: []dji.Media{
		{ID: "a", Name: "DJI_0001.MP4", Path: "DCIM/100MEDIA/DJI_0001.MP4", Storage: 0, Size: 10, Kind: "video", Date: "2026-10-23 10:30:00", Handle: 1},
		{ID: "b", Name: "DJI_0002.JPG", Path: "DCIM/100MEDIA/DJI_0002.JPG", Storage: 0, Size: 20, Kind: "photo", Date: "2026-10-23 10:31:00", Handle: 2},
	}, Complete: false, Cursor: 100}
	session.pages[100] = dji.Page{Files: []dji.Media{
		{ID: "c", Name: "DJI_0003.JPG", Path: "DCIM/100MEDIA/DJI_0003.JPG", Storage: 0, Size: 30, Kind: "photo", Date: "2026-10-23 10:32:00", Handle: 3},
	}, Complete: true, Cursor: 0}
	session.internalPages = map[uint32]dji.Page{0: {Files: []dji.Media{{ID: "internal", Name: "DJI_0004.DNG", Path: "DCIM/100MEDIA/DJI_0004.dng", Storage: 1, Size: 40, Kind: "photo", ThumbnailSize: 1024}}, Complete: true}}
	link := &djiLink{link: session}
	files, warning, err := link.List(context.Background())
	if err != nil || warning != "" {
		t.Fatalf("清单读取失败：%v %q", err, warning)
	}
	if len(files) != 4 {
		t.Fatalf("分页未合并：%d", len(files))
	}
	for _, m := range files {
		if m.ID == "internal" {
			if m.StorageID != "1" || m.StorageName != "机身存储" || !m.Thumbnail {
				t.Fatalf("机身素材映射错误：%+v", m)
			}
			continue
		}
		if m.StorageID != "0" || m.StorageName != "存储卡" || m.SizeEstimated != true {
			t.Fatalf("素材标准化错误：%+v", m)
		}
	}
	if _, ok := link.Source("b"); !ok {
		t.Fatal("来源映射缺少素材")
	}
	if session.closed != 0 {
		t.Fatal("列表读取不应关闭会话")
	}
}

func TestLinkListReportsEmptyStorage(t *testing.T) {
	session := &fakeSession{alive: true, pages: map[uint32]dji.Page{}}
	link := &djiLink{link: session}
	if _, _, err := link.List(context.Background()); err == nil {
		t.Fatal("空存储应返回错误")
	}
}

func TestDownloadRequiresLiveSessionAndMatchingDevice(t *testing.T) {
	session := &fakeSession{alive: true, pages: map[uint32]dji.Page{}}
	s := newTestAdapter(t, session)
	if _, err := s.Download(context.Background(), "device-a", deviceimport.Media{ID: "a"}, "/", "x", func(deviceimport.Progress) {}); err == nil {
		t.Fatal("未连接时下载应失败")
	}
}

func TestPreviewLookupGuardsSessionAndPaths(t *testing.T) {
	session := &fakeSession{alive: true, pages: map[uint32]dji.Page{}}
	s := newTestAdapter(t, session)
	link := &djiLink{link: session}
	s.mu.Lock()
	s.media = link
	s.mu.Unlock()
	media := dji.Media{ID: "fixture", Name: "TEST.MP4", Path: "DCIM/TEST/TEST.MP4", Storage: 0, Kind: "video"}
	session.pages[0] = dji.Page{Files: []dji.Media{media}, Complete: true}
	// 通过通用会话建立 connected 状态，并使用受控的素材来源。
	actions := &fakeActions{current: "HomeNet"}
	s.host = wifihost.New(
		wifihost.Config{Name: "DJI Mini 5 Pro", Timeout: 10 * time.Second},
		wifihost.Actions{Scan: actions.Scan, Join: actions.Join, CurrentSSID: actions.CurrentSSID, WaitReady: actions.WaitReady, Restore: actions.Restore},
		platform.Credentials{},
		diagnostics.New(t.TempDir()),
		func(context.Context) (wifihost.MediaLink, error) { return link, nil },
	)
	if err := s.host.ScanWiFi(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, "choosing_wifi")
	if err := s.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, "connected")
	token := s.host.SessionToken()
	if m, ok := s.previewLookup("fixture", token); !ok || m.Path != "DCIM/TEST/TEST.MP4" {
		t.Fatalf("受控素材查找失败：%+v %v", m, ok)
	}
	for _, tc := range []struct{ id, session string }{
		{"fixture", ""}, {"fixture", "old-session"}, {"unknown", token}, {"fixture/extra", token},
	} {
		if _, ok := s.previewLookup(tc.id, tc.session); ok {
			t.Fatalf("非受控查找被接受：%+v", tc)
		}
	}
	safe := media
	for _, p := range []string{"../secret", "DCIM/../secret", "DCIM/test\\file", "DCIM/t\x00est"} {
		session.pages[0] = dji.Page{Files: []dji.Media{merge(safe, p)}, Complete: true}
		link.sources = nil
		files, _, err := link.List(context.Background())
		if err != nil || len(files) != 0 {
			t.Fatalf("危险路径未被跳过：%q -> %v %v", p, files, err)
		}
	}
}

func merge(m dji.Media, path string) dji.Media {
	m.Path = path
	m.ID = "mutated"
	return m
}

func TestServeHTTPRejectsUnknownProviderIsHandledUpstream(t *testing.T) {
	// serveMedia 在 DeviceService 层按 provider 分发；这里只确认处理函数存在。
	s := newTestAdapter(t, &fakeSession{})
	recorder := &statusRecorder{header: http.Header{}}
	s.ServeHTTP(recorder, mustRequest("/device-media?preview=1&id=x&session="))
	if recorder.status != http.StatusNotFound {
		t.Fatalf("无会话预览应 404：%d", recorder.status)
	}
}

type statusRecorder struct {
	header http.Header
	status int
}

func (r *statusRecorder) Header() http.Header { return r.header }
func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(b), nil
}
func (r *statusRecorder) WriteHeader(status int) { r.status = status }

func mustRequest(target string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, target, nil)
	return r
}
