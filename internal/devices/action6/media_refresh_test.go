package action6

import (
	"cardatlas/internal/dji"
	"context"
	"errors"
	"strings"
	"testing"
)

type refreshMedia struct {
	fakeMedia
	files   []camera.Media
	warning string
	err     error
}

func (m *refreshMedia) List(context.Context) ([]camera.Media, string, error) {
	return m.files, m.warning, m.err
}

func TestRefreshRetainsLibraryOnPartialOrFailedListing(t *testing.T) {
	for _, tc := range []struct {
		name, warning string
		err           error
	}{
		{"部分存储失败", "SD 卡：素材列表接收不完整，请刷新重试", nil},
		{"全部存储失败", "", errors.New("内置存储与 SD 卡均未响应")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := connectionService(t)
			s.state.Phase = "connected"
			s.state.Media = []camera.Media{{ID: "old-internal", Storage: 1}, {ID: "old-sd", Storage: 0}}
			link := &refreshMedia{files: []camera.Media{{ID: "new-internal", Storage: 1}}, warning: tc.warning, err: tc.err}
			s.link = link
			err := s.Refresh()
			state := s.GetState()
			if err == nil || state.Busy || state.Phase != "connected" || len(state.Media) != 2 || state.Media[0].ID != "old-internal" || state.Media[1].ID != "old-sd" {
				t.Fatalf("刷新失败覆盖了已有清单或未恢复状态：%+v %v", state, err)
			}
			if tc.warning != "" && !strings.Contains(err.Error(), "已保留原素材列表") {
				t.Fatal("部分失败缺少保留原列表提示")
			}
			link.warning, link.err = "", nil
			if err := s.Refresh(); err != nil {
				t.Fatal(err)
			}
			state = s.GetState()
			if state.Busy || len(state.Media) != 1 || state.Media[0].ID != "new-internal" {
				t.Fatalf("再次刷新成功未更新清单：%+v", state)
			}
		})
	}
}
