package main

import (
	"cardatlas/internal/diagnostics"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// DiagnosticsService 只访问应用自己的离线日志，导出由用户选择的本地目录接收。
type DiagnosticsService struct {
	app          *application.App
	logs         *diagnostics.Store
	exportMu     sync.Mutex
	clientMu     sync.Mutex
	clientWindow time.Time
	clientCount  int
}

func NewDiagnosticsService() *DiagnosticsService {
	return &DiagnosticsService{logs: diagnostics.Shared(diagnostics.DefaultBase())}
}

func (s *DiagnosticsService) GetInfo() diagnostics.Info { return s.logs.Info() }
func (s *DiagnosticsService) GetLogs(query diagnostics.Query) (diagnostics.Page, error) {
	return s.logs.Read(query)
}

// 只修改详细程度，保留既有的留存策略。
func (s *DiagnosticsService) SetLevel(level string) (diagnostics.Info, error) {
	settings := s.logs.Info().Settings
	settings.Level = level
	if err := s.logs.SaveSettings(settings); err != nil {
		return s.logs.Info(), err
	}
	s.logs.Record("info", "app", "logging.level_changed", "日志记录级别已更新", map[string]string{"level": level})
	return s.logs.Info(), nil
}

func (s *DiagnosticsService) ExportLogs() (string, error) {
	if s.app == nil {
		return "", errors.New("请在桌面客户端中导出诊断包")
	}
	if !s.exportMu.TryLock() {
		return "", errors.New("正在导出诊断包，请稍候")
	}
	defer s.exportMu.Unlock()
	dir, err := s.app.Dialog.OpenFile().SetTitle("选择诊断包保存文件夹").CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || dir == "" {
		return "", err
	}
	path, err := s.exportToDirectory(dir)
	if err != nil {
		s.logs.Record("error", "app", "logging.export_failed", "诊断包导出失败", map[string]string{"error": err.Error()})
		return "", err
	}
	s.logs.Record("info", "app", "logging.exported", "离线诊断包已保存", nil)
	return path, nil
}

func (s *DiagnosticsService) exportToDirectory(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("请选择有效的本地文件夹")
	}
	// 独占新文件，不覆盖已存在的诊断包；失败或写入中断时移除不完整文件。
	f, err := os.CreateTemp(dir, "CardAtlas-诊断-"+time.Now().Format("20060102-150405")+"-*.zip")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			_ = os.Remove(f.Name())
		}
	}()
	if err = s.logs.Export(f); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	complete = true
	return f.Name(), nil
}

func (s *DiagnosticsService) OpenLogDirectory() error {
	if s.app == nil {
		return errors.New("请在桌面客户端中打开日志文件夹")
	}
	dir := s.logs.Info().Directory
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("/usr/bin/open", dir).Run()
	case "windows":
		return exec.Command("explorer.exe", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// 界面未捕获异常只记录文字摘要，限流避免异常循环写满磁盘；不接收页面、表单或素材内容。
func (s *DiagnosticsService) ReportClientError(kind, message string) {
	if kind != "error" && kind != "unhandledrejection" {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	s.clientMu.Lock()
	if time.Since(s.clientWindow) > time.Minute {
		s.clientWindow = time.Now()
		s.clientCount = 0
	}
	if s.clientCount >= 20 {
		s.clientMu.Unlock()
		return
	}
	s.clientCount++
	s.clientMu.Unlock()
	s.logs.Record("error", "frontend", "frontend."+kind, "界面运行异常", map[string]string{"error": message})
}

func logOperationResult(logs *diagnostics.Store, component, event, message string, err error, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	level := "info"
	if err != nil {
		level = "error"
		fields["error"] = diagnosticError(err)
	}
	logs.Record(level, component, event, message, fields)
}

// 文件系统错误保留操作和系统原因，去掉可能是相对路径的素材文件名。
func diagnosticError(err error) string {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Op + ": " + pathError.Err.Error()
	}
	return err.Error()
}

func jobLogFields(j JobStatus) map[string]string {
	return map[string]string{"job_id": fmt.Sprint(j.ID), "status": j.State, "total": fmt.Sprint(j.Total), "completed": fmt.Sprint(j.Completed), "failed": fmt.Sprint(j.Failed)}
}
