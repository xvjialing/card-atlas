package upnpav

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResourceSelectionUsesQualityAndMatchingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name     string
		res      []didlRes
		want     string
		size     int64
		duration string
	}{
		{"缩略图在前", []didlRes{
			{URL: "/thumb", Size: 512, ProtocolInfo: "http-get:*:image/jpeg:DLNA.ORG_PN=JPEG_TN;DLNA.ORG_CI=0", Resolution: "160x120"},
			{URL: "/original", Size: 4000000, ProtocolInfo: "http-get:*:image/jpeg:DLNA.ORG_PN=JPEG_LRG;DLNA.ORG_CI=0", Resolution: "6000x4000"},
		}, "/original", 4000000, ""},
		{"转码资源较大", []didlRes{
			{URL: "/converted", Size: 200, ProtocolInfo: "http-get:*:video/mp4:DLNA.ORG_CI=1", Duration: "0:00:05"},
			{URL: "/original", Size: 100, ProtocolInfo: "http-get:*:video/mp4:DLNA.ORG_CI=0", Duration: "0:00:10"},
		}, "/original", 100, "0:00:10"},
		{"无大小优先高分辨率", []didlRes{
			{URL: "/small", Resolution: "640x480", ProtocolInfo: "http-get:*:image/jpeg:*"},
			{URL: "/large", Resolution: "6000x4000", ProtocolInfo: "http-get:*:image/jpeg:*"},
		}, "/large", 0, ""},
		{"显式RAW原片", []didlRes{
			{URL: "/converted", Size: 2000000, ProtocolInfo: "http-get:*:image/jpeg:DLNA.ORG_CI=1"},
			{URL: "/raw", Size: 24000000, ProtocolInfo: "http-get:*:image/x-sony-arw:DLNA.ORG_CI=0"},
		}, "/raw", 24000000, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := selectResources(didlItem{Res: tc.res})
			if got.URL != tc.want || got.Size != tc.size || got.Duration != tc.duration {
				t.Fatalf("资源与元数据不匹配：%+v", got)
			}
		})
	}
	_, thumb := selectResources(didlItem{Res: []didlRes{{URL: "/movie", Size: 100, ProtocolInfo: "http-get:*:video/mp4:*"}, {URL: "/proxy", Size: 50, ProtocolInfo: "http-get:*:video/mp4:*"}}})
	if thumb != "" {
		t.Fatal("不能把视频代理当作缩略图")
	}
}

func didl(entries string) string {
	return `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` + entries + `</DIDL-Lite>`
}
func container(id, title string) string {
	return `<container id="` + escapedXML(id) + `"><dc:title>` + escapedXML(title) + `</dc:title></container>`
}
func photo(id string) string {
	return `<item id="` + id + `"><dc:title>` + id + `.JPG</dc:title><upnp:class>object.item.imageItem.photo</upnp:class><res size="10">/file/` + id + `</res></item>`
}
func pageXML(entries string, returned, total int, update string) string {
	return `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="` + contentDirST + `"><Result>` + escapedXML(didl(entries)) + `</Result><NumberReturned>` + fmt.Sprint(returned) + `</NumberReturned><TotalMatches>` + fmt.Sprint(total) + `</TotalMatches><UpdateID>` + update + `</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`
}
func faultXML(code int) string {
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>目录不存在</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code)
}
func browseRequest(t *testing.T, r *http.Request) (string, int) {
	t.Helper()
	var req struct {
		Body struct {
			Browse struct {
				ID    string `xml:"ObjectID"`
				Start int    `xml:"StartingIndex"`
			} `xml:"Browse"`
		} `xml:"Body"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
	}
	return req.Body.Browse.ID, req.Body.Browse.Start
}
func browsingServer(t *testing.T, handler http.HandlerFunc) (*Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Server{Location: server.URL + "/desc.xml", ControlURL: server.URL + "/browse"}, server.Client()
}

func TestBrowseCappedPagesMixedRootDeepTreeAndCycle(t *testing.T) {
	var offsets []int
	var mu sync.Mutex
	s, client := browsingServer(t, func(w http.ResponseWriter, r *http.Request) {
		id, start := browseRequest(t, r)
		switch id {
		case "0":
			mu.Lock()
			offsets = append(offsets, start)
			mu.Unlock()
			entries := []string{photo("root-photo"), container("a&b", "DCIM"), photo("other")}
			end := min(start+2, len(entries))
			fmt.Fprint(w, pageXML(strings.Join(entries[start:end], ""), end-start, len(entries), "1"))
		case "a&b":
			fmt.Fprint(w, pageXML(container("2", "100MSDCF"), 1, 1, "1"))
		case "2":
			fmt.Fprint(w, pageXML(container("3", "日期"), 1, 1, "1"))
		case "3":
			fmt.Fprint(w, pageXML(container("4", "分组"), 1, 1, "1"))
		case "4":
			fmt.Fprint(w, pageXML(photo("deep")+container("0", "循环"), 2, 2, "1"))
		default:
			t.Errorf("意外目录 %q", id)
		}
	})
	storages, err := s.BrowseAll(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(storages) != 1 || len(storages[0].Items) != 3 {
		t.Fatalf("遗漏根目录或深层素材：%+v", storages)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(offsets, []int{0, 2}) {
		t.Fatalf("短页被提前截断：%v", offsets)
	}
	if !reflect.DeepEqual(storages[0].Items[2].Folders, []string{"DCIM", "100MSDCF", "日期", "分组"}) {
		t.Fatalf("丢失目录层级：%+v", storages[0].Items[2])
	}
}

func TestSonyRootNegotiationAndEmptyDirectory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		empty     bool
		wantErr   bool
		wantRoots []string
	}{
		{"索尼拉取入口", 701, false, false, []string{"PushRoot", "PhotoRoot"}},
		{"空选择不回退整张卡", 0, true, false, []string{"PushRoot"}},
		{"真实服务错误不回退", 501, false, true, []string{"PushRoot"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var roots []string
			s, client := browsingServer(t, func(w http.ResponseWriter, r *http.Request) {
				id, _ := browseRequest(t, r)
				roots = append(roots, id)
				if id == "PushRoot" && tc.code != 0 {
					w.WriteHeader(500)
					fmt.Fprint(w, faultXML(tc.code))
					return
				}
				if tc.empty {
					fmt.Fprint(w, pageXML("", 0, 0, "1"))
				} else {
					fmt.Fprint(w, pageXML(photo("one"), 1, 1, "1"))
				}
			})
			stores, err := s.BrowseRoots(context.Background(), client, "PushRoot", "PhotoRoot", "0")
			if (err != nil) != tc.wantErr {
				t.Fatalf("入口协商失败：%v", err)
			}
			if !reflect.DeepEqual(roots, tc.wantRoots) {
				t.Fatalf("不应扩大素材选择范围：%v", roots)
			}
			if tc.empty && (len(stores) != 1 || len(stores[0].Items) != 0) {
				t.Fatal("空目录不是有效结果")
			}
		})
	}
}

func TestBrowseRejectsIncompleteAndChangingPages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response func(int) string
	}{
		{"分页提前结束", func(start int) string {
			if start == 0 {
				return pageXML(photo("one"), 1, 2, "1")
			}
			return pageXML("", 0, 2, "1")
		}},
		{"重复页", func(int) string { return pageXML(photo("one"), 1, 2, "1") }},
		{"目录变化", func(start int) string { return pageXML(photo(fmt.Sprint(start)), 1, 2, fmt.Sprint(start)) }},
		{"条目计数错误", func(int) string { return pageXML(photo("one"), 2, 2, "1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, client := browsingServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, start := browseRequest(t, r)
				fmt.Fprint(w, tc.response(start))
			})
			if _, err := s.BrowseAll(context.Background(), client); err == nil {
				t.Fatal("不能发布不完整清单")
			}
		})
	}
}

func TestDescriptionRejectsForeignEndpointsAndRedirects(t *testing.T) {
	for _, target := range []string{"http://192.0.2.1/browse", "http://user@127.0.0.1/browse", "https://127.0.0.1/browse"} {
		t.Run(target, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, strings.Replace(fixtureDescription, "/upnp/control/ContentDirectory", target, 1))
			}))
			defer server.Close()
			if _, err := Describe(context.Background(), server.Client(), server.URL); err == nil {
				t.Fatal("接受了其他设备控制地址")
			}
		})
	}
	visits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visits++; http.Redirect(w, r, "/elsewhere", 302) }))
	defer server.Close()
	if _, err := Describe(context.Background(), server.Client(), server.URL); err == nil || visits != 1 {
		t.Fatal("跟随了服务重定向")
	}
}

func TestSSDPUsesSeparateDatagramsAndValidatesResponsePeer(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	received := make(chan []string, 1)
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var packets []string
		buf := make([]byte, 4096)
		for range 3 {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				received <- packets
				return
			}
			packets = append(packets, string(buf[:n]))
			for _, location := range []string{"http://192.0.2.1/desc.xml", "http://localhost/desc.xml", "http://127.0.0.1:64321/DmsDescPush.xml"} {
				_, _ = conn.WriteToUDP([]byte("HTTP/1.1 200 OK\r\nLOCATION: "+location+"\r\n\r\n"), from)
			}
		}
		received <- packets
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	config := Discovery{Subnet: netip.MustParsePrefix("127.0.0.0/8"), LocalIP: netip.MustParseAddr("127.0.0.1"), Timeout: 80 * time.Millisecond}
	locations, err := searchAt(ctx, config, conn.LocalAddr().String(), contentDirST, mediaServerST, pushListST)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(locations, []string{"http://127.0.0.1:64321/DmsDescPush.xml"}) {
		t.Fatalf("错误的发现边界：%v", locations)
	}
	packets := <-received
	if len(packets) != 3 {
		t.Fatalf("应收到三个独立请求：%d", len(packets))
	}
	for _, p := range packets {
		if strings.Count(p, "M-SEARCH") != 1 {
			t.Fatalf("合并的发现请求：%q", p)
		}
	}
	if got, ok := ssdpLocation([]byte("HTTP/1.1 200 OK\r\nLOCATION: http://user@127.0.0.1/x\r\n\r\n")); ok {
		t.Fatalf("接受带凭据地址：%s", got)
	}
}

func TestReadXMLRejectsOversize(t *testing.T) {
	var out deviceDescription
	if err := readXML(strings.NewReader("<root>"+strings.Repeat(" ", 200)+"</root>"), 100, &out); err == nil {
		t.Fatal("超长XML未拒绝")
	}
}
