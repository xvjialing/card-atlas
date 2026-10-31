package upnpav

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type didlContainer struct {
	ID    string `xml:"id,attr"`
	Title string `xml:"http://purl.org/dc/elements/1.1/ title"`
}
type didlRes struct {
	Size         int64  `xml:"size,attr"`
	Duration     string `xml:"duration,attr"`
	ProtocolInfo string `xml:"protocolInfo,attr"`
	Resolution   string `xml:"resolution,attr"`
	URL          string `xml:",chardata"`
}
type didlItem struct {
	ID       string    `xml:"id,attr"`
	Title    string    `xml:"http://purl.org/dc/elements/1.1/ title"`
	Class    string    `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ class"`
	Date     string    `xml:"http://purl.org/dc/elements/1.1/ date"`
	AlbumArt string    `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ albumArtURI"`
	Res      []didlRes `xml:"res"`
}
type didlLite struct {
	Containers []didlContainer `xml:"container"`
	Items      []didlItem      `xml:"item"`
}
type browsePage struct {
	didlLite
	returned, total int
	update          string
}

func countValue(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, errors.New("素材目录分页数量无效")
	}
	return n, nil
}
func (s *Server) browse(ctx context.Context, client *http.Client, objectID string, start, count int) (browsePage, error) {
	var out browsePage
	args := fmt.Sprintf("<ObjectID>%s</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><Filter>*</Filter><StartingIndex>%d</StartingIndex><RequestedCount>%d</RequestedCount><SortCriteria></SortCriteria>", escapedXML(objectID), start, count)
	envelope, err := s.soap(ctx, client, s.ControlURL, contentDirST, "Browse", args)
	if err != nil {
		return out, err
	}
	response := envelope.Body.Response
	if strings.TrimSpace(response.Result) == "" {
		return out, errors.New("素材目录返回了空结果")
	}
	if err := xml.Unmarshal([]byte(response.Result), &out.didlLite); err != nil {
		return out, fmt.Errorf("素材目录结果无法解析：%w", err)
	}
	actual := len(out.Containers) + len(out.Items)
	out.returned, err = countValue(response.NumberReturned, actual)
	if err != nil {
		return out, err
	}
	out.total, err = countValue(response.TotalMatches, -1)
	if err != nil {
		return out, err
	}
	out.update = strings.TrimSpace(response.UpdateID)
	if out.returned != actual {
		return out, errors.New("素材目录分页数量与条目不符，请重试")
	}
	return out, nil
}

func (s *Server) browseChildren(ctx context.Context, client *http.Client, objectID string) (didlLite, error) {
	var all didlLite
	start, total, update := 0, -1, ""
	seen := map[[32]byte]bool{}
	for page := 0; page < 1000; page++ {
		result, err := s.browse(ctx, client, objectID, start, 500)
		if err != nil {
			return didlLite{}, err
		}
		if page == 0 {
			total, update = result.total, result.update
		} else if total != result.total || update != result.update {
			return didlLite{}, errors.New("读取期间相机目录已变化，请刷新重试")
		}
		if result.returned == 0 {
			if total >= 0 && start < total {
				return didlLite{}, errors.New("相机分页提前结束，素材清单不完整")
			}
			return all, nil
		}
		encoded, _ := xml.Marshal(result.didlLite)
		digest := sha256.Sum256(encoded)
		if seen[digest] {
			return didlLite{}, errors.New("相机重复返回同一页，已停止以避免遗漏素材")
		}
		seen[digest] = true
		all.Containers = append(all.Containers, result.Containers...)
		all.Items = append(all.Items, result.Items...)
		start += result.returned
		if start > 100000 {
			return didlLite{}, errors.New("相机目录超过读取限制")
		}
		if total >= 0 && start > total {
			return didlLite{}, errors.New("素材目录总数与分页不符")
		}
		if total >= 0 && start == total {
			return all, nil
		}
		// 固件可能把单页数量限制到 RequestedCount 以下，不能据此判定末页。
	}
	return didlLite{}, errors.New("素材目录分页超过读取限制，未发布不完整清单")
}

func (s *Server) BrowseAll(ctx context.Context, client *http.Client) ([]Storage, error) {
	return s.BrowseRoots(ctx, client, "0")
}

// BrowseRoots 按顺序协商入口，只对“目录不存在”回退；空目录为有效结果。
// 顶层对象通常是日期/媒体分类，不代表物理存储，全部归入一张存储卡。
func (s *Server) BrowseRoots(ctx context.Context, client *http.Client, roots ...string) ([]Storage, error) {
	for _, id := range roots {
		root, err := s.browseChildren(ctx, client, id)
		if err != nil {
			var fault *SOAPError
			if errors.As(err, &fault) && (fault.Code == 701 || fault.Code == 710) {
				continue
			}
			return nil, err
		}
		visited := map[string]bool{id: true}
		nodes := 1
		items := []Item{}
		if err := s.collect(ctx, client, root, nil, visited, &nodes, &items); err != nil {
			return nil, err
		}
		return []Storage{{ID: "0", Name: "存储卡", Items: items}}, nil
	}
	return nil, errors.New("相机未提供可浏览的素材目录，请进入「发送到智能手机」后重试")
}

func (s *Server) collect(ctx context.Context, client *http.Client, page didlLite, folders []string, visited map[string]bool, nodes *int, items *[]Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(folders) > 32 {
		return errors.New("相机目录层级超过读取限制，未发布不完整清单")
	}
	for _, entry := range page.Items {
		primary, thumb := selectResources(entry)
		if strings.TrimSpace(primary.URL) == "" {
			continue
		}
		*items = append(*items, Item{ID: entry.ID, Title: entry.Title, Class: entry.Class, Res: strings.TrimSpace(primary.URL), Size: primary.Size, Duration: primary.Duration, Date: entry.Date, Thumbnail: thumb, MIMEType: resourceMIME(primary), Converted: resourceFlag(primary, "DLNA.ORG_CI") == "1" || isThumbnail(primary), Folders: append([]string{}, folders...)})
		if len(*items) > 100000 {
			return errors.New("相机素材数量超过读取限制")
		}
	}
	for _, container := range page.Containers {
		if container.ID == "" {
			return errors.New("相机目录标识缺失")
		}
		if visited[container.ID] {
			continue
		}
		visited[container.ID] = true
		*nodes++
		if *nodes > 10000 {
			return errors.New("相机目录数量超过读取限制")
		}
		children, err := s.browseChildren(ctx, client, container.ID)
		if err != nil {
			return err
		}
		next := append(append([]string{}, folders...), container.Title)
		if err := s.collect(ctx, client, children, next, visited, nodes, items); err != nil {
			return err
		}
	}
	return nil
}
