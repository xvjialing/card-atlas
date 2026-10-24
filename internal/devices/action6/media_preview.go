package action6

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"cardatlas/internal/dji"
)

// 前端只提交素材 ID，路径、存储位置和相机地址始终由后端决定。
func (s *Adapter) previewMedia(id, session string) (camera.Media, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session == "" || session != s.connectionID || s.link == nil || !s.link.Alive() || s.state.Phase != "connected" {
		return camera.Media{}, false
	}
	for _, m := range s.state.Media {
		if m.ID == id && m.Storage >= 0 && m.Storage <= 1 && camera.SafePath(m.Path) {
			return m, true
		}
	}
	return camera.Media{}, false
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

func (s *Adapter) mediaHTTPError(w http.ResponseWriter, r *http.Request, event string, status int, upstream int, storage int) {
	s.mu.Lock()
	connectionID := s.connectionID
	s.mu.Unlock()
	fields := map[string]string{"status": fmt.Sprint(status), "upstream_status": fmt.Sprint(upstream), "storage": fmt.Sprint(storage), "connection_id": connectionID}
	s.logs.Record("warn", "media", event, "相机文件读取失败，尚未进入图片或视频解码", fields)
	http.Error(w, "相机文件读取失败，请检查连接或刷新素材列表后重试", status)
}

func (s *Adapter) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := s.previewMedia(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	upstream := 0
	for _, p := range thumbnailPaths(m.Thumb) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, camera.MediaURL("192.168.2.1", m.Storage, p), nil)
		resp, err := s.client.Do(req)
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
	s.mediaHTTPError(w, r, "media.thumbnail_failed", previewErrorStatus(upstream), upstream, m.Storage)
}

// 流式转发原文件及 Range 响应，使播放器可以按需读取和拖动进度，避免整段视频驻留内存。
func (s *Adapter) servePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := s.previewMedia(r.URL.Query().Get("id"), r.URL.Query().Get("session"))
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
	paths := []string{m.Path}
	// Action 6 的预览代理与原视频同目录同名，扩展名为 LRF；不存在才读取原文件。
	if m.Kind == "video" && r.URL.Query().Get("original") != "1" {
		paths = append([]string{strings.TrimSuffix(m.Path, path.Ext(m.Path)) + ".LRF"}, paths...)
	}
	for index, p := range paths {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, camera.MediaURL("192.168.2.1", m.Storage, p), nil)
		if value := r.Header.Get("Range"); value != "" {
			req.Header.Set("Range", value)
		}
		var err error
		resp, err = s.client.Do(req)
		if err != nil {
			if r.Context().Err() == nil {
				s.mediaHTTPError(w, r, "media.preview_failed", http.StatusBadGateway, 0, m.Storage)
			}
			return
		}
		if resp.StatusCode == http.StatusNotFound && index+1 < len(paths) {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			resp.Body.Close()
			s.mediaHTTPError(w, r, "media.preview_failed", previewErrorStatus(resp.StatusCode), resp.StatusCode, m.Storage)
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
			s.logs.Record("warn", "media", "media.preview_interrupted", "预览传输中断，请重新加载", nil)
		}
	}
}

func previewErrorStatus(upstream int) int {
	if upstream == http.StatusNotFound {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}
