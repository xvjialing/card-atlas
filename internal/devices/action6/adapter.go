package action6

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"cardatlas/internal/dji/transfer"
)

type State struct {
	WiFiNetworks []WiFiCandidate `json:"wifiNetworks"`
	Phase        string          `json:"phase"`
	Message      string          `json:"message"`
	Warning      string          `json:"warning"`
	DeviceName   string          `json:"deviceName"`
	SSID         string          `json:"ssid"`
	Media        []camera.Media  `json:"media"`
	Busy         bool            `json:"busy"`
	Revision     int64           `json:"revision"`
}
type Adapter struct {
	mu                                           sync.Mutex
	op                                           sync.Mutex
	state                                        State
	identity, configFile, deviceID, previousSSID string
	switched                                     bool
	bluetooth                                    camera.Bluetooth
	ble                                          bleConnection
	link                                         mediaConnection
	cancelOp                                     context.CancelFunc
	client                                       *http.Client
	logs                                         *diagnostics.Store
	connectionID                                 string
	lostLogged                                   bool
	connection                                   connectionActions
	credentials                                  credentialStore
	wifiHint                                     string
	deviceNames                                  map[string]string
}

func NewAdapter() *Adapter {
	base := os.Getenv("CARDATLAS_DEVICE_DATA")
	if base == "" {
		base, _ = os.UserConfigDir()
		base = filepath.Join(base, "CardAtlas", "devices")
	}
	s := &Adapter{state: State{Phase: "disconnected", Message: "连接相机，开始导入原始素材", Media: []camera.Media{}, WiFiNetworks: []WiFiCandidate{}}, configFile: filepath.Join(base, "action6-identity.json"), client: transfer.Client(), logs: diagnostics.Shared(base)}
	s.initConnectionActions()
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
	return s
}
func (s *Adapter) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.configFile), 0700); err != nil {
		return err
	}
	b, _ := json.Marshal(struct{ Identity string }{s.identity})
	if err := os.WriteFile(s.configFile+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(s.configFile+".tmp", s.configFile)
}
func (s *Adapter) getStateLocked() State {
	out := s.state
	out.WiFiNetworks = append([]WiFiCandidate{}, out.WiFiNetworks...)
	out.Media = append([]camera.Media{}, out.Media...)
	if s.link != nil && out.Phase == "connected" && !s.link.Alive() {
		out.Phase = "lost"
		out.Message = "设备连接已中断，请重新连接后继续导入"
	}
	return out
}
func (s *Adapter) GetState() State { s.mu.Lock(); defer s.mu.Unlock(); return s.getStateLocked() }
func (s *Adapter) Descriptor() deviceimport.Descriptor {
	return deviceimport.Descriptor{ID: "dji-action6", Name: "DJI Osmo Action 6", Brand: "DJI", Transport: "蓝牙配对 · Wi-Fi 传输", Available: runtime.GOOS == "darwin", Compatibility: "已接入，待实机验证", Storages: []deviceimport.Storage{{ID: "1", Name: "机身存储"}, {ID: "0", Name: "存储卡"}}}
}
func (s *Adapter) Snapshot() deviceimport.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.getStateLocked()
	out := deviceimport.Session{SessionToken: s.connectionID, DeviceID: s.deviceID, DeviceName: v.DeviceName, Phase: v.Phase, Message: v.Message, Warning: v.Warning, SSID: v.SSID, Busy: v.Busy, Revision: v.Revision, WiFiNetworks: []deviceimport.Network{}, Media: []deviceimport.Media{}}
	for _, n := range v.WiFiNetworks {
		out.WiFiNetworks = append(out.WiFiNetworks, deviceimport.Network{SSID: n.SSID, RSSI: n.RSSI, Security: n.Security, Channel: n.Channel, Supported: n.Supported, Likely: n.Likely, Reason: n.Reason, HasSavedPassword: n.HasSavedPassword})
	}
	for _, m := range v.Media {
		out.Media = append(out.Media, normalize(m))
	}
	return out
}
func normalize(m camera.Media) deviceimport.Media {
	return deviceimport.Media{ID: m.ID, Name: m.Name, Path: m.Path, StorageID: strconv.Itoa(m.Storage), StorageName: map[int]string{0: "SD 卡", 1: "内置存储"}[m.Storage], Size: m.Size, SizeEstimated: true, Duration: m.Duration, Kind: m.Kind, Date: m.Date, DateSource: "设备清单（本机时区）", Thumbnail: m.Thumb != ""}
}
func (s *Adapter) Discover() ([]deviceimport.Device, error) {
	items, err := s.Scan()
	out := []deviceimport.Device{}
	for _, d := range items {
		out = append(out, deviceimport.Device{ID: d.ID, Name: d.Name, RSSI: d.RSSI})
	}
	return out, err
}
func (s *Adapter) Connect(r deviceimport.ConnectionRequest) error {
	switch r.Action {
	case "prepare":
		return s.PrepareCamera(r.DeviceID)
	case "scan_wifi":
		return s.ScanWiFi()
	case "join_wifi":
		return s.JoinCameraWiFi(r.SSID, r.Password, r.UseSaved)
	case "forget_password":
		return s.ForgetWiFiPassword(r.SSID)
	case "manual":
		return s.connectManual("", true)
	default:
		return errors.New("此设备不支持该连接操作")
	}
}
func (s *Adapter) Download(ctx context.Context, device string, m deviceimport.Media, root, relative string, progress func(deviceimport.Progress)) (deviceimport.Result, error) {
	s.mu.Lock()
	if s.deviceID != device || s.link == nil || !s.link.Alive() {
		s.mu.Unlock()
		return deviceimport.Result{}, errors.New("请连接任务所属的原设备后重试")
	}
	var source camera.Media
	for _, item := range s.state.Media {
		if item.ID == m.ID {
			source = item
			break
		}
	}
	if source.ID == "" || normalize(source) != m {
		s.mu.Unlock()
		return deviceimport.Result{}, errors.New("素材已变化，请刷新清单后重新选择")
	}
	s.mu.Unlock()
	r, err := transfer.DownloadTo(diagnostics.With(ctx, s.logs, nil), s.client, "192.168.2.1", device, root, relative, source, func(p transfer.Progress) {
		progress(deviceimport.Progress{Bytes: p.Bytes, Total: p.Total, Speed: p.Speed, Phase: p.Phase})
	})
	return deviceimport.Result{Path: r.Path, SHA256: r.SHA256, Bytes: r.Bytes, Warning: r.Warning}, err
}
func (s *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("preview") == "1" {
		s.servePreview(w, r)
	} else {
		s.serveThumbnail(w, r)
	}
}
