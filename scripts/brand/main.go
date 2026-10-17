//go:build ignore

// 从品牌原图统一导出应用与网页图标，构建时无需 Python 或图片处理依赖。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "工程根目录")
	flag.Parse()
	if err := generate(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(root string) error {
	source, err := os.Open(filepath.Join(root, "assets/brand/cardatlas-original.png"))
	if err != nil {
		return err
	}
	original, err := png.Decode(source)
	source.Close()
	if err != nil {
		return err
	}
	if original.Bounds().Dx() != original.Bounds().Dy() {
		return fmt.Errorf("品牌图标原图必须是正方形")
	}
	for _, output := range []struct {
		path string
		size int
	}{
		{"assets/brand/cardatlas.png", 1024},
		{"build/appicon.png", 1024},
		{"frontend/public/cardatlas.png", 256},
		{"frontend/public/favicon.png", 32},
		{"frontend/public/apple-touch-icon.png", 180},
	} {
		var data bytes.Buffer
		if err := png.Encode(&data, resize(original, output.size)); err != nil {
			return err
		}
		path := filepath.Join(root, output.path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		// 内容未变则不更新时间，避免热更新与任务缓存反复失效。
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data.Bytes()) {
			continue
		}
		if err := os.WriteFile(path, data.Bytes(), 0644); err != nil {
			return err
		}
		fmt.Println("已导出图标：", output.path)
	}
	return nil
}

// 按覆盖面积缩小，并在预乘透明度空间平均，保留透明边缘与小尺寸轮廓。
func resize(source image.Image, size int) *image.RGBA {
	bounds := source.Bounds()
	scale := float64(bounds.Dx()) / float64(size)
	result := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		y0, y1 := float64(y)*scale, float64(y+1)*scale
		for x := 0; x < size; x++ {
			x0, x1 := float64(x)*scale, float64(x+1)*scale
			var sum [4]float64
			for sy := int(y0); sy < int(math.Ceil(y1)); sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); sx < int(math.Ceil(x1)); sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					r, g, b, a := source.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
					weight := wx * wy
					sum[0] += float64(r) * weight
					sum[1] += float64(g) * weight
					sum[2] += float64(b) * weight
					sum[3] += float64(a) * weight
				}
			}
			area := scale * scale * 257
			result.SetRGBA(x, y, color.RGBA{uint8(math.Round(sum[0] / area)), uint8(math.Round(sum[1] / area)), uint8(math.Round(sum[2] / area)), uint8(math.Round(sum[3] / area))})
		}
	}
	return result
}
