package deviceimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testProvider struct {
	mu      sync.Mutex
	session Session
	block   bool
	calls   int
	id      string
}

func (p *testProvider) Descriptor() Descriptor { return Descriptor{ID: p.id, Available: true} }
func (p *testProvider) Snapshot() Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	s.Media = append([]Media{}, s.Media...)
	return s
}
func (p *testProvider) Discover() ([]Device, error)     { return []Device{}, nil }
func (p *testProvider) Connect(ConnectionRequest) error { return nil }
func (p *testProvider) Disconnect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session.Phase = "disconnected"
	return nil
}
func (p *testProvider) Refresh() error { return nil }
func (p *testProvider) Download(ctx context.Context, _ string, _ Media, root, relative string, progress func(Progress)) (Result, error) {
	p.mu.Lock()
	p.calls++
	block := p.block
	p.mu.Unlock()
	progress(Progress{Bytes: 2, Total: 4, Phase: "downloading"})
	if block {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}
	target := filepath.Join(root, relative)
	if e := os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return Result{}, e
	}
	f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return Result{}, e
	}
	_, e = f.WriteString("test")
	f.Close()
	return Result{Path: target, Bytes: 4, SHA256: "local-fingerprint"}, e
}
func fixtureProvider() *testProvider {
	return &testProvider{id: "future-camera", session: Session{DeviceID: "camera-one", DeviceName: "测试摄影设备", Phase: "connected", Revision: 1, Media: []Media{
		{ID: "sd", Name: "DSC0001.JPG", Path: "DCIM/100/DSC0001.JPG", StorageID: "sd", StorageName: "SD 卡", Kind: "photo", Size: 4, Date: "2026-10-23 10:00:00"},
		{ID: "internal", Name: "DSC0001.JPG", Path: "DCIM/100/DSC0001.JPG", StorageID: "internal", StorageName: "内置存储", Kind: "photo", Size: 4, Date: "2026-10-23 10:00:00"},
	}}}
}
func waitFor(t *testing.T, m *Manager, done func(State) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if done(m.GetState()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("任务未达到预期：%+v", m.GetState())
}
func TestGenericProviderCollisionAndPersistedHistory(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	p := fixtureProvider()
	m := NewManager(base, p)
	existing := filepath.Join(root, "photo/2026/10/23/DSC0001.JPG")
	os.MkdirAll(filepath.Dir(existing), 0755)
	os.WriteFile(existing, []byte("keep"), 0600)
	plan, e := m.Prepare(ImportRequest{IDs: []string{"sd", "internal", "sd"}, Destination: root, Layout: "date"})
	if e != nil {
		t.Fatal(e)
	}
	if plan.Count != 2 || !strings.HasSuffix(plan.Tasks[0].RelativePath, "_001.JPG") || !strings.HasSuffix(plan.Tasks[1].RelativePath, "_002.JPG") {
		t.Fatal(plan)
	}
	if len(plan.TaskIDs) != plan.Count || plan.TaskIDs[0] != plan.Tasks[0].ID || plan.TaskIDs[1] != plan.Tasks[1].ID {
		t.Fatal("导入规划未返回准确的本次任务范围", plan.TaskIDs)
	}
	if e = m.Start(plan.ID); e != nil {
		t.Fatal(e)
	}
	waitFor(t, m, func(s State) bool { return len(s.Tasks) == 2 && s.Tasks[1].Status == "completed" })
	m.Pause()
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Fatal("覆盖了已有原片")
	}
	restarted := NewManager(base, p)
	if len(restarted.GetState().Tasks) != 2 {
		t.Fatal("导入记录未持久化")
	}
	again, e := restarted.Prepare(ImportRequest{IDs: []string{"sd", "internal"}, Destination: root, Layout: "date"})
	if e != nil || again.Count != 0 || again.Skipped != 2 || len(again.TaskIDs) != 0 {
		t.Fatal(again, e)
	}
	os.Remove(restarted.GetState().Tasks[0].Path)
	again, e = restarted.Prepare(ImportRequest{IDs: []string{"sd"}, Destination: root, Layout: "date"})
	if e != nil || again.Count != 1 {
		t.Fatal("已移走的文件不能被永久跳过", again, e)
	}
}
func TestPauseRestartAndWrongDeviceRetry(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	p := fixtureProvider()
	p.block = true
	m := NewManager(base, p)
	plan, _ := m.Prepare(ImportRequest{IDs: []string{"sd", "internal"}, Destination: root, Layout: "source"})
	if e := m.Start(plan.ID); e != nil {
		t.Fatal(e)
	}
	waitFor(t, m, func(s State) bool { return s.Tasks[0].Progress.Bytes == 2 })
	m.Pause()
	for _, task := range m.GetState().Tasks {
		if task.Status != "paused" {
			t.Fatal(task)
		}
	}
	restarted := NewManager(base, p)
	task := restarted.GetState().Tasks[0]
	p.mu.Lock()
	p.session.DeviceID = "another-camera"
	p.block = false
	p.mu.Unlock()
	if restarted.Retry(task.ID) == nil {
		t.Fatal("不同设备的同名素材被用于续传")
	}
	p.mu.Lock()
	p.session.DeviceID = "camera-one"
	p.mu.Unlock()
	if e := restarted.Retry(task.ID); e != nil {
		t.Fatal(e)
	}
	waitFor(t, restarted, func(s State) bool { return s.Tasks[0].Status == "completed" })
	restarted.Pause()
}
func TestStalePlansAndChangedMediaAreRejected(t *testing.T) {
	p := fixtureProvider()
	m := NewManager(t.TempDir(), p)
	request := ImportRequest{IDs: []string{"sd"}, Destination: t.TempDir(), Layout: "date"}
	plan, _ := m.Prepare(request)
	p.mu.Lock()
	p.session.Revision++
	p.mu.Unlock()
	if m.Start(plan.ID) == nil {
		t.Fatal("过期清单被导入")
	}
	p.block = true
	plan, _ = m.Prepare(request)
	if e := m.Start(plan.ID); e != nil {
		t.Fatal(e)
	}
	waitFor(t, m, func(s State) bool { return s.Tasks[0].Progress.Bytes == 2 })
	m.Pause()
	p.mu.Lock()
	p.session.Media[0].Size++
	p.mu.Unlock()
	if m.Retry(m.GetState().Tasks[0].ID) == nil {
		t.Fatal("文件已变化却继续旧任务")
	}
	if _, e := m.Prepare(ImportRequest{IDs: []string{"missing"}, Destination: request.Destination, Layout: "date"}); e == nil {
		t.Fatal("接受了清单外的 ID")
	}
}
func TestUnknownDatesStorageIsolationAndUnsafeNames(t *testing.T) {
	p := fixtureProvider()
	p.session.Media[0].Date = ""
	m := NewManager(t.TempDir(), p)
	root := t.TempDir()
	plan, e := m.Prepare(ImportRequest{IDs: []string{"sd"}, Destination: root, Layout: "date"})
	if e != nil || plan.UnknownDates != 1 || !strings.Contains(plan.Tasks[0].RelativePath, "日期未知") {
		t.Fatal(plan, e)
	}
	plan, e = m.Prepare(ImportRequest{IDs: []string{"sd", "internal"}, Destination: root, Layout: "source"})
	if e != nil || plan.Tasks[0].RelativePath == plan.Tasks[1].RelativePath {
		t.Fatal("不同存储的目录混淆", e)
	}
	for _, name := range []string{"../escape.JPG", "..", "x\\escape.JPG", "bad\x00.JPG"} {
		p.session.Media[0].Name = name
		if _, e = m.Prepare(ImportRequest{IDs: []string{"sd"}, Destination: root, Layout: "date"}); e == nil {
			t.Fatal("危险名称被接受", name)
		}
	}
}
func TestCorruptOrUnwritableHistoryNeverStartsDownload(t *testing.T) {
	for _, mode := range []string{"corrupt", "unwritable"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			if mode == "corrupt" {
				os.WriteFile(filepath.Join(base, "imports.json"), []byte("broken"), 0600)
			}
			p := fixtureProvider()
			m := NewManager(base, p)
			plan, e := m.Prepare(ImportRequest{IDs: []string{"sd"}, Destination: t.TempDir(), Layout: "date"})
			if mode == "corrupt" {
				if e == nil {
					t.Fatal("损坏的历史被静默覆盖")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			os.Mkdir(m.file+".tmp", 0700)
			if m.Start(plan.ID) == nil {
				t.Fatal("记录未落盘仍开始传输")
			}
			if len(m.GetState().Tasks) != 0 || p.calls != 0 {
				t.Fatal("任务没有回滚")
			}
		})
	}
}
func TestInterruptedQueuedTasksRestorePaused(t *testing.T) {
	base := t.TempDir()
	b, _ := json.Marshal([]Task{{ID: "1", Status: "downloading"}, {ID: "2", Status: "queued"}, {ID: "3", Status: "completed"}})
	os.WriteFile(filepath.Join(base, "imports.json"), b, 0600)
	m := NewManager(base, fixtureProvider())
	s := m.GetState()
	if s.Tasks[0].Status != "paused" || s.Tasks[1].Status != "paused" || s.Tasks[2].Status != "completed" {
		t.Fatal(s)
	}
	s.Tasks[0].Status = "edited"
	if m.GetState().Tasks[0].Status != "paused" {
		t.Fatal("状态快照共享数据")
	}
	if _, e := m.CompletedPath("missing"); e == nil || errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}

func TestResumeOnlyRestoresMatchingDeviceAndUnchangedMedia(t *testing.T) {
	p := fixtureProvider()
	m := NewManager(t.TempDir(), p)
	plan, e := m.Prepare(ImportRequest{IDs: []string{"sd", "internal"}, Destination: t.TempDir(), Layout: "date"})
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.tasks = plan.Tasks
	for i := range m.tasks {
		m.tasks[i].Status = "paused"
	}
	m.mu.Unlock()
	p.mu.Lock()
	p.session.Media[1].Date = "2026-10-24 10:00:00"
	p.mu.Unlock()
	count, e := m.Resume()
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	waitFor(t, m, func(s State) bool { return s.Tasks[0].Status == "completed" })
	m.Pause()
	if m.GetState().Tasks[1].Status != "paused" {
		t.Fatal("批量恢复接受了已变化的素材")
	}
	if _, e = m.Resume(); e == nil {
		t.Fatal("没有可恢复素材时未提示")
	}
}
