// Package wifihost 实现通过“Mac 加入设备热点”连接的通用会话状态机。
// 扫描、确认加入、手动接续、凭据记忆与网络恢复与品牌无关；素材协议由适配器通过
// MediaLink 提供。蓝牙配对类设备（Action 6）保持其专用实现。
package wifihost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/platform"
)

// MediaLink 是适配器提供的素材会话；Alive 用于把链路中断反映为 lost 阶段。
type MediaLink interface {
	Alive() bool
	Close()
	List(context.Context) ([]deviceimport.Media, string, error)
}

type CredentialStore interface {
	Has(string) (bool, error)
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

// Actions 抽象系统能力，测试中全部可替换。
// Prepare 与 ScanDevices 供蓝牙授权类设备使用；Teardown 在连接彻底清理时回调。
type Actions struct {
	Scan        func(context.Context) ([]platform.WiFiNetwork, error)
	Join        func(string, string) error
	CurrentSSID func() string
	WaitReady   func(context.Context, string) error
	Restore     func(context.Context, string) error
	ScanDevices func(context.Context) ([]deviceimport.Device, error)
	Prepare     func(ctx context.Context, deviceID string, stage func(string)) error
	Teardown    func()
}

type Config struct {
	Name        string   // 设备显示名，进入手动会话与提示文案
	HotKeywords []string // 判定“可能的设备热点”的 SSID 关键词（小写比较）
	Timeout     time.Duration
	// 飞行器失败后立即停止唤醒；等待用户选择热点时也不能无限保活。
	ReleasePreparationOnFailure bool
	PreparationIdleTimeout      time.Duration
}

type State struct {
	Phase, Message, Warning, SSID, DeviceName string
	Busy                                      bool
	Revision                                  int64
	WiFiNetworks                              []deviceimport.Network
	Media                                     []deviceimport.Media
}

// Host 串行执行连接操作；公开方法的行为契约与 Action 6 适配器保持一致。
type Host struct {
	mu, op                sync.Mutex
	cfg                   Config
	actions               Actions
	creds                 CredentialStore
	logs                  *diagnostics.Store
	open                  func(context.Context) (MediaLink, error)
	state                 State
	link                  MediaLink
	deviceID              string
	sessionToken          string
	previousSSID          string
	switched              bool
	cancelOp              context.CancelFunc
	provisions            []provision
	provisionMessage      string
	hint                  string
	lostLogged            bool
	preparationTimer      *time.Timer
	preparationGeneration uint64
}

func New(cfg Config, actions Actions, creds CredentialStore, logs *diagnostics.Store, open func(context.Context) (MediaLink, error)) *Host {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 100 * time.Second
	}
	if cfg.Name == "" {
		cfg.Name = "设备"
	}
	return &Host{cfg: cfg, actions: actions, creds: creds, logs: logs, open: open,
		state: State{Phase: "disconnected", Message: "连接设备，开始导入原始素材", Media: []deviceimport.Media{}, WiFiNetworks: []deviceimport.Network{}}}
}

// ReplaceActions 供适配器测试注入系统能力替身；不得在连接操作进行中调用。
func (h *Host) ReplaceActions(a Actions) { h.actions = a }

// SetIdentity 在蓝牙发现或配对后固化设备标识与显示名，用于任务归属与续传。
func (h *Host) SetIdentity(deviceID, name string) {
	h.mu.Lock()
	if deviceID != "" {
		h.deviceID = deviceID
	}
	if name != "" {
		h.state.DeviceName = name
	}
	h.mu.Unlock()
}

// SetHint 记录蓝牙侧提供的热点名提示；扫描结果中同名热点会标记为可能。
func (h *Host) SetHint(ssid string) {
	if ssid == "" {
		return
	}
	h.mu.Lock()
	h.hint = ssid
	h.mu.Unlock()
}

// Discover 列出可配对的设备；未提供蓝牙发现能力的适配器返回错误。
func (h *Host) Discover() ([]deviceimport.Device, error) {
	if !h.op.TryLock() {
		return nil, errors.New("正在处理设备操作，请稍候")
	}
	defer h.op.Unlock()
	if h.actions.ScanDevices == nil {
		return nil, errors.New("该设备不支持蓝牙发现，请直接扫描并加入设备热点")
	}
	return h.actions.ScanDevices(context.Background())
}

// Prepare 走蓝牙配对（如长按机身电源键授权），成功后直接进入热点扫描。
func (h *Host) Prepare(deviceID string) error {
	if h.actions.Prepare == nil {
		return errors.New("此设备不支持蓝牙配对，请扫描并加入设备热点")
	}
	if !h.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	h.mu.Lock()
	if h.link != nil {
		h.mu.Unlock()
		h.op.Unlock()
		return errors.New("请先断开当前设备")
	}
	h.beginSessionLocked()
	h.mu.Unlock()
	h.runConnection("preparing", func(ctx context.Context) error {
		if err := h.actions.Prepare(ctx, deviceID, func(message string) { h.stage(ctx, message) }); err != nil {
			return err
		}
		return h.scanWiFi(ctx)
	})
	return nil
}

type issue struct {
	phase string
	cause error
}

func (e *issue) Error() string { return e.cause.Error() }
func (e *issue) Unwrap() error { return e.cause }

func pendingPhase(phase string) bool {
	return phase == "choosing_wifi" || phase == "waiting_wifi" || phase == "connection_error"
}

// CredentialAccount 以设备型号与热点名为范围记忆密码；无蓝牙身份的设备不能依赖会话标识。
func CredentialAccount(model, ssid string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + ssid))
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

func (h *Host) stage(ctx context.Context, message string) {
	h.mu.Lock()
	h.state.Message = message
	h.mu.Unlock()
	diagnostics.From(ctx).Log("info", "connection", "connection.stage", message, nil)
}

// runConnection 要求调用方已持有 op；整个异步操作在完成或取消前独占连接状态。
func (h *Host) runConnection(phase string, work func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.Timeout)
	h.mu.Lock()
	h.stopPreparationTimerLocked()
	if h.sessionToken == "" {
		var id [6]byte
		_, _ = rand.Read(id[:])
		h.sessionToken = hex.EncodeToString(id[:])
	}
	ctx = diagnostics.With(ctx, h.logs, map[string]string{"connection_id": h.sessionToken})
	h.cancelOp, h.state.Busy, h.state.Phase = cancel, true, phase
	h.state.Warning, h.state.Message = "", "正在处理设备连接"
	h.state.Media = []deviceimport.Media{}
	h.lostLogged = false
	h.mu.Unlock()
	go func() {
		defer cancel()
		trace := diagnostics.From(ctx)
		start := time.Now()
		err := work(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			trace.Log("warn", "connection", "connection.incomplete", "设备连接尚未完成", map[string]string{"error": err.Error(), "elapsed_ms": fmt.Sprint(time.Since(start).Milliseconds())})
			var failed *issue
			if errors.As(err, &failed) && ctx.Err() == nil {
				h.releaseLink()
				if h.cfg.ReleasePreparationOnFailure && h.actions.Teardown != nil {
					h.actions.Teardown()
				}
				h.mu.Lock()
				h.state.Phase, h.state.Message = failed.phase, err.Error()
				h.mu.Unlock()
			} else {
				h.cleanup()
				h.mu.Lock()
				h.state.Phase, h.state.Message = "disconnected", err.Error()
				h.mu.Unlock()
			}
		}
		h.mu.Lock()
		h.cancelOp = nil
		h.state.Busy = false
		h.schedulePreparationExpiryLocked()
		// 对外发布“空闲”之前先释放操作锁，避免界面看到可操作却被 TryLock 拒绝。
		h.op.Unlock()
		h.mu.Unlock()
	}()
}

func (h *Host) stopPreparationTimerLocked() {
	h.preparationGeneration++
	if h.preparationTimer != nil {
		h.preparationTimer.Stop()
		h.preparationTimer = nil
	}
}

// 等待热点选择期间释放蓝牙唤醒，不切换用户的网络。会话令牌防止旧定时器影响下一次连接。
func (h *Host) schedulePreparationExpiryLocked() {
	if h.cfg.PreparationIdleTimeout <= 0 || !pendingPhase(h.state.Phase) || h.actions.Teardown == nil {
		return
	}
	token := h.sessionToken
	generation := h.preparationGeneration
	h.preparationTimer = time.AfterFunc(h.cfg.PreparationIdleTimeout, func() {
		h.op.Lock()
		defer h.op.Unlock()
		h.mu.Lock()
		if generation != h.preparationGeneration || token != h.sessionToken || h.state.Busy || !pendingPhase(h.state.Phase) {
			h.mu.Unlock()
			return
		}
		h.preparationTimer = nil
		h.state.Warning = "等待连接时间较长，已停止蓝牙唤醒。请让无人机休息散热，需要时重新进行蓝牙授权"
		h.mu.Unlock()
		h.actions.Teardown()
		h.logs.Record("info", "connection", "connection.preparation_expired", "等待连接超时，已停止设备蓝牙唤醒", nil)
	})
}

func (h *Host) releaseLink() {
	h.mu.Lock()
	link := h.link
	h.link = nil
	h.mu.Unlock()
	if link != nil {
		link.Close()
	}
}

func (h *Host) cleanup() {
	h.releaseLink()
	h.mu.Lock()
	h.stopPreparationTimerLocked()
	switched, previous, token := h.switched, h.previousSSID, h.sessionToken
	h.switched, h.previousSSID, h.sessionToken = false, "", ""
	h.state.WiFiNetworks = []deviceimport.Network{}
	teardown := h.actions.Teardown
	// 蓝牙会话随连接释放；热点提示只在本会话内有效。
	h.hint = ""
	h.mu.Unlock()
	if teardown != nil {
		teardown()
	}
	if switched {
		h.logs.Record("info", "wifi", "wifi.restore_start", "尝试恢复原 Wi-Fi", map[string]string{"connection_id": token})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := h.actions.Restore(ctx, previous)
		cancel()
		if err != nil {
			h.logs.Record("warn", "wifi", "wifi.restore_failed", "恢复原网络失败", map[string]string{"error": err.Error(), "connection_id": token})
			h.mu.Lock()
			h.state.Warning = err.Error()
			h.mu.Unlock()
		} else {
			h.logs.Record("info", "wifi", "wifi.restored", "已恢复原网络", map[string]string{"connection_id": token})
		}
	}
}

// provision 是一次二维码读取产生的连接信息；精确热点名优先，片段匹配兜底。
type provision struct {
	ssid     string
	fragment string
	password string
}

// Provision 记录从二维码照片读取到的连接信息；只有用户确认后才会切换 Wi-Fi。
func (h *Host) Provision(ssid, password string) {
	if ssid == "" {
		return
	}
	h.mu.Lock()
	h.provisions = append(h.provisions, provision{ssid: ssid, password: password})
	h.provisionMessage = fmt.Sprintf("已从二维码照片读取“%s”的连接信息，请选择该热点并确认加入", ssid)
	h.state.DeviceName = h.cfg.Name
	if h.deviceID == "" {
		h.deviceID = "qr-" + randomHex(4)
	}
	h.mu.Unlock()
}

// ProvisionFragment 记录热点名后缀的模糊匹配；热点名与二维码字段不一致时兜底。
func (h *Host) ProvisionFragment(fragment, password string) {
	if fragment == "" {
		return
	}
	h.mu.Lock()
	h.provisions = append(h.provisions, provision{fragment: fragment, password: password})
	h.state.DeviceName = h.cfg.Name
	if h.deviceID == "" {
		h.deviceID = "qr-" + randomHex(4)
	}
	h.mu.Unlock()
}

// provisionFor 返回该热点对应的二维码密码；精确匹配优先于片段包含。
// 调用方必须已持有 h.mu。
func (h *Host) provisionFor(ssid string) (string, bool) {
	fragmentMatch := -1
	for i, p := range h.provisions {
		if p.ssid != "" && p.ssid == ssid {
			return p.password, true
		}
		if p.fragment != "" && fragmentMatch < 0 && strings.Contains(ssid, p.fragment) {
			fragmentMatch = i
		}
	}
	if fragmentMatch >= 0 {
		return h.provisions[fragmentMatch].password, true
	}
	return "", false
}

// ProvisionMessage 返回待展示的二维码读取结果；空值表示没有待展示信息。
func (h *Host) ProvisionMessage() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.provisionMessage
}

// ClearProvisionMessage 在状态被前端读取后清空一次性提示。
func (h *Host) ClearProvisionMessage() {
	h.mu.Lock()
	h.provisionMessage = ""
	h.mu.Unlock()
}

// ScanWiFi 扫描附近热点并按设备特征排序；此方法绝不切换 Wi-Fi。
func (h *Host) ScanWiFi() error {
	if !h.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	h.mu.Lock()
	if h.link != nil {
		h.mu.Unlock()
		h.op.Unlock()
		return errors.New("请先断开当前设备")
	}
	h.beginSessionLocked()
	h.mu.Unlock()
	h.runConnection("choosing_wifi", h.scanWiFi)
	return nil
}

func (h *Host) beginSessionLocked() {
	if h.deviceID == "" {
		h.deviceID = "manual-" + randomHex(4)
	}
	h.state.DeviceName = h.cfg.Name
	if h.previousSSID == "" {
		h.previousSSID = h.actions.CurrentSSID()
		h.logs.RegisterSecrets(h.previousSSID)
	}
}

func (h *Host) scanWiFi(ctx context.Context) error {
	h.stage(ctx, "正在扫描附近热点；首次使用请允许系统定位授权以读取热点名称")
	h.mu.Lock()
	h.state.WiFiNetworks = []deviceimport.Network{}
	h.mu.Unlock()
	networks, err := h.actions.Scan(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	trace := diagnostics.From(ctx)
	if err != nil {
		trace.Log("warn", "wifi", "wifi.scan_failed", "扫描热点失败", platform.WiFiErrorFields(err))
		return &issue{"choosing_wifi", err}
	}
	// 同名热点合并，只使用本次实际扫描到的结果；不把提示伪装成扫描结果。
	unique := map[string]platform.WiFiNetwork{}
	for _, network := range networks {
		if network.SSID == "" || network.SSID == "<redacted>" || strings.ContainsRune(network.SSID, '\x00') {
			continue
		}
		h.logs.RegisterSecrets(network.SSID)
		if old, ok := unique[network.SSID]; !ok || network.RSSI > old.RSSI {
			unique[network.SSID] = network
		}
	}
	candidates := make([]deviceimport.Network, 0, len(unique))
	var keychainWarning bool
	h.mu.Lock()
	for _, network := range unique {
		candidate := deviceimport.Network{SSID: network.SSID, RSSI: network.RSSI, Security: network.Security, Channel: network.Channel, Supported: network.Supported, Reason: "其他附近热点，请核对设备无线信息"}
		lower := strings.ToLower(network.SSID)
		if network.SSID == h.hint {
			candidate.Likely, candidate.Reason = true, "与设备蓝牙提供的热点名称一致"
		} else if provisioned, ok := h.provisionFor(network.SSID); ok {
			candidate.Likely, candidate.Reason = true, "与二维码照片读取的热点名称一致"
			candidate.HasProvisionedPassword = provisioned != ""
		} else if matchesKeyword(lower, h.cfg.HotKeywords) {
			candidate.Likely, candidate.Reason = true, "名称包含设备特征，需由你确认"
		}
		saved, e := h.creds.Has(CredentialAccount(h.cfg.Name, network.SSID))
		if e != nil {
			keychainWarning = true
		}
		candidate.HasSavedPassword = saved && e == nil
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		aHint, bHint := a.SSID == h.hint, b.SSID == h.hint
		if aHint != bHint {
			return aHint
		}
		aProvision, bProvision := func() bool { _, ok := h.provisionFor(a.SSID); return ok }(), func() bool { _, ok := h.provisionFor(b.SSID); return ok }()
		if aProvision != bProvision {
			return aProvision
		}
		if a.Likely != b.Likely {
			return a.Likely
		}
		if a.RSSI != b.RSSI {
			return a.RSSI > b.RSSI
		}
		return a.SSID < b.SSID
	})
	h.mu.Unlock()
	h.mu.Lock()
	h.state.WiFiNetworks = candidates
	if !h.switched {
		h.previousSSID = h.actions.CurrentSSID()
		h.logs.RegisterSecrets(h.previousSSID)
	}
	h.state.Phase, h.state.Message = "choosing_wifi", "请选择设备热点，确认后才会切换 Wi-Fi"
	if h.provisionMessage != "" {
		h.state.Message = h.provisionMessage
		h.provisionMessage = ""
	}
	if keychainWarning {
		h.state.Warning = "暂时无法读取钥匙串状态，可以手动输入密码连接"
	}
	h.mu.Unlock()
	trace.Log("info", "wifi", "wifi.scan_complete", "热点扫描完成，等待用户选择", map[string]string{"count": fmt.Sprint(len(candidates)), "credential_status_unavailable": fmt.Sprint(keychainWarning)})
	return nil
}

func matchesKeyword(lowerSSID string, keywords []string) bool {
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(lowerSSID, keyword) {
			return true
		}
	}
	return false
}

func (h *Host) candidateLocked(ssid string) (deviceimport.Network, error) {
	if h.link != nil || !pendingPhase(h.state.Phase) {
		return deviceimport.Network{}, errors.New("请先扫描并选择设备热点")
	}
	for _, item := range h.state.WiFiNetworks {
		if item.SSID == ssid {
			if !item.Supported {
				return deviceimport.Network{}, errors.New("请选择使用个人密码认证的设备热点")
			}
			return item, nil
		}
	}
	return deviceimport.Network{}, errors.New("该热点已不在本次扫描结果中，请重新扫描并选择")
}

// JoinWiFi 只有在用户明确提交后才会切换 Wi-Fi；认证或会话失败均保留热点选择。
func (h *Host) JoinWiFi(ssid, password string, useSaved bool) error {
	if !h.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	h.mu.Lock()
	_, err := h.candidateLocked(ssid)
	account := CredentialAccount(h.cfg.Name, ssid)
	provisioned, hasProvision := h.provisionFor(ssid)
	h.mu.Unlock()
	if err == nil && !useSaved && password == "" && hasProvision {
		// 二维码已提供密码：仅在用户选定该热点时使用，不再要求重复输入。
		password = provisioned
	}
	if err == nil && !useSaved {
		err = validatePassword(password)
	}
	if err != nil {
		h.op.Unlock()
		return err
	}
	h.logs.RegisterSecrets(password, ssid)
	h.mu.Lock()
	h.state.SSID = ssid
	h.mu.Unlock()
	h.runConnection("connecting", func(ctx context.Context) error {
		defer func() { password = "" }()
		if useSaved {
			var err error
			password, err = h.creds.Get(account)
			if err != nil {
				return &issue{"waiting_wifi", err}
			}
			h.logs.RegisterSecrets(password)
		}
		if err := validatePassword(password); err != nil {
			return &issue{"waiting_wifi", err}
		}
		trace := diagnostics.From(ctx)
		// 先记住用户本次提交的密码，认证、DHCP 或素材会话失败均不撤销保存。
		if !useSaved {
			if err := h.creds.Set(account, password); err != nil {
				h.mu.Lock()
				h.state.Warning = "本次密码未能记住，下次可能需要重新输入：" + err.Error()
				h.mu.Unlock()
				trace.Log("warn", "wifi", "wifi.credential_save_failed", "密码保存至钥匙串失败", map[string]string{"error": err.Error()})
			} else {
				h.mu.Lock()
				for i := range h.state.WiFiNetworks {
					if h.state.WiFiNetworks[i].SSID == ssid {
						h.state.WiFiNetworks[i].HasSavedPassword = true
					}
				}
				h.mu.Unlock()
				trace.Log("info", "wifi", "wifi.credential_saved", "已在本机钥匙串记住本次提交的设备热点密码", nil)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current := h.actions.CurrentSSID()
		h.mu.Lock()
		if !h.switched {
			h.previousSSID = current
		}
		h.state.SSID = ssid
		h.logs.RegisterSecrets(current)
		h.mu.Unlock()
		h.stage(ctx, "正在加入选定热点，Wi-Fi 互联网连接可能暂时中断")
		trace.Log("info", "wifi", "wifi.join_attempt", "用户已确认，开始加入选定热点", map[string]string{"saved_credential": fmt.Sprint(useSaved)})
		err := h.actions.Join(ssid, password)
		// 即使系统调用报告失败，也保留已经切换的网络，直到用户主动取消。
		if err == nil || h.actions.CurrentSSID() == ssid {
			h.mu.Lock()
			h.switched = true
			h.mu.Unlock()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			trace.Log("warn", "wifi", "wifi.join_failed", "加入热点失败，等待用户修改密码或手动连接", platform.WiFiErrorFields(err))
			return &issue{"waiting_wifi", err}
		}
		h.stage(ctx, "已完成热点认证，正在等待网络地址")
		if err := h.actions.WaitReady(ctx, ssid); err != nil {
			return &issue{"waiting_wifi", err}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return h.openMedia(ctx)
	})
	return nil
}

func (h *Host) ForgetPassword(ssid string) error {
	if !h.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	defer h.op.Unlock()
	h.mu.Lock()
	_, err := h.candidateLocked(ssid)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err = h.creds.Delete(CredentialAccount(h.cfg.Name, ssid)); err != nil {
		return err
	}
	h.mu.Lock()
	for i := range h.state.WiFiNetworks {
		if h.state.WiFiNetworks[i].SSID == ssid {
			h.state.WiFiNetworks[i].HasSavedPassword = false
		}
	}
	h.mu.Unlock()
	h.logs.Record("info", "wifi", "wifi.credential_forgotten", "已移除该设备热点在CardAtlas钥匙串中的密码", nil)
	return nil
}

// ManualConnect 保留系统 Wi-Fi 菜单的手动接续入口；未识别设备身份的会话不参与续传。
func (h *Host) ManualConnect() error {
	if !h.op.TryLock() {
		return errors.New("正在处理连接，请稍候")
	}
	h.mu.Lock()
	if h.link != nil {
		h.mu.Unlock()
		h.op.Unlock()
		return errors.New("请先断开当前设备")
	}
	h.beginSessionLocked()
	expected := h.state.SSID
	h.mu.Unlock()
	h.runConnection("connecting", func(ctx context.Context) error {
		h.stage(ctx, "正在确认设备热点和网络地址")
		if err := h.actions.WaitReady(ctx, expected); err != nil {
			return &issue{"waiting_wifi", err}
		}
		current := h.actions.CurrentSSID()
		h.mu.Lock()
		if expected == "" {
			h.state.SSID = current
			h.logs.RegisterSecrets(current)
		}
		h.mu.Unlock()
		return h.openMedia(ctx)
	})
	return nil
}

func (h *Host) openMedia(ctx context.Context) error {
	trace := diagnostics.From(ctx)
	h.stage(ctx, "正在建立素材会话，请保持设备开机")
	link, err := h.open(ctx)
	if err != nil {
		return &issue{"connection_error", err}
	}
	h.mu.Lock()
	h.link = link
	h.mu.Unlock()
	h.stage(ctx, "正在读取素材清单")
	files, warning, err := link.List(ctx)
	if err != nil {
		return &issue{"connection_error", err}
	}
	h.mu.Lock()
	h.state.Media = files
	h.state.Revision++
	if warning != "" {
		if h.state.Warning != "" {
			h.state.Warning += "；"
		}
		h.state.Warning += warning
	}
	h.state.Phase, h.state.Message = "connected", "已连接，可选择素材导入"
	h.mu.Unlock()
	trace.Log("info", "media", "media.list_ready", "素材清单读取完成", map[string]string{"count": fmt.Sprint(len(files)), "warning": warning})
	return nil
}

func (h *Host) Disconnect() error {
	h.logs.Record("info", "connection", "connection.disconnect", "正在断开设备并暂停传输", nil)
	h.mu.Lock()
	if h.cancelOp != nil {
		h.cancelOp()
	}
	h.mu.Unlock()
	h.op.Lock()
	defer h.op.Unlock()
	h.cleanup()
	h.mu.Lock()
	h.state.Phase = "disconnected"
	h.state.Message = "设备已断开"
	h.state.DeviceName = ""
	h.state.SSID = ""
	h.state.Media = []deviceimport.Media{}
	h.state.Busy = false
	h.deviceID = ""
	h.mu.Unlock()
	return nil
}

func (h *Host) Refresh() error {
	if !h.op.TryLock() {
		return errors.New("正在读取设备，请稍候")
	}
	defer h.op.Unlock()
	h.mu.Lock()
	link := h.link
	if link == nil {
		h.mu.Unlock()
		return errors.New("请先连接设备")
	}
	if !link.Alive() {
		h.mu.Unlock()
		return errors.New("设备连接已中断，请重新连接")
	}
	h.state.Busy = true
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	files, warning, err := link.List(diagnostics.With(ctx, h.logs, nil))
	if err == nil && warning != "" {
		// 刷新作为一次整体替换；部分存储失败时不能用残缺清单覆盖已有素材。
		err = fmt.Errorf("刷新未完成，已保留原素材列表；%s", warning)
	}
	if err != nil {
		h.logs.Record("error", "media", "media.refresh_failed", "刷新素材清单失败", map[string]string{"error": err.Error()})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state.Busy = false
	if err != nil {
		return err
	}
	h.state.Media = files
	h.state.Revision++
	h.logs.Record("info", "media", "media.list_ready", "素材清单读取完成", map[string]string{"count": fmt.Sprint(len(files)), "warning": warning, "connection_id": h.sessionToken})
	h.state.Warning = warning
	return nil
}

// State 返回状态快照；Media 为副本，Phase 在链路中断时表现为 lost。
func (h *Host) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.state
	out.WiFiNetworks = append([]deviceimport.Network{}, h.state.WiFiNetworks...)
	out.Media = append([]deviceimport.Media{}, h.state.Media...)
	if h.link != nil && out.Phase == "connected" && !h.link.Alive() {
		out.Phase = "lost"
		out.Message = "设备连接已中断，请重新连接后继续导入"
	}
	return out
}

func (h *Host) SessionToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionToken
}

func (h *Host) DeviceID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deviceID
}

// Alive 暴露素材会话活性，供适配器在下载与预览前校验。
func (h *Host) Alive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.link != nil && h.link.Alive()
}

func randomHex(bytes int) string {
	var b [16]byte
	if bytes > len(b) {
		bytes = len(b)
	}
	_, _ = rand.Read(b[:bytes])
	return hex.EncodeToString(b[:bytes])
}
