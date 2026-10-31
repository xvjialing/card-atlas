// Package upnpav 实现相机热点内的 UPnP AV 发现和 ContentDirectory 浏览。
package upnpav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"cardatlas/internal/diagnostics"
)

const (
	mediaServerST = "urn:schemas-upnp-org:device:MediaServer:1"
	contentDirST  = "urn:schemas-upnp-org:service:ContentDirectory:1"
	pushListST    = "urn:schemas-sony-com:service:XPushList:1"
)

// Server 的端点均经同机校验；不同服务允许使用相机上的不同端口。
type Server struct {
	Location          string `json:"location"`
	BaseURL           string `json:"-"`
	FriendlyName      string `json:"friendlyName"`
	Manufacturer      string `json:"manufacturer"`
	ModelName         string `json:"modelName"`
	UDN               string `json:"udn"`
	ControlURL        string `json:"controlUrl"`
	PushControlURL    string `json:"-"`
	DigitalImagingURL string `json:"-"`
}

type Storage struct {
	ID, Name string
	Items    []Item
}

// Item 的大小、时长和 MIME 均来自同一个主资源，Folders 保留逻辑目录层级。
type Item struct {
	ID, Title, Class, Res, Duration, Date, Thumbnail string
	Size                                             int64
	MIMEType                                         string
	Converted                                        bool
	Folders                                          []string
}

type deviceDescription struct {
	URLBase      string `xml:"URLBase"`
	FriendlyName string `xml:"device>friendlyName"`
	Manufacturer string `xml:"device>manufacturer"`
	ModelName    string `xml:"device>modelName"`
	UDN          string `xml:"device>UDN"`
	Services     []struct {
		Type       string `xml:"serviceType"`
		ControlURL string `xml:"controlURL"`
		SCPDURL    string `xml:"SCPDURL"`
	} `xml:"device>serviceList>service"`
}

// cameraURL 只接受无凭据的 HTTP 字面 IP，不通过 DNS 扩大相机网络边界。
func cameraURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Host == "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("相机服务地址无效")
	}
	if _, err := netip.ParseAddr(u.Hostname()); err != nil {
		return nil, errors.New("相机服务地址必须为 IP 地址")
	}
	return u, nil
}

func resolveCameraURL(base *url.URL, raw string) (string, error) {
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("相机资源地址无效")
	}
	resolved := base.ResolveReference(ref)
	u, err := cameraURL(resolved.String())
	if err != nil {
		return "", err
	}
	a, _ := netip.ParseAddr(base.Hostname())
	b, _ := netip.ParseAddr(u.Hostname())
	if a.Unmap() != b.Unmap() {
		return "", errors.New("相机服务地址指向其他设备")
	}
	return u.String(), nil
}

// cameraDo 即使调用方使用默认 HTTP 客户端，也禁止设备端点重定向。
func cameraDo(client *http.Client, req *http.Request) (*http.Response, error) {
	if _, err := cameraURL(req.URL.String()); err != nil {
		return nil, err
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("不允许相机服务重定向") }
	return copy.Do(req)
}

func readXML(body io.Reader, limit int64, out any) error {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("相机 XML 响应超过读取限制")
	}
	return xml.Unmarshal(data, out)
}

// Describe 读取发现报文提供的描述文档，控制端点必须与描述文档同机。
func Describe(ctx context.Context, client *http.Client, location string) (*Server, error) {
	base, err := cameraURL(location)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cameraDo(client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("设备描述返回 HTTP %d", resp.StatusCode)
	}
	var doc deviceDescription
	if err := readXML(resp.Body, 1<<20, &doc); err != nil {
		return nil, err
	}
	if strings.TrimSpace(doc.URLBase) != "" {
		resolved, err := resolveCameraURL(base, doc.URLBase)
		if err != nil {
			return nil, err
		}
		base, _ = url.Parse(resolved)
	}
	s := &Server{Location: location, BaseURL: base.String(), FriendlyName: doc.FriendlyName, Manufacturer: doc.Manufacturer, ModelName: doc.ModelName, UDN: doc.UDN}
	for _, service := range doc.Services {
		if service.Type == "urn:schemas-sony-com:service:DigitalImaging:1" && strings.TrimSpace(service.SCPDURL) != "" {
			s.DigitalImagingURL, err = resolveCameraURL(base, service.SCPDURL)
			if err != nil {
				return nil, err
			}
			continue
		}
		if service.Type != contentDirST && service.Type != pushListST {
			continue
		}
		if strings.TrimSpace(service.ControlURL) == "" {
			return nil, errors.New("相机控制地址缺失")
		}
		control, err := resolveCameraURL(base, service.ControlURL)
		if err != nil {
			return nil, err
		}
		if service.Type == contentDirST {
			s.ControlURL = control
		} else {
			s.PushControlURL = control
		}
	}
	if s.ControlURL == "" {
		return nil, errors.New("设备未提供素材目录服务")
	}
	return s, nil
}

// PTPMediaSupported 只读相机明确发布的能力描述，不以旧 SOAP 服务仍在描述中作为传输协议依据。
func (s *Server) PTPMediaSupported(ctx context.Context, client *http.Client) (bool, error) {
	if s.DigitalImagingURL == "" {
		return false, nil
	}
	base, err := cameraURL(s.Location)
	if err != nil {
		return false, err
	}
	if _, err = resolveCameraURL(base, s.DigitalImagingURL); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.DigitalImagingURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := cameraDo(client, req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("相机能力描述返回 HTTP %d", resp.StatusCode)
	}
	var doc struct {
		XMLName xml.Name
		Inner   string `xml:",innerxml"`
	}
	if err := readXML(resp.Body, 1<<20, &doc); err != nil {
		return false, err
	}
	decoder := xml.NewDecoder(strings.NewReader(doc.Inner))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "X_PTP_MediaServerSupport" {
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return false, err
			}
			return strings.EqualFold(strings.TrimSpace(value), "Enable"), nil
		}
	}
}

// describe 保留包内原有调用入口。
func describe(ctx context.Context, client *http.Client, location string) (*Server, error) {
	return Describe(ctx, client, location)
}

// SOAPError 保留 UPnP 错误码，用于仅在“目录不存在”时切换索尼目录入口。
type SOAPError struct {
	Code        int
	Description string
}

func (e *SOAPError) Error() string {
	return fmt.Sprintf("相机目录服务错误 %d：%s", e.Code, e.Description)
}

type soapEnvelope struct {
	Body struct {
		Response struct {
			Result         string `xml:"Result"`
			NumberReturned string `xml:"NumberReturned"`
			TotalMatches   string `xml:"TotalMatches"`
			UpdateID       string `xml:"UpdateID"`
		} `xml:"BrowseResponse"`
		Fault *struct {
			Detail struct {
				Error struct {
					Code        int    `xml:"errorCode"`
					Description string `xml:"errorDescription"`
				} `xml:"UPnPError"`
			} `xml:"detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

func (s *Server) soap(ctx context.Context, client *http.Client, endpoint, service, action, args string) (soapEnvelope, error) {
	var envelope soapEnvelope
	base, err := cameraURL(s.Location)
	if err != nil {
		return envelope, err
	}
	if _, err := resolveCameraURL(base, endpoint); err != nil {
		return envelope, err
	}
	body := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + service + `">` + args + `</u:` + action + `></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return envelope, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"`+service+`#`+action+`"`)
	resp, err := cameraDo(client, req)
	if err != nil {
		diagnostics.From(ctx).Log("warn", "upnp", "upnp.soap_failed", "相机素材控制请求失败", map[string]string{"action": action, "service": service, "error": err.Error()})
		return envelope, err
	}
	defer resp.Body.Close()
	diagnostics.From(ctx).Log("info", "upnp", "upnp.soap_response", "相机素材控制请求已响应", map[string]string{"action": action, "service": service, "status": fmt.Sprint(resp.StatusCode)})
	err = readXML(resp.Body, 32<<20, &envelope)
	if envelope.Body.Fault != nil {
		fault := envelope.Body.Fault.Detail.Error
		return envelope, &SOAPError{Code: fault.Code, Description: fault.Description}
	}
	if resp.StatusCode != http.StatusOK {
		return envelope, fmt.Errorf("素材目录返回 HTTP %d", resp.StatusCode)
	}
	return envelope, err
}

// StartTransfer/EndTransfer 仅调用设备描述中明确发布的 Sony XPushList 服务。
func (s *Server) StartTransfer(ctx context.Context, client *http.Client) error {
	if s.PushControlURL == "" {
		return nil
	}
	_, err := s.soap(ctx, client, s.PushControlURL, pushListST, "X_TransferStart", "")
	return err
}
func (s *Server) EndTransfer(ctx context.Context, client *http.Client) error {
	if s.PushControlURL == "" {
		return nil
	}
	_, err := s.soap(ctx, client, s.PushControlURL, pushListST, "X_TransferEnd", "<ErrCode>0</ErrCode>")
	return err
}

func escapedXML(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
