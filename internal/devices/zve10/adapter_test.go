package zve10

import (
	"context"
	"runtime"
	"testing"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/platform"
	"cardatlas/internal/upnpav"
)

// fakeActions 替代系统能力，记录加入的热点。
type fakeActions struct {
	networks []platform.WiFiNetwork
	joined   []string
}

func (f *fakeActions) Scan(context.Context) ([]platform.WiFiNetwork, error) {
	return append([]platform.WiFiNetwork{}, f.networks...), nil
}
func (f *fakeActions) Join(ssid, password string) error {
	f.joined = append(f.joined, ssid+"\x00"+password)
	return nil
}
func (f *fakeActions) CurrentSSID() string                     { return "HomeNet" }
func (f *fakeActions) WaitReady(context.Context, string) error { return nil }
func (f *fakeActions) Restore(context.Context, string) error   { return nil }

func fakeNetworks(f *fakeActions, ssids ...string) {
	f.networks = nil
	for i, ssid := range ssids {
		f.networks = append(f.networks, platform.WiFiNetwork{SSID: ssid, RSSI: -40 - i, Security: "WPA2", Supported: true})
	}
}

// fakeLink 提供最小的素材会话替身。
type fakeLink struct{}

func (fakeLink) Alive() bool { return true }
func (fakeLink) Close()      {}
func (fakeLink) List(context.Context) ([]deviceimport.Media, string, error) {
	return []deviceimport.Media{{ID: "m1", Name: "C0001.MP4", Path: "100MSDCF/C0001.MP4", StorageID: "0", StorageName: "存储卡", Size: 4096, Kind: "video", Date: "2026-10-23 10:30:00"}}, "", nil
}

func newTestAdapter(t *testing.T) (*Adapter, *fakeActions) {
	t.Helper()
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	s := NewAdapter()
	actions := &fakeActions{}
	s.host = wifihost.New(
		wifihost.Config{Name: "Sony ZV-E10", HotKeywords: []string{"sony", "direct-", "zv"}, Timeout: 10 * time.Second},
		wifihost.Actions{Scan: actions.Scan, Join: actions.Join, CurrentSSID: actions.CurrentSSID, WaitReady: actions.WaitReady, Restore: actions.Restore},
		&memoryCredentials{values: map[string]string{}},
		s.logs,
		func(context.Context) (wifihost.MediaLink, error) { return fakeLink{}, nil },
	)
	t.Cleanup(func() { _ = s.Disconnect() })
	return s, actions
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

func TestDescriptor(t *testing.T) {
	s, _ := newTestAdapter(t)
	d := s.Descriptor()
	if d.ID != "sony-zve10" || d.Brand != "Sony" || d.Available != (runtime.GOOS == "darwin") || d.Compatibility == "" {
		t.Fatalf("描述错误：%+v", d)
	}
	if _, err := s.Discover(); err == nil {
		t.Fatal("ZV-E10 不应支持蓝牙发现")
	}
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "prepare"}); err == nil {
		t.Fatal("未知连接动作应报错")
	}
}

// 实机截图中的 DHCP 地址必须通过连接预检，且发现网段同时覆盖 Mac 与相机网关。
func TestSonyDHCPNetworkFromScreenshot(t *testing.T) {
	snapshot := platform.WiFiSnapshot{SSID: "DIRECT-RLE1:ZV-E10", Interface: "en1", Addresses: []string{"192.168.0.5/16"}}
	ip, subnet, err := platform.DeviceWiFiAddressForPeers(snapshot, snapshot.SSID, sonyPeers...)
	if err != nil || ip.String() != "192.168.0.5" || subnet.String() != "192.168.0.0/16" {
		t.Fatalf("截图中的实际网络被拒绝：%s %s %v", ip, subnet, err)
	}
	for _, peer := range sonyPeers {
		if peer.String() == "192.168.122.1" && subnet.Contains(peer) {
			return
		}
	}
	t.Fatal("未将相机网关纳入 SSDP 发现网段")
}

func TestSonyGenericDescriptionRequiresSelectedFirstGenerationHotspot(t *testing.T) {
	server := upnpav.Server{FriendlyName: "SonyDigitalMediaServer", Manufacturer: "Sony Corporation", ModelName: "SonyImagingDevice", ControlURL: "http://192.168.122.1:64321/upnp/control/ContentDirectory", PushControlURL: "http://192.168.122.1:64321/upnp/control/XPushList"}
	for _, tc := range []struct {
		ssid string
		want bool
	}{
		{"DIRECT-RLE1:ZV-E10", true}, {"DIRECT-abc:zv-e10", true},
		{"", false}, {"<redacted>", false}, {"Home", false}, {"DIRECT-:ZV-E10", false},
		{"DIRECT-abc:ZV-E10M2", false}, {"DIRECT-abc:ZV-E10 II", false}, {"DIRECT-abc:ILCE-6400", false},
		{"Home:ZV-E10", false}, {"DIRECT-abc:ZV-E10:Other", false},
	} {
		if got := isZVE10OnHotspot(&server, tc.ssid); got != tc.want {
			t.Errorf("通用服务与热点匹配错误：%q，得到 %v", tc.ssid, got)
		}
	}
	for _, field := range []string{"manufacturer", "friendlyName", "modelName", "contentDirectory", "pushList"} {
		other := server
		switch field {
		case "manufacturer":
			other.Manufacturer = "Other"
		case "friendlyName":
			other.FriendlyName = "Other server"
		case "modelName":
			other.ModelName = "ZV-E10M2"
		case "contentDirectory":
			other.ControlURL = ""
		case "pushList":
			other.PushControlURL = ""
		}
		if isZVE10OnHotspot(&other, "DIRECT-RLE1:ZV-E10") {
			t.Errorf("错误接受了不匹配字段：%s", field)
		}
	}
}
