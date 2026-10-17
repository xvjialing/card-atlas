package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func jpegFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 120, 255})
		}
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func assertThumbnail(t *testing.T, result Thumbnail) {
	t.Helper()
	if !strings.HasPrefix(result.DataURL, "data:image/jpeg;base64,") {
		t.Fatalf("未得到真实缩略图：%+v", result)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result.DataURL, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > 240 || img.Bounds().Dy() > 240 {
		t.Fatalf("未限制缩略图尺寸：%v", img.Bounds())
	}
}
func TestThumbnailImageAndInvalidFile(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "photo.JPG", jpegFixture(t), time.Now())
	fixture(t, root, "broken.MP4", []byte("损坏视频"), time.Now())
	s := scanFixture(t, root)
	ids := s.MatchingIDs(Filter{Kind: "photo"})
	thumb, err := s.GetThumbnail(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	assertThumbnail(t, thumb)
	if _, err := s.GetThumbnail("../../外部路径"); err == nil {
		t.Fatal("缩略图接受任意路径")
	}
	broken, err := s.GetThumbnail(s.MatchingIDs(Filter{Kind: "video"})[0])
	if err != nil || broken.DataURL != "" || broken.Message == "" {
		t.Fatalf("损坏视频未给出回退状态：%+v %v", broken, err)
	}
	if err := os.Remove(filepath.Join(root, "photo.JPG")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "外部照片.JPG"), filepath.Join(root, "photo.JPG")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetThumbnail(ids[0]); err == nil {
		t.Fatal("缩略图读取已替换的链接文件")
	}
}
