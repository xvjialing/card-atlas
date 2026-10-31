package upnpav

import (
	"strconv"
	"strings"
)

func resourceMIME(r didlRes) string {
	parts := strings.SplitN(r.ProtocolInfo, ":", 4)
	if len(parts) < 3 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[2]))
}
func resourceFlag(r didlRes, name string) string {
	parts := strings.SplitN(r.ProtocolInfo, ":", 4)
	if len(parts) < 4 {
		return ""
	}
	for _, field := range strings.Split(parts[3], ";") {
		key, value, _ := strings.Cut(field, "=")
		if strings.EqualFold(key, name) {
			return strings.ToUpper(strings.TrimSpace(value))
		}
	}
	return ""
}
func isThumbnail(r didlRes) bool {
	profile := resourceFlag(r, "DLNA.ORG_PN")
	return strings.HasPrefix(profile, "JPEG_TN") || strings.HasPrefix(profile, "JPEG_SM") || strings.HasPrefix(profile, "PNG_TN")
}
func pixels(r didlRes) int64 {
	w, h, ok := strings.Cut(strings.ToLower(r.Resolution), "x")
	if !ok {
		return 0
	}
	width, _ := strconv.ParseInt(w, 10, 64)
	height, _ := strconv.ParseInt(h, 10, 64)
	if width <= 0 || height <= 0 || width > 1000000 || height > 1000000 {
		return 0
	}
	return width * height
}
func sourceRank(r didlRes) int {
	if isThumbnail(r) {
		return -1
	}
	switch resourceFlag(r, "DLNA.ORG_CI") {
	case "0":
		return 2
	case "1":
		return 0
	}
	return 1
}

func profileRank(r didlRes) int {
	profile := resourceFlag(r, "DLNA.ORG_PN")
	switch {
	case strings.Contains(profile, "_LRG"):
		return 3
	case strings.Contains(profile, "_MED"):
		return 2
	case strings.Contains(profile, "_SM"):
		return 1
	}
	return 0
}
func betterResource(a, b didlRes) bool {
	if sourceRank(a) != sourceRank(b) {
		return sourceRank(a) > sourceRank(b)
	}
	if pixels(a) != pixels(b) {
		return pixels(a) > pixels(b)
	}
	if profileRank(a) != profileRank(b) {
		return profileRank(a) > profileRank(b)
	}
	return a.Size > b.Size
}

// selectResources 选择质量最高的可用资源，不依赖 res 的顺序；元数据始终来自被选资源。
func selectResources(entry didlItem) (didlRes, string) {
	var primary didlRes
	thumbnail := strings.TrimSpace(entry.AlbumArt)
	var thumb didlRes
	for _, res := range entry.Res {
		parts := strings.SplitN(res.ProtocolInfo, ":", 4)
		if strings.TrimSpace(res.URL) == "" || (len(parts) > 1 && parts[0] != "" && parts[0] != "http-get") {
			continue
		}
		mime := resourceMIME(res)
		if mime != "" && mime != "*" && mime != "application/octet-stream" && !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
			continue
		}
		if primary.URL == "" || betterResource(res, primary) {
			primary = res
		}
		if isThumbnail(res) && (thumb.URL == "" || (res.Size > 0 && (thumb.Size <= 0 || res.Size < thumb.Size))) {
			thumb = res
		}
	}
	if thumbnail == "" {
		thumbnail = strings.TrimSpace(thumb.URL)
	}
	// 无协议标记的旧固件仍可用较小的照片资源作缩略图，不能把另一个视频流当缩略图。
	if thumbnail == "" {
		for _, res := range entry.Res {
			if res.URL == primary.URL || strings.TrimSpace(res.URL) == "" || res.Size <= 0 || primary.Size <= 0 || res.Size >= primary.Size {
				continue
			}
			if resourceMIME(res) != "" && !strings.HasPrefix(resourceMIME(res), "image/") {
				continue
			}
			if thumb.URL == "" || res.Size < thumb.Size {
				thumb = res
			}
		}
		thumbnail = strings.TrimSpace(thumb.URL)
	}
	if thumbnail == strings.TrimSpace(primary.URL) {
		thumbnail = ""
	}
	return primary, thumbnail
}
