package platform

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type WiFiSnapshot struct {
	SSID      string
	Interface string
	Addresses []string
}

var cameraSubnet = netip.MustParsePrefix("192.168.2.0/24")

// cameraClientIP 仅选择 Wi-Fi 网卡自身的设备网段地址，不从有线网卡或默认路由猜测。
func cameraClientIP(snapshot WiFiSnapshot) (netip.Addr, error) {
	if snapshot.Interface != "" {
		reserved := reservedAddresses(cameraSubnet)
		for _, raw := range snapshot.Addresses {
			prefix, err := netip.ParsePrefix(raw)
			if err == nil && cameraSubnet.Contains(prefix.Addr()) && !reserved[prefix.Addr().String()] {
				return prefix.Addr(), nil
			}
		}
	}
	return netip.Addr{}, errors.New("Wi-Fi 网卡尚未取得无人机网段地址，请在系统 Wi-Fi 菜单加入 DJI 热点并等待 DHCP；有线网络可保持连接")
}

// deviceNetworkReady 判断 Mac 是否已获得指定设备网段的 IPv4 地址。
// 网段基地址、网关地址与广播地址都不算客户端就绪；不能把尚未完成 DHCP 的旧网段地址当成就绪。
func deviceNetworkReady(snapshot WiFiSnapshot, expected string, subnet netip.Prefix) (bool, string) {
	if reason := wifiSnapshotIssue(snapshot, expected); reason != "" {
		return false, reason
	}
	reserved := reservedAddresses(subnet)
	for _, raw := range snapshot.Addresses {
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			continue
		}
		ip := p.Addr().Unmap()
		if subnet.Contains(ip) && !reserved[ip.String()] {
			return true, ""
		}
	}
	return false, wifiAddressIssue(snapshot)
}

func wifiSnapshotIssue(snapshot WiFiSnapshot, expected string) string {
	if snapshot.Interface == "" {
		return "没有可用的 Wi-Fi 网卡"
	}
	if expected != "" && snapshot.SSID != "" && snapshot.SSID != "<redacted>" && snapshot.SSID != expected {
		return "Mac 仍连接其他 Wi-Fi，请先在系统菜单加入设备热点"
	}
	return ""
}

func wifiAddressIssue(snapshot WiFiSnapshot) string {
	var addresses []string
	for _, raw := range snapshot.Addresses {
		p, err := netip.ParsePrefix(raw)
		if err == nil && p.Addr().Is4() && p.Addr().IsGlobalUnicast() {
			addresses = append(addresses, p.String())
		}
	}
	if len(addresses) > 0 {
		return fmt.Sprintf("Wi-Fi 已取得 IPv4 地址（%s），但地址或子网掩码与设备网络不匹配。请确认已加入设备热点；若已使用 DHCP，可在系统设置 → Wi-Fi → 详细信息 → TCP/IP 中点「DHCP 续租」后重试", strings.Join(addresses, "、"))
	}
	return "Wi-Fi 尚未取得可用的 IPv4 地址，请确认设备热点并等待 DHCP；若已使用 DHCP，可在系统设置 → Wi-Fi → 详细信息 → TCP/IP 中点「DHCP 续租」后重试；如原网络使用固定 IP，请先记下原配置，切回原网络时恢复"
}

// reservedAddresses 返回网段内不可能属于 Mac 客户端的地址：基地址、广播地址与网关（/24 及更小主机段）。
func reservedAddresses(subnet netip.Prefix) map[string]bool {
	base := subnet.Masked().Addr()
	reserved := map[string]bool{base.String(): true}
	if !base.Is4() || subnet.Bits() >= 31 {
		return reserved
	}
	b := base.As4()
	if subnet.Bits() >= 24 {
		gateway := b
		gateway[3]++
		reserved[netip.AddrFrom4(gateway).String()] = true
	}
	reserved[ipv4Broadcast(subnet).String()] = true
	return reserved
}

func ipv4Broadcast(subnet netip.Prefix) netip.Addr {
	broadcast := subnet.Masked().Addr().As4()
	hostBits := 32 - subnet.Bits()
	for i := 3; i >= 0 && hostBits > 0; i-- {
		bits := min(hostBits, 8)
		broadcast[i] |= byte((1 << bits) - 1)
		hostBits -= bits
	}
	return netip.AddrFrom4(broadcast)
}

func waitDeviceNetwork(ctx context.Context, expected string, subnet netip.Prefix, inspect func() WiFiSnapshot, interval time.Duration) error {
	return waitDeviceNetworks(ctx, expected, []netip.Prefix{subnet}, inspect, interval)
}

// DeviceWiFiAddress 从实际 Wi-Fi 快照选取设备网段地址，供发现和 HTTP 绑定同一接口。
func DeviceWiFiAddress(snapshot WiFiSnapshot, expected string, subnets ...netip.Prefix) (netip.Addr, netip.Prefix, error) {
	reason := "Mac 尚未获得设备网段的 IPv4 地址，请确认设备热点并等待 DHCP"
	for _, subnet := range subnets {
		ready, current := deviceNetworkReady(snapshot, expected, subnet)
		if !ready {
			reason = current
			continue
		}
		for _, raw := range snapshot.Addresses {
			p, err := netip.ParsePrefix(raw)
			if err == nil && subnet.Contains(p.Addr().Unmap()) && !reservedAddresses(subnet)[p.Addr().Unmap().String()] {
				return p.Addr().Unmap(), subnet, nil
			}
		}
	}
	return netip.Addr{}, netip.Prefix{}, errors.New(reason)
}

// DeviceWiFiAddressForPeers 按实际 Wi-Fi 掩码选择能直连已知设备地址的客户端地址。
// Sony 的客户端地址与相机地址可能分属不同 /24；返回真实网段供 SSDP 校验，拒绝旧网段、非私网与设备自身地址。
func DeviceWiFiAddressForPeers(snapshot WiFiSnapshot, expected string, peers ...netip.Addr) (netip.Addr, netip.Prefix, error) {
	if reason := wifiSnapshotIssue(snapshot, expected); reason != "" {
		return netip.Addr{}, netip.Prefix{}, errors.New(reason)
	}
	for _, raw := range snapshot.Addresses {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() > 30 {
			continue
		}
		subnet := prefix.Masked()
		if !subnet.Addr().IsPrivate() || !ipv4Broadcast(subnet).IsPrivate() {
			continue
		}
		ip := prefix.Addr()
		reserved := reservedAddresses(subnet)
		onLink := false
		for _, peer := range peers {
			reserved[peer.String()] = true
			onLink = onLink || (peer.Is4() && subnet.Contains(peer))
		}
		if onLink && !reserved[ip.String()] {
			return ip, subnet, nil
		}
	}
	return netip.Addr{}, netip.Prefix{}, errors.New(wifiAddressIssue(snapshot))
}

func waitDeviceNetworks(ctx context.Context, expected string, subnets []netip.Prefix, inspect func() WiFiSnapshot, interval time.Duration) error {
	return waitDeviceWiFiAddress(ctx, inspect, interval, func(snapshot WiFiSnapshot) (netip.Addr, netip.Prefix, error) {
		return DeviceWiFiAddress(snapshot, expected, subnets...)
	})
}

func waitDeviceWiFiAddress(ctx context.Context, inspect func() WiFiSnapshot, interval time.Duration, selectAddress func(WiFiSnapshot) (netip.Addr, netip.Prefix, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	reason := "设备网络等待超时，请确认设备热点后重试"
	for {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New(reason)
			}
			return ctx.Err()
		}
		_, _, err := selectAddress(inspect())
		if err == nil {
			return nil
		}
		reason = err.Error()
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

func cameraNetworkReady(snapshot WiFiSnapshot, expected string) (bool, string) {
	return deviceNetworkReady(snapshot, expected, cameraSubnet)
}

func waitCameraNetwork(ctx context.Context, expected string, inspect func() WiFiSnapshot, interval time.Duration) error {
	return waitDeviceNetwork(ctx, expected, cameraSubnet, inspect, interval)
}

// WaitDeviceWiFi 等待 Mac 加入指定网段的设备热点并完成网络地址分配。
func WaitDeviceWiFi(ctx context.Context, expected string, subnet netip.Prefix) error {
	return WaitDeviceWiFiAny(ctx, expected, subnet)
}

// WaitDeviceWiFiAny 兼容同系列相机的多个热点网段，仍仅检查 Wi-Fi 网卡。
func WaitDeviceWiFiAny(ctx context.Context, expected string, subnets ...netip.Prefix) error {
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return waitDeviceNetworks(wait, expected, subnets, CurrentWiFi, 250*time.Millisecond)
}

// WaitDeviceWiFiForPeers 等待 Wi-Fi 的实际地址和掩码能够直连设备，供 Sony 的不同 DHCP 掩码共用。
func WaitDeviceWiFiForPeers(ctx context.Context, expected string, peers ...netip.Addr) error {
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return waitDeviceWiFiAddress(wait, CurrentWiFi, 250*time.Millisecond, func(snapshot WiFiSnapshot) (netip.Addr, netip.Prefix, error) {
		return DeviceWiFiAddressForPeers(snapshot, expected, peers...)
	})
}
