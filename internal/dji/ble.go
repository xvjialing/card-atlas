package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cardatlas/internal/diagnostics"
	"tinygo.org/x/bluetooth"
)

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	RSSI int16  `json:"rssi"`
}
type Bluetooth struct {
	mu      sync.Mutex
	enabled bool
	devices map[string]bluetooth.ScanResult
}

func (b *Bluetooth) Scan(ctx context.Context) ([]Device, error) {
	return b.scanFiltered(ctx, func(name string, dji bool) bool {
		return strings.Contains(name, "osmo") || dji
	}, "DJI 相机")
}

// ScanAircraft 扫描 DJI 飞行器蓝牙：名称包含 DJI，或携带 DJI 厂商数据。
func (b *Bluetooth) ScanAircraft(ctx context.Context) ([]Device, error) {
	return b.scanFiltered(ctx, func(name string, dji bool) bool {
		return dji || strings.Contains(name, "dji")
	}, "DJI 无人机")
}

func (b *Bluetooth) scanFiltered(ctx context.Context, match func(name string, dji bool) bool, fallbackName string) ([]Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := bluetooth.DefaultAdapter
	if !b.enabled {
		if err := a.Enable(); err != nil {
			return nil, fmt.Errorf("蓝牙不可用，请在系统设置中允许CardAtlas使用蓝牙：%w", err)
		}
		b.enabled = true
	}
	items := []Device{}
	b.devices = map[string]bluetooth.ScanResult{}
	var guard sync.Mutex
	done := make(chan error, 1)
	go func() {
		done <- a.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
			name := strings.ToLower(r.LocalName())
			var dji bool
			for _, d := range r.ManufacturerData() {
				if d.CompanyID == 0x08aa {
					dji = true
				}
			}
			if !match(name, dji) {
				return
			}
			guard.Lock()
			defer guard.Unlock()
			id := r.Address.String()
			if _, ok := b.devices[id]; ok {
				return
			}
			b.devices[id] = r
			display := r.LocalName()
			if display == "" {
				display = fallbackName
			}
			items = append(items, Device{id, display, r.RSSI})
		})
	}()
	select {
	case err := <-done:
		return items, err
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
	}
	_ = a.StopScan()
	err := <-done
	guard.Lock()
	defer guard.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return items, err
}

type BLESession struct {
	device        bluetooth.Device
	write         bluetooth.DeviceCharacteristic
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	trace         diagnostics.Trace
	lastSendError time.Time
}

func (s *BLESession) send(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.write.WriteWithoutResponse(m.Encode())
	if e != nil && time.Since(s.lastSendError) > 10*time.Second {
		s.lastSendError = time.Now()
		s.trace.Log("warn", "bluetooth", "ble.write_failed", "蓝牙命令写入失败", map[string]string{"command": fmt.Sprintf("%02x/%02x", m.Set, m.ID), "error": e.Error()})
	}
	return e
}
func (s *BLESession) Close() {
	if s == nil {
		return
	}
	s.cancel()
	<-s.done
	_ = s.device.Disconnect()
}

// Pair 以 Osmo 相机流程配对：确认动作是相机屏幕上的配对请求。
func (b *Bluetooth) Pair(ctx context.Context, id, identity string, stage func(string)) (*BLESession, string, error) {
	return b.pair(ctx, id, identity, stage, "请在相机屏幕上确认 OSMO 配对请求", "该设备未提供兼容的 Osmo 蓝牙服务", false)
}

// PairAircraft 以飞行器流程配对：确认动作是机身长按电源键约 3 秒（与 DJI Fly 手机快传首次授权一致）。
func (b *Bluetooth) PairAircraft(ctx context.Context, id, identity string, stage func(string)) (*BLESession, string, error) {
	return b.pair(ctx, id, identity, stage, "请在飞行器旁长按电源键约 3 秒完成授权，听到提示音即成功", "该设备未提供兼容的 DJI 蓝牙服务", true)
}

func (b *Bluetooth) pair(ctx context.Context, id, identity string, stage func(string), confirmStage, serviceError string, aircraft bool) (session *BLESession, ssid string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	trace := diagnostics.From(ctx)
	result, ok := b.devices[id]
	if !ok {
		return nil, "", errors.New("请选择扫描列表中的相机")
	}
	if trace.Store != nil {
		trace.Store.RegisterSecrets(id, identity, result.LocalName())
	}
	trace.Log("info", "bluetooth", "ble.connect", "正在连接相机蓝牙服务", nil)
	d, err := bluetooth.DefaultAdapter.Connect(result.Address, bluetooth.ConnectionParams{})
	if err != nil {
		return nil, "", fmt.Errorf("蓝牙连接失败：%w", err)
	}
	s := &BLESession{device: d, done: make(chan struct{}), trace: trace}
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	success := false
	running := false
	defer func() {
		if !success {
			cancel()
			if running {
				<-s.done
			}
			_ = d.Disconnect()
		}
	}()
	trace.Log("debug", "bluetooth", "ble.services", "开始发现相机服务与特征", nil)
	services, e := d.DiscoverServices(nil)
	if e != nil {
		return nil, "", e
	}
	var control bluetooth.DeviceCharacteristic
	haveControl, haveData := false, false
	for _, service := range services {
		chars, e := service.DiscoverCharacteristics(nil)
		if e != nil {
			continue
		}
		for _, c := range chars {
			u := c.UUID().String()
			if strings.HasPrefix(u, "0000fff4") {
				control = c
				haveControl = true
			}
			if strings.HasPrefix(u, "0000fff5") {
				s.write = c
				haveData = true
			}
		}
	}
	if !haveControl || !haveData {
		return nil, "", errors.New(serviceError)
	}
	trace.Log("debug", "bluetooth", "ble.characteristics_ready", "已找到相机控制与数据特征", nil)
	inbox := make(chan Message, 128)
	for _, c := range []bluetooth.DeviceCharacteristic{control, s.write} {
		var r Reader
		var mu sync.Mutex
		err = c.EnableNotifications(func(data []byte) {
			mu.Lock()
			defer mu.Unlock()
			for _, m := range r.Feed(data) {
				select {
				case inbox <- m:
				default:
				}
			}
		})
		if err != nil {
			return nil, "", fmt.Errorf("订阅相机蓝牙通知失败：%w", err)
		}
	}
	trace.Log("debug", "bluetooth", "ble.notifications_ready", "蓝牙通知订阅完成", nil)
	if _, err = control.Write([]byte{1, 0}); err != nil {
		return nil, "", err
	}
	time.Sleep(200 * time.Millisecond)
	if err = s.send(Message{2, 0xf0, 0x8100, 0x40, 0, 0x2b, []byte{4, 0}}); err != nil {
		return nil, "", err
	}
	time.Sleep(120 * time.Millisecond)
	pairing := Pairing(identity)
	if aircraft {
		pairing = AircraftPairing(identity)
	}
	if err = s.send(pairing); err != nil {
		return nil, "", err
	}
	stage(confirmStage)
	approval := time.NewTimer(45 * time.Second)
	defer approval.Stop()
	paired := false
	pairStatusCount := 0
	answer := func(m Message) {
		if m.Flags&0xc0 == 0x40 {
			p := m.Payload
			if m.Set == 0 && m.ID == 0x81 {
				p = appInfo()
			}
			_ = s.send(Message{2, m.Sender, m.Seq, 0xc0, m.Set, m.ID, p})
		}
	}
	for !paired {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-approval.C:
			return nil, "", errors.New("配对确认超时，请重新连接并完成机身授权")
		case m := <-inbox:
			// 仅记录配对状态前两字节，保留实际标志和长度；不将配对身份或凭据写入日志。
			// 限制条数，避免异常设备持续推送状态导致诊断文件快速增长。
			if m.Set == 7 && (m.ID == 0x45 || m.ID == 0x46) && pairStatusCount < 16 {
				pairStatusCount++
				trace.Log("info", "bluetooth", "ble.pair_status", "收到设备蓝牙配对状态", map[string]string{
					"command": fmt.Sprintf("07/%02x", m.ID), "flags": fmt.Sprintf("%02x", m.Flags),
					"status_prefix": fmt.Sprintf("%x", m.Payload[:min(2, len(m.Payload))]), "content_bytes": fmt.Sprint(len(m.Payload)),
				})
			}
			answer(m)
			if m.Set == 7 && m.ID == 0x45 && len(m.Payload) >= 2 && m.Payload[1] == 1 {
				trace.Log("info", "bluetooth", "ble.pair_known", "相机认可已保存的配对标识", nil)
				paired = true
			}
			if m.Set == 7 && m.ID == 0x46 {
				if len(m.Payload) > 0 && m.Payload[0] == 1 {
					trace.Log("info", "bluetooth", "ble.pair_approved", "用户已在相机屏幕确认配对", nil)
					paired = true
				} else {
					return nil, "", errors.New("相机拒绝了配对请求")
				}
			}
		}
	}
	_ = s.send(Message{2, 0x1c, 0x8101, 0x40, 0x53, 0x10, []byte{0, 0, 0, 0}})
	// 保活持续至用户断开，相机热点才不会因蓝牙空闲而休眠。
	running = true
	go func() {
		defer close(s.done)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				_ = s.send(Message{2, 0xf0, 0x8102, 0x40, 0, 0x2b, []byte{1, 1}})
			}
		}
	}()
	stage("正在唤醒相机热点，读取名称提示")
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-time.After(800 * time.Millisecond):
	}
	for attempt := 0; attempt < 2 && ssid == ""; attempt++ {
		_ = s.send(Message{2, 7, uint16(0x8200 + attempt*2), 0x40, 7, 7, nil})
		t := time.NewTimer(4 * time.Second)
	readCredentials:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, "", ctx.Err()
			case <-t.C:
				break readCredentials
			case m := <-inbox:
				answer(m)
				p := m.Payload
				if m.Set == 7 && m.ID == 7 && len(p) >= 2 && p[0] == 0 && int(p[1])+2 <= len(p) {
					v, decodeErr := decodeWiFiCredential(p, false)
					if decodeErr != nil {
						trace.Log("warn", "bluetooth", "ble.invalid_credentials", "相机热点信息格式校验失败", map[string]string{"error": decodeErr.Error()})
						continue
					}
					if trace.Store != nil {
						trace.Store.RegisterSecrets(v)
					}
					ssid = v
				}
				if ssid != "" {
					t.Stop()
					break readCredentials
				}
			}
		}
	}
	if ssid == "" {
		trace.Log("warn", "bluetooth", "ble.ssid_unavailable", "未取得热点名称提示，继续扫描附近 Wi-Fi 供用户选择", nil)
	}
	// 后续请求继续应答，但凭据不进入日志和应用状态。
	go func() {
		for {
			select {
			case <-runCtx.Done():
				return
			case m := <-inbox:
				answer(m)
			}
		}
	}()
	trace.Log("info", "bluetooth", "ble.hotspot_awake", "蓝牙配对完成，等待用户选择热点", nil)
	success = true
	return s, ssid, nil
}
