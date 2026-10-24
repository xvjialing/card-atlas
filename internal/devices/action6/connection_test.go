package action6

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"cardatlas/internal/platform"
)

type fakeBLE struct{ closed atomic.Bool }

func (b *fakeBLE) Close() { b.closed.Store(true) }

type fakeMedia struct{ closed atomic.Bool }

func (m *fakeMedia) Alive() bool { return !m.closed.Load() }
func (m *fakeMedia) Close()      { m.closed.Store(true) }
func (m *fakeMedia) List(context.Context) ([]camera.Media, string, error) {
	return []camera.Media{}, "", nil
}

type memoryCredentials struct {
	mu        sync.Mutex
	data      map[string]string
	reads     int
	saveError error
}

func (m *memoryCredentials) Has(k string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.data[k]
	return ok, nil
}
func (m *memoryCredentials) Get(k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if p, ok := m.data[k]; ok {
		return p, nil
	}
	return "", platform.ErrCredentialMissing
}
func (m *memoryCredentials) Set(k, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveError != nil {
		return m.saveError
	}
	m.data[k] = p
	return nil
}
func (m *memoryCredentials) Delete(k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, k)
	return nil
}
func connectionService(t *testing.T) (*Adapter, *fakeBLE, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	s := NewAdapter()
	ble := &fakeBLE{}
	pairs, restores := &atomic.Int32{}, &atomic.Int32{}
	s.credentials = &memoryCredentials{data: map[string]string{}}
	s.connection = connectionActions{
		pair: func(context.Context, string, string, func(string)) (bleConnection, string, error) {
			pairs.Add(1)
			return ble, "TestOsmo", nil
		},
		scan: func(context.Context) ([]platform.WiFiNetwork, error) {
			return []platform.WiFiNetwork{{SSID: "TestOsmo", RSSI: -52, Security: "WPA2", Supported: true, Channel: 149}, {SSID: "Home", RSSI: -22, Security: "WPA2", Supported: true}}, nil
		},
		join: func(string, string) error { return nil }, currentSSID: func() string { return "OriginalNetwork" }, waitReady: func(context.Context, string) error { return nil },
		open: func(context.Context, string, string) (mediaConnection, error) { return &fakeMedia{}, nil }, restore: func(context.Context, string) error { restores.Add(1); return nil },
	}
	return s, ble, pairs, restores
}
func awaitConnection(t *testing.T, s *Adapter, phase string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := s.GetState()
		if state.Phase == phase && !state.Busy && s.op.TryLock() {
			s.op.Unlock()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待 %s 超时，当前 %+v", phase, s.GetState())
}
func prepare(t *testing.T, s *Adapter) {
	t.Helper()
	if e := s.PrepareCamera("camera-stable-id"); e != nil {
		t.Fatal(e)
	}
	awaitConnection(t, s, "choosing_wifi")
}
func join(t *testing.T, s *Adapter, p string, saved bool, phase string) {
	t.Helper()
	if e := s.JoinCameraWiFi("TestOsmo", p, saved); e != nil {
		t.Fatal(e)
	}
	awaitConnection(t, s, phase)
}

func TestPrepareOnlyScansAndNeverJoins(t *testing.T) {
	s, ble, _, restores := connectionService(t)
	s.connection.join = func(string, string) error { t.Error("未确认就切换了 Wi-Fi"); return nil }
	s.credentials.Set(credentialAccount("camera-stable-id", "TestOsmo"), "SavedPass42")
	prepare(t, s)
	state := s.GetState()
	if len(state.WiFiNetworks) != 2 || state.WiFiNetworks[0].SSID != "TestOsmo" || !state.WiFiNetworks[0].HasSavedPassword {
		t.Fatal(state.WiFiNetworks)
	}
	if s.credentials.(*memoryCredentials).reads != 0 {
		t.Fatal("扫描时读取了密码正文")
	}
	state.WiFiNetworks[0].SSID = "changed"
	if s.GetState().WiFiNetworks[0].SSID != "TestOsmo" {
		t.Fatal("状态共享了可变切片")
	}
	if err := s.JoinCameraWiFi("not-scanned", "Password42", false); err == nil {
		t.Fatal("接受了扫描结果外的热点")
	}
	s.Disconnect()
	if !ble.closed.Load() || restores.Load() != 0 {
		t.Fatal("尚未切网的取消操作改变了网络")
	}
}
func TestFailedPasswordKeepsBLEAndSavesLatestCredential(t *testing.T) {
	s, ble, pairs, restores := connectionService(t)
	var joins atomic.Int32
	key := credentialAccount("camera-stable-id", "TestOsmo")
	s.credentials.Set(key, "SavedPass42")
	s.connection.join = func(string, string) error { joins.Add(1); return errors.New("认证失败") }
	prepare(t, s)
	join(t, s, "WrongPass42", false, "waiting_wifi")
	if ble.closed.Load() || restores.Load() != 0 || joins.Load() != 1 {
		t.Fatal("认证失败后应等待用户处理，不能反复切网")
	}
	p, _ := s.credentials.Get(key)
	if p != "WrongPass42" {
		t.Fatal("认证失败后没有记住本次提交的密码")
	}
	if _, err := s.Scan(); err == nil {
		t.Fatal("待处理会话被新扫描覆盖")
	}
	if err := s.PrepareCamera("another"); err == nil {
		t.Fatal("待处理会话被新配对覆盖")
	}
	s.connection.currentSSID = func() string { return "TestOsmo" }
	if err := s.connectManual("", true); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, s, "connected")
	if pairs.Load() != 1 || joins.Load() != 1 || ble.closed.Load() || s.deviceID != "camera-stable-id" {
		t.Fatal("手动接续没有复用会话")
	}
	s.Disconnect()
	if !ble.closed.Load() || restores.Load() != 1 {
		t.Fatal("主动断开没有释放会话")
	}
}
func TestRememberedPasswordReconnectAndForgetAreScoped(t *testing.T) {
	s, _, _, _ := connectionService(t)
	prepare(t, s)
	secret := " NewPass42 "
	s.connection.join = func(ssid, p string) error {
		if p != secret || ssid != "TestOsmo" {
			t.Error("密码被改写或关联到错误热点")
		}
		return nil
	}
	join(t, s, secret, false, "connected")
	key := credentialAccount("camera-stable-id", "TestOsmo")
	if p, _ := s.credentials.Get(key); p != secret {
		t.Fatal("未保存密码")
	}
	raw, _ := json.Marshal(s.GetState())
	config, _ := os.ReadFile(s.configFile)
	page, _ := s.logs.Read(diagnostics.Query{Limit: 100})
	logs, _ := json.Marshal(page)
	if strings.Contains(string(raw)+string(config)+string(logs), secret) {
		t.Fatal("密码进入状态、配置或日志")
	}
	s.Disconnect()
	prepare(t, s)
	join(t, s, "", true, "connected")
	s.Disconnect()
	prepare(t, s)
	other := credentialAccount("other-camera", "TestOsmo")
	s.credentials.Set(other, "OtherPass42")
	if e := s.ForgetWiFiPassword("TestOsmo"); e != nil {
		t.Fatal(e)
	}
	if ok, _ := s.credentials.Has(key); ok {
		t.Fatal("忘记密码未删除")
	}
	if ok, _ := s.credentials.Has(other); !ok {
		t.Fatal("删除了其他设备的密码")
	}
	if s.GetState().WiFiNetworks[0].HasSavedPassword {
		t.Fatal("已保存状态未更新")
	}
	s.Disconnect()
}
func TestKeychainFailureIsReportedRegardlessOfConnectionResult(t *testing.T) {
	for _, joinFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "连接成功", true: "认证失败"}[joinFailure], func(t *testing.T) {
			s, _, _, _ := connectionService(t)
			prepare(t, s)
			s.credentials.(*memoryCredentials).saveError = errors.New("拒绝访问")
			phase := "connected"
			if joinFailure {
				phase = "waiting_wifi"
				s.connection.join = func(string, string) error { return errors.New("认证失败") }
			}
			join(t, s, "ManualPass42", false, phase)
			if ok, _ := s.credentials.Has(credentialAccount("camera-stable-id", "TestOsmo")); ok {
				t.Fatal("钥匙串写入失败却记录为已保存")
			}
			if s.GetState().WiFiNetworks[0].HasSavedPassword || !strings.Contains(s.GetState().Warning, "密码未能记住") {
				t.Fatal("未准确反馈密码保存失败")
			}
			s.Disconnect()
		})
	}
}
func TestFailedFirstAttemptSavesBeforeJoinAndReusesPassword(t *testing.T) {
	s, _, _, _ := connectionService(t)
	prepare(t, s)
	secret := " NewPass42 "
	key := credentialAccount("camera-stable-id", "TestOsmo")
	s.connection.join = func(_ string, password string) error {
		stored, err := s.credentials.Get(key)
		if err != nil || stored != secret || password != secret {
			t.Error("加入热点前未保存本次提交的完整密码")
		}
		return errors.New("认证失败")
	}
	join(t, s, secret, false, "waiting_wifi")
	if !s.GetState().WiFiNetworks[0].HasSavedPassword {
		t.Fatal("首次认证失败后没有更新已记住状态")
	}
	raw, _ := json.Marshal(s.GetState())
	config, _ := os.ReadFile(s.configFile)
	page, _ := s.logs.Read(diagnostics.Query{Limit: 100})
	logs, _ := json.Marshal(page)
	if strings.Contains(string(raw)+string(config)+string(logs), secret) {
		t.Fatal("失败连接的密码进入状态、配置或日志")
	}
	s.Disconnect()
	prepare(t, s)
	if !s.GetState().WiFiNetworks[0].HasSavedPassword {
		t.Fatal("重新配对后丢失已记住状态")
	}
	join(t, s, "", true, "waiting_wifi")
	s.Disconnect()
}
func TestInvalidPasswordDoesNotReplaceSavedCredential(t *testing.T) {
	s, _, _, _ := connectionService(t)
	prepare(t, s)
	key := credentialAccount("camera-stable-id", "TestOsmo")
	s.credentials.Set(key, "SavedPass42")
	if err := s.JoinCameraWiFi("TestOsmo", "short", false); err == nil {
		t.Fatal("接受了格式无效的密码")
	}
	if password, _ := s.credentials.Get(key); password != "SavedPass42" {
		t.Fatal("格式无效的密码覆盖了已记住的密码")
	}
	s.Disconnect()
}
func TestMissingSavedPasswordAllowsManualRetry(t *testing.T) {
	s, ble, _, _ := connectionService(t)
	prepare(t, s)
	s.connection.join = func(string, string) error { t.Error("缺失密码时进行了认证"); return nil }
	join(t, s, "", true, "waiting_wifi")
	if ble.closed.Load() {
		t.Fatal("读取钥匙串失败不应关闭蓝牙")
	}
	s.connection.join = func(string, string) error { return nil }
	join(t, s, "ManualPass42", false, "connected")
	s.Disconnect()
}
func TestMediaFailureRetainsWiFiAndValidPassword(t *testing.T) {
	s, ble, _, restores := connectionService(t)
	prepare(t, s)
	s.connection.open = func(context.Context, string, string) (mediaConnection, error) {
		return nil, errors.New("UDP 未响应")
	}
	join(t, s, "ManualPass42", false, "connection_error")
	if ble.closed.Load() || restores.Load() != 0 {
		t.Fatal("素材握手失败不应切回公网")
	}
	if ok, _ := s.credentials.Has(credentialAccount("camera-stable-id", "TestOsmo")); !ok {
		t.Fatal("热点认证成功的密码应保存")
	}
	s.Disconnect()
	if restores.Load() != 1 {
		t.Fatal("主动取消未恢复网络")
	}
}
func TestDHCPFailureKeepsPasswordWithoutOpeningMedia(t *testing.T) {
	s, ble, _, restores := connectionService(t)
	prepare(t, s)
	s.connection.waitReady = func(context.Context, string) error { return errors.New("网络地址尚未就绪") }
	s.connection.open = func(context.Context, string, string) (mediaConnection, error) {
		t.Error("地址未就绪就打开媒体")
		return &fakeMedia{}, nil
	}
	join(t, s, "ManualPass42", false, "waiting_wifi")
	if ble.closed.Load() || restores.Load() != 0 {
		t.Fatal("应保留会话")
	}
	if ok, _ := s.credentials.Has(credentialAccount("camera-stable-id", "TestOsmo")); !ok {
		t.Fatal("DHCP 失败后没有保留密码")
	}
	s.Disconnect()
}
func TestCancelWhileWaitingForAddressCleansUpOnce(t *testing.T) {
	s, ble, _, restores := connectionService(t)
	prepare(t, s)
	started := make(chan struct{})
	s.connection.waitReady = func(ctx context.Context, _ string) error { close(started); <-ctx.Done(); return ctx.Err() }
	if err := s.JoinCameraWiFi("TestOsmo", "ManualPass42", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("等待未开始")
	}
	s.Disconnect()
	awaitConnection(t, s, "disconnected")
	if !ble.closed.Load() || restores.Load() != 1 {
		t.Fatal("取消清理缺失或重复")
	}
	if ok, _ := s.credentials.Has(credentialAccount("camera-stable-id", "TestOsmo")); !ok {
		t.Fatal("取消连接不应清除已提交的密码")
	}
}
func TestScanWithoutBluetoothHintAndRefreshFailure(t *testing.T) {
	s, _, _, _ := connectionService(t)
	s.connection.pair = func(context.Context, string, string, func(string)) (bleConnection, string, error) {
		return &fakeBLE{}, "", nil
	}
	prepare(t, s)
	if len(s.GetState().WiFiNetworks) != 2 {
		t.Fatal("缺少蓝牙 SSID 不应阻止热点选择")
	}
	s.connection.scan = func(context.Context) ([]platform.WiFiNetwork, error) { return nil, errors.New("定位权限未开启") }
	if e := s.ScanWiFi(); e != nil {
		t.Fatal(e)
	}
	awaitConnection(t, s, "choosing_wifi")
	if len(s.GetState().WiFiNetworks) != 0 {
		t.Fatal("刷新失败后仍允许连接过时的扫描结果")
	}
	if e := s.JoinCameraWiFi("TestOsmo", "ManualPass42", false); e == nil {
		t.Fatal("接受了过时热点")
	}
	s.Disconnect()
}
func TestWiFiPasswordValidation(t *testing.T) {
	for _, p := range []string{"", "1234567", "12345678\x00", strings.Repeat("z", 64)} {
		if validatePassword(p) == nil {
			t.Fatal("接受了无效密码")
		}
	}
	for _, p := range []string{"12345678", " 12345678 ", strings.Repeat("a", 64)} {
		if e := validatePassword(p); e != nil {
			t.Fatal(e)
		}
	}
}
