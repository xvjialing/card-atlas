package zve10

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/upnpav"
)

// 前端只提交素材 ID 与会话令牌；资源地址由素材清单提供并经 sameCamera 校验。

func (s *Adapter) previewLookup(id, session string) (upnpav.Item, deviceimport.Media, *upnpLink, bool) {
	if session == "" || session != s.host.SessionToken() || !s.host.Alive() || s.host.State().Phase != "connected" {
		return upnpav.Item{}, deviceimport.Media{}, nil, false
	}
	link := s.currentMedia()
	if link == nil {
		return upnpav.Item{}, deviceimport.Media{}, nil, false
	}
	item, ok := link.Item(id)
	if !ok {
		return upnpav.Item{}, deviceimport.Media{}, nil, false
	}
	if err := sameCamera(item.Res, link.Host()); err != nil {
		return upnpav.Item{}, deviceimport.Media{}, nil, false
	}
	if item.Thumbnail != "" {
		if err := sameCamera(item.Thumbnail, link.Host()); err != nil {
			item.Thumbnail = ""
		}
	}
	media, _ := link.Normalized(id)
	return item, media, link, true
}

func (s *Adapter) mediaHTTPError(w http.ResponseWriter, event string, status, upstream int) {
	s.logs.Record("warn", "media", event, "相机文件读取失败，尚未进入图片或视频解码", map[string]string{"status": fmt.Sprint(status), "upstream_status": fmt.Sprint(upstream), "connection_id": s.host.SessionToken()})
	http.Error(w, "相机文件读取失败，请检查连接或刷新素材列表后重试", status)
}

// serveThumbnail 优先返回相机的缩略图资源；照片原文件较小时直接作为缩略图。
func (s *Adapter) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	item, media, link, ok := s.previewLookup(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	target := item.Thumbnail
	if target == "" && strings.HasPrefix(previewKind(media.Name), "image/") && media.Size > 0 && media.Size <= 8<<20 {
		target = item.Res
	}
	if target == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	ctx, stop := link.requestContext(ctx)
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	resp, err := link.client.Do(req)
	if err != nil {
		s.mediaHTTPError(w, "media.thumbnail_failed", http.StatusBadGateway, 0)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.mediaHTTPError(w, "media.thumbnail_failed", upstreamStatus(resp.StatusCode), resp.StatusCode)
		return
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	kind := http.DetectContentType(data)
	if err != nil || len(data) > limit || (kind != "image/jpeg" && kind != "image/png") {
		s.mediaHTTPError(w, "media.thumbnail_failed", http.StatusBadGateway, resp.StatusCode)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// servePreview 流式转发原文件及 Range 响应；RAW 等本机格式返回 415，提示导入后查看。
func (s *Adapter) servePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	item, media, link, ok := s.previewLookup(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 索尼资源 URL 可以是 /file/123，预览类型应依据已标准化的文件名。
	kind := previewKind(media.Name)
	if kind == "" {
		http.Error(w, "此格式请导入后使用本机应用查看", http.StatusUnsupportedMediaType)
		return
	}
	ctx, cancel := link.requestContext(r.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, r.Method, item.Res, nil)
	if value := r.Header.Get("Range"); value != "" {
		req.Header.Set("Range", value)
	}
	resp, err := link.client.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			s.mediaHTTPError(w, "media.preview_failed", http.StatusBadGateway, 0)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		s.mediaHTTPError(w, "media.preview_failed", upstreamStatus(resp.StatusCode), resp.StatusCode)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	for _, key := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, resp.Body); err != nil && r.Context().Err() == nil {
			s.logs.Record("warn", "media", "media.preview_interrupted", "预览传输中断，请重新加载", nil)
		}
	}
}

func previewKind(res string) string {
	name := ""
	if parsed, err := url.Parse(res); err == nil {
		name = path.Base(parsed.Path)
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	}
	return ""
}

func upstreamStatus(upstream int) int {
	if upstream == http.StatusNotFound {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}
