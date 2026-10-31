//go:build !darwin || !cgo

package platform

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
)

var errUnsupported = errors.New("当前平台暂不支持无线设备连接，请使用 macOS 客户端或已挂载的存储目录")
var ErrCredentialMissing = errors.New("未找到已记住的密码")

type Credentials struct{}

func (Credentials) Has(string) (bool, error)   { return false, errUnsupported }
func (Credentials) Get(string) (string, error) { return "", errUnsupported }
func (Credentials) Set(string, string) error   { return errUnsupported }
func (Credentials) Delete(string) error        { return errUnsupported }
func CurrentSSID() string                      { return "" }
func CurrentWiFi() WiFiSnapshot                { return WiFiSnapshot{} }
func WiFiSocketControl(WiFiSnapshot, netip.Addr) (func(string, string, syscall.RawConn) error, error) {
	return nil, errUnsupported
}
func CameraWiFiDialer(string, int) (*net.Dialer, error) { return nil, errUnsupported }
func ScanWiFi(context.Context) ([]WiFiNetwork, error)   { return nil, errUnsupported }
func JoinWiFi(string, string) error                     { return errUnsupported }
func WaitCameraWiFi(context.Context, string) error      { return errUnsupported }
func RestoreWiFi(context.Context, string) error         { return errUnsupported }
