package upnpav

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"cardatlas/internal/diagnostics"
)

// 模拟相机接收发现包但不发布 SSDP 回应，用于验证已知描述地址的回退。
func silentDiscoveryTarget(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().String()
}

func TestDiscoveryFallsBackWithoutMulticastResponse(t *testing.T) {
	var visits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visits.Add(1)
		if r.URL.Path != "/DmsDescPush.xml" {
			t.Errorf("访问了未知服务：%s", r.URL.Path)
		}
		fmt.Fprint(w, fixtureDescription)
	}))
	defer server.Close()
	logs := diagnostics.New(t.TempDir())
	ctx, cancel := context.WithTimeout(diagnostics.With(context.Background(), logs, nil), time.Second)
	defer cancel()
	config := Discovery{Subnet: netip.MustParsePrefix("127.0.0.0/8"), Timeout: 10 * time.Millisecond,
		FallbackLocations: []string{server.URL + "/DmsDescPush.xml"},
		Accept:            func(s *Server) bool { return s.FriendlyName == "ZV-E10" },
	}
	found, err := discoverAt(ctx, server.Client(), config, silentDiscoveryTarget(t))
	if err != nil || found.FriendlyName != "ZV-E10" || visits.Load() != 1 {
		t.Fatalf("SSDP 静默时未使用描述地址回退：%+v %v，访问 %d 次", found, err, visits.Load())
	}
	page, err := logs.Read(diagnostics.Query{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var complete, fallback, ready bool
	for _, entry := range page.Entries {
		complete = complete || (entry.Event == "upnp.ssdp_complete" && entry.Fields["datagrams"] == "0")
		fallback = fallback || (entry.Event == "upnp.description_attempt" && entry.Fields["method"] == "known_address")
		ready = ready || entry.Event == "upnp.service_ready"
	}
	if !complete || !fallback || !ready {
		t.Fatalf("缺少发现阶段日志：完成=%v 回退=%v 成功=%v", complete, fallback, ready)
	}
}

func TestDiscoverySocketControlFailureCanUseHTTPFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, fixtureDescription) }))
	defer server.Close()
	blocked := errors.New("模拟组播套接字被拒绝")
	var calls int
	config := Discovery{Subnet: netip.MustParsePrefix("127.0.0.0/8"), FallbackLocations: []string{server.URL + "/DmsDescPush.xml"},
		Control: func(network, _ string, _ syscall.RawConn) error {
			calls++
			if network != "udp4" {
				t.Errorf("发现套接字协议错误：%s", network)
			}
			return blocked
		},
	}
	if _, err := discoverAt(context.Background(), server.Client(), config, "127.0.0.1:1900"); err != nil || calls != 1 {
		t.Fatalf("未配置发现接口或错误拦截了 HTTP 回退：%d %v", calls, err)
	}
	config.FallbackLocations = nil
	if _, err := discoverAt(context.Background(), server.Client(), config, "127.0.0.1:1900"); !errors.Is(err, blocked) {
		t.Fatalf("无回退时未保留底层错误：%v", err)
	}
}

func TestDiscoveryFallbackKeepsAddressAndModelBoundaries(t *testing.T) {
	config := Discovery{Subnet: netip.MustParsePrefix("192.168.0.0/16"), LocalIP: netip.MustParseAddr("192.168.0.5")}
	for _, location := range []string{
		"http://192.168.122.1:64321/DmsDescPush.xml",
	} {
		if !discoveryLocationOnLink(config, location) {
			t.Fatalf("拒绝了真实链路内相机：%s", location)
		}
	}
	for _, location := range []string{
		"http://10.0.0.1:64321/DmsDescPush.xml", "http://192.168.0.5:64321/DmsDescPush.xml",
		"http://localhost:64321/DmsDescPush.xml", "http://user@192.168.122.1:64321/DmsDescPush.xml",
		"https://192.168.122.1:64321/DmsDescPush.xml",
	} {
		if discoveryLocationOnLink(config, location) {
			t.Fatalf("接受了无效回退地址：%s", location)
		}
	}
	for _, tc := range []struct {
		name string
		doc  string
		want string
	}{
		{"机型不符", strings.Replace(fixtureDescription, "ZV-E10", "Other camera", 1), "设备描述未匹配"},
		{"损坏描述", "not xml", "未收到 Sony 素材服务响应"},
		{"跨机控制地址", strings.Replace(fixtureDescription, "/upnp/control/ContentDirectory", "http://192.0.2.1/control", 1), "未收到 Sony 素材服务响应"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.doc) }))
			defer server.Close()
			logs := diagnostics.New(t.TempDir())
			ctx, cancel := context.WithTimeout(diagnostics.With(context.Background(), logs, nil), time.Second)
			defer cancel()
			config := Discovery{Subnet: netip.MustParsePrefix("127.0.0.0/8"), Timeout: 10 * time.Millisecond,
				FallbackLocations: []string{server.URL}, Accept: func(s *Server) bool { return s.FriendlyName == "ZV-E10" },
			}
			if _, err := discoverAt(ctx, server.Client(), config, silentDiscoveryTarget(t)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("未拒绝无效服务或错误提示失真：%v", err)
			}
			page, _ := logs.Read(diagnostics.Query{Limit: 20})
			wantEvent := "upnp.description_failed"
			if tc.name == "机型不符" {
				wantEvent = "upnp.model_rejected"
			}
			for _, entry := range page.Entries {
				if entry.Event == wantEvent {
					return
				}
			}
			t.Fatalf("缺少诊断事件 %s", wantEvent)
		})
	}
}

func TestDiscoveryCancellationSkipsHTTPFallback(t *testing.T) {
	var visits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { visits.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := Discovery{Subnet: netip.MustParsePrefix("127.0.0.0/8"), FallbackLocations: []string{server.URL}}
	if _, err := discoverAt(ctx, server.Client(), config, "127.0.0.1:1900"); !errors.Is(err, context.Canceled) || visits.Load() != 0 {
		t.Fatalf("取消后仍然访问了回退服务：%d %v", visits.Load(), err)
	}
}
