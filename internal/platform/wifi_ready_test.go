package platform

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestSonyNetworkSelectionUsesWiFiAndBothSubnets(t *testing.T) {
	subnets := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("192.168.122.0/24")}
	for _, raw := range []string{"10.0.0.2/24", "192.168.122.2/24"} {
		snapshot := WiFiSnapshot{SSID: "DIRECT-test:ZV-E10", Interface: "en0", Addresses: []string{"fe80::1/64", "192.168.5.22/24", raw}}
		ip, subnet, err := DeviceWiFiAddress(snapshot, snapshot.SSID, subnets...)
		if err != nil || !subnet.Contains(ip) || ip.String() != strings.Split(raw, "/")[0] {
			t.Fatalf("错误的接口绑定：%s %s %v", ip, subnet, err)
		}
		snapshot.SSID = "Home"
		if _, _, err := DeviceWiFiAddress(snapshot, "DIRECT-test:ZV-E10", subnets...); err == nil {
			t.Fatal("忽略了热点不匹配")
		}
	}
	for _, raw := range []string{"10.0.0.0/24", "10.0.0.1/24", "10.0.0.255/24", "192.168.122.1/24", "169.254.5.2/16"} {
		if _, _, err := DeviceWiFiAddress(WiFiSnapshot{Interface: "en0", Addresses: []string{raw}}, "", subnets...); err == nil {
			t.Fatalf("无效客户端地址：%s", raw)
		}
	}
}

func TestDevicePeerSelectionUsesActualWiFiMask(t *testing.T) {
	peers := []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("192.168.122.1")}
	for _, tc := range []struct {
		address string
		network string
	}{
		{"192.168.0.5/16", "192.168.0.0/16"},
		{"192.168.122.5/24", "192.168.122.0/24"},
		{"10.0.0.5/24", "10.0.0.0/24"},
		{"192.168.0.255/16", "192.168.0.0/16"},
		{"192.168.122.5/25", "192.168.122.0/25"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			snapshot := WiFiSnapshot{"DIRECT-test:ZV-E10", "en1", []string{"fe80::1/64", "192.168.5.47/24", tc.address}}
			ip, subnet, err := DeviceWiFiAddressForPeers(snapshot, snapshot.SSID, peers...)
			if err != nil || ip.String() != strings.Split(tc.address, "/")[0] || subnet.String() != tc.network {
				t.Fatalf("没有按实际 Wi-Fi 掩码选择地址：%s %s %v", ip, subnet, err)
			}
			if !subnet.Contains(peers[0]) && !subnet.Contains(peers[1]) {
				t.Fatal("发现网段未包含相机地址")
			}
		})
	}
}

func TestDevicePeerSelectionRejectsInvalidClients(t *testing.T) {
	peers := []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("192.168.122.1")}
	for _, address := range []string{
		"192.168.0.5/24", "192.168.5.47/24", "192.168.0.5/8", "192.168.0.5/0",
		"192.168.0.0/16", "192.168.255.255/16", "192.168.122.1/16",
		"192.168.122.127/25", "192.168.122.5/32", "10.0.0.1/24", "10.0.0.255/24",
		"169.254.5.2/16", "127.0.0.1/8", "fe80::1/64", "invalid",
	} {
		t.Run(address, func(t *testing.T) {
			snapshot := WiFiSnapshot{"Camera", "en1", []string{address}}
			if _, _, err := DeviceWiFiAddressForPeers(snapshot, "Camera", peers...); err == nil {
				t.Fatalf("接受了无效客户端地址：%s", address)
			}
		})
	}
	for _, tc := range []struct {
		ssid      string
		iface     string
		wantReady bool
	}{
		{"Camera", "en1", true}, {"", "en1", true}, {"<redacted>", "en1", true},
		{"Home", "en1", false}, {"Camera", "", false},
	} {
		snapshot := WiFiSnapshot{tc.ssid, tc.iface, []string{"192.168.0.5/16"}}
		_, _, err := DeviceWiFiAddressForPeers(snapshot, "Camera", peers...)
		if (err == nil) != tc.wantReady {
			t.Fatalf("忽略了热点或网卡状态：%+v，%v", tc, err)
		}
	}
}

func TestCameraSourceAddressIgnoresOtherSubnets(t *testing.T) {
	ip, err := cameraClientIP(WiFiSnapshot{Interface: "en1", Addresses: []string{"fe80::1/64", "192.168.5.47/24", "192.168.2.88/24"}})
	if err != nil || ip.String() != "192.168.2.88" {
		t.Fatalf("未绑定 DJI Wi-Fi 地址：%s %v", ip, err)
	}
	for _, raw := range []string{"192.168.5.47/24", "192.168.2.1/24", "192.168.2.0/24", "192.168.2.255/24", "169.254.1.2/16"} {
		if _, err := cameraClientIP(WiFiSnapshot{Interface: "en1", Addresses: []string{raw}}); err == nil {
			t.Fatalf("不应使用 %s 连接无人机", raw)
		}
	}
}

func TestCameraNetworkReadiness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot WiFiSnapshot
		ready    bool
	}{
		{"ready", WiFiSnapshot{"Camera", "en0", []string{"192.168.2.2/24"}}, true},
		{"hidden-ssid", WiFiSnapshot{"", "en0", []string{"192.168.2.3/24"}}, true},
		{"other-network", WiFiSnapshot{"Home", "en0", []string{"192.168.2.2/24"}}, false},
		{"old-address", WiFiSnapshot{"Camera", "en0", []string{"192.168.1.20/24"}}, false},
		{"self-assigned", WiFiSnapshot{"Camera", "en0", []string{"169.254.10.20/16"}}, false},
		{"camera-address", WiFiSnapshot{"Camera", "en0", []string{"192.168.2.1/24"}}, false},
		{"no-wifi", WiFiSnapshot{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready, _ := cameraNetworkReady(tc.snapshot, "Camera")
			if ready != tc.ready {
				t.Fatal("网络就绪状态错误")
			}
		})
	}
}

func TestAddressTimeoutDistinguishesMissingAndMismatchedAddresses(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    string
	}{
		{"192.168.5.22/24", "已取得 IPv4 地址（192.168.5.22/24）"},
		{"169.254.5.22/16", "尚未取得可用的 IPv4 地址"},
		{"fe80::1/64", "尚未取得可用的 IPv4 地址"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := waitCameraNetwork(ctx, "Camera", func() WiFiSnapshot {
				return WiFiSnapshot{"Camera", "en1", []string{tc.address}}
			}, time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "若已使用 DHCP") || !strings.Contains(err.Error(), "DHCP 续租") {
				t.Fatalf("地址错误提示不准确：%v", err)
			}
			if !strings.HasPrefix(tc.address, "192.") && !strings.Contains(err.Error(), "先记下原配置") {
				t.Fatalf("缺少固定 IP 恢复指引：%v", err)
			}
		})
	}
}

func TestWaitForDevicePeerDHCP(t *testing.T) {
	n := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitDeviceWiFiAddress(ctx, func() WiFiSnapshot {
		n++
		address := "192.168.5.47/24"
		if n >= 3 {
			address = "192.168.0.5/16"
		}
		return WiFiSnapshot{"Camera", "en1", []string{address}}
	}, time.Millisecond, func(snapshot WiFiSnapshot) (netip.Addr, netip.Prefix, error) {
		return DeviceWiFiAddressForPeers(snapshot, "Camera", netip.MustParseAddr("192.168.122.1"))
	})
	if err != nil || n != 3 {
		t.Fatalf("未正确等待 Sony /16 DHCP 地址：检查 %d 次，%v", n, err)
	}
}

func TestWaitForDHCPAndCancellation(t *testing.T) {
	n := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitCameraNetwork(ctx, "Camera", func() WiFiSnapshot {
		n++
		if n < 3 {
			return WiFiSnapshot{"Camera", "en0", nil}
		}
		return WiFiSnapshot{"Camera", "en0", []string{"192.168.2.2/24"}}
	}, time.Millisecond)
	if err != nil || n < 3 {
		t.Fatal("未等待 DHCP", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := waitCameraNetwork(canceled, "Camera", func() WiFiSnapshot { return WiFiSnapshot{} }, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未生效", err)
	}
}
