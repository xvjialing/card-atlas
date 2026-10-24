package platform

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

type WiFiSnapshot struct {
	SSID      string
	Interface string
	Addresses []string
}

var cameraSubnet = netip.MustParsePrefix("192.168.2.0/24")

// 相机固定服务地址为 192.168.2.1；不能把尚未完成 DHCP 的旧网段地址当成就绪。
func cameraNetworkReady(snapshot WiFiSnapshot, expected string) (bool, string) {
	if snapshot.Interface == "" {
		return false, "没有可用的 Wi-Fi 网卡"
	}
	if expected != "" && snapshot.SSID != "" && snapshot.SSID != "<redacted>" && snapshot.SSID != expected {
		return false, "Mac 仍连接其他 Wi-Fi，请先在系统菜单加入相机热点"
	}
	for _, raw := range snapshot.Addresses {
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			continue
		}
		ip := p.Addr().Unmap()
		if cameraSubnet.Contains(ip) && ip.String() != "192.168.2.1" && ip.String() != "192.168.2.0" && ip.String() != "192.168.2.255" {
			return true, ""
		}
	}
	return false, "Mac 尚未获得相机网段的 IPv4 地址。如果已连接相机热点，请到系统设置 → Wi-Fi → 详细信息 → TCP/IP，将配置 IPv4 设为“使用 DHCP”后重试；如原网络使用固定 IP，请先记下原配置，切回原网络时恢复"
}

func waitCameraNetwork(ctx context.Context, expected string, inspect func() WiFiSnapshot, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	reason := "设备网络等待超时，请确认相机热点后重试"
	for {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New(reason)
			}
			return ctx.Err()
		}
		ok, currentReason := cameraNetworkReady(inspect(), expected)
		reason = currentReason
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New(reason)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
