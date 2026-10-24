package diagnostics

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	s.clock = func() time.Time { return time.Date(2026, 10, 22, 14, 0, 0, 0, time.FixedZone("CST", 8*3600)) }
	return s
}
func allData(t *testing.T, s *Store) string {
	t.Helper()
	files, err := s.files()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(s.directory, f.name))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

func TestRedactionBeforeDiskAndExport(t *testing.T) {
	s := testStore(t)
	s.RegisterSecrets("MyPrivateWifi", "NeverWriteThisPassword", "pair-credential-012345")
	s.Record("error", "wifi", "wifi.failed", "join MyPrivateWifi password=NeverWriteThisPassword failed AABBCCDD-1122-3344-5566-778899AABBCC", map[string]string{"ssid": "MyPrivateWifi", "error": "GET http://192.168.2.1/v2?path=DCIM/private.MP4 failed", "payload": "pair-credential-012345", "path": "/Users/realperson/Movies/private.MP4"})
	raw := allData(t, s)
	for _, secret := range []string{"MyPrivateWifi", "NeverWriteThisPassword", "pair-credential-012345", "private.MP4", "realperson", "AABBCCDD-1122"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("磁盘日志泄漏 %q", secret)
		}
	}
	var b bytes.Buffer
	if err := s.Export(&b); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 3 {
		t.Fatal("诊断包只应包含元数据、日志和说明")
	}
	for _, f := range z.File {
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		r.Close()
		if strings.Contains(string(data), "NeverWriteThisPassword") || strings.Contains(f.Name, "settings") {
			t.Fatal("导出内容含敏感数据")
		}
	}
}

func TestSettingsPersistAndDateRetention(t *testing.T) {
	s := testStore(t)
	dates := []int{10, 15, 16, 22}
	for _, day := range dates {
		s.clock = func() time.Time { return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC) }
		s.Record("info", "app", "test", "记录", nil)
	}
	s.clock = func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	if err := s.SaveSettings(Settings{7, "debug"}); err != nil {
		t.Fatal(err)
	}
	files, _ := s.files()
	for _, f := range files {
		if f.date < "2026-10-16" {
			t.Fatal("过期日志未清理")
		}
	}
	if len(files) != 2 {
		t.Fatalf("保留边界错误：%+v", files)
	}
	var settings Settings
	b, _ := os.ReadFile(s.configPath)
	if err := json.Unmarshal(b, &settings); err != nil || settings.RetentionDays != 7 || settings.Level != "debug" {
		t.Fatal(settings, err)
	}
	for _, bad := range []Settings{{0, "info"}, {91, "info"}, {7, "trace"}} {
		if s.SaveSettings(bad) == nil {
			t.Fatal("非法设置被接受")
		}
	}
}

func TestDetailedLevelPaginationAndSearch(t *testing.T) {
	s := testStore(t)
	s.Record("debug", "datalink", "debug.hidden", "隐藏", nil)
	if s.Info().Files != 0 {
		t.Fatal("标准模式写入了详细日志")
	}
	if err := s.SaveSettings(Settings{7, "debug"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 17; i++ {
		level := "info"
		if i%2 == 0 {
			level = "error"
		}
		s.Record(level, "wifi", "wifi.test", fmt.Sprintf("连接记录 %02d", i), nil)
	}
	page, err := s.Read(Query{Level: "error", Search: "连接记录", Limit: 3})
	if err != nil || len(page.Entries) != 3 || !page.HasMore {
		t.Fatal(page, err)
	}
	if page.Entries[0].Message != "连接记录 16" {
		t.Fatal("未按最新优先排序")
	}
	seen := map[string]bool{}
	count := 0
	for {
		for _, e := range page.Entries {
			if seen[e.ID] {
				t.Fatal("分页重复记录")
			}
			seen[e.ID] = true
			count++
		}
		if !page.HasMore {
			break
		}
		page, err = s.Read(Query{Level: "error", Search: "连接记录", Before: page.NextCursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 9 {
		t.Fatalf("遗漏记录：%d", count)
	}
}

func TestRotationSizeBudgetAndClearScope(t *testing.T) {
	s := testStore(t)
	s.fileLimit = 2048
	s.totalLimit = 8192
	for i := 0; i < 70; i++ {
		s.Record("info", "app", "test", strings.Repeat("x", 200), nil)
	}
	info := s.Info()
	if info.Files < 2 || info.Bytes > s.totalLimit {
		t.Fatal("滚动或容量上限失效", info)
	}
	foreign := filepath.Join(s.directory, "keep-me.txt")
	os.WriteFile(foreign, []byte("保留"), 0600)
	outside := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(outside, []byte("不可删除"), 0600)
	link := filepath.Join(s.directory, "osmo-bridge-2026-10-22-abcdef012345-0001.jsonl")
	os.Symlink(outside, link)
	result, err := s.Clean(true)
	if err != nil || result.Files == 0 {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(outside); err != nil {
		t.Fatal("删除了目录外文件")
	}
	if _, err = os.Stat(foreign); err != nil {
		t.Fatal("删除了非日志文件")
	}
	s.Record("info", "app", "after.clear", "清理后继续写入", nil)
	page, err := s.Read(Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Event != "after.clear" {
		t.Fatal(page, err)
	}
}

func TestConcurrentWriteReadAndCleanup(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for range 20 {
				s.Record("info", "app", "parallel", "并发记录", nil)
				if _, err := s.Read(Query{Limit: 5}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Go(func() {
		for range 5 {
			if _, err := s.Clean(true); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	if s.Info().LastError != "" {
		t.Fatal(s.Info().LastError)
	}
}

func TestUnwritableDirectoryIsVisible(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "logs"), []byte("不是目录"), 0600)
	s := New(base)
	s.Record("error", "app", "test", "测试", nil)
	if s.Info().LastError == "" {
		t.Fatal("日志写入失败未暴露给设置界面")
	}
}

func TestOverlappingSecretsAndUsefulFilesystemError(t *testing.T) {
	s := testStore(t)
	s.RegisterSecrets("camera", "cameraSuperSecret")
	s.Record("error", "app", "write.failed", "cameraSuperSecret", map[string]string{"error": "open /Users/private person/secret.MP4: permission denied"})
	raw := allData(t, s)
	if strings.Contains(raw, "SuperSecret") || strings.Contains(raw, "secret.MP4") || !strings.Contains(raw, "permission denied") {
		t.Fatal("脱敏不完整或丢失了文件系统错误原因", raw)
	}
}
