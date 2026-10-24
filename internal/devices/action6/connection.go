package action6

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"cardatlas/internal/platform"
)

type bleConnection interface{ Close() }
type mediaConnection interface {
	Alive() bool
	Close()
	List(context.Context) ([]camera.Media, string, error)
}
type credentialStore interface {
	Has(string) (bool, error)
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}
type WiFiCandidate struct {
	platform.WiFiNetwork
	Likely           bool   `json:"likely"`
	Reason           string `json:"reason"`
	HasSavedPassword bool   `json:"hasSavedPassword"`
}
type connectionActions struct {
	pair        func(context.Context, string, string, func(string)) (bleConnection, string, error)
	scan        func(context.Context) ([]platform.WiFiNetwork, error)
	join        func(string, string) error
	currentSSID func() string
	waitReady   func(context.Context, string) error
	open        func(context.Context, string, string) (mediaConnection, error)
	restore     func(context.Context, string) error
}

func (s *Adapter) initConnectionActions() {
	s.credentials = platform.Credentials{}
	s.connection = connectionActions{
		pair: func(ctx context.Context, id, identity string, stage func(string)) (bleConnection, string, error) {
			return s.bluetooth.Pair(ctx, id, identity, stage)
		},
		scan: platform.ScanWiFi, join: platform.JoinWiFi, currentSSID: platform.CurrentSSID, waitReady: platform.WaitCameraWiFi,
		open: func(ctx context.Context, host, identity string) (mediaConnection, error) {
			return camera.OpenLink(ctx, host, identity)
		},
		restore: platform.RestoreWiFi,
	}
}

type connectionIssue struct {
	phase string
	cause error
}

func (e *connectionIssue) Error() string { return e.cause.Error() }
func (e *connectionIssue) Unwrap() error { return e.cause }
func pendingPhase(phase string) bool {
	return phase == "choosing_wifi" || phase == "waiting_wifi" || phase == "connection_error"
}

func credentialAccount(device, ssid string) string {
	sum := sha256.Sum256([]byte(device + "\x00" + ssid))
	return hex.EncodeToString(sum[:])
}
func validatePassword(password string) error {
	if !utf8.ValidString(password) || strings.ContainsAny(password, "\x00\r\n\t") {
		return errors.New("密码包含不支持的字符，请重新核对")
	}
	if len(password) >= 8 && len(password) <= 63 {
		return nil
	}
	if len(password) == 64 {
		if _, err := hex.DecodeString(password); err == nil {
			return nil
		}
	}
	return errors.New("请输入 8–63 字节的 Wi-Fi 密码，或 64 位十六进制密钥；注意大小写及空格")
}

// 调用方已持有 op；整个异步操作在完成或取消前独占连接状态。
func (s *Adapter) runConnection(phase string, work func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	s.mu.Lock()
	if s.connectionID == "" {
		var id [6]byte
		_, _ = rand.Read(id[:])
		s.connectionID = hex.EncodeToString(id[:])
	}
	ctx = diagnostics.With(ctx, s.logs, map[string]string{"connection_id": s.connectionID})
	s.cancelOp, s.state.Busy, s.state.Phase = cancel, true, phase
	s.state.Warning, s.state.Message = "", "正在处理相机连接"
	s.state.Media = []camera.Media{}
	s.lostLogged = false
	s.mu.Unlock()
	go func() {
		defer s.op.Unlock()
		defer cancel()
		trace := diagnostics.From(ctx)
		start := time.Now()
		err := work(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			trace.Log("warn", "connection", "connection.incomplete", "相机连接尚未完成", map[string]string{"error": err.Error(), "elapsed_ms": fmt.Sprint(time.Since(start).Milliseconds())})
			var issue *connectionIssue
			if errors.As(err, &issue) && ctx.Err() == nil {
				s.releaseMediaConnection()
				s.mu.Lock()
				s.state.Phase, s.state.Message = issue.phase, err.Error()
				s.mu.Unlock()
			} else {
				s.cleanupConnection()
				s.mu.Lock()
				s.state.Phase, s.state.Message = "disconnected", err.Error()
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.cancelOp = nil
		s.state.Busy = false
		s.mu.Unlock()
	}()
}
func (s *Adapter) connectionStage(ctx context.Context, message string) {
	s.mu.Lock()
	s.state.Message = message
	s.mu.Unlock()
	diagnostics.From(ctx).Log("info", "connection", "connection.stage", message, nil)
}

// 蓝牙只负责配对及唤醒热点；此方法绝不切换 Wi-Fi。
func (s *Adapter) PrepareCamera(id string) error {
	if !s.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	s.mu.Lock()
	if s.ble != nil || s.link != nil || pendingPhase(s.state.Phase) {
		s.mu.Unlock()
		s.op.Unlock()
		return errors.New("请先取消或断开当前相机")
	}
	err := s.saveLocked()
	s.connectionID = ""
	s.mu.Unlock()
	if err != nil {
		s.op.Unlock()
		return fmt.Errorf("无法保存配对标识：%w", err)
	}
	s.runConnection("preparing", func(ctx context.Context) error {
		ble, hint, err := s.connection.pair(ctx, id, s.identity, func(message string) { s.connectionStage(ctx, message) })
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.ble, s.deviceID, s.wifiHint = ble, id, hint
		s.state.SSID, s.state.DeviceName = hint, "DJI Osmo"
		s.previousSSID = s.connection.currentSSID()
		s.logs.RegisterSecrets(s.previousSSID)
		if name := s.deviceNames[id]; name != "" {
			s.state.DeviceName = name
		}
		s.logs.RegisterSecrets(id, hint, s.state.DeviceName)
		s.mu.Unlock()
		return s.scanWiFi(ctx)
	})
	return nil
}

func (s *Adapter) scanWiFi(ctx context.Context) error {
	s.connectionStage(ctx, "正在扫描附近热点；首次使用请允许系统定位授权以读取热点名称")
	s.mu.Lock()
	s.state.WiFiNetworks = []WiFiCandidate{}
	hint, device := s.wifiHint, s.deviceID
	s.mu.Unlock()
	networks, err := s.connection.scan(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	trace := diagnostics.From(ctx)
	if err != nil {
		trace.Log("warn", "wifi", "wifi.scan_failed", "扫描热点失败", platform.WiFiErrorFields(err))
		return &connectionIssue{"choosing_wifi", err}
	}
	// 同名热点合并，只使用本次实际扫描到的结果；不把蓝牙提示伪装成扫描结果。
	unique := map[string]platform.WiFiNetwork{}
	for _, network := range networks {
		if network.SSID == "" || network.SSID == "<redacted>" || strings.ContainsRune(network.SSID, '\x00') {
			continue
		}
		s.logs.RegisterSecrets(network.SSID)
		if old, ok := unique[network.SSID]; !ok || network.RSSI > old.RSSI {
			unique[network.SSID] = network
		}
	}
	candidates := make([]WiFiCandidate, 0, len(unique))
	var keychainWarning bool
	for _, network := range unique {
		candidate := WiFiCandidate{WiFiNetwork: network, Reason: "其他附近热点，请核对相机无线信息"}
		lower := strings.ToLower(network.SSID)
		if network.SSID == hint {
			candidate.Likely, candidate.Reason = true, "与相机蓝牙提供的热点名称一致"
		} else if strings.Contains(lower, "osmo") || strings.Contains(lower, "dji") {
			candidate.Likely, candidate.Reason = true, "名称包含 Osmo / DJI，需由你确认"
		}
		saved, e := s.credentials.Has(credentialAccount(device, network.SSID))
		if e != nil {
			keychainWarning = true
		}
		candidate.HasSavedPassword = saved && e == nil
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if (a.SSID == hint) != (b.SSID == hint) {
			return a.SSID == hint
		}
		if a.Likely != b.Likely {
			return a.Likely
		}
		if a.RSSI != b.RSSI {
			return a.RSSI > b.RSSI
		}
		return a.SSID < b.SSID
	})
	s.mu.Lock()
	s.state.WiFiNetworks = candidates
	if !s.switched {
		s.previousSSID = s.connection.currentSSID()
		s.logs.RegisterSecrets(s.previousSSID)
	}
	s.state.Phase, s.state.Message = "choosing_wifi", "请选择相机热点，确认后才会切换 Wi-Fi"
	if keychainWarning {
		s.state.Warning = "暂时无法读取钥匙串状态，可以手动输入密码连接"
	}
	s.mu.Unlock()
	trace.Log("info", "wifi", "wifi.scan_complete", "热点扫描完成，等待用户选择", map[string]string{"count": fmt.Sprint(len(candidates)), "credential_status_unavailable": fmt.Sprint(keychainWarning)})
	return nil
}
func (s *Adapter) ScanWiFi() error {
	if !s.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	s.mu.Lock()
	allowed := s.ble != nil && s.link == nil
	s.mu.Unlock()
	if !allowed {
		s.op.Unlock()
		return errors.New("请先选择并配对蓝牙相机")
	}
	s.runConnection("choosing_wifi", s.scanWiFi)
	return nil
}
func (s *Adapter) candidateLocked(ssid string) (WiFiCandidate, error) {
	if s.ble == nil || s.link != nil || !pendingPhase(s.state.Phase) {
		return WiFiCandidate{}, errors.New("请先选择并配对蓝牙相机")
	}
	for _, item := range s.state.WiFiNetworks {
		if item.SSID == ssid {
			if !item.Supported {
				return WiFiCandidate{}, errors.New("请选择使用个人密码认证的相机热点")
			}
			return item, nil
		}
	}
	return WiFiCandidate{}, errors.New("该热点已不在本次扫描结果中，请重新扫描并选择")
}

func (s *Adapter) JoinCameraWiFi(ssid, password string, useSaved bool) error {
	if !s.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	s.mu.Lock()
	_, err := s.candidateLocked(ssid)
	account := credentialAccount(s.deviceID, ssid)
	s.mu.Unlock()
	if err == nil && !useSaved {
		err = validatePassword(password)
	}
	if err != nil {
		s.op.Unlock()
		return err
	}
	s.logs.RegisterSecrets(password, ssid)
	s.mu.Lock()
	s.state.SSID = ssid
	s.mu.Unlock()
	s.runConnection("connecting", func(ctx context.Context) error {
		defer func() { password = "" }()
		if useSaved {
			var err error
			password, err = s.credentials.Get(account)
			if err != nil {
				return &connectionIssue{"waiting_wifi", err}
			}
			s.logs.RegisterSecrets(password)
		}
		if err := validatePassword(password); err != nil {
			return &connectionIssue{"waiting_wifi", err}
		}
		trace := diagnostics.From(ctx)
		// 先记住用户本次提交的密码，认证、DHCP 或素材会话失败均不撤销保存。
		if !useSaved {
			if err := s.credentials.Set(account, password); err != nil {
				s.mu.Lock()
				s.state.Warning = "本次密码未能记住，下次可能需要重新输入：" + err.Error()
				s.mu.Unlock()
				trace.Log("warn", "wifi", "wifi.credential_save_failed", "密码保存至钥匙串失败", map[string]string{"error": err.Error()})
			} else {
				s.mu.Lock()
				for i := range s.state.WiFiNetworks {
					if s.state.WiFiNetworks[i].SSID == ssid {
						s.state.WiFiNetworks[i].HasSavedPassword = true
					}
				}
				s.mu.Unlock()
				trace.Log("info", "wifi", "wifi.credential_saved", "已在本机钥匙串记住本次提交的相机热点密码", nil)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current := s.connection.currentSSID()
		s.mu.Lock()
		if !s.switched {
			s.previousSSID = current
		}
		s.state.SSID = ssid
		s.logs.RegisterSecrets(current)
		s.mu.Unlock()
		s.connectionStage(ctx, "正在加入选定热点，Wi-Fi 互联网连接可能暂时中断")
		trace.Log("info", "wifi", "wifi.join_attempt", "用户已确认，开始加入选定热点", map[string]string{"saved_credential": fmt.Sprint(useSaved)})
		err := s.connection.join(ssid, password)
		// 即使系统调用报告失败，也保留已经切换的网络，直到用户主动取消。
		if err == nil || s.connection.currentSSID() == ssid {
			s.mu.Lock()
			s.switched = true
			s.mu.Unlock()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			trace.Log("warn", "wifi", "wifi.join_failed", "加入热点失败，等待用户修改密码或手动连接", platform.WiFiErrorFields(err))
			return &connectionIssue{"waiting_wifi", err}
		}
		s.connectionStage(ctx, "已完成热点认证，正在等待网络地址")
		if err := s.connection.waitReady(ctx, ssid); err != nil {
			return &connectionIssue{"waiting_wifi", err}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.openMedia(ctx)
	})
	return nil
}
func (s *Adapter) ForgetWiFiPassword(ssid string) error {
	if !s.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	_, err := s.candidateLocked(ssid)
	account := credentialAccount(s.deviceID, ssid)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = s.credentials.Delete(account); err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.state.WiFiNetworks {
		if s.state.WiFiNetworks[i].SSID == ssid {
			s.state.WiFiNetworks[i].HasSavedPassword = false
		}
	}
	s.mu.Unlock()
	s.logs.Record("info", "wifi", "wifi.credential_forgotten", "已移除该相机热点在CardAtlas钥匙串中的密码", nil)
	return nil
}

// 保留系统 Wi-Fi 菜单的手动接续入口；非手动调用只准备热点选择。
func (s *Adapter) connectManual(id string, manual bool) error {
	if !manual {
		return s.PrepareCamera(id)
	}
	if !s.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	s.mu.Lock()
	if s.link != nil {
		s.mu.Unlock()
		s.op.Unlock()
		return errors.New("请先断开当前相机")
	}
	if s.ble == nil && !pendingPhase(s.state.Phase) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		s.deviceID = "manual-" + hex.EncodeToString(b[:])
		s.state.DeviceName, s.state.SSID = "DJI Osmo · 手动连接", ""
	}
	expected := s.state.SSID
	s.mu.Unlock()
	s.runConnection("connecting", func(ctx context.Context) error {
		s.connectionStage(ctx, "正在确认相机热点和网络地址")
		if err := s.connection.waitReady(ctx, expected); err != nil {
			return &connectionIssue{"waiting_wifi", err}
		}
		current := s.connection.currentSSID()
		s.mu.Lock()
		if s.ble != nil {
			s.switched = true
		}
		if expected == "" {
			s.state.SSID = current
			s.logs.RegisterSecrets(current)
		}
		s.mu.Unlock()
		return s.openMedia(ctx)
	})
	return nil
}
func (s *Adapter) openMedia(ctx context.Context) error {
	trace := diagnostics.From(ctx)
	s.connectionStage(ctx, "正在建立素材会话，请保持相机开机")
	link, err := s.connection.open(ctx, "192.168.2.1", s.identity)
	if err != nil {
		return &connectionIssue{"connection_error", err}
	}
	s.mu.Lock()
	s.link = link
	s.mu.Unlock()
	s.connectionStage(ctx, "正在读取内置存储和 SD 卡")
	files, warning, err := link.List(ctx)
	if err != nil {
		return &connectionIssue{"connection_error", err}
	}
	s.mu.Lock()
	s.state.Media = files
	s.state.Revision++
	if warning != "" {
		if s.state.Warning != "" {
			s.state.Warning += "；"
		}
		s.state.Warning += warning
	}
	s.state.Phase, s.state.Message = "connected", "已连接，可选择素材导入"
	s.mu.Unlock()
	trace.Log("info", "media", "media.list_ready", "素材清单读取完成", map[string]string{"count": fmt.Sprint(len(files)), "warning": warning})
	return nil
}
