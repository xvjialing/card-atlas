package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
)

func TestDownloadDiagnosticsIncludeHTTPRetry(t *testing.T) {
	logs := diagnostics.New(t.TempDir())
	if err := logs.SaveSettings(diagnostics.Settings{RetentionDays: 7, Level: "debug"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		http.ServeContent(w, r, "private-video.MP4", time.Time{}, bytes.NewReader(sample))
	}))
	defer s.Close()
	ctx := diagnostics.With(context.Background(), logs, map[string]string{"task_id": "test-task"})
	if _, err := Download(ctx, Client(), strings.TrimPrefix(s.URL, "http://"), "camera-one", t.TempDir(), media(), func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	page, err := logs.Read(diagnostics.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]bool{}
	for _, e := range page.Entries {
		events[e.Event] = true
		if e.Fields["task_id"] != "test-task" {
			t.Fatal("下载任务关联丢失")
		}
		if strings.Contains(fmt.Sprint(e), "private-video") {
			t.Fatal("素材文件名进入了日志")
		}
	}
	if !events["http.request"] || !events["http.response"] || !events["http.status_error"] {
		t.Fatal("缺少 HTTP 请求与重试的诊断事件", events)
	}
}

var sample = bytes.Repeat([]byte("original-camera-media-"), 32768)

func media() camera.Media {
	return camera.Media{ID: "one", Name: "test.MP4", Path: "DCIM/DJI_001/test.MP4", Storage: 1, Size: int64(len(sample)), Date: "2026-10-22 12:00:00"}
}
func run(t *testing.T, server *httptest.Server, root string, progress func(Progress)) (Result, error) {
	t.Helper()
	return Download(context.Background(), Client(), strings.TrimPrefix(server.URL, "http://"), "camera-one", root, media(), progress)
}

func TestDownloadOriginalHashAndNoOverwrite(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "test.MP4", time.Time{}, bytes.NewReader(sample))
	}))
	defer s.Close()
	root := t.TempDir()
	r, e := run(t, s, root, func(Progress) {})
	if e != nil {
		t.Fatal(e)
	}
	want := sha256.Sum256(sample)
	if r.SHA256 != hex.EncodeToString(want[:]) || r.Bytes != int64(len(sample)) {
		t.Fatal(r)
	}
	b, _ := os.ReadFile(r.Path)
	if !bytes.Equal(b, sample) {
		t.Fatal("字节不一致")
	}
	if _, e = run(t, s, root, func(Progress) {}); e == nil {
		t.Fatal("覆盖已有文件")
	}
}

func TestPauseAndResumeExactBytes(t *testing.T) {
	var resumed atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-" {
			resumed.Store(true)
		}
		http.ServeContent(w, r, "test.MP4", time.Time{}, bytes.NewReader(sample))
	}))
	defer s.Close()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	_, e := Download(ctx, Client(), strings.TrimPrefix(s.URL, "http://"), "camera-one", root, media(), func(Progress) { cancel() })
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("暂停未生效：%v", e)
	}
	r, e := run(t, s, root, func(Progress) {})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(r.Path)
	if !resumed.Load() || !bytes.Equal(b, sample) {
		t.Fatal("续传数据不一致")
	}
}

func TestServerIgnoringRangeRestartsSafely(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(sample)))
		w.Write(sample)
	}))
	defer s.Close()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	_, _ = Download(ctx, Client(), strings.TrimPrefix(s.URL, "http://"), "camera-one", root, media(), func(Progress) { cancel() })
	r, e := run(t, s, root, func(Progress) {})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(r.Path)
	if !bytes.Equal(b, sample) {
		t.Fatal("完整响应被错误追加")
	}
}

func TestInvalidRangeAndTruncationNeverPublish(t *testing.T) {
	for _, mode := range []string{"range", "truncated", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "range":
					w.Header().Set("Content-Range", "bytes 20-29/30")
					w.Header().Set("Content-Length", "10")
					w.WriteHeader(206)
					w.Write([]byte("0123456789"))
				case "truncated":
					w.Header().Set("Content-Length", fmt.Sprint(len(sample)))
					w.Write([]byte("short"))
				case "redirect":
					http.Redirect(w, r, "http://example.invalid/private", 302)
				}
			}))
			defer s.Close()
			root := t.TempDir()
			if _, e := run(t, s, root, func(Progress) {}); e == nil {
				t.Fatal("错误响应被接受")
			}
			_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() && strings.HasSuffix(p, ".MP4") {
					t.Fatal("发布了不完整文件")
				}
				return nil
			})
		})
	}
}

func TestSymlinkCannotEscapeDestination(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	hash := sha256.Sum256([]byte("camera-one"))
	if e := os.Symlink(outside, filepath.Join(root, hex.EncodeToString(hash[:6]))); e != nil {
		t.Fatal(e)
	}
	_, e := Download(context.Background(), Client(), "127.0.0.1:1", "camera-one", root, media(), func(Progress) {})
	if e == nil {
		t.Fatal("符号链接越界")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("写入了目标目录之外")
	}
}

func TestDownloadUsesCameraWireFormatAndResumesOriginal(t *testing.T) {
	for _, storage := range []int{0, 1} {
		t.Run(fmt.Sprint(storage), func(t *testing.T) {
			m := media()
			m.Storage = storage
			var resumed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// 按真实请求字符串匹配，不能先 Query 解码而掩盖分隔符和参数顺序差异。
				expected := fmt.Sprintf("/v2?storage=%d&path=DCIM/DJI_001/test.MP4", storage)
				if r.RequestURI != expected {
					http.NotFound(w, r)
					return
				}
				if r.Header.Get("Range") != "bytes=0-" {
					resumed.Store(true)
				}
				http.ServeContent(w, r, "test.MP4", time.Time{}, bytes.NewReader(sample))
			}))
			defer server.Close()
			host, root := strings.TrimPrefix(server.URL, "http://"), t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			_, err := Download(ctx, Client(), host, "camera-one", root, m, func(Progress) { cancel() })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("暂停失败：%v", err)
			}
			result, err := Download(context.Background(), Client(), host, "camera-one", root, m, func(Progress) {})
			if err != nil {
				t.Fatal(err)
			}
			st, _ := os.Stat(result.Path)
			expected, _ := time.ParseInLocation("2006-01-02 15:04:05", m.Date, time.Local)
			if st == nil || !st.ModTime().Equal(expected) {
				t.Fatal("续传完成后未保留相机时间")
			}
			data, _ := os.ReadFile(result.Path)
			sum := sha256.Sum256(sample)
			if !resumed.Load() || !bytes.Equal(data, sample) || result.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("原文件续传不完整")
			}
		})
	}
}

func TestNotFoundDoesNotRetryOrSwitchStorage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.NotFound(w, r) }))
	defer server.Close()
	_, err := run(t, server, t.TempDir(), func(Progress) {})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || calls.Load() != 1 {
		t.Fatalf("404 不应重复请求或切换物理存储：%v, %d", err, calls.Load())
	}
}

func TestPublishOnFilesystemWithoutHardLinks(t *testing.T) {
	root, e := os.OpenRoot(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = root.WriteFile("original.part", sample, 0600); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(sample)
	if e = publishCopy(context.Background(), root, "original.part", "result.MP4", hex.EncodeToString(h[:])); e != nil {
		t.Fatal(e)
	}
	data, e := root.ReadFile("result.MP4")
	if e != nil || !bytes.Equal(data, sample) {
		t.Fatal("最终文件与原片不一致", e)
	}
	if e = publishCopy(context.Background(), root, "original.part", "result.MP4", hex.EncodeToString(h[:])); e == nil {
		t.Fatal("覆盖了已有文件")
	}
	if e = publishCopy(context.Background(), root, "original.part", "bad.MP4", "wrong-hash"); e == nil {
		t.Fatal("校验失败仍发布文件")
	}
	if _, e = root.Stat("bad.MP4"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("失败的目标未清理")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = publishCopy(ctx, root, "original.part", "cancelled.MP4", hex.EncodeToString(h[:])); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = root.Stat("original.part"); e != nil {
		t.Fatal("取消丢失续传文件")
	}
}

func TestPlannedDestinationRejectsEscapeAndPartialSymlinks(t *testing.T) {
	for _, relative := range []string{"../escape.MP4", "/absolute.MP4"} {
		if _, e := DownloadTo(context.Background(), Client(), "127.0.0.1:1", "camera", t.TempDir(), relative, media(), func(Progress) {}); e == nil {
			t.Fatal("接受了越界规划路径")
		}
	}
	root := t.TempDir()
	original := filepath.Join(root, "keep.MP4")
	os.WriteFile(original, []byte("keep"), 0600)
	os.Symlink(original, filepath.Join(root, "test.MP4.part"))
	if _, e := DownloadTo(context.Background(), Client(), "127.0.0.1:1", "camera", root, "test.MP4", media(), func(Progress) {}); e == nil {
		t.Fatal("接受了符号链接续传片段")
	}
	if data, _ := os.ReadFile(original); string(data) != "keep" {
		t.Fatal("已有文件被修改")
	}
}
