package action6

import (
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Adapter) Scan() ([]camera.Device, error) {
	if !s.op.TryLock() {
		return nil, errors.New("正在处理相机连接，请稍候")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	if s.link != nil || s.ble != nil || pendingPhase(s.state.Phase) {
		s.mu.Unlock()
		return nil, errors.New("请先继续或取消当前相机连接")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	s.cancelOp = cancel
	s.state.Busy = true
	s.state.Message = "正在搜索附近的 DJI 相机"
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancelOp = nil; s.state.Busy = false; s.mu.Unlock() }()
	s.logs.Record("info", "bluetooth", "ble.scan_start", "开始扫描附近相机", nil)
	start := time.Now()
	devices, err := s.bluetooth.Scan(diagnostics.With(ctx, s.logs, nil))
	fields := map[string]string{"elapsed_ms": fmt.Sprint(time.Since(start).Milliseconds()), "count": fmt.Sprint(len(devices))}
	if err != nil {
		fields["error"] = err.Error()
		s.logs.Record("error", "bluetooth", "ble.scan_failed", "扫描相机失败", fields)
	} else {
		s.logs.Record("info", "bluetooth", "ble.scan_complete", "相机扫描完成", fields)
	}
	if err == nil {
		s.mu.Lock()
		s.deviceNames = map[string]string{}
		for _, d := range devices {
			s.deviceNames[d.ID] = d.Name
		}
		s.mu.Unlock()
	}
	return devices, err
}

func (s *Adapter) releaseMediaConnection() {
	s.mu.Lock()
	link := s.link
	s.link = nil
	s.mu.Unlock()
	if link != nil {
		link.Close()
	}
}

func (s *Adapter) cleanupConnection() {
	s.mu.Lock()
	link, ble := s.link, s.ble
	s.link = nil
	s.ble = nil
	previous, switched := s.previousSSID, s.switched
	connectionID := s.connectionID
	s.switched = false
	s.previousSSID = ""
	s.connectionID = ""
	s.wifiHint = ""
	s.state.WiFiNetworks = []WiFiCandidate{}
	s.mu.Unlock()
	if link != nil {
		link.Close()
	}
	if ble != nil {
		ble.Close()
	}
	if switched {
		s.logs.Record("info", "wifi", "wifi.restore_start", "尝试恢复原 Wi-Fi", map[string]string{"connection_id": connectionID})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := s.connection.restore(ctx, previous)
		cancel()
		if err != nil {
			s.logs.Record("warn", "wifi", "wifi.restore_failed", "恢复原网络失败", map[string]string{"error": err.Error(), "connection_id": connectionID})
		} else {
			s.logs.Record("info", "wifi", "wifi.restored", "已恢复原网络", map[string]string{"connection_id": connectionID})
		}
		if err != nil {
			s.mu.Lock()
			s.state.Warning = err.Error()
			s.mu.Unlock()
		}
	}
}

func (s *Adapter) Disconnect() error {
	s.logs.Record("info", "connection", "connection.disconnect", "正在断开相机并暂停传输", nil)
	s.mu.Lock()
	if s.cancelOp != nil {
		s.cancelOp()
	}
	s.mu.Unlock()

	s.op.Lock()
	defer s.op.Unlock()
	s.cleanupConnection()
	s.mu.Lock()
	s.state.Phase = "disconnected"
	s.state.Message = "相机已断开"
	s.state.DeviceName = ""
	s.state.SSID = ""
	s.state.Media = []camera.Media{}
	s.state.Busy = false
	s.mu.Unlock()
	return nil
}

func (s *Adapter) Refresh() error {
	if !s.op.TryLock() {
		return errors.New("正在读取相机，请稍候")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	link := s.link
	if link == nil {
		s.mu.Unlock()
		return errors.New("请先连接相机")
	}
	s.state.Busy = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	files, warning, err := link.List(diagnostics.With(ctx, s.logs, nil))
	if err == nil && warning != "" {
		// 刷新作为一次整体替换；部分存储失败时不能用残缺清单覆盖已有素材。
		err = fmt.Errorf("刷新未完成，已保留原素材列表；%s", warning)
	}
	if err != nil {
		s.logs.Record("error", "media", "media.refresh_failed", "刷新素材清单失败", map[string]string{"error": err.Error()})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Busy = false
	if err != nil {
		return err
	}
	s.state.Media = files
	s.state.Revision++
	s.logs.Record("info", "media", "media.list_ready", "素材清单读取完成", map[string]string{"count": fmt.Sprint(len(files)), "warning": warning, "connection_id": s.connectionID})
	s.state.Warning = warning
	return nil
}
