package zve10

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/upnpav"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func encodeQR(t *testing.T, text string) []byte {
	t.Helper()
	writer := qrcode.NewQRCodeWriter()
	// 强制字节模式编码，避免编码器混合分段在解码端的自兼容问题。
	hints := map[gozxing.EncodeHintType]interface{}{gozxing.EncodeHintType_CHARACTER_SET: "ISO-8859-1"}
	matrix, err := writer.Encode(text, gozxing.BarcodeFormat_QR_CODE, 240, 240, hints)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, matrix.GetWidth(), matrix.GetHeight()))
	for y := 0; y < matrix.GetHeight(); y++ {
		for x := 0; x < matrix.GetWidth(); x++ {
			if matrix.Get(x, y) {
				img.Pix[y*img.Stride+x] = 0
			} else {
				img.Pix[y*img.Stride+x] = 255
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseConnectionInfoFormats(t *testing.T) {
	for _, tc := range []struct {
		name, text, ssid, password string
		fail                       bool
	}{
		{"android-uri", "WIFI:T:WPA;S:DIRECT-e6:ZV-E10;P:sony-pass;;", "DIRECT-e6:ZV-E10", "sony-pass", false},
		{"android-uri-escape", "WIFI:T:WPA;S:DIRECT-1a;P:ab\\;cd;;", "DIRECT-1a", "ab;cd", false},
		{"sony-w01", "W01:S:RLE1;P:k5oPRHk6;C:ZV-E10;M:D81068060F1B;", "DIRECT-RLE1:ZV-E10", "k5oPRHk6", false},
		{"sony-w01-full-ssid", "W01:S:DIRECT-X1:ZV-E10;P:pass1234;C:ZV-E10;", "DIRECT-X1:ZV-E10", "pass1234", false},
		{"sony-w01-no-model", "W01:S:ABCD;P:pass1234;", "DIRECT-ABCD", "pass1234", false},
		{"keyed-colon", "SSID: DIRECT-ab\nPASSWORD: camera-pw", "DIRECT-ab", "camera-pw", false},
		{"keyed-equals", "SSID=DIRECT-ab\nPWD=camera-pw\nKEY_INDEX=1", "DIRECT-ab", "camera-pw", false},
		{"chinese", "网络名称：DIRECT-ab\n密码：camera-pw", "DIRECT-ab", "camera-pw", false},
		{"bare-direct", "DIRECT-ab ZV-E10", "DIRECT-ab", "", false},
		{"url-only", "https://sony.example/pairing", "", "", true},
		{"garbage", "no connection info here", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := parseConnectionInfo(tc.text)
			if tc.fail {
				if err == nil || info.ssid != "" {
					t.Fatalf("应解析失败：%+v %v", info, err)
				}
				return
			}
			if err != nil || info.ssid != tc.ssid || info.password != tc.password {
				t.Fatalf("解析错误：%+v %v", info, err)
			}
		})
	}
}

// 实机二维码（ZV-E10，Imaging Edge Mobile 配对界面）的格式回归。
func TestParseSonyProvisionFragment(t *testing.T) {
	info, err := parseConnectionInfo("W01:S:RLE1;P:k5oPRHk6;C:ZV-E10;M:D81068060F1B;")
	if err != nil || info.fragment != "RLE1" || info.ssid != "DIRECT-RLE1:ZV-E10" {
		t.Fatalf("索尼格式片段解析错误：%+v %v", info, err)
	}
}

func TestDecodeQRRoundtrip(t *testing.T) {
	text := "WIFI:T:WPA;S:DIRECT-e6:ZV-E10;P:sony-pass;;"
	photo := encodeQR(t, text)
	decoded, err := decodeQR(photo)
	if err != nil || decoded != text {
		t.Fatalf("二维码往返失败：%q %v", decoded, err)
	}
	if _, err := decodeQR([]byte("not an image")); err == nil {
		t.Fatal("非图片应报错")
	}
	// 无连接信息的二维码仍可解码；拒绝发生在 parseConnectionInfo。
	if text, err := decodeQR(encodeQR(t, "https://sony.example/only-url")); err != nil || text != "https://sony.example/only-url" {
		t.Fatalf("可解码二维码应返回原文：%q %v", text, err)
	}
}

func TestImportQREndToEnd(t *testing.T) {
	photo := encodeQR(t, "WIFI:T:WPA;S:DIRECT-e6:ZV-E10;P:sony-pass;;")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(photo)
	s, actions := newTestAdapter(t)
	fakeNetworks(actions, "DIRECT-e6:ZV-E10", "HomeNet")
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "import_qr", Photo: dataURL}); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, s, "choosing_wifi")
	if !strings.Contains(st.Message, "二维码") {
		t.Fatalf("缺少二维码提示：%q", st.Message)
	}
	found := false
	for _, n := range st.WiFiNetworks {
		if n.SSID == "DIRECT-e6:ZV-E10" {
			found = true
			if !n.Likely || !strings.Contains(n.Reason, "二维码") {
				t.Fatalf("二维码热点未标记：%+v", n)
			}
		}
	}
	if !found {
		t.Fatalf("二维码热点未出现在扫描结果：%+v", st.WiFiNetworks)
	}
	if err := s.host.JoinWiFi("DIRECT-e6:ZV-E10", "", false); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, "connected")
	if joined := actions.joined; len(joined) != 1 || !strings.Contains(joined[0], "sony-pass") {
		t.Fatalf("未使用二维码中的密码：%v", joined)
	}
}

func TestImportQREjectsBadPhotos(t *testing.T) {
	s, _ := newTestAdapter(t)
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "import_qr", Photo: "not-base64!!"}); err == nil {
		t.Fatal("损坏的照片数据应报错")
	}
	noQR := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encodeQR(t, "https://sony.example/only-url"))
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "import_qr", Photo: noQR}); err == nil {
		t.Fatal("无连接信息的二维码应报错")
	}
}

func TestNormalizeAndGuards(t *testing.T) {
	storage := upnpav.Storage{ID: "0", Name: "100MSDCF"}
	m, ok := normalize(upnpav.Item{ID: "10", Title: "DSC00001.JPG", Class: "object.item.imageItem.photo", Res: "http://10.0.0.1:52235/file/10", Size: 2048, Date: "2026-10-23T10:30:00", Thumbnail: "http://10.0.0.1:52235/thumb/10"}, storage)
	if !ok || m.Kind != "photo" || m.StorageID != "0" || m.StorageName != "100MSDCF" || !m.Thumbnail || m.SizeEstimated {
		t.Fatalf("照片标准化错误：%+v %v", m, ok)
	}
	if m.Path != "100MSDCF/DSC00001.JPG" {
		t.Fatalf("设备内路径错误：%q", m.Path)
	}
	if m.Date != "2026-10-23 10:30:00" {
		t.Fatalf("拍摄时间转换错误：%q", m.Date)
	}
	video, ok := normalize(upnpav.Item{ID: "11", Title: "C0001.MP4", Class: "object.item.videoItem.movie", Res: "http://10.0.0.1:52235/file/11", Size: 0, Duration: "0:01:05.500"}, upnpav.Storage{ID: "0", Name: ""})
	if !ok || video.Kind != "video" || video.Duration != 65 || !video.SizeEstimated || video.StorageName != "存储卡" {
		t.Fatalf("视频标准化错误：%+v %v", video, ok)
	}
	for _, bad := range []upnpav.Item{
		{Title: "song.mp3", Class: "object.item.audioItem.musicTrack", Res: "http://10.0.0.1/x"},
		{Title: "", Res: "http://10.0.0.1/x"},
		{Title: "DSC00001.JPG", Class: "object.item.imageItem.photo", Res: ""},
		{Title: "unknown.bin", Res: "http://10.0.0.1/x"},
		{Title: "..", Class: "object.item.imageItem.photo", Res: "http://10.0.0.1/.."},
	} {
		if _, ok := normalize(bad, storage); ok {
			t.Fatalf("不安全条目被接受：%+v", bad)
		}
	}
	// 标题不可信时回退到资源地址文件名，且不产生路径穿越。
	rescued, ok := normalize(upnpav.Item{Title: "../evil.JPG", Class: "object.item.imageItem.photo", Res: "http://10.0.0.1:52235/file/x.JPG"}, storage)
	if !ok || rescued.Name != "x.JPG" || rescued.Path != "100MSDCF/x.JPG" {
		t.Fatalf("不可信标题未安全回退：%+v %v", rescued, ok)
	}
}

func TestSameCameraHostValidation(t *testing.T) {
	if err := sameCamera("http://10.0.0.1:52235/file/1", "10.0.0.1:52235"); err != nil {
		t.Fatalf("同机资源应通过：%v", err)
	}
	if err := sameCamera("http://10.0.0.1:52235/file/1", "10.0.0.1"); err != nil {
		t.Fatalf("无端口服务器地址应通过：%v", err)
	}
	for _, tc := range []struct{ res, host string }{
		{"http://192.168.1.9/file/1", "10.0.0.1:52235"},
		{"https://10.0.0.1/file/1", "10.0.0.1:52235"},
		{"http://user@10.0.0.1/file/1", "10.0.0.1:52235"},
		{"http://example.invalid/file/1", "10.0.0.1:52235"},
	} {
		if err := sameCamera(tc.res, tc.host); err == nil {
			t.Fatalf("外部资源地址应被拒绝：%+v", tc)
		}
	}
}

func TestDurationAndDateHelpers(t *testing.T) {
	if durationSeconds("0:01:05.500") != 65 || durationSeconds("1:00:00") != 3600 || durationSeconds("bad") != 0 {
		t.Fatal("时长解析错误")
	}
	if normalizeDate("2026-10-23T10:30:00") != "2026-10-23 10:30:00" || normalizeDate("") != "" {
		t.Fatal("时间解析错误")
	}
}
