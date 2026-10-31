package camera

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ParseAircraftManifest 按 Mini 5 Pro 实际记录边界解析 CompositePack。
// 包 #13316–13325 的头部为 45/23318，但线上只有 12326 字节；第二字段
// 在捕获中恒为 8+518*记录数，不能用它代替传输长度或假设固定记录步长。
func ParseAircraftManifest(raw []byte, storage int) ([]Media, error) {
	if len(raw) < 8 || storage < 0 || storage > 1 {
		return nil, errors.New("无人机目录头无效")
	}
	count := int(le.Uint32(raw))
	if count < 0 || count > 10000 || count > (len(raw)-8)/25 {
		return nil, errors.New("无人机目录记录数不完整")
	}
	files := make([]Media, 0, count)
	at := 8
	for index := 0; index < count; index++ {
		if at+25 > len(raw) || string(raw[at+18:at+23]) != "\x19\x06\x00\x00\x00" {
			return nil, fmt.Errorf("无人机第 %d 条素材的扩展记录头不兼容", index+1)
		}
		extended := int(le.Uint16(raw[at+23:]))
		if extended < 7 || raw[at+17] != byte(min(extended, 255)) || at+18+extended > len(raw) {
			return nil, errors.New("无人机素材扩展记录长度不完整")
		}
		record := raw[at : at+18+extended]
		at += len(record)
		m := Media{Storage: storage, Handle: le.Uint32(record[8:]), Size: int64(le.Uint32(record[4:])), Duration: int(le.Uint16(record[12:])), Date: fatDate(le.Uint32(record))}
		if int(m.Handle>>30) != storage {
			return nil, errors.New("无人机素材所属存储与查询不一致")
		}
		for i := 25; i+2 < len(record); i++ {
			if record[i] == 0x0d {
				n := int(record[i+1])
				if n > 4 && i+2+n <= len(record) {
					name := string(record[i+2 : i+2+n])
					switch strings.ToUpper(path.Ext(name)) {
					case ".MP4", ".MOV", ".JPG", ".JPEG", ".DNG":
						m.Name = name
					}
				}
			}
			if i+5 > len(record) {
				continue
			}
			if record[i] != 0x1a && record[i] != 0x1d {
				continue
			}
			n := int(le.Uint32(record[i+1:]))
			if n < 5 || n > len(record)-i-1 {
				continue
			}
			if record[i] == 0x1a && n > 6 {
				p := strings.TrimRight(string(record[i+6:i+1+n]), "\x00")
				if record[i+5] == 1 && SafePath(p) {
					m.Path = p
				}
				if record[i+5] == 2 && strings.HasPrefix(p, "MISC/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n") {
					m.Thumb = p
				}
			} else if record[i] == 0x1d && n == 28 {
				// #13622/#15257 的 HTTP Range 与这两个字段逐字节对应，响应是 JPEG。
				p := record[i+5 : i+1+n]
				size, offset := int64(le.Uint32(p)), le.Uint64(p[8:])
				if size > 0 && size <= 4<<20 && offset <= 1<<53 && offset+uint64(size) >= offset {
					m.ThumbnailSize, m.ThumbnailOffset = size, int64(offset)
				}
			}
		}
		if m.Name == "" || path.Base(m.Name) != m.Name || m.Path == "" {
			return nil, errors.New("无人机素材缺少有效名称或物理路径")
		}
		ext := path.Ext(m.Name)
		if path.Base(m.Path) != strings.TrimSuffix(m.Name, ext) && !strings.EqualFold(path.Base(m.Path), m.Name) {
			return nil, errors.New("无人机素材名称与物理路径不一致")
		}
		if path.Ext(m.Path) == "" {
			m.Path += strings.ToLower(ext)
		}
		if m.Thumb != "" && path.Ext(m.Thumb) == "" {
			m.Thumb += ".thm"
		}
		m.Kind = "photo"
		if strings.EqualFold(ext, ".MP4") || strings.EqualFold(ext, ".MOV") {
			m.Kind = "video"
		} else {
			m.Duration = 0
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", storage, m.Path)))
		m.ID = hex.EncodeToString(hash[:12])
		files = append(files, m)
	}
	if at != len(raw) {
		return nil, errors.New("无人机目录包含未解析的尾部数据")
	}
	return files, nil
}
