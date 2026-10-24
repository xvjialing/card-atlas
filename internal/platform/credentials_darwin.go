package platform

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Security
#include <stdlib.h>
#include <string.h>
int ob_credential_read(const char *, int, char **);
int ob_credential_save(const char *, const char *);
int ob_credential_delete(const char *);
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// 凭据只存于当前用户的本机钥匙串，账号由设备标识和 SSID 的摘要组成。
type Credentials struct{}

var ErrCredentialMissing = errors.New("未找到已记住的密码，请重新输入")

func credentialError(code C.int) error {
	if code == 0 {
		return nil
	}
	if code == -25300 {
		return ErrCredentialMissing
	}
	if code == 100001 {
		return errors.New("当前运行环境禁止访问钥匙串，请在正常启动的CardAtlas应用中检查权限（错误码 100001）")
	}
	return fmt.Errorf("无法访问 macOS 钥匙串（错误码 %d），请检查钥匙串是否已解锁或改为手动输入", int(code))
}
func (Credentials) Has(account string) (bool, error) {
	a := C.CString(account)
	defer C.free(unsafe.Pointer(a))
	code := C.ob_credential_read(a, 0, nil)
	if code == -25300 {
		return false, nil
	}
	return code == 0, credentialError(code)
}
func (Credentials) Get(account string) (string, error) {
	a := C.CString(account)
	defer C.free(unsafe.Pointer(a))
	var value *C.char
	code := C.ob_credential_read(a, 1, &value)
	if err := credentialError(code); err != nil {
		return "", err
	}
	if value == nil {
		return "", ErrCredentialMissing
	}
	defer func() { C.memset(unsafe.Pointer(value), 0, C.strlen(value)); C.free(unsafe.Pointer(value)) }()
	return C.GoString(value), nil
}
func (Credentials) Set(account, password string) error {
	a, p := C.CString(account), C.CString(password)
	defer C.free(unsafe.Pointer(a))
	defer func() { C.memset(unsafe.Pointer(p), 0, C.strlen(p)); C.free(unsafe.Pointer(p)) }()
	return credentialError(C.ob_credential_save(a, p))
}
func (Credentials) Delete(account string) error {
	a := C.CString(account)
	defer C.free(unsafe.Pointer(a))
	err := credentialError(C.ob_credential_delete(a))
	if errors.Is(err, ErrCredentialMissing) {
		return nil
	}
	return err
}
