// Package preview 代理 DJI 相机素材的缩略图与原文件预览。
// 前端只提交素材 ID 与会话令牌；路径、存储位置和相机地址始终由后端决定。
package preview

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
)

type Handler struct {
	Client       *http.Client
	Host         string
	Logs         *diagnostics.Store
	ConnectionID func() string
	// Lookup 校验会话与素材身份并返回受控的相机素材；会话失效、路径不安全时返回 false。
	Lookup func(id, session string) (camera.Media, bool)
	// Alternates 返回原文件之外的候选预览路径（按优先级排列，如低分辨率代理）。
	Alternates func(m camera.Media, r *http.Request) []string
	// MediaURL 仅由适配器提供，处理机型之间的 HTTP 路径差异。
	MediaURL func(storage int, path string) string
}

func (h *Handler) mediaURL(storage int, path string) string {
	if h.MediaURL != nil {
		return h.MediaURL(storage, path)
	}
	return camera.MediaURL(h.Host, storage, path)
}

func thumbnailPaths(p string) []string {
	if !strings.HasPrefix(p, "MISC/") || path.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") {
		return nil
	}
	paths := []string{p}
	stem := strings.TrimSuffix(p, path.Ext(p))
	for _, ext := range []string{".scr", ".THM", ".thm"} {
		if candidate := stem + ext; candidate != p {
			paths = append(paths, candidate)
		}
	}
	return paths
}

func (h *Handler) httpError(w http.ResponseWriter, event string, status, upstream, storage int) {
	connectionID := ""
	if h.ConnectionID != nil {
		connectionID = h.ConnectionID()
	}
	h.Logs.Record("warn", "media", event, "相机文件读取失败，尚未进入图片或视频解码", map[string]string{"status": fmt.Sprint(status), "upstream_status": fmt.Sprint(upstream), "storage": fmt.Sprint(storage), "connection_id": connectionID})
	http.Error(w, "相机文件读取失败，请检查连接或刷新素材列表后重试", status)
}

func upstreamErrorStatus(upstream int) int {
	if upstream == http.StatusNotFound {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func (h *Handler) Thumbnail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := h.Lookup(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if m.Thumb == "" && m.ThumbnailSize > 0 {
		h.embeddedThumbnail(ctx, w, r, m)
		return
	}
	upstream := 0
	for _, p := range thumbnailPaths(m.Thumb) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.mediaURL(m.Storage, p), nil)
		resp, err := h.Client.Do(req)
		if err != nil {
			break
		}
		upstream = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				continue
			}
			break
		}
		const limit = 4 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		kind := http.DetectContentType(data)
		if err != nil || len(data) > limit || (kind != "image/jpeg" && kind != "image/png") {
			continue
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}
	h.httpError(w, "media.thumbnail_failed", upstreamErrorStatus(upstream), upstream, m.Storage)
}

// 目录给出原文件中的 JPEG 范围时，只读取该范围；服务器忽略 Range 时立即关闭响应。
func (h *Handler) embeddedThumbnail(ctx context.Context, w http.ResponseWriter, r *http.Request, m camera.Media) {
	const limit = 4 << 20
	if m.ThumbnailOffset < 0 || m.ThumbnailSize <= 0 || m.ThumbnailSize > limit || m.ThumbnailOffset > 1<<53 || !camera.SafePath(m.Path) {
		http.Error(w, "缩略图范围无效", http.StatusBadGateway)
		return
	}
	end := m.ThumbnailOffset + m.ThumbnailSize - 1
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.mediaURL(m.Storage, m.Path), nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", m.ThumbnailOffset, end))
	resp, err := h.Client.Do(req)
	if err != nil {
		h.httpError(w, "media.thumbnail_range_failed", http.StatusBadGateway, 0, m.Storage)
		return
	}
	defer resp.Body.Close()
	var start, finish, total int64
	_, rangeErr := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &finish, &total)
	if resp.StatusCode != http.StatusPartialContent || rangeErr != nil || start != m.ThumbnailOffset || finish != end || total <= end || resp.ContentLength != m.ThumbnailSize {
		h.httpError(w, "media.thumbnail_range_invalid", http.StatusBadGateway, resp.StatusCode, m.Storage)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, m.ThumbnailSize+1))
	kind := http.DetectContentType(data)
	if err != nil || int64(len(data)) != m.ThumbnailSize || (kind != "image/jpeg" && kind != "image/png") {
		h.httpError(w, "media.thumbnail_range_invalid", http.StatusBadGateway, resp.StatusCode, m.Storage)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

// Preview 流式转发原文件及 Range 响应，使播放器可以按需读取和拖动进度，避免整段视频驻留内存。
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := h.Lookup(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	kind := map[string]string{".JPG": "image/jpeg", ".JPEG": "image/jpeg", ".MP4": "video/mp4", ".MOV": "video/quicktime"}[strings.ToUpper(path.Ext(m.Path))]
	if kind == "" {
		http.Error(w, "此格式请导入后使用本机应用查看", http.StatusUnsupportedMediaType)
		return
	}
	var resp *http.Response
	var paths []string
	if h.Alternates != nil {
		paths = h.Alternates(m, r)
	}
	paths = append(paths, m.Path)
	for index, p := range paths {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, h.mediaURL(m.Storage, p), nil)
		if value := r.Header.Get("Range"); value != "" {
			req.Header.Set("Range", value)
		}
		var err error
		resp, err = h.Client.Do(req)
		if err != nil {
			if r.Context().Err() == nil {
				h.httpError(w, "media.preview_failed", http.StatusBadGateway, 0, m.Storage)
			}
			return
		}
		if resp.StatusCode == http.StatusNotFound && index+1 < len(paths) {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			resp.Body.Close()
			h.httpError(w, "media.preview_failed", upstreamErrorStatus(resp.StatusCode), resp.StatusCode, m.Storage)
			return
		}
		if p != m.Path {
			kind = "video/mp4"
		}
		break
	}
	defer resp.Body.Close()
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
			h.Logs.Record("warn", "media", "media.preview_interrupted", "预览传输中断，请重新加载", nil)
		}
	}
}
