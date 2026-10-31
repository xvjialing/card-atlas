// Package zve10 实现 Sony ZV-E10 的无线素材导入适配。
// 相机在“发送到智能手机”等待状态显示连接二维码，读取后经用户确认加入热点。
// 实际可传输内容与质量由相机设置及媒体服务发布的资源决定。
// 协议适配性标记为待实机验证。
package zve10

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
	"cardatlas/internal/dji/transfer"
	"cardatlas/internal/platform"
	"cardatlas/internal/upnpav"
)

var sonyPeers = []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("192.168.122.1")}

// Adapter 通过通用 Wi-Fi 会话驱动 UPnP 媒体服务。
type Adapter struct {
	mu    sync.Mutex
	media *upnpLink
	host  *wifihost.Host
	logs  *diagnostics.Store
}

func NewAdapter() *Adapter {
	base := os.Getenv("CARDATLAS_DEVICE_DATA")
	if base == "" {
		base, _ = os.UserConfigDir()
		base = filepath.Join(base, "CardAtlas", "devices")
	}
	s := &Adapter{logs: diagnostics.Shared(base)}
	s.host = wifihost.New(
		wifihost.Config{Name: "Sony ZV-E10", HotKeywords: []string{"sony", "direct-", "zv"}, Timeout: 4 * time.Minute},
		wifihost.Actions{
			Scan:        platform.ScanWiFi,
			Join:        platform.JoinWiFi,
			CurrentSSID: platform.CurrentSSID,
			WaitReady: func(ctx context.Context, expected string) error {
				return platform.WaitDeviceWiFiForPeers(ctx, expected, sonyPeers...)
			},
			Restore: platform.RestoreWiFi,
		},
		platform.Credentials{},
		s.logs,
		s.openLink,
	)
	return s
}

func (s *Adapter) Descriptor() deviceimport.Descriptor {
	return deviceimport.Descriptor{ID: "sony-zve10", Name: "Sony ZV-E10", Brand: "Sony", Transport: "Wi-Fi 热点传输", Available: runtime.GOOS == "darwin", Compatibility: "已接入，待实机验证", Storages: []deviceimport.Storage{{ID: "0", Name: "存储卡"}}}
}

func (s *Adapter) Snapshot() deviceimport.Session {
	st := s.host.State()
	out := deviceimport.Session{SessionToken: s.host.SessionToken(), DeviceID: s.host.DeviceID(), DeviceName: st.DeviceName, Phase: st.Phase, Message: st.Message, Warning: st.Warning, SSID: st.SSID, Busy: st.Busy, Revision: st.Revision, WiFiNetworks: st.WiFiNetworks, Media: st.Media}
	if out.WiFiNetworks == nil {
		out.WiFiNetworks = []deviceimport.Network{}
	}
	if out.Media == nil {
		out.Media = []deviceimport.Media{}
	}
	return out
}

func (s *Adapter) Discover() ([]deviceimport.Device, error) {
	return nil, errors.New("Sony ZV-E10 不提供蓝牙发现，请在连接窗口导入二维码照片或扫描相机热点")
}

func (s *Adapter) Connect(r deviceimport.ConnectionRequest) error {
	switch r.Action {
	case "scan_wifi":
		return s.host.ScanWiFi()
	case "join_wifi":
		return s.host.JoinWiFi(r.SSID, r.Password, r.UseSaved)
	case "forget_password":
		return s.host.ForgetPassword(r.SSID)
	case "manual":
		return s.host.ManualConnect()
	case "import_qr":
		return s.importQR(r.Photo)
	default:
		return errors.New("此设备不支持该连接操作")
	}
}

func (s *Adapter) Disconnect() error { return s.host.Disconnect() }
func (s *Adapter) Refresh() error    { return s.host.Refresh() }

// importQR 解码二维码照片并记录连接信息，随后扫描热点供用户确认；不会自动切换 Wi-Fi。
func (s *Adapter) importQR(photo string) error {
	raw := photo
	if content, found := strings.CutPrefix(raw, "data:"); found {
		if _, payload, ok := strings.Cut(content, ","); ok {
			raw = payload
		} else {
			raw = content
		}
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return errors.New("二维码照片无法读取，请重新选择图片")
	}
	text, err := decodeQR(data)
	if err != nil {
		return err
	}
	info, err := parseConnectionInfo(text)
	if err != nil {
		return err
	}
	s.logs.RegisterSecrets(info.ssid, info.fragment, info.password)
	s.host.Provision(info.ssid, info.password)
	// 部分机型热点后缀与二维码字段不完全一致，片段匹配作为兜底。
	if info.fragment != "" && info.fragment != info.ssid {
		s.host.ProvisionFragment(info.fragment, info.password)
	}
	return s.host.ScanWiFi()
}

// openLink 发现相机媒体服务器并建立浏览会话。
func (s *Adapter) openLink(ctx context.Context) (wifihost.MediaLink, error) {
	snapshot := platform.CurrentWiFi()
	expected := s.host.State().SSID
	if expected == "" {
		expected = snapshot.SSID
	}
	ip, subnet, err := platform.DeviceWiFiAddressForPeers(snapshot, expected, sonyPeers...)
	if err != nil {
		return nil, err
	}
	diagnostics.From(ctx).Log("info", "wifi", "wifi.address_ready", "已取得相机链路地址，开始发现素材服务", map[string]string{"interface": snapshot.Interface, "client_ipv4": ip.String(), "subnet": subnet.String()})
	control, err := platform.WiFiSocketControl(snapshot, ip)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IP(ip.AsSlice())}, Control: control}
	// 一代热点已由用户选择时先尝试实际素材协议，并以 PTP 设备信息再次核验型号。
	// 固件 2.01 的旧描述服务可能尚未就绪，不能让它成为 PTP/IP 的必经前置条件。
	if isZVE10Hotspot(expected) {
		for _, peer := range sonyPeers {
			if !subnet.Contains(peer) {
				continue
			}
			for attempt := 0; attempt < 3; attempt++ {
				link, ptpErr := openPTPMedia(ctx, &upnpav.Server{Location: "http://" + net.JoinHostPort(peer.String(), "64321") + "/DmsDescPush.xml"}, dialer.DialContext)
				if ptpErr == nil {
					s.mu.Lock()
					s.media = link
					s.mu.Unlock()
					return link, nil
				}
				if errors.Is(ptpErr, errPTPModel) {
					return nil, ptpErr
				}
				var networkErr *net.OpError
				if !errors.As(ptpErr, &networkErr) {
					return nil, ptpErr
				}
				if attempt == 2 {
					diagnostics.From(ctx).Log("info", "ptp", "ptp.direct_unavailable", "PTP/IP 暂不可用，检查相机发布的旧素材服务", map[string]string{"error": ptpErr.Error()})
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
	}
	client := transfer.ClientWithDialer(dialer)
	keepHTTP := false
	defer func() {
		if !keepHTTP {
			client.CloseIdleConnections()
		}
	}()
	var fallback []string
	for _, peer := range sonyPeers {
		if subnet.Contains(peer) {
			fallback = append(fallback, "http://"+net.JoinHostPort(peer.String(), "64321")+"/DmsDescPush.xml")
		}
	}
	config := upnpav.Discovery{Subnet: subnet, LocalIP: ip, Control: control, Accept: func(server *upnpav.Server) bool {
		return isZVE10OnHotspot(server, expected)
	}, FallbackLocations: fallback}
	var server *upnpav.Server
	for attempt := 0; attempt < 3; attempt++ {
		server, err = upnpav.DiscoverOn(ctx, client, config)
		if err == nil {
			break
		}
		if attempt == 2 || (!errors.Is(err, syscall.EHOSTUNREACH) && !errors.Is(err, syscall.ENETUNREACH)) {
			break
		}
		diagnostics.From(ctx).Log("info", "wifi", "wifi.route_wait", "热点地址已分配，等待 Wi-Fi 路由就绪后重试素材发现", nil)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, err
	}
	ptpSupported, err := server.PTPMediaSupported(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("读取相机传输能力失败：%w", err)
	}
	var link *upnpLink
	if ptpSupported {
		link, err = openPTPMedia(ctx, server, dialer.DialContext)
		client.CloseIdleConnections()
		if err != nil {
			return nil, err
		}
	} else {
		keepHTTP = true
		link = newUPNPLink(server, client)
	}
	s.mu.Lock()
	s.media = link
	s.mu.Unlock()
	return link, nil
}

// 明确标识机型的描述仍按一代型号筛选。
func isZVE10(server *upnpav.Server) bool {
	name := strings.ToUpper(strings.TrimSpace(server.FriendlyName + " " + server.ModelName))
	return strings.Contains(strings.ToLower(server.Manufacturer), "sony") &&
		strings.Contains(name, "ZV-E10") && !strings.Contains(name, "ZV-E10M2") && !strings.Contains(name, "ZV-E10 II")
}

// 一代实机也会仅发布 Sony 的通用名称；此时同时要求已选热点明确标识一代机型、厂家和两项素材服务。
// 不将所有 Sony 通用服务器直接视为 ZV-E10，同机端点校验仍由 Describe 执行。
func isZVE10OnHotspot(server *upnpav.Server, ssid string) bool {
	if isZVE10(server) {
		return true
	}
	if !isZVE10Hotspot(ssid) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(server.Manufacturer), "Sony Corporation") &&
		strings.EqualFold(strings.TrimSpace(server.FriendlyName), "SonyDigitalMediaServer") &&
		strings.EqualFold(strings.TrimSpace(server.ModelName), "SonyImagingDevice") &&
		server.ControlURL != "" && server.PushControlURL != ""
}

func isZVE10Hotspot(ssid string) bool {
	network, model, ok := strings.Cut(strings.ToUpper(ssid), ":")
	return ok && strings.HasPrefix(network, "DIRECT-") && len(network) > len("DIRECT-") && model == "ZV-E10"
}

func (s *Adapter) currentMedia() *upnpLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media == nil || !s.media.Alive() {
		return nil
	}
	return s.media
}

func (s *Adapter) Download(ctx context.Context, device string, m deviceimport.Media, root, relative string, progress func(deviceimport.Progress)) (deviceimport.Result, error) {
	link := s.currentMedia()
	if link == nil || s.host.DeviceID() != device {
		return deviceimport.Result{}, errors.New("请连接任务所属的原设备后重试")
	}
	item, ok := link.Item(m.ID)
	if !ok {
		return deviceimport.Result{}, errors.New("素材已变化，请刷新清单后重新选择")
	}
	if err := sameCamera(item.Res, link.Host()); err != nil {
		return deviceimport.Result{}, err
	}
	actual, ok := link.Normalized(m.ID)
	if !ok || actual != m {
		return deviceimport.Result{}, errors.New("素材快照已变化，请刷新清单后重新选择")
	}
	ctx, cancel := link.requestContext(ctx)
	defer cancel()
	cameraMedia := cameraMedia(m)
	r, err := transfer.DownloadURL(diagnostics.With(ctx, s.logs, nil), link.client, item.Res, device, root, relative, cameraMedia, func(p transfer.Progress) {
		if progress != nil {
			progress(deviceimport.Progress{Bytes: p.Bytes, Total: p.Total, Speed: p.Speed, Phase: p.Phase})
		}
	})
	if item.Converted && err == nil {
		if r.Warning != "" {
			r.Warning += "；"
		}
		r.Warning += "相机提供的是转换版本；如需原始文件，请核对相机发送设置或从存储卡导入"
	}
	return deviceimport.Result{Path: r.Path, SHA256: r.SHA256, Bytes: r.Bytes, Warning: r.Warning}, err
}

// cameraMedia 把通用素材还原为传输层需要的快照；字段与导入规划一致，保证续传核对稳定。
func cameraMedia(m deviceimport.Media) camera.Media {
	return camera.Media{ID: m.ID, Name: m.Name, Path: m.Path, Size: m.Size, Duration: m.Duration, Kind: m.Kind, Date: m.Date}
}

// upnpLink 维护相机媒体服务会话：素材映射、地址校验与轻量保活探测。
type upnpLink struct {
	server    *upnpav.Server
	client    *http.Client
	mu        sync.Mutex
	items     map[string]upnpav.Item
	medias    map[string]deviceimport.Media
	lastOK    time.Time
	stop      chan struct{}
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	listMu    sync.Mutex
	started   bool
	closed    bool
	ptp       *ptpMedia
}

func newUPNPLink(server *upnpav.Server, client *http.Client) *upnpLink {
	return newMediaLink(server, client, nil)
}

func newMediaLink(server *upnpav.Server, client *http.Client, ptp *ptpMedia) *upnpLink {
	guarded := *client
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("不允许相机素材重定向") }
	l := &upnpLink{server: server, client: &guarded, ptp: ptp, items: map[string]upnpav.Item{}, medias: map[string]deviceimport.Media{}, stop: make(chan struct{}), done: make(chan struct{})}
	l.lastOK = time.Now()
	// 连接操作的 ctx 在成功返回时即取消，会话有独立生命周期。
	l.ctx, l.cancel = context.WithCancel(context.Background())
	go l.probe()
	return l
}

// 请求同时服从调用方和设备会话的取消；断开后不继续读取旧设备。
func (l *upnpLink) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(l.ctx, cancel)
	if l.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// probe 周期性访问设备描述地址；持续失败会让 Alive 判定为中断。
func (l *upnpLink) probe() {
	defer close(l.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(l.ctx, 1500*time.Millisecond)
			ok := false
			if l.ptp != nil {
				ok = l.ptp.alive()
			} else {
				ok = reachable(ctx, l.client, l.server.Location)
			}
			cancel()
			if ok {
				l.mu.Lock()
				l.lastOK = time.Now()
				l.mu.Unlock()
			}
		}
	}
}

func reachable(ctx context.Context, client *http.Client, location string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (l *upnpLink) Alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.closed && ((l.ptp != nil && l.ptp.alive()) || (l.ptp == nil && time.Since(l.lastOK) < 15*time.Second))
}

func (l *upnpLink) Close() {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.cancel()
		close(l.stop)
		<-l.done
		l.listMu.Lock()
		defer l.listMu.Unlock()
		if l.ptp != nil {
			l.ptp.close()
		} else if l.started {
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			_ = l.server.EndTransfer(ctx, l.client)
		}
		l.client.CloseIdleConnections()
	})
}

func (l *upnpLink) Host() string {
	if base, err := url.Parse(l.server.Location); err == nil {
		return base.Host
	}
	return ""
}

func (l *upnpLink) Item(id string) (upnpav.Item, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.items[id]
	return item, ok
}

// Normalized 返回 List 时的标准化素材快照；预览与缩略图据此判断类型与大小。
func (l *upnpLink) Normalized(id string) (deviceimport.Media, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.medias[id]
	return m, ok
}

func (l *upnpLink) List(ctx context.Context) ([]deviceimport.Media, string, error) {
	l.listMu.Lock()
	defer l.listMu.Unlock()
	ctx, cancel := l.requestContext(ctx)
	defer cancel()
	if err := l.ctx.Err(); err != nil {
		return nil, "", err
	}
	if l.ptp != nil {
		return l.listPTP(ctx)
	}
	if !l.started {
		if err := l.server.StartTransfer(ctx, l.client); err != nil {
			return nil, "", err
		}
		l.started = true
	}
	storages, err := l.server.BrowseRoots(ctx, l.client, "PushRoot", "PhotoRoot", "0")
	if err != nil {
		return nil, "", err
	}
	out := []deviceimport.Media{}
	items := map[string]upnpav.Item{}
	medias := map[string]deviceimport.Media{}
	baseURL := l.server.BaseURL
	if baseURL == "" {
		baseURL = l.server.Location
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, "", errors.New("相机资源基地址无效")
	}
	for _, storage := range storages {
		for _, item := range storage.Items {
			resolved, err := base.Parse(item.Res)
			if err != nil {
				return nil, "", errors.New("素材资源地址无效")
			}
			item.Res = resolved.String()
			if err := sameCamera(item.Res, l.Host()); err != nil {
				return nil, "", err
			}
			if item.Thumbnail != "" {
				thumb, err := base.Parse(item.Thumbnail)
				if err == nil && sameCamera(thumb.String(), l.Host()) == nil {
					item.Thumbnail = thumb.String()
				} else {
					item.Thumbnail = ""
				}
			}
			m, ok := normalize(item, storage)
			if !ok {
				continue
			}
			if _, dup := medias[m.ID]; dup {
				continue
			}
			items[m.ID] = item
			medias[m.ID] = m
			out = append(out, m)
		}
	}
	l.mu.Lock()
	l.items = items
	l.medias = medias
	l.lastOK = time.Now()
	l.mu.Unlock()
	return out, "", nil
}

// normalize 把 UPnP 条目转换为通用素材；来源信息不安全或类型不明的条目会被跳过。
func normalize(item upnpav.Item, storage upnpav.Storage) (deviceimport.Media, bool) {
	if strings.TrimSpace(item.Res) == "" {
		return deviceimport.Media{}, false
	}
	title := safeName(item.Title, item.Res)
	// 某些固件以 RAW 标题发布转换后的 JPEG，落盘扩展名必须符合实际资源类型。
	if title != "" && item.MIMEType == "image/jpeg" && strings.ToLower(path.Ext(title)) != ".jpg" && strings.ToLower(path.Ext(title)) != ".jpeg" {
		title = strings.TrimSuffix(title, path.Ext(title)) + ".JPG"
	}
	if title == "" {
		return deviceimport.Media{}, false
	}
	kind := classify(item.Class, title)
	if kind == "" {
		return deviceimport.Media{}, false
	}
	storageID := storage.ID
	if !filepath.IsLocal(storageID) || strings.ContainsAny(storageID, "/\\\x00") {
		return deviceimport.Media{}, false
	}
	storageName := storage.Name
	if storageName == "" {
		storageName = "存储卡"
	}
	// source 布局保留媒体服务的逻辑目录，不能推断卡上的真实文件系统路径。
	parts := []string{safeElement(storageName)}
	for _, folder := range item.Folders {
		if folder != safeElement(folder) || folder == "." || folder == ".." {
			return deviceimport.Media{}, false
		}
		parts = append(parts, folder)
	}
	itemPath := path.Join(append(parts, title)...)
	if !filepath.IsLocal(itemPath) || strings.ContainsAny(itemPath, "\\\x00") {
		return deviceimport.Media{}, false
	}
	identity := item.ID
	if identity == "" {
		identity = itemPath
	}
	// 对象标识不依赖带会话参数、端口或临时令牌的下载地址。
	sum := sha256.Sum256([]byte(storageID + "\x00" + identity))
	date := normalizeDate(item.Date)
	dateSource := ""
	if date != "" {
		dateSource = "相机媒体服务"
	}
	return deviceimport.Media{ID: hex.EncodeToString(sum[:12]), Name: title, Path: itemPath, StorageID: storageID, StorageName: storageName, Size: max(0, item.Size), SizeEstimated: item.Size <= 0, Duration: durationSeconds(item.Duration), Kind: kind, Date: date, DateSource: dateSource, Thumbnail: item.Thumbnail != ""}, true
}

// safeName 优先使用条目标题；标题缺失或包含路径成分时回退到资源地址的最后一段。
func safeName(title, res string) string {
	name := strings.TrimSpace(title)
	if name == "" || name == "." || name == ".." || name != path.Base(name) || strings.ContainsAny(name, "\\/\x00\r\n") {
		if res == "" {
			return ""
		}
		if parsed, err := url.Parse(res); err == nil {
			name = path.Base(parsed.Path)
		}
	}
	if name == "" || name == "." || name == ".." || name == "/" || name != path.Base(name) || strings.ContainsAny(name, "\\/\x00\r\n") {
		return ""
	}
	return name
}

// safeElement 限制目录名只能作为单个路径元素使用。
func safeElement(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name != path.Base(name) || strings.ContainsAny(name, "\\/\x00") {
		return "相机素材"
	}
	return name
}

// sameCamera 校验素材资源地址确实指向已连接的相机；资源地址来自相机自身响应，仍需确认。
func sameCamera(res, serverHost string) error {
	parsed, err := url.Parse(res)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("素材资源地址无效")
	}
	serverIP := hostIP(serverHost)
	resIP := hostIP(parsed.Host)
	if serverIP == "" || resIP == "" || serverIP != resIP {
		return errors.New("素材资源地址与相机不一致，已停止下载")
	}
	return nil
}

func hostIP(host string) string {
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	addr, err := netip.ParseAddr(name)
	if err != nil {
		return ""
	}
	return addr.String()
}

func (s *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("preview") == "1" {
		s.servePreview(w, r)
	} else {
		s.serveThumbnail(w, r)
	}
}
