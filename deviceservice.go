package main

import (
	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/action6"
	"cardatlas/internal/devices/mini5pro"
	"cardatlas/internal/devices/zve10"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// DeviceService 只暴露通用设备与导入操作；硬件协议留在适配器内。
type DeviceService struct {
	manager  *deviceimport.Manager
	action6  *action6.Adapter
	mini5pro *mini5pro.Adapter
	zve10    *zve10.Adapter
	app      *application.App
}

func NewDeviceService() *DeviceService {
	base := os.Getenv("CARDATLAS_DEVICE_DATA")
	if base == "" {
		base, _ = os.UserConfigDir()
		base = filepath.Join(base, "CardAtlas", "devices")
	}
	p := action6.NewAdapter()
	m := mini5pro.NewAdapter()
	z := zve10.NewAdapter()
	return &DeviceService{manager: deviceimport.NewManager(base, p, m, z), action6: p, mini5pro: m, zve10: z}
}
func (s *DeviceService) GetState() deviceimport.State              { return s.manager.GetState() }
func (s *DeviceService) GetLibrary() ([]deviceimport.Media, error) { return s.manager.Library() }
func (s *DeviceService) SelectProvider(id string) error            { return s.manager.SelectProvider(id) }
func (s *DeviceService) Discover() ([]deviceimport.Device, error)  { return s.manager.Discover() }
func (s *DeviceService) Connect(request deviceimport.ConnectionRequest) error {
	return s.manager.Connect(request)
}
func (s *DeviceService) Disconnect() error { return s.manager.Disconnect() }
func (s *DeviceService) Refresh() error    { return s.manager.Refresh() }
func (s *DeviceService) PrepareImport(request deviceimport.ImportRequest) (deviceimport.Plan, error) {
	return s.manager.Prepare(request)
}
func (s *DeviceService) StartImport(planID string) error { return s.manager.Start(planID) }
func (s *DeviceService) PauseImports()                   { s.manager.Pause() }
func (s *DeviceService) ResumeImports() (int, error)     { return s.manager.Resume() }
func (s *DeviceService) RetryImport(id string) error     { return s.manager.Retry(id) }
func (s *DeviceService) ChooseDestination() (string, error) {
	if s.app == nil {
		return "", errors.New("请在桌面客户端中选择导入位置")
	}
	return s.app.Dialog.OpenFile().SetTitle("选择设备素材导入位置").CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).PromptForSingleSelection()
}
func (s *DeviceService) RevealImport(id string) error {
	p, e := s.manager.CompletedPath(id)
	if e != nil {
		return e
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("/usr/bin/open", "-R", p).Run()
	case "windows":
		return exec.Command("explorer.exe", "/select,", p).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(p)).Start()
	}
}
func (s *DeviceService) ServiceShutdown() error { return s.manager.Disconnect() }
func (s *DeviceService) serveMedia(w http.ResponseWriter, r *http.Request) {
	// 预览路由按设备分发；前端只提交素材 ID 与会话令牌，不提交设备地址或路径。
	switch r.URL.Query().Get("provider") {
	case "dji-action6":
		s.action6.ServeHTTP(w, r)
	case "dji-mini5pro":
		s.mini5pro.ServeHTTP(w, r)
	case "sony-zve10":
		s.zve10.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
