package upnpav

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const fixtureDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
<device><friendlyName>ZV-E10</friendlyName><manufacturer>Sony Corporation</manufacturer>
<serviceList><service>
<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
<serviceId>urn:schemas-upnp-org:serviceId:ContentDirectory</serviceId>
<controlURL>/upnp/control/ContentDirectory</controlURL>
</service></serviceList></device>
</root>`

func didlFor(objectID string) string {
	var b strings.Builder
	b.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)
	switch objectID {
	case "0":
		b.WriteString(`<container id="1" parentID="0" childCount="2"><dc:title>100MSDCF</dc:title><upnp:class>object.container.storageFolder</upnp:class></container>`)
	case "1":
		b.WriteString(`<item id="10" parentID="1"><dc:title>DSC00001.JPG</dc:title><upnp:class>object.item.imageItem.photo</upnp:class><dc:date>2026-10-23T10:30:00</dc:date>` +
			`<res size="2048" duration="0:00:00">http://10.0.0.1:52235/file/10</res><res size="128">http://10.0.0.1:52235/thumb/10</res></item>`)
		b.WriteString(`<item id="11" parentID="1"><dc:title>C0001.MP4</dc:title><upnp:class>object.item.videoItem.movie</upnp:class>` +
			`<res size="4096" duration="0:01:05.500">http://10.0.0.1:52235/file/11</res></item>`)
	}
	b.WriteString(`</DIDL-Lite>`)
	return b.String()
}

func soapResult(didl string) string {
	escaped := xmlEscape(didl)
	var entries didlLite
	_ = xml.Unmarshal([]byte(didl), &entries)
	count := fmt.Sprint(len(entries.Containers) + len(entries.Items))
	return `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` +
		`<u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">` +
		`<Result>` + escaped + `</Result>` +
		`<NumberReturned>` + count + `</NumberReturned><TotalMatches>` + count + `</TotalMatches>` +
		`</u:BrowseResponse></s:Body></s:Envelope>`
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&escapeWriter{b: &b}, []byte(s))
	return b.String()
}

type escapeWriter struct{ b *strings.Builder }

func (w *escapeWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func fixtureServer(t *testing.T, calls *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/desc.xml":
			fmt.Fprint(w, fixtureDescription)
		case r.URL.Path == "/upnp/control/ContentDirectory":
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			if !strings.Contains(string(body), "BrowseDirectChildren") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			objectID := between(string(body), "<ObjectID>", "</ObjectID>")
			w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
			fmt.Fprint(w, soapResult(didlFor(objectID)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestDescribeResolvesControlURL(t *testing.T) {
	var calls []string
	server := fixtureServer(t, &calls)
	location := server.URL + "/desc.xml"
	server2, err := describe(context.Background(), server.Client(), location)
	if err != nil {
		t.Fatal(err)
	}
	if server2.FriendlyName != "ZV-E10" || server2.Manufacturer != "Sony Corporation" {
		t.Fatalf("设备信息缺失：%+v", server2)
	}
	if server2.ControlURL != server.URL+"/upnp/control/ContentDirectory" {
		t.Fatalf("控制地址未按描述文档解析：%q", server2.ControlURL)
	}
}

func TestBrowseAllCollectsNestedItems(t *testing.T) {
	var calls []string
	server := fixtureServer(t, &calls)
	server2, err := describe(context.Background(), server.Client(), server.URL+"/desc.xml")
	if err != nil {
		t.Fatal(err)
	}
	storages, err := server2.BrowseAll(context.Background(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(storages) != 1 {
		t.Fatalf("存储数量错误：%+v", storages)
	}
	storage := storages[0]
	if storage.ID != "0" || storage.Name != "存储卡" {
		t.Fatalf("单存储应命名为存储卡：%+v", storage)
	}
	if len(storage.Items) != 2 {
		t.Fatalf("素材数量错误：%d", len(storage.Items))
	}
	photo := storage.Items[0]
	if photo.Title != "DSC00001.JPG" || photo.Res != "http://10.0.0.1:52235/file/10" || photo.Size != 2048 || photo.Thumbnail != "http://10.0.0.1:52235/thumb/10" || photo.Date != "2026-10-23T10:30:00" {
		t.Fatalf("照片条目解析错误：%+v", photo)
	}
	video := storage.Items[1]
	if video.Duration != "0:01:05.500" || video.Thumbnail != "" {
		t.Fatalf("视频条目解析错误：%+v", video)
	}
}

func TestBrowseRejectsBrokenResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/desc.xml" {
			fmt.Fprint(w, fixtureDescription)
			return
		}
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		fmt.Fprint(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>not-xml</Result><NumberReturned>1</NumberReturned></u:BrowseResponse></s:Body></s:Envelope>`)
	}))
	defer server.Close()
	server2, err := describe(context.Background(), server.Client(), server.URL+"/desc.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server2.BrowseAll(context.Background(), server.Client()); err == nil {
		t.Fatal("无法解析的目录结果应报错")
	}
}

func TestSsdpLocationExtraction(t *testing.T) {
	response := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://10.0.0.1:52235/desc.xml\r\nST: " + contentDirST + "\r\n\r\n"
	location, ok := ssdpLocation([]byte(response))
	if !ok || location != "http://10.0.0.1:52235/desc.xml" {
		t.Fatalf("LOCATION 解析错误：%q %v", location, ok)
	}
	if _, ok := ssdpLocation([]byte("HTTP/1.1 200 OK\r\n\r\n")); ok {
		t.Fatal("缺少 LOCATION 时不应返回结果")
	}
}

func TestDiscoverFailsWithoutServer(t *testing.T) {
	// 已取消的发现不发送任何数据报；离线测试不向真实网络广播。
	subnet := netip.MustParsePrefix("10.99.0.0/24")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cancel()
	if _, err := Discover(ctx, http.DefaultClient, subnet); err == nil {
		t.Fatal("无设备时应返回错误")
	}
}
