package camera

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

type Media struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Thumb    string `json:"thumb"`
	Storage  int    `json:"storage"`
	Handle   uint32 `json:"handle"`
	Size     int64  `json:"size"`
	Duration int    `json:"duration"`
	Kind     string `json:"kind"`
	Date     string `json:"date"`
}

func SafePath(p string) bool {
	return strings.HasPrefix(p, "DCIM/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n") && !strings.Contains(p, "../")
}

func MediaURL(host string, storage int, p string) string {
	// 使用相机参考实现的请求布局：storage 在前，保留路径分隔符。
	// 其余查询分隔符仍转义，避免自定义文件名注入参数；空格不用表单编码的 +。
	escaped := strings.ReplaceAll(url.QueryEscape(p), "%2F", "/")
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	return fmt.Sprintf("http://%s/v2?storage=%d&path=%s", host, storage, escaped)
}

// ParseManifest 以记录边界解码，避免把下一条素材的元数据归到当前素材。
func ParseManifest(b []byte, storage int) ([]Media, error) {
	tags := []int{}
	for i := 18; i+2 < len(b); i++ {
		if b[i] == 0x19 && b[i+1] == 6 && (b[i-2] == 0 || b[i-2] == 3 || b[i-2] == 4) && (b[i-1] == 0xff || b[i-1] == 0xfe || b[i-1] == 0xf6 || b[i-1] == 0xc7 || b[i-1] == 0xe5 || b[i-1] == 0xe7) {
			tags = append(tags, i)
		}
	}
	files := []Media{}
	for index, t := range tags {
		end := len(b)
		if index+1 < len(tags) {
			end = tags[index+1] - 18
		}
		if end <= t {
			continue
		}
		name, media, thumb := "", "", ""
		for i := t + 2; i+2 < end; i++ {
			if b[i] == 0x0d {
				n := int(b[i+1])
				if n > 4 && i+2+n <= end {
					candidate := string(b[i+2 : i+2+n])
					ext := strings.ToUpper(path.Ext(candidate))
					if ext == ".MP4" || ext == ".MOV" || ext == ".JPG" || ext == ".JPEG" || ext == ".DNG" {
						name = candidate
					}
				}
			}
			if b[i] == 0x1a && i+6 < end && bytes.Equal(b[i+2:i+5], []byte{0, 0, 0}) {
				n := int(b[i+1]) - 6
				if n < 1 || i+6+n > end {
					continue
				}
				s := strings.TrimRight(string(b[i+6:i+6+n]), "\x00")
				if b[i+5] == 1 && strings.HasPrefix(s, "DCIM/") {
					media = s
				}
				if b[i+5] == 2 && strings.HasPrefix(s, "MISC/") {
					thumb = s
				}
			}
		}
		if name == "" || media == "" {
			continue
		}
		if path.Ext(media) == "" {
			media += path.Ext(name)
		}
		if !SafePath(media) || path.Base(name) != name {
			continue
		}
		if thumb != "" && path.Ext(thumb) == "" {
			thumb += ".scr"
		}
		kind := "photo"
		if strings.EqualFold(path.Ext(name), ".MP4") || strings.EqualFold(path.Ext(name), ".MOV") {
			kind = "video"
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", storage, media)))
		m := Media{ID: hex.EncodeToString(hash[:12]), Name: name, Path: media, Thumb: thumb, Storage: storage, Handle: le.Uint32(b[t-10:]), Size: int64(le.Uint32(b[t-14:])), Kind: kind, Date: fatDate(le.Uint32(b[t-18:]))}
		if kind == "video" {
			m.Duration = int(le.Uint16(b[t-6:]))
		}
		files = append(files, m)
	}
	if len(tags) > 0 && len(files) == 0 {
		return nil, fmt.Errorf("相机素材列表格式暂不兼容，请记录固件版本后重试")
	}
	return files, nil
}

func fatDate(v uint32) string {
	d, c := v>>16, v&0xffff
	y, mo, day := int(1980+(d>>9)), int((d>>5)&15), int(d&31)
	h, mi, s := int(c>>11), int((c>>5)&63), int(c&31)*2
	if mo < 1 || mo > 12 || day < 1 || day > 31 || h > 23 || mi > 59 || s > 59 {
		return ""
	}
	return time.Date(y, time.Month(mo), day, h, mi, s, 0, time.UTC).Format("2006-01-02 15:04:05")
}

func listPayload(counter uint16, cursor uint32) []byte {
	b := []byte{0x4a, 0, 0x2a, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 45, 0, 0x0d, 1, 0, 255, 255, 255, 255, 255, 255, 255, 255, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	le.PutUint32(b[4:], uint32(counter))
	le.PutUint32(b[10:], cursor)
	return b
}

type Page struct {
	Files    []Media `json:"files"`
	Complete bool    `json:"complete"`
	Cursor   uint32  `json:"cursor"`
	Storage  int     `json:"storage"`
}
