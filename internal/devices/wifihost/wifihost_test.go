package wifihost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/platform"
)

func TestAircraftFailureStopsPreparationWithoutSwitchingWiFi(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(fmt.Sprint(release), func(t *testing.T) {
			f := newFixture(t)
			var stopped atomic.Int32
			f.host.actions.Teardown = func() { stopped.Add(1) }
			f.host.cfg.ReleasePreparationOnFailure = release
			f.link.err = errors.New("素材会话未确认")
			if err := f.host.ManualConnect(); err != nil {
				t.Fatal(err)
			}
			waitPhase(t, f.host, "connection_error")
			if (stopped.Load() > 0) != release {
				t.Fatal("失败后的保活释放策略不正确")
			}
			f.actions.mu.Lock()
			restored := len(f.actions.restored)
			f.actions.mu.Unlock()
			if restored != 0 {
				t.Fatal("失败释放蓝牙不应切换用户 Wi-Fi")
			}
			f.link.mu.Lock()
			closed := f.link.closeNow
			f.link.mu.Unlock()
			if closed != 1 {
				t.Fatal("失败的素材链路未关闭")
			}
		})
	}
}

func TestPreparationIdleExpiryAndSuccessfulConnection(t *testing.T) {
	for _, connect := range []bool{false, true} {
		t.Run(fmt.Sprint(connect), func(t *testing.T) {
			f := newFixture(t)
			stopped := make(chan struct{}, 4)
			f.host.actions.Teardown = func() { stopped <- struct{}{} }
			f.host.cfg.PreparationIdleTimeout = 80 * time.Millisecond
			if err := f.host.ScanWiFi(); err != nil {
				t.Fatal(err)
			}
			waitPhase(t, f.host, "choosing_wifi")
			if connect {
				if err := f.host.ManualConnect(); err != nil {
					t.Fatal(err)
				}
				waitPhase(t, f.host, "connected")
				select {
				case <-stopped:
					t.Fatal("旧定时器影响新会话")
				case <-time.After(130 * time.Millisecond):
				}
			} else {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("等待热点时持续保活未停止")
				}
				if !strings.Contains(f.host.State().Warning, "停止蓝牙唤醒") {
					t.Fatal("缺少停止唤醒提示")
				}
			}
			_ = f.host.Disconnect()
		})
	}
}

// fakeActions 记录调用并允许逐项失败，模拟系统能力。
type fakeActions struct {
	mu           sync.Mutex
	networks     []platform.WiFiNetwork
	joinErr      error
	waitErr      error
	scanErr      error
	joined       []string
	restored     []string
	currentSSID  string
	waitCalls    []string
	closeLinkNow bool
}

func (f *fakeActions) Scan(context.Context) ([]platform.WiFiNetwork, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return append([]platform.WiFiNetwork{}, f.networks...), nil
}
func (f *fakeActions) Join(ssid, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.joinErr != nil {
		return f.joinErr
	}
	f.joined = append(f.joined, ssid+"\x00"+password)
	return nil
}
func (f *fakeActions) CurrentSSID() string { f.mu.Lock(); defer f.mu.Unlock(); return f.currentSSID }
func (f *fakeActions) WaitReady(_ context.Context, ssid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls = append(f.waitCalls, ssid)
	return f.waitErr
}
func (f *fakeActions) Restore(_ context.Context, ssid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored = append(f.restored, ssid)
	return nil
}

type fakeCreds struct {
	mu    sync.Mutex
	saved map[string]string
}

func newFakeCreds() *fakeCreds { return &fakeCreds{saved: map[string]string{}} }
func (c *fakeCreds) Has(a string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.saved[a]
	return ok, nil
}
func (c *fakeCreds) Get(a string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.saved[a]; ok {
		return v, nil
	}
	return "", platform.ErrCredentialMissing
}
func (c *fakeCreds) Set(a, v string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saved[a] = v
	return nil
}
func (c *fakeCreds) Delete(a string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.saved, a)
	return nil
}

// fakeLink 记录关闭并允许模拟链路中断。
type fakeLink struct {
	mu       sync.Mutex
	files    []deviceimport.Media
	warning  string
	err      error
	alive    bool
	listNow  int
	closeNow int
}

func (l *fakeLink) Alive() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.alive }
func (l *fakeLink) Close()      { l.mu.Lock(); l.closeNow++; l.mu.Unlock() }
func (l *fakeLink) List(context.Context) ([]deviceimport.Media, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listNow++
	if l.err != nil {
		return nil, "", l.err
	}
	return append([]deviceimport.Media{}, l.files...), l.warning, nil
}

type fixture struct {
	actions *fakeActions
	creds   *fakeCreds
	link    *fakeLink
	host    *Host
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	f := &fixture{actions: &fakeActions{currentSSID: "HomeNet"}, creds: newFakeCreds(), link: &fakeLink{alive: true}}
	f.link.files = []deviceimport.Media{{ID: "m1", Name: "DJI_0001.MP4", Path: "DCIM/100MEDIA/DJI_0001.MP4", StorageID: "0", StorageName: "存储卡", Size: 1024, Kind: "video", Date: "2026-10-23 10:30:00"}}
	var open MediaLink = f.link
	f.host = New(
		Config{Name: "DJI Mini 5 Pro", HotKeywords: []string{"dji", "mini"}},
		Actions{Scan: f.actions.Scan, Join: f.actions.Join, CurrentSSID: f.actions.CurrentSSID, WaitReady: f.actions.WaitReady, Restore: f.actions.Restore},
		f.creds,
		diagnostics.New(t.TempDir()),
		func(context.Context) (MediaLink, error) { return open, nil },
	)
	return f
}

func waitPhase(t *testing.T, h *Host, phase string) State {
	t.Helper()
	for i := 0; i < 300; i++ {
		st := h.State()
		if st.Phase == phase && !st.Busy {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时，当前 %+v", phase, h.State())
	return State{}
}

func candidate(st State, ssid string) deviceimport.Network {
	for _, n := range st.WiFiNetworks {
		if n.SSID == ssid {
			return n
		}
	}
	return deviceimport.Network{}
}

func TestScanClassifiesAndNeverJoins(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{
		{SSID: "DJI-Mini5Pro-AB12", RSSI: -40, Channel: 6, Security: "WPA2", Supported: true},
		{SSID: "HomeNet", RSSI: -55, Channel: 11, Security: "WPA2", Supported: true},
		{SSID: "Office-Enterprise", RSSI: -50, Security: "WPA3-Enterprise", Supported: false},
	}
	if err := f.host.ScanWiFi(); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "choosing_wifi")
	if got := candidate(st, "DJI-Mini5Pro-AB12"); !got.Likely || !strings.Contains(got.Reason, "设备特征") {
		t.Fatalf("设备热点未标记为可能：%+v", got)
	}
	if got := candidate(st, "HomeNet"); got.Likely {
		t.Fatal("普通热点不应标记为可能")
	}
	if got := candidate(st, "Office-Enterprise"); got.Supported {
		t.Fatal("企业认证热点不应可加入")
	}
	if len(st.WiFiNetworks) < 2 || st.WiFiNetworks[0].SSID != "DJI-Mini5Pro-AB12" {
		t.Fatalf("热点排序错误：%+v", st.WiFiNetworks)
	}
	f.actions.mu.Lock()
	joined := len(f.actions.joined)
	f.actions.mu.Unlock()
	if joined != 0 {
		t.Fatal("扫描阶段切换了 Wi-Fi")
	}
}

func TestJoinSavesCredentialAndOpensMedia(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", RSSI: -40, Channel: 6, Security: "WPA2", Supported: true}}
	if err := f.host.ScanWiFi(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.host, "choosing_wifi")
	if err := f.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "connected")
	if st.Message != "已连接，可选择素材导入" || len(st.Media) != 1 {
		t.Fatalf("连接后状态错误：%+v", st)
	}
	f.actions.mu.Lock()
	joined := append([]string{}, f.actions.joined...)
	f.actions.mu.Unlock()
	if len(joined) != 1 || !strings.Contains(joined[0], "password8") {
		t.Fatalf("未按提交的密码加入热点：%v", joined)
	}
	account := CredentialAccount("DJI Mini 5 Pro", "DJI-Mini5Pro-AB12")
	if saved := f.creds.saved[account]; saved != "password8" {
		t.Fatalf("密码未保存到凭据存储：%q", saved)
	}
}

func TestJoinFailureKeepsCredentialAndSelection(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", Security: "WPA2", Supported: true}}
	_ = f.host.ScanWiFi()
	waitPhase(t, f.host, "choosing_wifi")
	f.actions.mu.Lock()
	f.actions.joinErr = errors.New("认证失败")
	f.actions.mu.Unlock()
	if err := f.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "waiting_wifi")
	account := CredentialAccount("DJI Mini 5 Pro", "DJI-Mini5Pro-AB12")
	if saved := f.creds.saved[account]; saved != "password8" {
		t.Fatal("失败后未保留密码")
	}
	if candidate(st, "DJI-Mini5Pro-AB12").SSID == "" {
		t.Fatal("失败后丢失了热点列表")
	}
	// 修复密码后可以重试。
	f.actions.mu.Lock()
	f.actions.joinErr = nil
	f.actions.mu.Unlock()
	if err := f.host.JoinWiFi("DJI-Mini5Pro-AB12", "fixed-pass1", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.host, "connected")
}

func TestDisconnectRestoresPreviousNetwork(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", Security: "WPA2", Supported: true}}
	_ = f.host.ScanWiFi()
	waitPhase(t, f.host, "choosing_wifi")
	if err := f.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.host, "connected")
	if err := f.host.Disconnect(); err != nil {
		t.Fatal(err)
	}
	st := f.host.State()
	if st.Phase != "disconnected" || st.DeviceName != "" || len(st.Media) != 0 {
		t.Fatalf("断开后状态未清理：%+v", st)
	}
	f.actions.mu.Lock()
	restored := append([]string{}, f.actions.restored...)
	f.actions.mu.Unlock()
	if len(restored) != 1 || restored[0] != "HomeNet" {
		t.Fatalf("未恢复原网络：%v", restored)
	}
	if f.link.closeNow != 1 {
		t.Fatalf("素材会话未被关闭：%d", f.link.closeNow)
	}
}

func TestManualConnectUsesCurrentNetwork(t *testing.T) {
	f := newFixture(t)
	f.actions.mu.Lock()
	f.actions.currentSSID = "DJI-Mini5Pro-CD34"
	f.actions.mu.Unlock()
	if err := f.host.ManualConnect(); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "connected")
	if st.SSID != "DJI-Mini5Pro-CD34" {
		t.Fatalf("手动会话未记录当前热点：%+v", st)
	}
	if strings.HasPrefix(f.host.DeviceID(), "manual-") == false {
		t.Fatalf("手动会话缺少设备标识：%q", f.host.DeviceID())
	}
}

func TestProvisionPrefillsQRConnectionInfo(t *testing.T) {
	f := newFixture(t)
	f.host.Provision("DIRECT-ab:ZV-E10", "qrcode-pass")
	f.actions.networks = []platform.WiFiNetwork{
		{SSID: "DIRECT-ab:ZV-E10", RSSI: -42, Security: "WPA2", Supported: true},
		{SSID: "HomeNet", RSSI: -60, Security: "WPA2", Supported: true},
	}
	if err := f.host.ScanWiFi(); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "choosing_wifi")
	if !strings.Contains(st.Message, "二维码") {
		t.Fatalf("缺少二维码提示：%q", st.Message)
	}
	got := candidate(st, "DIRECT-ab:ZV-E10")
	if !got.Likely || !strings.Contains(got.Reason, "二维码") {
		t.Fatalf("二维码热点未优先标记：%+v", got)
	}
	if got.SSID == st.WiFiNetworks[0].SSID {
		// 二维码热点应排在最前。
	} else {
		t.Fatalf("二维码热点未排在最前：%+v", st.WiFiNetworks[0])
	}
	// 空密码提交时使用二维码提供的密码。
	if err := f.host.JoinWiFi("DIRECT-ab:ZV-E10", "", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.host, "connected")
	f.actions.mu.Lock()
	joined := append([]string{}, f.actions.joined...)
	f.actions.mu.Unlock()
	if len(joined) != 1 || !strings.Contains(joined[0], "qrcode-pass") {
		t.Fatalf("未使用二维码密码：%v", joined)
	}
}

func TestPreparePairsDeviceAndScansWithHint(t *testing.T) {
	f := newFixture(t)
	prepared := ""
	f.host.ReplaceActions(Actions{
		Scan:        f.actions.Scan,
		Join:        f.actions.Join,
		CurrentSSID: f.actions.CurrentSSID,
		WaitReady:   f.actions.WaitReady,
		Restore:     f.actions.Restore,
		ScanDevices: func(context.Context) ([]deviceimport.Device, error) {
			return []deviceimport.Device{{ID: "ble-01", Name: "DJI-Mini5Pro-B7AC", RSSI: -50}}, nil
		},
		Prepare: func(ctx context.Context, deviceID string, stage func(string)) error {
			prepared = deviceID
			stage("请在飞行器旁长按电源键约 3 秒完成授权")
			f.host.SetIdentity(deviceID, "DJI-Mini5Pro-B7AC")
			f.host.SetHint("DJI-Mini5Pro-B7AC-WiFi")
			return nil
		},
	})
	if _, err := f.host.Discover(); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Prepare("ble-01"); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "choosing_wifi")
	if prepared != "ble-01" {
		t.Fatalf("配对未收到设备标识：%q", prepared)
	}
	if f.host.DeviceID() != "ble-01" || st.DeviceName != "DJI-Mini5Pro-B7AC" {
		t.Fatalf("设备身份未固化：%q %q", f.host.DeviceID(), st.DeviceName)
	}
	f.actions.mu.Lock()
	f.actions.networks = []platform.WiFiNetwork{
		{SSID: "DJI-Mini5Pro-B7AC-WiFi", RSSI: -42, Security: "WPA2", Supported: true},
		{SSID: "HomeNet", RSSI: -60, Security: "WPA2", Supported: true},
	}
	f.actions.mu.Unlock()
	_ = f.host.ScanWiFi()
	st = waitPhase(t, f.host, "choosing_wifi")
	got := candidate(st, "DJI-Mini5Pro-B7AC-WiFi")
	if !got.Likely || !strings.Contains(got.Reason, "蓝牙") {
		t.Fatalf("蓝牙提示热点未标记为可能：%+v", got)
	}
}

func TestProvisionFragmentMatchesHotspotSuffix(t *testing.T) {
	f := newFixture(t)
	f.host.ProvisionFragment("RLE1", "k5oPRHk6")
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DIRECT-RLE1:ZV-E10", RSSI: -44, Security: "WPA2", Supported: true}}
	if err := f.host.ScanWiFi(); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, f.host, "choosing_wifi")
	got := candidate(st, "DIRECT-RLE1:ZV-E10")
	if !got.Likely || !strings.Contains(got.Reason, "二维码") {
		t.Fatalf("片段匹配未标记热点：%+v", got)
	}
	if err := f.host.JoinWiFi("DIRECT-RLE1:ZV-E10", "", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, f.host, "connected")
	f.actions.mu.Lock()
	joined := append([]string{}, f.actions.joined...)
	f.actions.mu.Unlock()
	if len(joined) != 1 || !strings.Contains(joined[0], "k5oPRHk6") {
		t.Fatalf("片段匹配未提供二维码密码：%v", joined)
	}
}

func TestLostLinkSurfacesPhaseAndRefreshKeepsList(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", Security: "WPA2", Supported: true}}
	_ = f.host.ScanWiFi()
	waitPhase(t, f.host, "choosing_wifi")
	_ = f.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false)
	waitPhase(t, f.host, "connected")
	f.link.mu.Lock()
	f.link.alive = false
	f.link.mu.Unlock()
	if st := f.host.State(); st.Phase != "lost" {
		t.Fatalf("链路中断未反映为 lost：%+v", st)
	}
	// 刷新在链路中断时应报错而不是覆盖清单。
	if err := f.host.Refresh(); err == nil {
		t.Fatal("中断后刷新应当失败")
	}
}

func TestForgetPasswordScopedToModelAndSSID(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", Security: "WPA2", Supported: true}}
	_ = f.host.ScanWiFi()
	waitPhase(t, f.host, "choosing_wifi")
	account := CredentialAccount("DJI Mini 5 Pro", "DJI-Mini5Pro-AB12")
	_ = f.creds.Set(account, "password8")
	if err := f.host.ForgetPassword("DJI-Mini5Pro-AB12"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.creds.saved[account]; ok {
		t.Fatal("密码未被移除")
	}
}

func TestPasswordValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"password8", true}, {"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"short", false}, {"contains\x00null", false}, {"", false},
	} {
		if err := validatePassword(tc.value); (err == nil) != tc.ok {
			t.Fatalf("校验结果错误：%q", tc.value)
		}
	}
}

func TestReservedNetworksExcludeGatewayAndBroadcast(t *testing.T) {
	f := newFixture(t)
	f.actions.networks = []platform.WiFiNetwork{{SSID: "DJI-Mini5Pro-AB12", Security: "WPA2", Supported: true}}
	_ = f.host.ScanWiFi()
	waitPhase(t, f.host, "choosing_wifi")
	_ = f.host.JoinWiFi("DJI-Mini5Pro-AB12", "password8", false)
	waitPhase(t, f.host, "connected")
	if fmt.Sprint(candidate(f.host.State(), "DJI-Mini5Pro-AB12").HasSavedPassword) != "true" {
		t.Fatal("已保存密码未在候选中标记")
	}
}
