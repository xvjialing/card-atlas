package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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

func TestAddressTimeoutExplainsDHCPAndPreservesManualSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := waitCameraNetwork(ctx, "Camera", func() WiFiSnapshot {
		return WiFiSnapshot{"Camera", "en1", []string{"192.168.5.22/24"}}
	}, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "DHCP") || !strings.Contains(err.Error(), "先记下原配置") {
		t.Fatalf("缺少固定 IP 恢复指引：%v", err)
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
