package main

import (
	"cardatlas/internal/diagnostics"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type MediaFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	Extension    string `json:"extension"`
	Size         int64  `json:"size"`
	Date         string `json:"date"`
	DateSource   string `json:"dateSource"`
	Modified     int64  `json:"modified"`
}
type DayStat struct {
	Date   string `json:"date"`
	Photos int    `json:"photos"`
	Videos int    `json:"videos"`
	Bytes  int64  `json:"bytes"`
}
type ScanSummary struct {
	Path     string    `json:"path"`
	Count    int       `json:"count"`
	Photos   int       `json:"photos"`
	Videos   int       `json:"videos"`
	Bytes    int64     `json:"bytes"`
	Fallback int       `json:"fallback"`
	Ignored  int       `json:"ignored"`
	Warnings []string  `json:"warnings"`
	Days     []DayStat `json:"days"`
}
type JobStatus struct {
	ID           int64    `json:"id"`
	Kind         string   `json:"kind"`
	State        string   `json:"state"`
	Current      string   `json:"current"`
	Completed    int      `json:"completed"`
	Total        int      `json:"total"`
	Bytes        int64    `json:"bytes"`
	TotalBytes   int64    `json:"totalBytes"`
	Copied       int      `json:"copied"`
	Failed       int      `json:"failed"`
	Errors       []string `json:"errors"`
	Destination  string   `json:"destination"`
	Phase        string   `json:"phase"`
	CurrentBytes int64    `json:"currentBytes"`
	CurrentSize  int64    `json:"currentSize"`
	WorkDone     int64    `json:"workDone"`
	TotalWork    int64    `json:"totalWork"`
	StartedAt    int64    `json:"startedAt"`
	FinishedAt   int64    `json:"finishedAt"`
}
type Filter struct {
	Search string `json:"search"`
	Kind   string `json:"kind"`
	From   string `json:"from"`
	To     string `json:"to"`
	Page   int    `json:"page"`
	Sort   string `json:"sort"`
}
type FilePage struct {
	Files []MediaFile `json:"files"`
	Total int         `json:"total"`
	Bytes int64       `json:"bytes"`
}
type ExportPlan struct {
	Count       int       `json:"count"`
	Bytes       int64     `json:"bytes"`
	Directories []DayStat `json:"directories"`
	Examples    []string  `json:"examples"`
}

type MediaService struct {
	logs       *diagnostics.Store
	app        *application.App
	mu         sync.RWMutex
	files      []MediaFile
	summary    ScanSummary
	job        JobStatus
	history    []JobStatus
	cancel     context.CancelFunc
	thumbnails *thumbnailCache
}

func NewMediaService() *MediaService {
	return &MediaService{files: []MediaFile{}, summary: ScanSummary{Days: []DayStat{}, Warnings: []string{}}, job: JobStatus{State: "idle", Errors: []string{}}, history: []JobStatus{}, thumbnails: newThumbnailCache()}
}

func (s *MediaService) ChooseDirectory(export bool) (string, error) {
	if s.app == nil {
		return "", errors.New("文件夹选择仅在桌面客户端中可用")
	}
	title := "选择分析文件夹"
	if export {
		title = "选择导出文件夹"
	}
	return s.app.Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(export).PromptForSingleSelection()
}

func (s *MediaService) GetStatus() JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneJob(s.job)
}
func (s *MediaService) GetSummary() ScanSummary { s.mu.RLock(); defer s.mu.RUnlock(); return s.summary }
func (s *MediaService) GetHistory() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]JobStatus{}, s.history...)
}
func cloneJob(j JobStatus) JobStatus { j.Errors = append([]string{}, j.Errors...); return j }

func canonicalDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("请先选择文件夹")
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("文件夹不可访问：%w", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("请选择文件夹")
	}
	return p, nil
}

func (s *MediaService) StartScan(path string) (JobStatus, error) {
	root, err := canonicalDirectory(path)
	if err != nil {
		logOperationResult(s.logs, "scan", "scan.start_failed", "无法开始本地素材扫描", err, nil)
		return JobStatus{}, err
	}
	s.mu.Lock()
	if s.job.State == "running" {
		s.mu.Unlock()
		return JobStatus{}, errors.New("请等待或取消当前任务")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.job = JobStatus{ID: time.Now().UnixNano(), Kind: "scan", State: "running", Errors: []string{}}
	j := s.job
	s.mu.Unlock()
	s.logs.Record("info", "scan", "scan.started", "开始扫描本地素材", jobLogFields(j))
	go s.scan(ctx, root)
	return j, nil
}

func (s *MediaService) scan(ctx context.Context, root string) {
	files := []MediaFile{}
	summary := ScanSummary{Path: root, Warnings: []string{}, Days: []DayStat{}}
	days := map[string]*DayStat{}
	warn := func(message string) {
		if len(summary.Warnings) < 100 {
			summary.Warnings = append(summary.Warnings, message)
		}
	}
	source, err := os.OpenRoot(root)
	if err == nil {
		defer source.Close()
		err = fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				warn(path + "：" + walkErr.Error())
				return nil
			}
			if path != "." && strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return fs.SkipDir
				}
				summary.Ignored++
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				summary.Ignored++
				return nil
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))
			kind := "photo"
			if !photoExtensions[ext] {
				if !videoExtensions[ext] {
					summary.Ignored++
					return nil
				}
				kind = "video"
			}
			f, openErr := source.Open(path)
			if openErr != nil {
				warn(path + "：" + openErr.Error())
				return nil
			}
			info, infoErr := f.Stat()
			if infoErr != nil || !info.Mode().IsRegular() {
				f.Close()
				warn(path + "：无法读取普通文件")
				return nil
			}
			t, dateSource := mediaDate(f, kind, ext, info.ModTime())
			f.Close()
			hash := sha256.Sum256([]byte(path))
			item := MediaFile{ID: hex.EncodeToString(hash[:12]), Name: entry.Name(), RelativePath: path, Kind: kind, Extension: strings.ToUpper(ext), Size: info.Size(), Date: t.Format("2006-01-02T15:04:05"), DateSource: dateSource, Modified: info.ModTime().UnixNano()}
			files = append(files, item)
			summary.Count++
			summary.Bytes += item.Size
			if kind == "photo" {
				summary.Photos++
			} else {
				summary.Videos++
			}
			if dateSource == "文件修改时间" {
				summary.Fallback++
			}
			day := item.Date[:10]
			if days[day] == nil {
				days[day] = &DayStat{Date: day}
			}
			if kind == "photo" {
				days[day].Photos++
			} else {
				days[day].Videos++
			}
			days[day].Bytes += item.Size
			s.mu.Lock()
			s.job.Completed = summary.Count
			s.job.Current = path
			s.job.Bytes = summary.Bytes
			s.mu.Unlock()
			return nil
		})
	}
	for _, day := range days {
		summary.Days = append(summary.Days, *day)
	}
	sort.Slice(summary.Days, func(i, j int) bool { return summary.Days[i].Date < summary.Days[j].Date })
	sort.Slice(files, func(i, j int) bool {
		if files[i].Date == files[j].Date {
			return files[i].RelativePath < files[j].RelativePath
		}
		return files[i].Date > files[j].Date
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	if errors.Is(err, context.Canceled) {
		s.job.State = "cancelled"
		s.logs.Record("info", "scan", "scan.cancelled", "本地素材扫描已取消", jobLogFields(s.job))
		return
	}
	if err != nil {
		s.job.State = "failed"
		s.job.Errors = append(s.job.Errors, err.Error())
		logOperationResult(s.logs, "scan", "scan.failed", "本地素材扫描失败", err, jobLogFields(s.job))
		return
	}
	s.files, s.summary = files, summary
	s.job.State = "completed"
	s.logs.Record("info", "scan", "scan.completed", "本地素材扫描完成", map[string]string{"files": fmt.Sprint(summary.Count), "warnings": fmt.Sprint(len(summary.Warnings)), "bytes": fmt.Sprint(summary.Bytes)})
}

func matches(f MediaFile, q Filter) bool {
	return (q.Kind == "" || q.Kind == "all" || f.Kind == q.Kind) && (q.From == "" || f.Date[:10] >= q.From) && (q.To == "" || f.Date[:10] <= q.To) && (q.Search == "" || strings.Contains(strings.ToLower(f.RelativePath), strings.ToLower(q.Search)))
}
func (s *MediaService) QueryFiles(q Filter) FilePage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := FilePage{Files: []MediaFile{}}
	filtered := []MediaFile{}
	for _, f := range s.files {
		if matches(f, q) {
			filtered = append(filtered, f)
			result.Bytes += f.Size
		}
	}
	result.Total = len(filtered)
	if q.Sort == "size" {
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Size > filtered[j].Size })
	}
	if q.Sort == "name" {
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	}
	page := max(1, q.Page)
	start := min((page-1)*30, len(filtered))
	end := min(start+30, len(filtered))
	result.Files = filtered[start:end]
	return result
}
func (s *MediaService) MatchingIDs(q Filter) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := []string{}
	for _, f := range s.files {
		if matches(f, q) {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

func (s *MediaService) selectedFiles(ids []string) ([]MediaFile, error) {
	if len(ids) == 0 {
		return nil, errors.New("请先选择需要导出的文件")
	}
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	files := []MediaFile{}
	for _, f := range s.files {
		if set[f.ID] {
			files = append(files, f)
			delete(set, f.ID)
		}
	}
	if len(set) != 0 {
		return nil, errors.New("选择已失效，请重新扫描并选择文件")
	}
	return files, nil
}
func targetPath(f MediaFile) string {
	return filepath.Join(f.Kind, f.Date[:4], f.Date[5:7], f.Date[8:10], f.Name)
}

func (s *MediaService) PrepareExport(ids []string) (ExportPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	files, err := s.selectedFiles(ids)
	if err != nil {
		return ExportPlan{}, err
	}
	plan := ExportPlan{Count: len(files), Directories: []DayStat{}, Examples: []string{}}
	dirs := map[string]*DayStat{}
	for _, f := range files {
		plan.Bytes += f.Size
		dir := filepath.ToSlash(filepath.Dir(targetPath(f)))
		if dirs[dir] == nil {
			dirs[dir] = &DayStat{Date: dir}
		}
		dirs[dir].Bytes += f.Size
		if f.Kind == "photo" {
			dirs[dir].Photos++
		} else {
			dirs[dir].Videos++
		}
		if len(plan.Examples) < 6 {
			plan.Examples = append(plan.Examples, filepath.ToSlash(targetPath(f)))
		}
	}
	for _, d := range dirs {
		plan.Directories = append(plan.Directories, *d)
	}
	sort.Slice(plan.Directories, func(i, j int) bool { return plan.Directories[i].Date < plan.Directories[j].Date })
	return plan, nil
}

func overlaps(a, b string) bool {
	within := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func (s *MediaService) StartExport(destination string, ids []string) (JobStatus, error) {
	dest, err := canonicalDirectory(destination)
	if err != nil {
		logOperationResult(s.logs, "export", "export.start_failed", "无法开始本地素材导出", err, nil)
		return JobStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.State == "running" {
		return JobStatus{}, errors.New("请等待或取消当前任务")
	}
	if overlaps(s.summary.Path, dest) {
		return JobStatus{}, errors.New("分析文件夹与导出文件夹不能相同或互相包含，请选择独立的导出文件夹")
	}
	files, err := s.selectedFiles(ids)
	if err != nil {
		return JobStatus{}, err
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.Size
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.job = JobStatus{ID: time.Now().UnixNano(), Kind: "export", State: "running", Phase: "preparing", Total: len(files), TotalBytes: totalBytes, TotalWork: totalBytes * 2, StartedAt: time.Now().UnixMilli(), Destination: dest, Errors: []string{}}
	s.logs.Record("info", "export", "export.started", "开始导出本地素材", jobLogFields(s.job))
	go s.export(ctx, s.summary.Path, dest, files)
	return cloneJob(s.job), nil
}

func (s *MediaService) CancelJob() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *MediaService) export(ctx context.Context, source, destination string, files []MediaFile) {
	src, err := os.OpenRoot(source)
	if err != nil {
		s.finishExport("failed", err)
		return
	}
	defer src.Close()
	dst, err := os.OpenRoot(destination)
	if err != nil {
		s.finishExport("failed", err)
		return
	}
	defer dst.Close()
	for _, f := range files {
		if ctx.Err() != nil {
			s.finishExport("cancelled", nil)
			return
		}
		s.mu.Lock()
		s.job.Current = f.Name
		before := s.job.Bytes
		workBefore := s.job.WorkDone
		s.job.Phase = "copying"
		s.job.CurrentBytes = 0
		s.job.CurrentSize = f.Size
		s.mu.Unlock()
		err := copyMedia(ctx, src, dst, f, func(phase string, n int64) {
			s.mu.Lock()
			s.job.Phase = phase
			s.job.CurrentBytes = n
			if phase == "copying" {
				s.job.Bytes = before + n
				s.job.WorkDone = workBefore + n
			} else {
				s.job.WorkDone = workBefore + f.Size + n
			}
			s.mu.Unlock()
		})
		if errors.Is(err, context.Canceled) {
			s.mu.Lock()
			s.job.Bytes = before
			s.mu.Unlock()
			s.finishExport("cancelled", nil)
			return
		}
		s.mu.Lock()
		s.job.Completed++
		// 总进度包含复制与读回校验；失败文件处理结束后计入已处理工作量。
		s.job.WorkDone = workBefore + f.Size*2
		if err != nil {
			s.job.Failed++
			logOperationResult(s.logs, "export", "export.file_failed", "一个素材导出失败", err, map[string]string{"job_id": fmt.Sprint(s.job.ID)})
			if len(s.job.Errors) < 100 {
				s.job.Errors = append(s.job.Errors, f.RelativePath+"："+err.Error())
			}
			s.job.Bytes = before
		} else {
			s.job.Copied++
			s.job.Bytes = before + f.Size
		}
		s.mu.Unlock()
	}
	s.finishExport("completed", nil)
}
func (s *MediaService) finishExport(state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.State = state
	s.job.Phase = "finished"
	s.job.FinishedAt = time.Now().UnixMilli()
	s.cancel = nil
	if err != nil {
		s.job.Errors = append(s.job.Errors, err.Error())
	}
	level := "info"
	if state == "failed" || s.job.Failed > 0 {
		level = "error"
	}
	fields := jobLogFields(s.job)
	if err != nil {
		fields["error"] = err.Error()
	}
	s.logs.Record(level, "export", "export.finished", "本地素材导出已结束", fields)
	s.history = append([]JobStatus{cloneJob(s.job)}, s.history...)
	if len(s.history) > 20 {
		s.history = s.history[:20]
	}
}

func copyMedia(ctx context.Context, src, dst *os.Root, f MediaFile, progress func(string, int64)) error {
	info, err := src.Lstat(f.RelativePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("源文件已变为符号链接或特殊文件")
	}
	in, err := src.Open(f.RelativePath)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err = in.Stat()
	if err != nil {
		return err
	}
	if info.Size() != f.Size || info.ModTime().UnixNano() != f.Modified {
		return errors.New("源文件扫描后已变化，请重新扫描")
	}
	dir := filepath.Dir(targetPath(f))
	if err := dst.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// 独占创建保证同名文件永不被覆盖，且兼容不支持硬链接的 exFAT 存储卡。
	var out *os.File
	var target string
	for i := 0; i < 10000; i++ {
		name := f.Name
		if i > 0 {
			ext := filepath.Ext(name)
			name = strings.TrimSuffix(name, ext) + fmt.Sprintf("_%03d", i) + ext
		}
		target = filepath.Join(dir, name)
		out, err = dst.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err != nil {
		return errors.New("同名文件过多，无法分配文件名")
	}
	success := false
	defer func() {
		out.Close()
		if !success {
			_ = dst.Remove(target)
		}
	}()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var written int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				return err
			}
			_, _ = h.Write(buf[:n])
			written += int64(n)
			progress("copying", written)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if written != f.Size {
		return errors.New("源文件大小变化，已取消该文件")
	}
	endInfo, err := in.Stat()
	if err != nil {
		return err
	}
	if endInfo.ModTime().UnixNano() != f.Modified {
		return errors.New("源文件在复制期间变化")
	}
	progress("verifying", 0)
	if err = out.Sync(); err != nil {
		return err
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	verify := sha256.New()
	var verified int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := out.Read(buf)
		if n > 0 {
			_, _ = verify.Write(buf[:n])
			verified += int64(n)
			progress("verifying", verified)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(verify.Sum(nil)) {
		return errors.New("SHA-256 校验失败")
	}
	if err = out.Close(); err != nil {
		return err
	}
	// 时间戳保留失败不影响已经校验成功的文件内容。
	_ = dst.Chtimes(target, time.Now(), info.ModTime())
	success = true
	return nil
}
