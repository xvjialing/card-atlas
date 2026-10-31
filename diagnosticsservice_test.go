package main

import (
	"archive/zip"
	"cardatlas/internal/diagnostics"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsOfflinePersistenceSettingsAndExport(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARDATLAS_DEVICE_DATA", base)
	s := NewDiagnosticsService()
	device := diagnostics.Shared(filepath.Join(base, "."))
	device.RegisterSecrets("PrivateHotspot", "SecretPass123")
	device.Record("info", "wifi", "wifi.joined", "已连接 PrivateHotspot", nil)
	device.Record("debug", "datalink", "hidden", "默认不记录详细报文", nil)
	page, err := s.GetLogs(diagnostics.Query{})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("标准记录错误：%+v %v", page, err)
	}
	if _, err = s.SetLevel("debug"); err != nil {
		t.Fatal(err)
	}
	device.Record("debug", "datalink", "detail", "详细握手", map[string]string{"password": "SecretPass123"})
	// 新 Store 从磁盘读取，验证记录无需依赖原进程内存或网络。
	restarted := diagnostics.New(base)
	page, err = restarted.Read(diagnostics.Query{Level: "debug"})
	if err != nil || len(page.Entries) != 1 || restarted.Info().Settings.Level != "debug" {
		t.Fatalf("离线重启读取失败：%+v %v", page, err)
	}
	dir := t.TempDir()
	first, err := s.exportToDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.exportToDirectory(dir)
	if err != nil || first == second {
		t.Fatal("重复导出覆盖已有包")
	}
	info, err := os.Stat(first)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("导出文件权限错误")
	}
	z, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 3 {
		t.Fatal("诊断包包含意外文件")
	}
	for _, file := range z.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "PrivateHotspot") || strings.Contains(string(data), "SecretPass123") {
			t.Fatal("导出包未脱敏")
		}
		if file.Name == "events.jsonl" && !strings.Contains(string(data), "detail") {
			t.Fatal("导出缺少设备详细记录")
		}
		if file.Name == "diagnostics.json" {
			var meta map[string]any
			if json.Unmarshal(data, &meta) != nil || meta["app"] != "CardAtlas" {
				t.Fatal("缺少版本信息")
			}
		}
	}
	if _, err = s.SetLevel("invalid"); err == nil {
		t.Fatal("接受非法记录级别")
	}
	if _, err = s.exportToDirectory("relative"); err == nil {
		t.Fatal("接受相对导出路径")
	}
	if _, err = s.exportToDirectory(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("写入失败未返回")
	}
}

func TestClientErrorsAreRedactedAndBounded(t *testing.T) {
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	s := NewDiagnosticsService()
	for i := 0; i < 35; i++ {
		s.ReportClientError("unhandledrejection", `{"password":"NeverExpose42","ssid":"PrivateNetwork"} 请求失败`)
	}
	s.ReportClientError("arbitrary", "不应被记录")
	page, err := s.GetLogs(diagnostics.Query{Limit: 100})
	if err != nil || len(page.Entries) != 20 {
		t.Fatalf("界面错误限流失效：%d %v", len(page.Entries), err)
	}
	for _, entry := range page.Entries {
		if strings.Contains(entry.Fields["error"], "NeverExpose42") || strings.Contains(entry.Fields["error"], "PrivateNetwork") {
			t.Fatal("JSON 格式凭据未脱敏")
		}
	}
}

func TestLocalScanFailuresAreLoggedWithoutFilePaths(t *testing.T) {
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	d := NewDiagnosticsService()
	s := NewMediaService()
	s.logs = d.logs
	_, err := s.StartScan(filepath.Join(t.TempDir(), "private-folder"))
	if err == nil {
		t.Fatal("应报告目录不存在")
	}
	page, err := d.GetLogs(diagnostics.Query{Level: "error"})
	if err != nil || len(page.Entries) != 1 {
		t.Fatal("未记录本地操作错误")
	}
	if strings.Contains(page.Entries[0].Fields["error"], "private-folder") {
		t.Fatal("本地路径泄漏")
	}
}
