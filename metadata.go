package main

import (
	"encoding/binary"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

var photoExtensions = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "heic": true, "heif": true,
	"tif": true, "tiff": true, "webp": true, "avif": true, "dng": true,
	"cr2": true, "cr3": true, "nef": true, "arw": true, "raf": true,
	"orf": true, "rw2": true, "pef": true, "srw": true, "bmp": true, "gif": true,
}
var videoExtensions = map[string]bool{
	"mp4": true, "mov": true, "m4v": true, "mts": true, "m2ts": true,
	"avi": true, "mkv": true, "mpg": true, "mpeg": true, "wmv": true,
	"webm": true, "3gp": true, "mxf": true,
}

func mediaDate(f *os.File, kind, ext string, fallback time.Time) (time.Time, string) {
	if kind == "photo" {
		// 只解析 EXIF，不解码大幅照片；无时区的相机时间按本机时区解释。
		x, err := exif.Decode(io.LimitReader(f, 32<<20))
		if err == nil {
			for _, name := range []exif.FieldName{exif.DateTimeOriginal, exif.DateTimeDigitized, exif.DateTime} {
				tag, err := x.Get(name)
				if err != nil {
					continue
				}
				value, err := tag.StringVal()
				if err != nil {
					continue
				}
				if t, err := time.ParseInLocation("2006:01:02 15:04:05", strings.TrimRight(value, "\x00"), time.Local); err == nil && t.Year() >= 1970 && t.Year() <= 2200 {
					return t, "EXIF 拍摄时间"
				}
			}
		}
	}
	if kind == "video" && (ext == "mp4" || ext == "mov" || ext == "m4v" || ext == "3gp") {
		if info, err := f.Stat(); err == nil {
			if t, ok := movieDate(f, 0, info.Size(), 0); ok {
				return t.In(time.Local), "视频创建时间"
			}
		}
	}
	return fallback, "文件修改时间"
}

// 遍历 ISO BMFF 的 moov/mvhd，不读取可能数 GB 的 mdat 内容。
func movieDate(r io.ReaderAt, start, end int64, depth int) (time.Time, bool) {
	if depth > 4 {
		return time.Time{}, false
	}
	for pos, count := start, 0; pos+8 <= end && count < 10000; count++ {
		var header [16]byte
		if _, err := r.ReadAt(header[:8], pos); err != nil {
			break
		}
		size, offset := int64(binary.BigEndian.Uint32(header[:4])), int64(8)
		if size == 1 {
			if _, err := r.ReadAt(header[8:], pos+8); err != nil {
				break
			}
			u := binary.BigEndian.Uint64(header[8:])
			if u > uint64(end-pos) {
				break
			}
			size, offset = int64(u), 16
		} else if size == 0 {
			size = end - pos
		}
		if size < offset || size > end-pos {
			break
		}
		switch string(header[4:8]) {
		case "moov":
			if t, ok := movieDate(r, pos+offset, pos+size, depth+1); ok {
				return t, true
			}
		case "mvhd":
			var data [12]byte
			if size-offset < 8 {
				break
			}
			n := 8
			if size-offset >= 12 {
				n = 12
			}
			if _, err := r.ReadAt(data[:n], pos+offset); err != nil {
				break
			}
			seconds := uint64(binary.BigEndian.Uint32(data[4:8]))
			if data[0] == 1 {
				if n < 12 {
					break
				}
				seconds = binary.BigEndian.Uint64(data[4:12])
			} else if data[0] != 0 {
				break
			}
			if seconds == 0 || seconds > 10_000_000_000 {
				break
			}
			t := time.Unix(int64(seconds)-2082844800, 0).UTC()
			if t.Year() >= 1970 && t.Year() <= 2200 {
				return t, true
			}
		}
		pos += size
	}
	return time.Time{}, false
}
