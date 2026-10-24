package camera

import (
	"strings"
	"testing"
)

func TestWiFiCredentialWireFormat(t *testing.T) {
	encode := func(value string) []byte { return append([]byte{0, byte(len(value))}, []byte(value)...) }
	for _, value := range []string{"testpass42", " password ", "testpass42\x00", strings.Repeat("a", 64)} {
		got, err := decodeWiFiCredential(encode(value), true)
		if err != nil || got != strings.TrimRight(value, "\x00") {
			t.Fatalf("有效密码格式解析失败：%v", err)
		}
	}
	for _, p := range [][]byte{{}, {0, 9, 'a'}, encode("short"), encode("test\x00pass42"), encode(strings.Repeat("x", 64)), {1, 8, '1', '2', '3', '4', '5', '6', '7', '8'}} {
		if _, err := decodeWiFiCredential(p, true); err == nil {
			t.Fatal("接受了非法密码报文")
		}
	}
	if v, err := decodeWiFiCredential(encode("Osmo 测试"), false); err != nil || v != "Osmo 测试" {
		t.Fatal("SSID 解析失败")
	}
}
