package zve10

import (
	"bytes"
	"errors"
	"image"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "image/jpeg"
	_ "image/png"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// decodeQR 从照片字节中读取二维码文本；前端会把照片缩放到常规尺寸后提交。
func decodeQR(data []byte) (string, error) {
	if len(data) > 8<<20 {
		return "", errors.New("二维码照片过大，请缩小后重试")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return "", errors.New("二维码照片尺寸无效或过大，请使用 JPG 或 PNG 小图")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("无法解析所选照片，请使用 JPG 或 PNG 格式的二维码照片")
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", errors.New("无法识别照片中的二维码")
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		return "", errors.New("照片中未发现二维码，请在相机屏幕重新显示后重拍")
	}
	return result.GetText(), nil
}

// connectionInfo 是从二维码读出的连接信息；fragment 用于扫描结果里模糊匹配热点后缀。
type connectionInfo struct {
	ssid     string
	fragment string
	password string
}

// parseConnectionInfo 从二维码文本中提取热点名称与密码。
// 支持 Android Wi-Fi 格式（WIFI:S:…;P:…;;）、索尼配对格式（W01:S:…;P:…;C:…;M:…;）
// 以及常见 key: value / key=value 行式。
func parseConnectionInfo(text string) (connectionInfo, error) {
	trimmed := strings.TrimSpace(text)
	info := connectionInfo{}
	switch {
	case strings.HasPrefix(trimmed, "WIFI:"):
		info.ssid, info.password = parseWifiURI(trimmed)
	case sonyProvisionPrefix.MatchString(trimmed):
		info = parseSonyProvision(trimmed)
	}
	if info.ssid == "" {
		info.ssid, info.password = parseKeyedLines(trimmed)
	}
	if info.ssid == "" {
		// 索尼直连热点 SSID 以 DIRECT- 开头；二维码常包含裸热点名。
		for _, field := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == ';' }) {
			if strings.HasPrefix(strings.ToUpper(field), "DIRECT-") {
				info.ssid = field
				break
			}
		}
	}
	info.ssid = strings.Trim(info.ssid, "\"'，,")
	info.fragment = strings.Trim(info.fragment, "\"'，,")
	if info.ssid == "" {
		return connectionInfo{}, errors.New("二维码中未找到 Wi-Fi 连接信息；请确认拍摄的是相机当前的连接二维码，或在相机上选择「通过密码连接」查看密码后手动输入")
	}
	if strings.ContainsAny(info.ssid, "\x00\r\n") {
		return connectionInfo{}, errors.New("二维码中的热点名称无效")
	}
	return info, nil
}

// sonyProvisionPrefix 匹配索尼配对二维码的版本前缀（如 W01:）。
var sonyProvisionPrefix = regexp.MustCompile(`^W\d{2}:`)

// parseSonyProvision 解析索尼 W01:S:<后缀>;P:<密码>;C:<型号>;M:<MAC>; 格式。
// 直连热点名为 DIRECT-<S>:<C>；S 也可直接携带完整热点名。
func parseSonyProvision(text string) connectionInfo {
	info := connectionInfo{}
	model := ""
	body := strings.TrimPrefix(text, sonyProvisionPrefix.FindString(text))
	for _, field := range splitEscaped(body, ';') {
		key, value, found := strings.Cut(field, ":")
		if !found {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "S":
			info.ssid, info.fragment = value, value
		case "P":
			info.password = value
		case "C":
			model = value
		}
	}
	if info.ssid != "" && !strings.HasPrefix(strings.ToUpper(info.ssid), "DIRECT-") {
		ssid := "DIRECT-" + info.ssid
		if model != "" {
			ssid += ":" + model
		}
		info.ssid = ssid
	}
	return info
}

// parseWifiURI 解析 WIFI:T:WPA;S:<ssid>;P:<password>;; 格式，分号支持反斜杠转义。
func parseWifiURI(text string) (string, string) {
	fields := splitEscaped(strings.TrimPrefix(text, "WIFI:"), ';')
	ssid, password := "", ""
	for _, field := range fields {
		key, value, found := strings.Cut(field, ":")
		if !found {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "S":
			ssid = value
		case "P":
			password = value
		}
	}
	return ssid, password
}

func splitEscaped(text string, sep rune) []string {
	parts := []string{}
	current := strings.Builder{}
	escaped := false
	for _, r := range text {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == sep:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	parts = append(parts, current.String())
	return parts
}

// parseKeyedLines 逐行匹配 SSID 与密码键名；兼容中英文与冒号（含全角）、等号分隔。
func parseKeyedLines(text string) (string, string) {
	ssid, password := "", ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.ReplaceAll(strings.TrimSpace(line), "：", ":")
		key, value, found := strings.Cut(line, ":")
		if !found {
			key, value, found = strings.Cut(line, "=")
		}
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if value == "" {
			continue
		}
		switch {
		case containsAny(key, "ssid", "wifi名称", "网络名称", "热点名称", "网络名"):
			if ssid == "" {
				ssid = value
			}
		case containsAny(key, "password", "pwd", "密码", "passphrase", "密钥", "key"):
			if password == "" {
				password = value
			}
		}
	}
	return ssid, password
}

func containsAny(key string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(key, keyword) {
			return true
		}
	}
	return false
}

// durationSeconds 解析 UPnP 的 H:MM:SS[.fraction] 时长。
func durationSeconds(value string) int {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 {
		return 0
	}
	seconds := 0
	for index, part := range parts[:2] {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 || (index == 0 && n > 1000000) || (index == 1 && n >= 60) {
			return 0
		}
		seconds = seconds*60 + n
	}
	fraction, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil || math.IsNaN(fraction) || math.IsInf(fraction, 0) || fraction < 0 || fraction >= 60 {
		return 0
	}
	return seconds*60 + int(fraction)
}

// normalizeDate 把 dc:date 转成展示与续传核对用的 "2006-01-02 15:04:05"。
// 不带时区的时间按相机本地时间原样保留；带时区的时间换算到本机时区。
func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if stamp, err := time.Parse(time.RFC3339, value); err == nil {
		return stamp.Local().Format("2006-01-02 15:04:05")
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04:05.000", "2006-01-02 15:04:05"} {
		if stamp, err := time.Parse(layout, value); err == nil {
			return stamp.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

var mediaExtensions = map[string]string{
	".jpg": "photo", ".jpeg": "photo", ".png": "photo", ".heic": "photo", ".arw": "photo", ".tif": "photo", ".tiff": "photo",
	".mp4": "video", ".mov": "video", ".mts": "video", ".m2ts": "video", ".avi": "video",
}

// classify 依 UPnP 类别或扩展名判断素材类型；音频等非照片视频素材会被跳过。
func classify(class, name string) string {
	lower := strings.ToLower(class)
	switch {
	case strings.Contains(lower, "imageitem"):
		return "photo"
	case strings.Contains(lower, "videoitem"):
		return "video"
	case strings.Contains(lower, "audioitem"), strings.Contains(lower, "playlist"), strings.Contains(lower, "storage"):
		return ""
	}
	return mediaExtensions[strings.ToLower(extOf(name))]
}

func extOf(name string) string {
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 {
		return ""
	}
	ext := name[dot:]
	for _, r := range ext {
		if r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return ext
}
