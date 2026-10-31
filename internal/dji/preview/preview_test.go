package preview

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	camera "cardatlas/internal/dji"
)

func TestEmbeddedThumbnailUsesOnlyAdvertisedRange(t *testing.T) {
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 16}, bytes.Repeat([]byte{0}, 32)...)
	for _, mode := range []string{"range", "ignored", "wrong-range"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != fmt.Sprintf("bytes=101888-%d", 101888+len(jpeg)-1) {
					t.Error("未按目录范围请求缩略图")
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(jpeg)))
				if mode == "ignored" {
					w.WriteHeader(http.StatusOK)
				} else {
					start := 101888
					if mode == "wrong-range" {
						start++
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/200000", start, 101888+len(jpeg)-1))
					w.WriteHeader(http.StatusPartialContent)
				}
				_, _ = w.Write(jpeg)
			}))
			defer upstream.Close()
			h := Handler{Client: upstream.Client(), Lookup: func(string, string) (camera.Media, bool) {
				return camera.Media{Path: "DCIM/DJI_001/TEST.dng", Storage: 1, ThumbnailOffset: 101888, ThumbnailSize: int64(len(jpeg))}, true
			}, MediaURL: func(int, string) string { return upstream.URL + "/v2" }}
			w := httptest.NewRecorder()
			h.Thumbnail(w, httptest.NewRequest(http.MethodGet, "/device-media", nil))
			if mode == "range" {
				if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(w.Body.Bytes(), jpeg) {
					t.Fatal("未返回原片中的 JPEG")
				}
			} else if w.Code != http.StatusBadGateway {
				t.Fatal("接受忽略或错位的 Range 响应")
			}
		})
	}
}

func TestPreviewWithoutAlternatePathsAndWithAdapterURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") != "DCIM/100MEDIA/TEST.MP4" {
			t.Errorf("无人机物理路径错误：%s", r.URL.String())
		}
		w.Header().Set("Content-Range", "bytes 0-3/12")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("test"))
	}))
	defer upstream.Close()
	h := Handler{Client: upstream.Client(), Lookup: func(string, string) (camera.Media, bool) {
		return camera.Media{Path: "DCIM/100MEDIA/TEST.MP4", Kind: "video"}, true
	}, MediaURL: func(_ int, p string) string { return upstream.URL + "/v2?storage=0&path=" + p }}
	r := httptest.NewRequest(http.MethodGet, "/device-media?id=x&session=s", nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	h.Preview(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "test" || w.Header().Get("Content-Range") != "bytes 0-3/12" {
		t.Fatal("原文件范围预览失败")
	}
}
