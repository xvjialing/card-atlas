package camera

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// 解码 DJI 的 status + 长度 + 字符串；只移除结尾 C 字符串终止符，保留合法空格。
func decodeWiFiCredential(payload []byte, password bool) (string, error) {
	if len(payload) < 2 || payload[0] != 0 || payload[1] == 0 || int(payload[1])+2 > len(payload) {
		return "", errors.New("相机返回的热点信息格式不完整")
	}
	value := strings.TrimRight(string(payload[2:2+int(payload[1])]), "\x00")
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", errors.New("相机返回的热点信息包含不支持的字符")
	}
	if !password {
		if len(value) < 1 || len(value) > 32 {
			return "", errors.New("相机热点名称长度不正确")
		}
		return value, nil
	}
	if len(value) >= 8 && len(value) <= 63 {
		return value, nil
	}
	if len(value) == 64 {
		for _, c := range value {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return "", errors.New("相机返回的 WPA2 密钥格式不正确")
			}
		}
		return value, nil
	}
	return "", errors.New("相机返回的热点密码长度不正确，请在相机无线信息页面核对")
}
