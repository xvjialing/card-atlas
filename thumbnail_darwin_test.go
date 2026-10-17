//go:build darwin && cgo

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeVideoThumbnail(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("测试视频生成需要 ffmpeg；应用取帧不依赖该工具")
	}
	root := t.TempDir()
	video := filepath.Join(root, "clip.MP4")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=10", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", video)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("测试视频生成失败：%s %v", output, err)
	}
	s := scanFixture(t, root)
	thumb, err := s.GetThumbnail(s.MatchingIDs(Filter{})[0])
	if err != nil {
		t.Fatal(err)
	}
	assertThumbnail(t, thumb)
}
