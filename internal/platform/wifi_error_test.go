package platform

import "testing"

func TestWiFiErrorKeepsNativeDetailsOutOfUserMessage(t *testing.T) {
	err := decodeWiFiError(`{"message":"请手动连接相机热点","stage":"associate","domain":"测试错误域","code":-42,"reason":"测试原生原因"}`)
	if err.Error() != "请手动连接相机热点" {
		t.Fatal("界面混入原生错误数据")
	}
	fields := WiFiErrorFields(err)
	if fields["system_code"] != "-42" || fields["system_domain"] != "测试错误域" || fields["system_stage"] != "associate" || fields["system_reason"] != "测试原生原因" {
		t.Fatal("日志缺失原生错误详情", fields)
	}
}
