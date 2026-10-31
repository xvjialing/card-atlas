package platform

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework Foundation -framework CoreWLAN -framework CoreLocation
#include <stdlib.h>
int ob_wifi_authorization(int);
char *ob_wifi_ssid(void);
char *ob_wifi_interface(void);
char *ob_wifi_scan(char **);
char *ob_wifi_join(const char *, const char *);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// WiFiSocketControl 将设备连接固定到同一 Wi-Fi 接口，并为 UDP 显式指定组播出口。
// 仅绑定源 IP 不会替代 macOS 的组播接口选择；有线网卡仍可保留默认路由。
func WiFiSocketControl(snapshot WiFiSnapshot, ip netip.Addr) (func(string, string, syscall.RawConn) error, error) {
	if !ip.Is4() || snapshot.Interface == "" {
		return nil, errors.New("相机 Wi-Fi 接口或 IPv4 地址无效")
	}
	iface, err := net.InterfaceByName(snapshot.Interface)
	if err != nil {
		return nil, err
	}
	return func(network, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
			if socketErr == nil && strings.HasPrefix(network, "udp") {
				socketErr = unix.SetsockoptInet4Addr(int(fd), unix.IPPROTO_IP, unix.IP_MULTICAST_IF, ip.As4())
			}
		}); err != nil {
			return err
		}
		return socketErr
	}, nil
}

// CameraWiFiDialer 将素材 UDP 与 HTTP 都固定到 Wi-Fi 接口，不修改全局网络服务顺序或路由。
func CameraWiFiDialer(network string, localPort int) (*net.Dialer, error) {
	snapshot := CurrentWiFi()
	ip, err := cameraClientIP(snapshot)
	if err != nil {
		return nil, err
	}
	iface, err := net.InterfaceByName(snapshot.Interface)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	switch network {
	case "udp4":
		d.LocalAddr = &net.UDPAddr{IP: net.IP(ip.AsSlice()), Port: localPort}
	case "tcp4":
		d.LocalAddr = &net.TCPAddr{IP: net.IP(ip.AsSlice()), Port: localPort}
	default:
		return nil, errors.New("设备网络只支持 IPv4 UDP/TCP")
	}
	d.Control = func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, iface.Index)
		}); err != nil {
			return err
		}
		return socketErr
	}
	return d, nil
}

func CurrentSSID() string {
	s := C.ob_wifi_ssid()
	if s == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(s))
	name := C.GoString(s)
	if name == "<redacted>" {
		return ""
	}
	return name
}

func CurrentWiFi() WiFiSnapshot {
	snapshot := WiFiSnapshot{SSID: CurrentSSID()}
	name := C.ob_wifi_interface()
	if name == nil {
		return snapshot
	}
	defer C.free(unsafe.Pointer(name))
	snapshot.Interface = C.GoString(name)
	iface, err := net.InterfaceByName(snapshot.Interface)
	if err != nil {
		return snapshot
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return snapshot
	}
	for _, address := range addresses {
		snapshot.Addresses = append(snapshot.Addresses, address.String())
	}
	return snapshot
}

func WaitCameraWiFi(ctx context.Context, expected string) error {
	return WaitDeviceWiFi(ctx, expected, cameraSubnet)
}
func JoinWiFi(ssid, password string) error {
	s, p := C.CString(ssid), C.CString(password)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(p))
	e := C.ob_wifi_join(s, p)
	if e != nil {
		defer C.free(unsafe.Pointer(e))
		return decodeWiFiError(C.GoString(e))
	}
	return nil
}
func RestoreWiFi(ctx context.Context, ssid string) error {
	if ssid == "" {
		return errors.New("未能读取原 Wi-Fi 名称，请通过 macOS 菜单栏手动恢复网络")
	}
	// 仅传网络名称，密码由系统钥匙串处理。
	data, err := exec.CommandContext(ctx, "/usr/sbin/networksetup", "-listallhardwareports").Output()
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	device := ""
	for i, l := range lines {
		if strings.Contains(l, "Wi-Fi") && i+1 < len(lines) {
			device = strings.TrimSpace(strings.TrimPrefix(lines[i+1], "Device:"))
			break
		}
	}
	if device == "" {
		return errors.New("无法识别 Wi-Fi 网卡，请手动恢复网络")
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/networksetup", "-setairportnetwork", device, ssid).CombinedOutput()
	if err != nil || strings.Contains(string(out), "Could not") || strings.Contains(string(out), "Failed") {
		return errors.New("恢复原 Wi-Fi 失败，请通过 macOS 菜单栏重新连接")
	}
	return nil
}

func ScanWiFi(ctx context.Context) ([]WiFiNetwork, error) {
	if err := authorizeWiFiScan(ctx); err != nil {
		return nil, err
	}
	var nativeError *C.char
	data := C.ob_wifi_scan(&nativeError)
	if nativeError != nil {
		defer C.free(unsafe.Pointer(nativeError))
		return nil, decodeWiFiError(C.GoString(nativeError))
	}
	if data == nil {
		return nil, errors.New("无法读取 Wi-Fi 扫描结果，请检查系统权限")
	}
	defer C.free(unsafe.Pointer(data))
	networks := []WiFiNetwork{}
	if err := json.Unmarshal([]byte(C.GoString(data)), &networks); err != nil {
		return nil, errors.New("Wi-Fi 扫描结果格式异常")
	}
	return networks, nil
}

// SSID 读取由 macOS 定位服务授权控制；仅请求权限，不启动位置更新。
func authorizeWiFiScan(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request := C.int(1)
	for {
		status := int(C.ob_wifi_authorization(request))
		request = 0
		switch status {
		case 3, 4:
			return nil
		case -1:
			return errors.New("请在 macOS 系统设置 → 隐私与安全性 → 定位服务中开启定位服务，才能扫描热点名称")
		case 1, 2:
			return errors.New("请在 macOS 系统设置 → 隐私与安全性 → 定位服务中允许CardAtlas，再重新扫描热点")
		}
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("尚未获得热点扫描权限，请在系统弹窗允许定位访问后重试；建议使用打包后的CardAtlas应用")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
