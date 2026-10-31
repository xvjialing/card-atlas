// Package diagnostics 保存离线诊断事件。原始协议载荷、凭据和素材内容不进入日志。
package diagnostics

import (
	"archive/zip"
	"bufio"
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const MaxFileBytes int64 = 5 << 20
const MaxTotalBytes int64 = 100 << 20
const stamp = "2006-01-02T15:04:05.000000000Z"

type Settings struct {
	RetentionDays int    `json:"retentionDays"`
	Level         string `json:"level"`
}
type Entry struct {
	ID        string            `json:"id"`
	Time      string            `json:"time"`
	Session   string            `json:"session"`
	Level     string            `json:"level"`
	Component string            `json:"component"`
	Event     string            `json:"event"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
}
type Info struct {
	Settings  Settings `json:"settings"`
	Directory string   `json:"directory"`
	Files     int      `json:"files"`
	Bytes     int64    `json:"bytes"`
	MaxBytes  int64    `json:"maxBytes"`
	Session   string   `json:"session"`
	LastError string   `json:"lastError"`
}
type Query struct {
	Level  string `json:"level"`
	Search string `json:"search"`
	Before string `json:"before"`
	Limit  int    `json:"limit"`
}
type Page struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
	Skipped    int     `json:"skipped"`
}
type Cleanup struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}
type Store struct {
	mu                                                 sync.Mutex
	directory, configPath, session, current, lastError string
	settings                                           Settings
	sequence                                           uint64
	part                                               int
	day                                                string
	currentBytes                                       int64
	secrets                                            []string
	clock                                              func() time.Time
	fileLimit, totalLimit                              int64
}

var filePattern = regexp.MustCompile(`^(?:cardatlas|osmo-bridge)-(\d{4}-\d{2}-\d{2})-[0-9a-f]{12}-[0-9]{4,8}\.jsonl$`)
var sensitiveKey = regexp.MustCompile(`(?i)password|passphrase|passwd|ssid|token|identity|authorization|cookie|secret|payload|body|args|filename|filepath|^path$|^device$|^data$`)
var assignment = regexp.MustCompile(`(?i)(password|passphrase|passwd|ssid|token|identity|authorization|secret|密码)["']?\s*[=:：]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
var uuid = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b|\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b`)
var localPath = regexp.MustCompile(`(?:/Users/|/Volumes/|/private/|/tmp/|/home/|DCIM/|MISC/)[^\r\n]*`)
var queryURL = regexp.MustCompile(`https?://[^\s"<>]+`)

func New(base string) *Store {
	var b [6]byte
	_, _ = rand.Read(b[:])
	s := &Store{directory: filepath.Join(base, "logs"), configPath: filepath.Join(base, "logging.json"), session: hex.EncodeToString(b[:]), settings: Settings{7, "info"}, clock: time.Now, fileLimit: MaxFileBytes, totalLimit: MaxTotalBytes}
	if b, e := os.ReadFile(s.configPath); e == nil {
		var config Settings
		if json.Unmarshal(b, &config) != nil || validate(config) != nil {
			s.lastError = "日志设置文件无效，当前使用默认设置"
		} else {
			s.settings = config
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		s.lastError = "日志设置读取失败"
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		s.lastError = "无法创建日志目录，请检查本地目录权限"
		return s
	}
	if _, err := s.cleanLocked(false, 0); err != nil {
		s.lastError = "日志自动清理失败：" + s.redact(err.Error())
	}
	return s
}
func validate(v Settings) error {
	if v.RetentionDays < 1 || v.RetentionDays > 90 {
		return errors.New("日志保留天数须为 1 到 90 的整数")
	}
	if v.Level != "info" && v.Level != "debug" {
		return errors.New("请选择标准或详细日志")
	}
	return nil
}
func (s *Store) Session() string { return s.session }
func (s *Store) RegisterSecrets(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range values {
		if len(v) < 3 {
			continue
		}
		found := false
		for _, old := range s.secrets {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			s.secrets = append(s.secrets, v)
			sort.Slice(s.secrets, func(i, j int) bool { return len(s.secrets[i]) > len(s.secrets[j]) })
		}
	}
}

func (s *Store) redact(value string) string {
	for _, secret := range s.secrets {
		value = strings.ReplaceAll(value, secret, "[已脱敏]")
	}
	value = assignment.ReplaceAllString(value, "$1=[已脱敏]")
	value = uuid.ReplaceAllString(value, "[设备标识已脱敏]")
	value = queryURL.ReplaceAllStringFunc(value, func(v string) string {
		if i := strings.IndexAny(v, "?#"); i >= 0 {
			v = v[:i] + "?[参数已脱敏]"
		}
		return v
	})
	value = localPath.ReplaceAllStringFunc(value, func(path string) string {
		for _, reason := range []string{"permission denied", "operation not permitted", "no such file or directory", "file exists", "read-only file system", "disk quota exceeded", "no space left on device"} {
			if strings.HasSuffix(path, ": "+reason) {
				return "[本地路径已脱敏]: " + reason
			}
		}
		return "[本地路径已脱敏]"
	})
	r := []rune(value)
	if len(r) > 1200 {
		value = string(r[:1200]) + "…[已截断]"
	}
	return value
}
func (s *Store) safeEntry(e Entry) Entry {
	e.Message = s.redact(e.Message)
	e.Component = s.redact(e.Component)
	e.Event = s.redact(e.Event)
	fields := map[string]string{}
	for k, v := range e.Fields {
		if len(fields) >= 16 {
			break
		}
		if sensitiveKey.MatchString(k) {
			fields[k] = "[已脱敏]"
		} else {
			fields[k] = s.redact(v)
		}
	}
	e.Fields = fields
	return e
}

// Record 在返回前写入并同步磁盘；每次只打开一次文件，清理后新事件会创建新文件。
func (s *Store) Record(level, component, event, message string, fields map[string]string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if level == "debug" && s.settings.Level != "debug" {
		return
	}
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		level = "info"
	}
	now := s.clock()
	s.sequence++
	e := s.safeEntry(Entry{ID: fmt.Sprintf("%s-%012d", s.session, s.sequence), Time: now.UTC().Format(stamp), Session: s.session, Level: level, Component: component, Event: event, Message: message, Fields: fields})
	data, err := json.Marshal(e)
	if err != nil {
		s.lastError = "日志编码失败"
		return
	}
	data = append(data, '\n')
	day := now.Format("2006-01-02")
	if s.current == "" || s.day != day || s.currentBytes+int64(len(data)) > s.fileLimit {
		s.day = day
		s.part++
		s.current = fmt.Sprintf("cardatlas-%s-%s-%04d.jsonl", day, s.session, s.part)
		s.currentBytes = 0
		if _, err = s.cleanLocked(false, int64(len(data))); err != nil {
			s.lastError = "日志自动清理失败：" + s.redact(err.Error())
		}
	}
	if err = os.MkdirAll(s.directory, 0700); err != nil {
		s.lastError = "无法创建日志目录"
		return
	}
	// 限制最终文件为普通文件，避免跟随外部建立的符号链接。
	f, err := os.OpenFile(filepath.Join(s.directory, s.current), os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err == nil {
		n, e := f.Write(data)
		s.currentBytes += int64(n)
		err = e
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		s.lastError = "写入日志失败：" + s.redact(err.Error())
	}
}

type logFile struct {
	name, date string
	size       int64
	modified   time.Time
}

func (s *Store) files() ([]logFile, error) {
	entries, err := os.ReadDir(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return []logFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := []logFile{}
	for _, e := range entries {
		m := filePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		stat, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{e.Name(), m[1], stat.Size(), stat.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].date != files[j].date {
			return files[i].date < files[j].date
		}
		if !files[i].modified.Equal(files[j].modified) {
			return files[i].modified.Before(files[j].modified)
		}
		return files[i].name < files[j].name
	})
	return files, nil
}
func (s *Store) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Info{Settings: s.settings, Directory: s.directory, Session: s.session, LastError: s.lastError, MaxBytes: s.totalLimit}
	files, err := s.files()
	if err != nil {
		v.LastError = "无法读取日志目录"
	}
	v.Files = len(files)
	for _, f := range files {
		v.Bytes += f.size
	}
	return v
}
func (s *Store) SaveSettings(v Settings) error {
	if err := validate(v); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.MarshalIndent(v, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.configPath), ".logging-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, s.configPath); err != nil {
		return err
	}
	s.settings = v
	if _, err = s.cleanLocked(false, 0); err != nil {
		s.lastError = "设置已保存，但过期日志清理失败"
		return errors.New(s.lastError)
	}
	s.lastError = ""
	return nil
}
func (s *Store) Clean(all bool) (Cleanup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanLocked(all, 0)
}
func (s *Store) cleanLocked(all bool, reserve int64) (Cleanup, error) {
	files, err := s.files()
	if err != nil {
		return Cleanup{}, err
	}
	var result Cleanup
	var total int64
	for _, f := range files {
		total += f.size
	}
	cutoff := s.clock().AddDate(0, 0, 1-s.settings.RetentionDays).Format("2006-01-02")
	for _, f := range files {
		if !all && f.date >= cutoff && total+reserve <= s.totalLimit-s.fileLimit {
			continue
		}
		if err := os.Remove(filepath.Join(s.directory, f.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		result.Files++
		result.Bytes += f.size
		total -= f.size
		if f.name == s.current {
			s.current = ""
			s.currentBytes = 0
		}
	}
	return result, nil
}

func key(e Entry) string { return e.Time + "|" + e.ID }

type entryHeap []Entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return key(h[i]) < key(h[j]) }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(v any)        { *h = append(*h, v.(Entry)) }
func (h *entryHeap) Pop() any          { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }

func (s *Store) walk(files []logFile, visit func(Entry) error) (int, error) {
	skipped := 0
	for _, item := range files {
		f, err := os.OpenFile(filepath.Join(s.directory, item.name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return skipped, err
		}
		// 文件清理与追加可以同时发生；只读枚举时已经存在的长度，内存始终有界。
		scanner := bufio.NewScanner(io.LimitReader(f, item.size))
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var e Entry
			if json.Unmarshal(scanner.Bytes(), &e) != nil || e.ID == "" || e.Time == "" {
				skipped++
				continue
			}
			if err = visit(e); err != nil {
				f.Close()
				return skipped, err
			}
		}
		if scanner.Err() != nil {
			skipped++
		}
		f.Close()
	}
	return skipped, nil
}
func (s *Store) Read(q Query) (Page, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 100
	}
	if len([]rune(q.Search)) > 256 {
		return Page{}, errors.New("搜索内容不能超过 256 个字符")
	}
	if len(q.Before) > 100 {
		return Page{}, errors.New("无效的日志游标")
	}
	if q.Level != "" && q.Level != "all" && q.Level != "debug" && q.Level != "info" && q.Level != "warn" && q.Level != "error" {
		return Page{}, errors.New("无效日志级别")
	}
	s.mu.Lock()
	files, err := s.files()
	s.mu.Unlock()
	if err != nil {
		return Page{}, err
	}
	h := &entryHeap{}
	search := strings.ToLower(q.Search)
	skipped, err := s.walk(files, func(e Entry) error {
		if q.Before != "" && key(e) >= q.Before {
			return nil
		}
		if q.Level != "" && q.Level != "all" && e.Level != q.Level {
			return nil
		}
		s.mu.Lock()
		e = s.safeEntry(e)
		s.mu.Unlock()
		if search != "" {
			b, _ := json.Marshal(e)
			if !strings.Contains(strings.ToLower(string(b)), search) {
				return nil
			}
		}
		heap.Push(h, e)
		if h.Len() > q.Limit+1 {
			heap.Pop(h)
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	entries := []Entry(*h)
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) > key(entries[j]) })
	page := Page{Entries: entries, HasMore: len(entries) > q.Limit, Skipped: skipped}
	if page.HasMore {
		page.Entries = entries[:q.Limit]
		page.NextCursor = key(page.Entries[len(page.Entries)-1])
	}
	return page, nil
}

// Export 只导出日志快照和版本信息；不包含应用配置、媒体清单、配对标识或素材文件。
func (s *Store) Export(dst io.Writer) error {
	s.mu.Lock()
	files, err := s.files()
	settings := s.settings
	s.mu.Unlock()
	if err != nil {
		return err
	}
	z := zip.NewWriter(dst)
	manifest, err := z.Create("diagnostics.json")
	if err != nil {
		return err
	}
	metadata := map[string]any{"app": "CardAtlas", "version": "0.1.0", "wails": "未知", "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "exportedAt": time.Now().UTC().Format(time.RFC3339), "settings": settings, "scope": "本地应用诊断日志；无素材、密码、Wi-Fi 名称和配置文件"}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range build.Deps {
			if dependency.Path == "github.com/wailsapp/wails/v3" {
				metadata["wails"] = dependency.Version
			}
		}
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				metadata[setting.Key] = setting.Value
			}
		}
	}
	metadata["systemVersion"] = systemVersion()
	if err = json.NewEncoder(manifest).Encode(metadata); err != nil {
		return err
	}
	writer, err := z.Create("events.jsonl")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	skipped, err := s.walk(files, func(e Entry) error { s.mu.Lock(); e = s.safeEntry(e); s.mu.Unlock(); return encoder.Encode(e) })
	if err != nil {
		return err
	}
	note, err := z.Create("说明.txt")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(note, "CardAtlas离线诊断包\n日志均为本机采集，时间采用带时区的 UTC 时间戳。\nWi-Fi 密码、配对标识、原始报文、素材内容不采集；已知 Wi-Fi 名称和本地路径在写入前脱敏。\n已跳过 %d 条无法解析的记录。包含日志保留范围内全部会话，不受界面搜索条件限制。\n请自行检查后再分享。\n", skipped)
	if err != nil {
		return err
	}
	return z.Close()
}

// Wails 只接入警告/错误，避免频繁的界面轮询淹没有意义的连接日志。
type handler struct {
	store *Store
	attrs []slog.Attr
}

func (s *Store) Slog() *slog.Logger                            { return slog.New(handler{store: s}) }
func (h handler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (h handler) Handle(_ context.Context, r slog.Record) error {
	fields := map[string]string{}
	add := func(a slog.Attr) {
		if a.Value.Kind() == slog.KindString || a.Value.Kind() == slog.KindInt64 || a.Value.Kind() == slog.KindBool || a.Value.Kind() == slog.KindDuration {
			fields[a.Key] = a.Value.String()
		} else {
			fields[a.Key] = "[复杂数据未采集]"
		}
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	level := "warn"
	if r.Level >= slog.LevelError {
		level = "error"
	}
	h.store.Record(level, "framework", "framework.message", r.Message, fields)
	return nil
}
func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return h
}
func (h handler) WithGroup(_ string) slog.Handler { return h }

type contextKey struct{}
type Trace struct {
	Store  *Store
	Fields map[string]string
}

func With(ctx context.Context, s *Store, fields map[string]string) context.Context {
	return context.WithValue(ctx, contextKey{}, Trace{s, fields})
}
func From(ctx context.Context) Trace {
	if t, ok := ctx.Value(contextKey{}).(Trace); ok {
		return t
	}
	return Trace{}
}
func (t Trace) Log(level, component, event, message string, fields map[string]string) {
	if t.Store == nil {
		return
	}
	merged := map[string]string{}
	for k, v := range t.Fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	t.Store.Record(level, component, event, message, merged)
}
