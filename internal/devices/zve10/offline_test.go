package zve10

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/devices/wifihost"
	"cardatlas/internal/platform"
	"cardatlas/internal/upnpav"
)

// 离线用例的系统动作和凭据均在内存中，不访问 Wi-Fi、钥匙串或真实设备。
type memoryCredentials struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memoryCredentials) Has(key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.values[key]
	return ok, nil
}
func (m *memoryCredentials) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	if !ok {
		return "", platform.ErrCredentialMissing
	}
	return value, nil
}
func (m *memoryCredentials) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}
func (m *memoryCredentials) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

type sonySimulator struct {
	server                                     *httptest.Server
	photo, movie                               []byte
	failBrowse, empty, shortFirst, ignoreRange atomic.Bool
	starts, ends                               atomic.Int32
	mu                                         sync.Mutex
	ranges                                     []string
}

func testSOAP(result string, count int) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`+result+`</DIDL-Lite>`))
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>%s</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>1</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`, escaped.String(), count, count)
}
func newSonySimulator(t *testing.T) *sonySimulator {
	t.Helper()
	sim := &sonySimulator{movie: bytes.Repeat([]byte("离线 Sony 视频验证数据。"), 16384)}
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	sim.photo = photo.Bytes()
	sim.server = httptest.NewServer(http.HandlerFunc(sim.serve))
	t.Cleanup(sim.server.Close)
	return sim
}
func (sim *sonySimulator) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/desc.xml":
		if r.Method == http.MethodHead {
			w.WriteHeader(405)
			return
		}
		fmt.Fprint(w, `<root><device><friendlyName>ZV-E10</friendlyName><manufacturer>Sony Corporation</manufacturer><modelName>SonyImagingDevice</modelName><UDN>uuid:offline-sony</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/browse</controlURL></service><service><serviceType>urn:schemas-sony-com:service:XPushList:1</serviceType><controlURL>/transfer</controlURL></service></serviceList></device></root>`)
	case "/transfer":
		action := "X_TransferStart"
		if strings.Contains(r.Header.Get("SOAPACTION"), "X_TransferEnd") {
			action = "X_TransferEnd"
			sim.ends.Add(1)
		} else {
			sim.starts.Add(1)
		}
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="urn:schemas-sony-com:service:XPushList:1"/></s:Body></s:Envelope>`, action)
	case "/browse":
		if sim.failBrowse.Load() {
			http.Error(w, "离线模拟服务故障", 503)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "<ObjectID>PushRoot</ObjectID>") {
			w.WriteHeader(500)
			fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>701</errorCode><errorDescription>No Such Object</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		if sim.empty.Load() {
			fmt.Fprint(w, testSOAP("", 0))
			return
		}
		// 缩略图在前；RAW 标题实际对应转换后的 JPEG。地址故意不含文件扩展名。
		photo := fmt.Sprintf(`<item id="photo"><dc:title>DSC00001.ARW</dc:title><upnp:class>object.item.imageItem.photo</upnp:class><dc:date>2026-10-28T09:30:00</dc:date><res size="%d" protocolInfo="http-get:*:image/jpeg:DLNA.ORG_PN=JPEG_TN">/thumb/photo</res><res size="%d" resolution="6000x4000" protocolInfo="http-get:*:image/jpeg:DLNA.ORG_PN=JPEG_LRG;DLNA.ORG_CI=1">/file/photo?session=offline</res></item>`, len(sim.photo), len(sim.photo))
		movie := fmt.Sprintf(`<item id="movie"><dc:title>C0001.MP4</dc:title><upnp:class>object.item.videoItem.movie</upnp:class><res size="%d" duration="0:00:05.5" protocolInfo="http-get:*:video/mp4:DLNA.ORG_CI=0">/file/movie</res></item>`, len(sim.movie))
		fmt.Fprint(w, testSOAP(photo+movie, 2))
	case "/thumb/photo", "/file/photo":
		http.ServeContent(w, r, "photo.jpg", time.Time{}, bytes.NewReader(sim.photo))
	case "/file/movie":
		sim.mu.Lock()
		sim.ranges = append(sim.ranges, r.Header.Get("Range"))
		sim.mu.Unlock()
		if sim.shortFirst.Swap(false) {
			w.Header().Set("Content-Length", fmt.Sprint(len(sim.movie)))
			w.WriteHeader(200)
			_, _ = w.Write(sim.movie[:64<<10])
			return
		}
		if sim.ignoreRange.Load() {
			r.Header.Del("Range")
		}
		http.ServeContent(w, r, "movie.mp4", time.Time{}, bytes.NewReader(sim.movie))
	default:
		http.NotFound(w, r)
	}
}
func connectSimulator(t *testing.T, sim *sonySimulator) *Adapter {
	t.Helper()
	s, actions := newTestAdapter(t)
	s.host = wifihost.New(wifihost.Config{Name: "Sony ZV-E10", Timeout: 5 * time.Second}, wifihost.Actions{Scan: actions.Scan, Join: actions.Join, CurrentSSID: actions.CurrentSSID, WaitReady: actions.WaitReady, Restore: actions.Restore}, &memoryCredentials{values: map[string]string{}}, s.logs, func(ctx context.Context) (wifihost.MediaLink, error) {
		server, err := upnpav.Describe(ctx, sim.server.Client(), sim.server.URL+"/desc.xml")
		if err != nil {
			return nil, err
		}
		link := newUPNPLink(server, sim.server.Client())
		s.mu.Lock()
		s.media = link
		s.mu.Unlock()
		return link, nil
	})
	if err := s.Connect(deviceimport.ConnectionRequest{Action: "manual"}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, s, "connected")
	return s
}
func requestPreview(t *testing.T, s *Adapter, media deviceimport.Media, preview bool, rangeValue, session string) *httptest.ResponseRecorder {
	t.Helper()
	target := "http://localhost/device?id=" + media.ID + "&session=" + session
	if preview {
		target += "&preview=1"
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if rangeValue != "" {
		req.Header.Set("Range", rangeValue)
	}
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, req)
	return recorder
}

func TestOfflineSonyImportPreviewAndResume(t *testing.T) {
	for _, ignoreRange := range []bool{false, true} {
		t.Run(fmt.Sprintf("忽略Range=%t", ignoreRange), func(t *testing.T) {
			sim := newSonySimulator(t)
			s := connectSimulator(t, sim)
			state := s.Snapshot()
			if len(state.Media) != 2 {
				t.Fatalf("清单错误：%+v", state)
			}
			photo, movie := state.Media[0], state.Media[1]
			if photo.Name != "DSC00001.JPG" || photo.Size != int64(len(sim.photo)) {
				t.Fatalf("原片选择或扩展名错误：%+v", photo)
			}
			thumb := requestPreview(t, s, photo, false, "", state.SessionToken)
			if thumb.Code != 200 || !bytes.Equal(thumb.Body.Bytes(), sim.photo) {
				t.Fatalf("缩略图失败：%d", thumb.Code)
			}
			preview := requestPreview(t, s, movie, true, "bytes=20-39", state.SessionToken)
			if preview.Code != 206 || preview.Header().Get("Content-Type") != "video/mp4" || !bytes.Equal(preview.Body.Bytes(), sim.movie[20:40]) {
				t.Fatalf("无扩展名URL预览失败：%d %s", preview.Code, preview.Header())
			}
			invalid := requestPreview(t, s, photo, true, "", "过期会话")
			if invalid.Code != 404 {
				t.Fatal("旧会话可以读取素材")
			}
			sim.ignoreRange.Store(ignoreRange)
			sim.shortFirst.Store(true)
			root := t.TempDir()
			result, err := s.Download(context.Background(), state.DeviceID, movie, root, "存储卡/C0001.MP4", nil)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(result.Path)
			if err != nil || !bytes.Equal(data, sim.movie) {
				t.Fatalf("文件内容错误：%v", err)
			}
			sum := sha256.Sum256(sim.movie)
			if result.SHA256 != hex.EncodeToString(sum[:]) || result.Bytes != int64(len(data)) {
				t.Fatal("文件长度或SHA256错误")
			}
			photoResult, err := s.Download(context.Background(), state.DeviceID, photo, root, "存储卡/DSC00001.JPG", nil)
			if err != nil {
				t.Fatal(err)
			}
			photoData, err := os.ReadFile(photoResult.Path)
			if err != nil || !bytes.Equal(photoData, sim.photo) || !strings.Contains(photoResult.Warning, "转换版本") {
				t.Fatalf("JPEG 转换版本的文件内容或说明错误：%v %s", err, photoResult.Warning)
			}
			sim.mu.Lock()
			ranges := append([]string{}, sim.ranges...)
			sim.mu.Unlock()
			if len(ranges) < 3 || ranges[len(ranges)-1] != "bytes=65536-" {
				t.Fatalf("中断后没有使用实际偏移续传：%v", ranges)
			}
			if _, err := os.Stat(filepath.Join(root, "存储卡/C0001.MP4.part")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("完成后遗留临时文件")
			}
			changed := movie
			changed.Size++
			if _, err := s.Download(context.Background(), state.DeviceID, changed, root, "changed.MP4", nil); err == nil {
				t.Fatal("接受了不一致的素材快照")
			}
			if err := s.Disconnect(); err != nil {
				t.Fatal(err)
			}
			if s.currentMedia() != nil || sim.starts.Load() != 1 || sim.ends.Load() != 1 {
				t.Fatalf("会话未释放或传输通知次数不符：%d/%d", sim.starts.Load(), sim.ends.Load())
			}
			s.media.Close() // 重复关闭必须安全。
		})
	}
}

func TestOfflineRefreshPreservesListOnFailureAndAcceptsEmpty(t *testing.T) {
	sim := newSonySimulator(t)
	s := connectSimulator(t, sim)
	before := s.Snapshot()
	sim.failBrowse.Store(true)
	if err := s.Refresh(); err == nil {
		t.Fatal("目录故障应返回错误")
	}
	after := s.Snapshot()
	if after.Revision != before.Revision || len(after.Media) != 2 {
		t.Fatal("刷新失败覆盖了原清单")
	}
	sim.failBrowse.Store(false)
	sim.empty.Store(true)
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Media) != 0 {
		t.Fatal("有效空清单未替换旧素材")
	}
}

func TestStableIdentityFoldersAndConvertedNames(t *testing.T) {
	storage := upnpav.Storage{ID: "0", Name: "存储卡"}
	item := upnpav.Item{ID: "same-object", Title: "DSC00001.ARW", Class: "object.item.imageItem.photo", MIMEType: "image/jpeg", Res: "http://10.0.0.1:123/file?token=old", Folders: []string{"DCIM", "100MSDCF"}}
	a, ok := normalize(item, storage)
	if !ok {
		t.Fatal("有效素材被拒绝")
	}
	item.Res = "http://10.0.0.1:456/file?token=new"
	b, _ := normalize(item, storage)
	if a.ID != b.ID || a.Path != "存储卡/DCIM/100MSDCF/DSC00001.JPG" {
		t.Fatalf("身份或目录不稳定：%+v %+v", a, b)
	}
	item.Folders = []string{".."}
	if _, ok := normalize(item, storage); ok {
		t.Fatal("目录穿越未被拒绝")
	}
	sim := newSonySimulator(t)
	if !reachable(context.Background(), sim.server.Client(), sim.server.URL+"/desc.xml") {
		t.Fatal("不支持 HEAD 的设备未通过 GET 探测")
	}
}

func TestModelFilterAndQRPasswordEscapes(t *testing.T) {
	for _, tc := range []struct {
		name, manufacturer string
		want               bool
	}{
		{"ZV-E10", "Sony Corporation", true}, {"ZV-E10 II", "Sony Corporation", false}, {"ZV-E10M2", "Sony Corporation", false}, {"ZV-E10", "Other", false}, {"Other camera", "Sony Corporation", false},
	} {
		if got := isZVE10(&upnpav.Server{FriendlyName: tc.name, Manufacturer: tc.manufacturer}); got != tc.want {
			t.Errorf("机型过滤错误：%+v", tc)
		}
	}
	for _, pass := range []string{`a\;b`, `"pass,'`, `pass\`, `a\\;b`, ` space `} {
		encoded := strings.NewReplacer(`\`, `\\`, `;`, `\;`).Replace(pass)
		info, err := parseConnectionInfo("WIFI:S:DIRECT-test;P:" + encoded + ";;")
		if err != nil || info.password != pass {
			t.Fatalf("密码字符发生变化：%q != %q：%v", info.password, pass, err)
		}
	}
	for _, duration := range []string{"0:00:NaN", "0:00:+Inf", "0:60:00", "0:00:60", "9999999999999999:00:00"} {
		if durationSeconds(duration) != 0 {
			t.Fatalf("无效时长被接受：%s", duration)
		}
	}
}
