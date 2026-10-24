package deviceimport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager 统一管理设备适配器、导入规划和可恢复队列。所有用户修改串行，状态查询不会等待网络。
type Manager struct {
	mu            sync.Mutex
	op            sync.Mutex
	providers     map[string]Provider
	active        string
	tasks         []Task
	plan          *Plan
	file, warning string
	loadError     error
	cancel        context.CancelFunc
	done          chan struct{}
}

func NewManager(base string, providers ...Provider) *Manager {
	m := &Manager{providers: map[string]Provider{}, tasks: []Task{}, file: filepath.Join(base, "imports.json")}
	for _, p := range providers {
		id := p.Descriptor().ID
		m.providers[id] = p
		if m.active == "" {
			m.active = id
		}
	}
	b, err := os.ReadFile(m.file)
	if err == nil {
		err = json.Unmarshal(b, &m.tasks)
		if err == nil {
			for i := range m.tasks {
				t := &m.tasks[i]
				if t.Status == "downloading" || t.Status == "queued" {
					t.Status = "paused"
				}
				t.Progress.Speed = 0
			}
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.tasks = []Task{}
		m.loadError = err
		m.warning = "导入记录读取失败。为保护原记录，已暂停新导入：" + err.Error()
	}
	return m
}
func (m *Manager) providerLocked() (Provider, error) {
	p := m.providers[m.active]
	if p == nil || !p.Descriptor().Available {
		return nil, errors.New("该设备尚未提供连接适配")
	}
	return p, nil
}
func (m *Manager) GetState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := State{ActiveProvider: m.active, Providers: []Descriptor{}, Tasks: append([]Task{}, m.tasks...), Warning: m.warning, Connection: Session{Phase: "disconnected", WiFiNetworks: []Network{}}}
	for _, p := range m.providers {
		s.Providers = append(s.Providers, p.Descriptor())
	}
	sort.Slice(s.Providers, func(i, j int) bool { return s.Providers[i].ID < s.Providers[j].ID })
	if p := m.providers[m.active]; p != nil {
		s.Connection = p.Snapshot()
		s.Connection.Media = nil
	}
	return s
}
func (m *Manager) Library() ([]Media, error) {
	m.mu.Lock()
	p, e := m.providerLocked()
	m.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return p.Snapshot().Media, nil
}
func (m *Manager) SelectProvider(id string) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == m.active {
		return nil
	}
	p := m.providers[id]
	if p == nil || !p.Descriptor().Available {
		return errors.New("该设备尚未提供连接适配")
	}
	if m.cancel != nil {
		return errors.New("请先暂停导入")
	}
	if old := m.providers[m.active]; old != nil {
		s := old.Snapshot()
		if s.Phase != "disconnected" || s.Busy {
			return errors.New("请先断开当前设备")
		}
	}
	m.active = id
	m.plan = nil
	return nil
}
func (m *Manager) Discover() ([]Device, error) {
	if !m.op.TryLock() {
		return nil, errors.New("正在处理设备操作，请稍候")
	}
	defer m.op.Unlock()
	m.mu.Lock()
	p, e := m.providerLocked()
	m.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return p.Discover()
}
func (m *Manager) Connect(r ConnectionRequest) error {
	if !m.op.TryLock() {
		return errors.New("正在处理设备操作，请稍候")
	}
	defer m.op.Unlock()
	m.mu.Lock()
	p, e := m.providerLocked()
	running := m.cancel != nil
	m.mu.Unlock()
	if e != nil {
		return e
	}
	if running {
		return errors.New("请先暂停导入，再调整设备连接")
	}
	return p.Connect(r)
}
func (m *Manager) Refresh() error {
	if !m.op.TryLock() {
		return errors.New("正在处理设备操作，请稍候")
	}
	defer m.op.Unlock()
	m.mu.Lock()
	p, e := m.providerLocked()
	running := m.cancel != nil
	m.mu.Unlock()
	if e != nil {
		return e
	}
	if running {
		return errors.New("请先暂停导入，再刷新素材")
	}
	return p.Refresh()
}
func (m *Manager) Disconnect() error {
	m.op.Lock()
	defer m.op.Unlock()
	m.pause()
	m.mu.Lock()
	p := m.providers[m.active]
	m.plan = nil
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Disconnect()
}

func (m *Manager) saveLocked() error {
	if m.loadError != nil {
		return errors.New("原导入记录无法读取，不能覆盖")
	}
	if e := os.MkdirAll(filepath.Dir(m.file), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(m.tasks, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(m.file+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(m.file+".tmp", m.file)
}
func token() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func taskKey(provider, device, root, layout string, media Media) string {
	b, _ := json.Marshal([]any{provider, device, root, layout, media.ID, media.Path, media.StorageID, media.Date, media.Size})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
func validMedia(f Media) bool {
	return f.ID != "" && f.Name != "" && f.Name != "." && f.Name != ".." && filepath.Base(f.Name) == f.Name && !strings.ContainsAny(f.Name, "\\\x00\r\n") && f.Size >= 0 && (f.Kind == "photo" || f.Kind == "video")
}
func relativePath(layout, provider, device string, f Media) (string, error) {
	if !validMedia(f) {
		return "", errors.New("设备返回了无效的素材名称或类型")
	}
	if layout == "date" {
		date := "日期未知"
		if stamp, e := time.ParseInLocation("2006-01-02 15:04:05", f.Date, time.Local); e == nil {
			date = stamp.Format("2006/01/02")
		}
		return filepath.Join(f.Kind, filepath.FromSlash(date), f.Name), nil
	}
	if layout != "source" {
		return "", errors.New("请选择按拍摄日期归档或保留设备目录")
	}
	if !filepath.IsLocal(filepath.FromSlash(f.Path)) || strings.ContainsAny(f.Path, "\\\x00") {
		return "", errors.New("设备素材路径无效")
	}
	if !filepath.IsLocal(f.StorageID) || strings.ContainsAny(f.StorageID, "/\\\x00") {
		return "", errors.New("设备存储标识无效")
	}
	h := sha256.Sum256([]byte(provider + "|" + device))
	return filepath.Join("设备原目录", hex.EncodeToString(h[:6]), f.StorageID, filepath.FromSlash(f.Path)), nil
}
func (m *Manager) Prepare(r ImportRequest) (Plan, error) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out Plan
	if m.loadError != nil {
		return out, errors.New(m.warning)
	}
	p, e := m.providerLocked()
	if e != nil {
		return out, e
	}
	s := p.Snapshot()
	if s.Phase != "connected" || s.Busy {
		return out, errors.New("请连接设备并等待读取完成")
	}
	if len(r.IDs) == 0 {
		return out, errors.New("请先选择要导入的素材")
	}
	root, e := filepath.Abs(strings.TrimSpace(r.Destination))
	if r.Destination == "" || e != nil {
		return out, errors.New("请选择导入位置")
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return out, fmt.Errorf("导入目录无法访问：%w", e)
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return out, errors.New("请选择可访问的文件夹")
	}
	dir, e := os.OpenRoot(root)
	if e != nil {
		return out, e
	}
	defer dir.Close()
	out = Plan{ID: token(), Destination: root, Layout: r.Layout, DeviceName: s.DeviceName, Revision: s.Revision, Examples: []string{}, Tasks: []Task{}}
	lookup := map[string]Media{}
	for _, f := range s.Media {
		lookup[f.ID] = f
	}
	seen := map[string]bool{}
	reserved := map[string]bool{}
	old := map[string]Task{}
	for _, t := range m.tasks {
		old[t.ID] = t
		if t.Root == root {
			reserved[strings.ToLower(t.RelativePath)] = true
		}
	}
	for _, id := range r.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		f, ok := lookup[id]
		if !ok {
			return Plan{}, errors.New("素材清单已变化，请重新选择")
		}
		key := taskKey(m.active, s.DeviceID, root, r.Layout, f)
		previous, exists := old[key]
		if exists && (previous.Status == "queued" || previous.Status == "downloading") {
			out.Skipped++
			continue
		}
		if exists && previous.Status == "completed" {
			if st, e := os.Stat(previous.Path); e == nil && st.Mode().IsRegular() && st.Size() == previous.Progress.Total {
				out.Skipped++
				continue
			}
		}
		rel, e := relativePath(r.Layout, m.active, s.DeviceID, f)
		if e != nil {
			return Plan{}, e
		}
		if exists && (previous.Status == "paused" || previous.Status == "failed") {
			rel = previous.RelativePath
		} else {
			base := rel
			for i := 0; ; i++ {
				if i > 0 {
					ext := filepath.Ext(base)
					rel = strings.TrimSuffix(base, ext) + fmt.Sprintf("_%03d", i) + ext
				}
				_, e := dir.Lstat(rel)
				_, pe := dir.Lstat(rel + ".part")
				_, me := dir.Lstat(rel + ".part.json")
				if !reserved[strings.ToLower(rel)] && errors.Is(e, os.ErrNotExist) && errors.Is(pe, os.ErrNotExist) && errors.Is(me, os.ErrNotExist) {
					break
				}
				if i >= 10000 {
					return Plan{}, errors.New("目标目录重名过多，请更换导入位置")
				}
			}
		}
		reserved[strings.ToLower(rel)] = true
		t := Task{ID: key, ProviderID: m.active, DeviceID: s.DeviceID, DeviceName: s.DeviceName, Media: f, Root: root, RelativePath: rel, Layout: r.Layout, Status: "queued", Progress: Progress{Total: f.Size}, CreatedAt: time.Now().UnixMilli()}
		if exists && (previous.Status == "paused" || previous.Status == "failed") {
			t.Progress = previous.Progress
			t.Progress.Speed = 0
		}
		out.Tasks = append(out.Tasks, t)
		out.TaskIDs = append(out.TaskIDs, t.ID)
		out.Count++
		out.Bytes += f.Size
		if strings.Contains(rel, "日期未知") {
			out.UnknownDates++
		}
		if len(out.Examples) < 5 {
			out.Examples = append(out.Examples, rel)
		}
	}
	m.plan = &out
	return out, nil
}
func (m *Manager) Start(planID string) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plan == nil || m.plan.ID != planID {
		return errors.New("导入预览已过期，请重新预览")
	}
	p, e := m.providerLocked()
	if e != nil {
		return e
	}
	s := p.Snapshot()
	plan := m.plan
	if s.Phase != "connected" || s.Busy || s.Revision != plan.Revision {
		return errors.New("设备或素材清单已变化，请重新预览")
	}
	if len(plan.Tasks) == 0 {
		return errors.New("所选素材已导入或已在队列中")
	}
	for _, t := range plan.Tasks {
		if t.DeviceID != s.DeviceID || t.ProviderID != m.active {
			return errors.New("设备会话已改变，请重新预览")
		}
		for _, existing := range m.tasks {
			if existing.ID == t.ID && (existing.Status == "queued" || existing.Status == "downloading") {
				return errors.New("这些素材已加入任务，请重新预览以更新导入范围")
			}
		}
	}
	before := append([]Task{}, m.tasks...)
	for _, t := range plan.Tasks {
		found := false
		for i, old := range m.tasks {
			if old.ID == t.ID {
				m.tasks[i] = t
				found = true
				break
			}
		}
		if !found {
			m.tasks = append(m.tasks, t)
		}
	}
	if e = m.saveLocked(); e != nil {
		m.tasks = before
		return fmt.Errorf("无法保存导入队列，尚未开始下载：%w", e)
	}
	m.plan = nil
	m.startLocked()
	return nil
}
func (m *Manager) startLocked() {
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer func() {
			m.mu.Lock()
			m.cancel = nil
			close(m.done)
			// 排空检查与退出之间可能收到新任务，交接时再次检查，避免任务停在等待中。
			if ctx.Err() == nil {
				for _, t := range m.tasks {
					if t.Status == "queued" {
						m.startLocked()
						break
					}
				}
			}
			m.mu.Unlock()
			cancel()
		}()
		for {
			m.mu.Lock()
			if ctx.Err() != nil {
				m.mu.Unlock()
				return
			}
			index := -1
			for i, t := range m.tasks {
				if t.Status == "queued" {
					index = i
					break
				}
			}
			if index < 0 {
				m.mu.Unlock()
				return
			}
			t := m.tasks[index]
			p := m.providers[t.ProviderID]
			m.tasks[index].Status = "downloading"
			m.mu.Unlock()
			var result Result
			var err error
			if p == nil {
				err = errors.New("设备适配器不可用")
			} else {
				s := p.Snapshot()
				if s.Phase != "connected" || s.DeviceID != t.DeviceID {
					err = errors.New("请重新连接原设备后继续")
				} else {
					result, err = p.Download(ctx, t.DeviceID, t.Media, t.Root, t.RelativePath, func(progress Progress) { m.mu.Lock(); m.tasks[index].Progress = progress; m.mu.Unlock() })
				}
			}
			m.mu.Lock()
			current := &m.tasks[index]
			current.Progress.Speed = 0
			if err == nil {
				current.Status = "completed"
				current.Path = result.Path
				current.SHA256 = result.SHA256
				current.Warning = result.Warning
				current.Error = ""
				current.Progress = Progress{Bytes: result.Bytes, Total: result.Bytes, Phase: "completed"}
			} else if ctx.Err() != nil {
				current.Status = "paused"
			} else {
				current.Status = "failed"
				current.Error = err.Error()
			}
			if err != nil && p != nil {
				session := p.Snapshot()
				if session.Phase != "connected" || session.DeviceID != t.DeviceID {
					for i := range m.tasks {
						if m.tasks[i].Status == "queued" && m.tasks[i].DeviceID == t.DeviceID {
							m.tasks[i].Status = "paused"
						}
					}
				}
			}
			if e := m.saveLocked(); e != nil {
				m.warning = "导入记录保存失败，请保留本次结果并检查磁盘：" + e.Error()
				for i := range m.tasks {
					if m.tasks[i].Status == "queued" {
						m.tasks[i].Status = "paused"
					}
				}
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
		}
	}()
}
func (m *Manager) pause() {
	m.mu.Lock()
	for i := range m.tasks {
		if m.tasks[i].Status == "queued" {
			m.tasks[i].Status = "paused"
		}
	}
	cancel, done := m.cancel, m.done
	if cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	if cancel != nil {
		<-done
	}
	m.mu.Lock()
	if e := m.saveLocked(); e != nil {
		m.warning = "暂停记录未能保存：" + e.Error()
	}
	m.mu.Unlock()
}
func (m *Manager) Pause() { m.op.Lock(); defer m.op.Unlock(); m.pause() }

// Resume 仅恢复当前设备上仍与原清单一致的任务。
func (m *Manager) Resume() (int, error) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.providerLocked()
	if err != nil {
		return 0, err
	}
	s := p.Snapshot()
	if s.Phase != "connected" || s.Busy {
		return 0, errors.New("请先连接原设备并等待读取完成")
	}
	lookup := map[string]Media{}
	for _, f := range s.Media {
		lookup[f.ID] = f
	}
	before := append([]Task{}, m.tasks...)
	count := 0
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.ProviderID != m.active || t.DeviceID != s.DeviceID || (t.Status != "paused" && t.Status != "failed") {
			continue
		}
		if f, ok := lookup[t.Media.ID]; !ok || f != t.Media {
			continue
		}
		t.Status = "queued"
		t.Error = ""
		count++
	}
	if count == 0 {
		return 0, errors.New("没有可恢复的任务，请确认已连接原设备且原素材未变化")
	}
	if err = m.saveLocked(); err != nil {
		m.tasks = before
		return 0, err
	}
	m.startLocked()
	return count, nil
}
func (m *Manager) Retry(id string) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.tasks {
		t := &m.tasks[i]
		if t.ID != id {
			continue
		}
		if t.Status != "failed" && t.Status != "paused" {
			return errors.New("该任务无需继续")
		}
		p := m.providers[t.ProviderID]
		if p == nil {
			return errors.New("设备适配器不可用")
		}
		s := p.Snapshot()
		if s.Phase != "connected" || s.Busy || s.DeviceID != t.DeviceID {
			return errors.New("请通过蓝牙连接原设备；旧手动会话请重新选择素材导入")
		}
		matched := false
		for _, f := range s.Media {
			if f == t.Media {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("原素材已变化或已不在设备中，请重新选择")
		}
		before := *t
		t.Status = "queued"
		t.Error = ""
		if e := m.saveLocked(); e != nil {
			*t = before
			return e
		}
		m.startLocked()
		return nil
	}
	return errors.New("导入任务不存在")
}
func (m *Manager) CompletedPath(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID == id && t.Status == "completed" {
			st, e := os.Stat(t.Path)
			if e != nil || !st.Mode().IsRegular() {
				return "", errors.New("文件已移动或删除")
			}
			return t.Path, nil
		}
	}
	return "", errors.New("文件尚未导入完成")
}
