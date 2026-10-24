package platform

import (
	"encoding/json"
	"errors"
	"strconv"
)

type WiFiError struct {
	Message string `json:"message"`
	Stage   string `json:"stage"`
	Domain  string `json:"domain"`
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
}

func (e *WiFiError) Error() string { return e.Message }
func decodeWiFiError(raw string) error {
	var e WiFiError
	if json.Unmarshal([]byte(raw), &e) != nil || e.Message == "" {
		return errors.New("系统返回了无法解析的 Wi-Fi 错误，请手动连接热点")
	}
	return &e
}

// 字段必须经过诊断日志的脱敏处理，不能直接输出到控制台或文件。
func WiFiErrorFields(err error) map[string]string {
	fields := map[string]string{"error": err.Error()}
	var e *WiFiError
	if errors.As(err, &e) {
		fields["system_stage"] = e.Stage
		fields["system_domain"] = e.Domain
		fields["system_code"] = strconv.Itoa(e.Code)
		fields["system_reason"] = e.Reason
	}
	return fields
}
