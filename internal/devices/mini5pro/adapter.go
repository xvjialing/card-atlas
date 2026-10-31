// Package mini5pro 实现 DJI Mini 5 Pro 的无线素材导入适配。
// 蓝牙用于 DJI Fly 首次授权，Wi-Fi 使用飞行器专用快传会话。
package mini5pro

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"cardatlas/internal/dji/preview"
	"cardatlas/internal/dji/transfer"
	"cardatlas/internal/platform"
)

const cameraHost = "192.168.2.1"

// Adapter 通过通用 Wi-Fi 会话驱动 DJI 传输协议；蓝牙负责首次授权。
type Adapter struct {
	mu         sync.Mutex
	media      *djiLink
	bluetooth  camera.Bluetooth
	ble        *camera.BLESession
	names      map[string]string
	host       *wifihost.Host
	client     *http.Client
	logs       *diagnostics.Store
	identity   string
	configFile string
}

func NewAdapter() *Adapter {
	base := os.Getenv("CARDATLAS_DEVICE_DATA")
	if base == "" {
		base, _ = os.UserConfigDir()
		base = filepath.Join(base, "CardAtlas", "devices")
	}
	s := &Adapter{configFile: filepath.Join(base, "mini5pro-identity.json"), client: transfer.Client(), logs: diagnostics.Shared(base)}
	var saved struct{ Identity string }
	if b, err := os.ReadFile(s.configFile); err == nil {
		_ = json.Unmarshal(b, &saved)
	}
	s.identity = saved.Identity
	if len(s.identity) != 32 {
		var id [16]byte
		_, _ = rand.Read(id[:])
		s.identity = hex.EncodeToString(id[:])
	}
	s.logs.RegisterSecrets(s.identity)
	s.names = map[string]string{}
	s.host = wifihost.New(
		wifihost.Config{Name: "DJI Mini 5 Pro", HotKeywords: []string{"dji", "mini"}, ReleasePreparationOnFailure: true, PreparationIdleTimeout: 90 * time.Second},
		wifihost.Actions{
			Scan:        platform.ScanWiFi,
			Join:        platform.JoinWiFi,
			CurrentSSID: platform.CurrentSSID,
			WaitReady:   platform.WaitCameraWiFi,
			Restore:     platform.RestoreWiFi,
			ScanDevices: s.scanAircraft,
			Prepare:     s.prepareAircraft,
			Teardown:    s.closeBLE,
		},
		platform.Credentials{},
		s.logs,
		s.openLink,
	)
	return s
}

// scanAircraft 扫描附近的 DJI 飞行器蓝牙广播。
func (s *Adapter) scanAircraft(ctx context.Context) ([]deviceimport.Device, error) {
	scanned, err := s.bluetooth.ScanAircraft(diagnostics.With(ctx, s.logs, nil))
	devices := make([]deviceimport.Device, 0, len(scanned))
	s.mu.Lock()
	s.names = map[string]string{}
	for _, d := range scanned {
		s.names[d.ID] = d.Name
		devices = append(devices, deviceimport.Device{ID: d.ID, Name: d.Name, RSSI: d.RSSI})
	}
	s.mu.Unlock()
	return devices, err
}

// prepareAircraft 走蓝牙配对，由用户长按机身电源键授权（与 DJI Fly 手机快传一致）。
func (s *Adapter) prepareAircraft(ctx context.Context, deviceID string, stage func(string)) error {
	s.closeBLE()
	if err := s.saveIdentity(); err != nil {
		return fmt.Errorf("无法保存无人机配对身份：%w", err)
	}
	ble, hint, err := s.bluetooth.PairAircraft(diagnostics.With(ctx, s.logs, nil), deviceID, s.identity, stage)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ble = ble
	name := s.names[deviceID]
	s.mu.Unlock()
	s.host.SetIdentity(deviceID, name)
	s.host.SetHint(hint)
	return nil
}

// 配对身份必须在请求授权前落盘，否则每次重启都会创建新的授权身份。
func (s *Adapter) saveIdentity() error {
	if err := os.MkdirAll(filepath.Dir(s.configFile), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.configFile), ".mini5pro-identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	data, _ := json.Marshal(struct{ Identity string }{s.identity})
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), s.configFile)
}

func (s *Adapter) closeBLE() {
	s.mu.Lock()
	ble := s.ble
	s.ble = nil
	s.mu.Unlock()
	if ble != nil {
		ble.Close()
	}
}

func (s *Adapter) Descriptor() deviceimport.Descriptor {
	return deviceimport.Descriptor{ID: "dji-mini5pro", Name: "DJI Mini 5 Pro", Brand: "DJI", Transport: "蓝牙授权 · Wi-Fi 传输", Available: runtime.GOOS == "darwin", Compatibility: "实验接入：授权与协议待实机验证", Storages: []deviceimport.Storage{{ID: "0", Name: "存储卡"}, {ID: "1", Name: "机身存储"}}}
}

func (s *Adapter) Snapshot() deviceimport.Session {
	st := s.host.State()
	out := deviceimport.Session{SessionToken: s.host.SessionToken(), DeviceID: s.host.DeviceID(), DeviceName: st.DeviceName, Phase: st.Phase, Message: st.Message, Warning: st.Warning, SSID: st.SSID, Busy: st.Busy, Revision: st.Revision, WiFiNetworks: st.WiFiNetworks, Media: st.Media}
	if out.WiFiNetworks == nil {
		out.WiFiNetworks = []deviceimport.Network{}
	}
	if out.Media == nil {
		out.Media = []deviceimport.Media{}
	}
	return out
}

func (s *Adapter) Discover() ([]deviceimport.Device, error) {
	return s.host.Discover()
}

func (s *Adapter) Connect(r deviceimport.ConnectionRequest) error {
	switch r.Action {
	case "prepare":
		return s.host.Prepare(r.DeviceID)
	case "scan_wifi":
		return s.host.ScanWiFi()
	case "join_wifi":
		return s.host.JoinWiFi(r.SSID, r.Password, r.UseSaved)
	case "forget_password":
		return s.host.ForgetPassword(r.SSID)
	case "manual":
		return s.host.ManualConnect()
	default:
		return errors.New("此设备不支持该连接操作")
	}
}

func (s *Adapter) Disconnect() error {
	s.closeBLE()
	return s.host.Disconnect()
}
func (s *Adapter) Refresh() error { return s.host.Refresh() }

// openLink 建立素材会话；适配器保留引用供下载与预览读取素材来源。
func (s *Adapter) openLink(ctx context.Context) (wifihost.MediaLink, error) {
	// 无论成功还是失败，素材链路建立尝试结束后不再用蓝牙持续唤醒飞行器。
	defer s.closeBLE()
	udpDialer, err := platform.CameraWiFiDialer("udp4", 9003)
	if err != nil {
		return nil, err
	}
	httpDialer, err := platform.CameraWiFiDialer("tcp4", 0)
	if err != nil {
		return nil, err
	}
	link, err := camera.OpenAircraftLink(ctx, cameraHost, s.identity, udpDialer)
	if err != nil {
		s.logs.Record("warn", "datalink", "link.open_failed_drone", "无人机素材会话未建立", map[string]string{"error": err.Error()})
		return nil, err
	}
	l := &djiLink{link: link}
	s.mu.Lock()
	s.client.CloseIdleConnections()
	s.client = transfer.ClientWithDialer(httpDialer)
	s.media = l
	s.mu.Unlock()
	return l, nil
}

func (s *Adapter) httpClient() *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

func aircraftMediaURL(storage int, p string) string {
	return camera.MediaURL(cameraHost, storage, p)
}

func (s *Adapter) currentMedia() *djiLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media == nil || !s.media.Alive() {
		return nil
	}
	return s.media
}

func (s *Adapter) Download(ctx context.Context, device string, m deviceimport.Media, root, relative string, progress func(deviceimport.Progress)) (deviceimport.Result, error) {
	media := s.currentMedia()
	if media == nil || s.host.DeviceID() != device {
		return deviceimport.Result{}, errors.New("请连接任务所属的原设备后重试")
	}
	source, ok := media.Source(m.ID)
	if !ok || normalize(source) != m {
		return deviceimport.Result{}, errors.New("素材已变化，请刷新清单后重新选择")
	}
	r, err := transfer.DownloadURL(diagnostics.With(ctx, s.logs, nil), s.httpClient(), aircraftMediaURL(source.Storage, source.Path), device, root, relative, source, func(p transfer.Progress) {
		progress(deviceimport.Progress{Bytes: p.Bytes, Total: p.Total, Speed: p.Speed, Phase: p.Phase})
	})
	return deviceimport.Result{Path: r.Path, SHA256: r.SHA256, Bytes: r.Bytes, Warning: r.Warning}, err
}

func (s *Adapter) previewLookup(id, session string) (camera.Media, bool) {
	if session == "" || session != s.host.SessionToken() || !s.host.Alive() || s.host.State().Phase != "connected" {
		return camera.Media{}, false
	}
	media := s.currentMedia()
	if media == nil {
		return camera.Media{}, false
	}
	m, ok := media.Source(id)
	if !ok || (m.Storage != 0 && m.Storage != 1) || !camera.SafePath(m.Path) {
		return camera.Media{}, false
	}
	return m, true
}

func (s *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler := &preview.Handler{
		Client: s.httpClient(),
		Host:   cameraHost,
		Logs:   s.logs,
		ConnectionID: func() string {
			return s.host.SessionToken()
		},
		Lookup:   s.previewLookup,
		MediaURL: aircraftMediaURL,
	}
	if r.URL.Query().Get("preview") == "1" {
		handler.Preview(w, r)
	} else {
		handler.Thumbnail(w, r)
	}
}

func normalize(m camera.Media) deviceimport.Media {
	return deviceimport.Media{ID: m.ID, Name: m.Name, Path: m.Path, StorageID: strconv.Itoa(m.Storage), StorageName: map[int]string{0: "存储卡", 1: "机身存储"}[m.Storage], Size: m.Size, SizeEstimated: true, Duration: m.Duration, Kind: m.Kind, Date: m.Date, DateSource: "设备清单（本机时区）", Thumbnail: m.Thumb != "" || m.ThumbnailSize > 0}
}

// djiSession 是 DJI 素材链路所需的最小能力集；测试中可替换。
type djiSession interface {
	Alive() bool
	Close()
	ListPage(ctx context.Context, storage int, cursor uint32) (camera.Page, error)
}

// djiLink 分别查询存储卡与机身存储，素材身份包含存储编号。
type djiLink struct {
	mu      sync.RWMutex
	link    djiSession
	sources map[string]camera.Media
}

func (l *djiLink) Alive() bool { return l.link.Alive() }
func (l *djiLink) Close()      { l.link.Close() }

func (l *djiLink) Source(id string) (camera.Media, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	m, ok := l.sources[id]
	return m, ok
}

func (l *djiLink) List(ctx context.Context) ([]deviceimport.Media, string, error) {
	files := []camera.Media{}
	seen := map[string]bool{}
	var warnings []string
	for _, storage := range []int{0, 1} {
		storageName := map[int]string{0: "存储卡", 1: "机身存储"}[storage]
		cursor := uint32(0)
		for pageIndex := 0; pageIndex < 200; pageIndex++ {
			page, err := l.link.ListPage(ctx, storage, cursor)
			if err != nil {
				warnings = append(warnings, storageName+"："+err.Error())
				break
			}
			added := 0
			for _, m := range page.Files {
				if !seen[m.ID] && camera.SafePath(m.Path) && path.Base(m.Name) == m.Name {
					seen[m.ID] = true
					files = append(files, m)
					added++
				}
			}
			if page.Complete {
				break
			}
			if added == 0 || page.Cursor == cursor {
				warnings = append(warnings, storageName+"分页未前进，当前列表可能不完整，请刷新重试")
				break
			}
			cursor = page.Cursor
			if pageIndex == 199 {
				warnings = append(warnings, storageName+"素材数量超过单次读取上限，当前列表可能不完整")
			}
		}
	}
	if len(files) == 0 && len(warnings) > 0 {
		return []deviceimport.Media{}, "", errors.New(strings.Join(warnings, "；"))
	}
	out := make([]deviceimport.Media, 0, len(files))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sources = map[string]camera.Media{}
	for _, m := range files {
		l.sources[m.ID] = m
		out = append(out, normalize(m))
	}
	return out, strings.Join(warnings, "；"), nil
}
