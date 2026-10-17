//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Foundation -framework ImageIO -framework CoreGraphics -framework AVFoundation -framework CoreMedia
#include <stdlib.h>
unsigned char *cardatlas_thumbnail(int fd, int video, const char *extension, size_t *length);
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"
)

func platformThumbnail(file *os.File, kind string) ([]byte, error) {
	var length C.size_t
	video := C.int(0)
	if kind == "video" {
		video = 1
	}
	extension := C.CString(filepath.Ext(file.Name()))
	defer C.free(unsafe.Pointer(extension))
	data := C.cardatlas_thumbnail(C.int(file.Fd()), video, extension, &length)
	if data == nil {
		return nil, errors.New("系统解码器无法生成缩略图")
	}
	defer C.free(unsafe.Pointer(data))
	if length == 0 || length > 512<<10 {
		return nil, errors.New("缩略图大小异常")
	}
	return C.GoBytes(unsafe.Pointer(data), C.int(length)), nil
}
