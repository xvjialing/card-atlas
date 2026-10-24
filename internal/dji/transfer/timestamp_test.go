package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadPreservesCameraTimeAndHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "test.MP4", time.Time{}, bytes.NewReader(sample))
	}))
	defer server.Close()
	for _, date := range []string{"2026-10-21 13:14:08", "2024-02-29 00:00:02"} {
		m := media()
		m.Date = date
		result, err := Download(context.Background(), Client(), strings.TrimPrefix(server.URL, "http://"), "camera", t.TempDir(), m, func(Progress) {})
		if err != nil || result.Warning != "" {
			t.Fatalf("下载异常：%v %s", err, result.Warning)
		}
		st, err := os.Stat(result.Path)
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := time.ParseInLocation("2006-01-02 15:04:05", date, time.Local)
		if !st.ModTime().Equal(expected) {
			t.Fatalf("修改日期错误：%v，期望 %v", st.ModTime(), expected)
		}
		data, _ := os.ReadFile(result.Path)
		hash := sha256.Sum256(data)
		if !bytes.Equal(data, sample) || result.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatal("写入修改日期改变了文件内容")
		}
	}
}

func TestInvalidCameraTimeKeepsCompleteDownloadAndWarns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "test.MP4", time.Time{}, bytes.NewReader(sample))
	}))
	defer server.Close()
	for _, date := range []string{"", "unknown", "2026-02-30 12:00:00", "1970-01-01 00:00:00"} {
		m := media()
		m.Date = date
		result, err := Download(context.Background(), Client(), strings.TrimPrefix(server.URL, "http://"), "camera", t.TempDir(), m, func(Progress) {})
		if err != nil || result.Warning == "" {
			t.Fatalf("无效时间应只警告而保留文件：%v %s", err, result.Warning)
		}
		data, _ := os.ReadFile(result.Path)
		if !bytes.Equal(data, sample) {
			t.Fatal("无效日期造成完整文件丢失")
		}
	}
}

func TestCaptureTimeUsesOpenedFileInsteadOfReplacedPath(t *testing.T) {
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("untouched"), 0600)
	before, _ := os.Stat(outside)
	p := filepath.Join(root, "file")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(p, p+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	if err := SetCaptureTime(f, "2026-10-21 13:14:08"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(outside)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("修改日期越过了已打开的文件")
	}
}
