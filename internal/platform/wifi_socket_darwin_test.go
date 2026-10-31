//go:build darwin && cgo

package platform

import (
	"context"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// 核对真实内核套接字选项，不读取 CoreWLAN、切换热点或访问相机。
func TestWiFiSocketControlBindsUDPAndTCPToInterface(t *testing.T) {
	iface, err := net.InterfaceByName("lo0")
	if err != nil {
		t.Fatal(err)
	}
	ip := netip.MustParseAddr("127.0.0.1")
	control, err := WiFiSocketControl(WiFiSnapshot{Interface: iface.Name}, ip)
	if err != nil {
		t.Fatal(err)
	}
	check := func(raw syscall.RawConn, multicast bool) {
		t.Helper()
		var bound int
		var outgoing [4]byte
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			bound, optionErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF)
			if optionErr == nil && multicast {
				outgoing, optionErr = unix.GetsockoptInet4Addr(int(fd), unix.IPPROTO_IP, unix.IP_MULTICAST_IF)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if optionErr != nil || bound != iface.Index || (multicast && outgoing != ip.As4()) {
			t.Fatalf("未实际绑定 Wi-Fi 接口/组播出口：接口=%d 组播=%v，%v", bound, outgoing, optionErr)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	config := net.ListenConfig{Control: control}
	packet, err := config.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	raw, err := packet.(*net.UDPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	check(raw, true)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dialer := net.Dialer{Control: control, LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip.String())}}
	conn, err := dialer.DialContext(ctx, "tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw, err = conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	check(raw, false)
}
