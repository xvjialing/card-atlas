package action6

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cardatlas/internal/dji"
	"cardatlas/internal/dji/transfer"
)

type previewTransport func(*http.Request) (*http.Response, error)

func (f previewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func previewService(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	t.Setenv("CARDATLAS_DEVICE_DATA", t.TempDir())
	s := NewAdapter()
	s.link = &fakeMedia{}
	s.connectionID = "test-session"
	s.state.Phase = "connected"
	s.state.Media = []camera.Media{{ID: "fixture", Name: "TEST.MP4", Path: "DCIM/TEST/TEST.MP4", Thumb: "MISC/THM/TEST/TEST.scr", Storage: 1, Kind: "video"}}
	client := transfer.Client()
	transport := client.Transport
	t.Cleanup(func() { transport.(*http.Transport).CloseIdleConnections() })
	client.Transport = previewTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "192.168.2.1" || r.URL.Path != "/v2" {
			t.Error("预览绕过了固定相机接口")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Host = target.Host
		return transport.RoundTrip(clone)
	})
	s.client = client
	return s
}

func TestPreviewRangeHeadAndIntegrity(t *testing.T) {
	content := []byte("fixture-video-content-0123456789")
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI == "/v2?storage=1&path=DCIM/TEST/TEST.LRF" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("path") != "DCIM/TEST/TEST.MP4" || r.URL.Query().Get("storage") != "1" {
			t.Error("素材路径或存储被改写")
		}
		http.ServeContent(w, r, "TEST.MP4", time.Time{}, bytes.NewReader(content))
	})
	for _, tc := range []struct {
		method, rangeValue string
		status             int
		body               string
	}{
		{"GET", "", 200, string(content)}, {"GET", "bytes=8-12", 206, string(content[8:13])},
		{"GET", "bytes=999-", 416, ""}, {"HEAD", "", 200, ""},
	} {
		t.Run(tc.method+tc.rangeValue, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/device-media?session=test-session&preview=1&id=fixture", nil)
			r.Header.Set("Range", tc.rangeValue)
			w := httptest.NewRecorder()
			s.servePreview(w, r)
			if w.Code != tc.status {
				t.Fatalf("状态：%d", w.Code)
			}
			if tc.status != 416 && w.Body.String() != tc.body {
				t.Fatal("预览字节内容不完整")
			}
			if tc.status == 206 && w.Header().Get("Content-Range") == "" {
				t.Fatal("缺少范围头")
			}
			if tc.method == "HEAD" && w.Header().Get("Content-Length") == "" {
				t.Fatal("HEAD 未返回长度")
			}
		})
	}
}

func TestThumbnailFallbackAndContentValidation(t *testing.T) {
	var jpegData bytes.Buffer
	jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	var paths []string
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		paths = append(paths, p)
		if strings.HasSuffix(p, ".THM") {
			http.NotFound(w, r)
			return
		}
		w.Write(jpegData.Bytes())
	})
	s.state.Media[0].Thumb = "MISC/THM/TEST/TEST.THM"
	w := httptest.NewRecorder()
	s.serveThumbnail(w, httptest.NewRequest("GET", "/device-media?session=test-session&id=fixture", nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), jpegData.Bytes()) || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("缩略图未正确解析")
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[1], ".scr") {
		t.Fatalf("未回退到相机 scr 文件：%v", paths)
	}
	bad := previewService(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>not an image</html>") })
	w = httptest.NewRecorder()
	bad.serveThumbnail(w, httptest.NewRequest("GET", "/device-media?session=test-session&id=fixture", nil))
	if w.Code != 502 {
		t.Fatal("将非图片内容作为缩略图返回")
	}
}

func TestPreviewRejectsUnknownUnsafeAndDisconnectedMedia(t *testing.T) {
	var requests atomic.Int32
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
	for _, session := range []string{"", "previous-session"} {
		w := httptest.NewRecorder()
		s.servePreview(w, httptest.NewRequest("GET", "/device-media?preview=1&id=fixture&session="+session, nil))
		if w.Code != 404 {
			t.Fatal("旧设备会话访问了当前素材")
		}
	}
	for _, p := range []string{"../secret", "DCIM/../secret", "DCIM/test\\file", "DCIM/test\x00"} {
		s.state.Media[0].Path = p
		w := httptest.NewRecorder()
		s.servePreview(w, httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id=fixture", nil))
		if w.Code != 404 {
			t.Fatal("接受了危险路径")
		}
	}
	s.state.Media[0].Path = "DCIM/TEST/TEST.MP4"
	for _, id := range []string{"unknown", "fixture/extra"} {
		s.servePreview(httptest.NewRecorder(), httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id="+id, nil))
	}
	for _, p := range []string{"MISC/../../secret", "MISC/test\\file", "DCIM/file.scr"} {
		s.state.Media[0].Thumb = p
		s.serveThumbnail(httptest.NewRecorder(), httptest.NewRequest("GET", "/device-media?session=test-session&id=fixture", nil))
	}
	s.state.Phase = "disconnected"
	s.servePreview(httptest.NewRecorder(), httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id=fixture", nil))
	if requests.Load() != 0 {
		t.Fatal("不安全或失效请求访问了相机")
	}
}

func TestPreviewCancellationAndRedirect(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id=fixture", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.servePreview(httptest.NewRecorder(), r) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("预览请求未开始")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("关闭预览后相机请求未取消")
	}
	<-done
	s = previewService(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/private", http.StatusFound)
	})
	w := httptest.NewRecorder()
	s.servePreview(w, httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id=fixture", nil))
	if w.Code != 502 {
		t.Fatal("没有阻止重定向")
	}
}

func TestPreviewUsesLRFAndOriginalRemainsAvailable(t *testing.T) {
	original, proxy := []byte("original-high-resolution"), []byte("proxy-clip")
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.RequestURI {
		case "/v2?storage=1&path=DCIM/TEST/TEST.LRF":
			http.ServeContent(w, r, "proxy.mp4", time.Time{}, bytes.NewReader(proxy))
		case "/v2?storage=1&path=DCIM/TEST/TEST.MP4":
			http.ServeContent(w, r, "original.mp4", time.Time{}, bytes.NewReader(original))
		default:
			http.NotFound(w, r)
		}
	})
	for _, tc := range []struct {
		query string
		want  []byte
	}{{"", proxy}, {"&original=1", original}} {
		w := httptest.NewRecorder()
		s.servePreview(w, httptest.NewRequest("GET", "/device-media?session=test-session&preview=1&id=fixture"+tc.query, nil))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), tc.want) {
			t.Fatalf("预览源选择错误：%d %q", w.Code, w.Body.String())
		}
	}
}

func TestPreview404RetainsUpstreamStatus(t *testing.T) {
	s := previewService(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	for _, thumbnail := range []bool{false, true} {
		w := httptest.NewRecorder()
		if thumbnail {
			s.serveThumbnail(w, httptest.NewRequest("HEAD", "/device-media?session=test-session&id=fixture", nil))
		} else {
			s.servePreview(w, httptest.NewRequest("HEAD", "/device-media?session=test-session&preview=1&id=fixture", nil))
		}
		if w.Code != 404 {
			t.Fatalf("文件缺失被掩盖：%d", w.Code)
		}
	}
}
