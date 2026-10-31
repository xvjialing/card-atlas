package action6

import (
	"net/http"
	"path"
	"strings"

	"cardatlas/internal/dji"
	"cardatlas/internal/dji/preview"
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

func (s *Adapter) previewHandler() *preview.Handler {
	return &preview.Handler{
		Client: s.client,
		Host:   "192.168.2.1",
		Logs:   s.logs,
		ConnectionID: func() string {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.connectionID
		},
		Lookup: s.previewMedia,
		Alternates: func(m camera.Media, r *http.Request) []string {
			if m.Kind != "video" || r.URL.Query().Get("original") == "1" {
				return nil
			}
			// Action 6 的预览代理与原视频同目录同名，扩展名为 LRF；不存在才读取原文件。
			return []string{strings.TrimSuffix(m.Path, path.Ext(m.Path)) + ".LRF"}
		},
	}
}

func (s *Adapter) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	s.previewHandler().Thumbnail(w, r)
}

func (s *Adapter) servePreview(w http.ResponseWriter, r *http.Request) {
	s.previewHandler().Preview(w, r)
}
