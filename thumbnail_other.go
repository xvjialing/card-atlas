//go:build !darwin || !cgo

package main

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
	"os"
)

// 其他平台使用 Go 图片解码；原生视频缩略图目前由 macOS 提供。
func platformThumbnail(file *os.File, kind string) ([]byte, error) {
	if kind == "video" {
		return nil, errors.New("当前平台尚未提供视频取帧")
	}
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return nil, errors.New("图片尺寸过大")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return nil, err
	}
	original, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}
	scale := math.Min(1, 240/float64(max(config.Width, config.Height)))
	w, h := max(1, int(float64(config.Width)*scale)), max(1, int(float64(config.Height)*scale))
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	bounds := original.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			thumb.Set(x, y, original.At(bounds.Min.X+x*config.Width/w, bounds.Min.Y+y*config.Height/h))
		}
	}
	var out bytes.Buffer
	err = jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 75})
	return out.Bytes(), err
}
