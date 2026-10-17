package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, dir, name string, data []byte, date time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, date, date); err != nil {
		t.Fatal(err)
	}
}
func waitJob(t *testing.T, s *MediaService) JobStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j := s.GetStatus()
		if j.State != "running" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("后台任务超时")
	return JobStatus{}
}
func scanFixture(t *testing.T, root string) *MediaService {
	t.Helper()
	s := NewMediaService()
	if _, err := s.StartScan(root); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, s); j.State != "completed" {
		t.Fatalf("扫描失败：%+v", j)
	}
	return s
}
func TestScanFilterAndExportCollision(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	date := time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local)
	fixture(t, root, "DCIM/A/DSC001.JPG", []byte("第一张照片"), date)
	fixture(t, root, "DCIM/B/DSC001.JPG", []byte("第二张照片"), date)
	fixture(t, root, "DCIM/clip.MP4", []byte("视频内容"), date.Add(48*time.Hour))
	fixture(t, root, "notes.txt", []byte("忽略"), date)
	fixture(t, root, ".hidden.JPG", []byte("隐藏"), date)
	if err := os.Symlink(filepath.Join(root, "DCIM/A/DSC001.JPG"), filepath.Join(root, "link.JPG")); err != nil {
		t.Fatal(err)
	}
	s := scanFixture(t, root)
	summary := s.GetSummary()
	if summary.Count != 3 || summary.Photos != 2 || summary.Videos != 1 || summary.Fallback != 3 || summary.Ignored != 3 {
		t.Fatalf("统计错误：%+v", summary)
	}
	q := Filter{From: "2026-10-08", To: "2026-10-08", Kind: "photo", Search: "DSC"}
	page := s.QueryFiles(q)
	if page.Total != 2 || len(page.Files) != 2 {
		t.Fatalf("闭区间筛选错误：%+v", page)
	}
	ids := s.MatchingIDs(q)
	plan, err := s.PrepareExport(ids)
	if err != nil || plan.Count != 2 || len(plan.Directories) != 1 {
		t.Fatalf("预览错误：%+v %v", plan, err)
	}
	fixture(t, out, "photo/2026/10/08/DSC001.JPG", []byte("已有文件"), date)
	if _, err := s.StartExport(out, ids); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s)
	if job.State != "completed" || job.Copied != 2 || job.Failed != 0 {
		t.Fatalf("导出失败：%+v", job)
	}
	for name, want := range map[string]string{"DSC001.JPG": "已有文件", "DSC001_001.JPG": "第一张照片", "DSC001_002.JPG": "第二张照片"} {
		got, err := os.ReadFile(filepath.Join(out, "photo/2026/10/08", name))
		if err != nil || string(got) != want {
			t.Fatalf("内容错误 %s：%s %v", name, got, err)
		}
	}
	if len(s.GetHistory()) != 1 {
		t.Fatal("未记录导出结果")
	}
	if _, err := os.Stat(filepath.Join(root, "DCIM/A/DSC001.JPG")); err != nil {
		t.Fatal("源文件被移除")
	}
}
func TestMutationInvalidSelectionAndOverlaps(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	date := time.Now().Add(-time.Hour)
	fixture(t, root, "one.JPG", []byte("原内容"), date)
	s := scanFixture(t, root)
	ids := s.MatchingIDs(Filter{})
	if _, err := s.StartExport(root, ids); err == nil {
		t.Fatal("允许导出到源目录")
	}
	child := filepath.Join(root, "output")
	os.MkdirAll(child, 0755)
	if _, err := s.StartExport(child, ids); err == nil {
		t.Fatal("允许嵌套输出目录")
	}
	if _, err := s.PrepareExport([]string{"失效 ID"}); err == nil {
		t.Fatal("接受失效选择")
	}
	fixture(t, root, "one.JPG", []byte("文件已被修改"), time.Now())
	if _, err := s.StartExport(out, ids); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, s)
	if j.Failed != 1 || j.Copied != 0 {
		t.Fatalf("未发现源文件变化：%+v", j)
	}
}
func TestCopyCancelAndDestinationSymlink(t *testing.T) {
	root, out, external := t.TempDir(), t.TempDir(), t.TempDir()
	date := time.Now().Add(-time.Hour)
	fixture(t, root, "one.JPG", bytes.Repeat([]byte("a"), 2<<20), date)
	s := scanFixture(t, root)
	f := s.QueryFiles(Filter{}).Files[0]
	src, _ := os.OpenRoot(root)
	defer src.Close()
	dst, _ := os.OpenRoot(out)
	defer dst.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyMedia(ctx, src, dst, f, func(string, int64) {}); err != context.Canceled {
		t.Fatalf("取消未生效：%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, targetPath(f))); !os.IsNotExist(err) {
		t.Fatal("取消留下不完整文件")
	}
	os.RemoveAll(filepath.Join(out, "photo"))
	if err := os.Symlink(external, filepath.Join(out, "photo")); err != nil {
		t.Fatal(err)
	}
	if err := copyMedia(context.Background(), src, dst, f, func(string, int64) {}); err == nil {
		t.Fatal("允许写出导出范围")
	}
	entries, _ := os.ReadDir(external)
	if len(entries) != 0 {
		t.Fatal("错误写入外部目录")
	}
}
func atom(kind string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	copy(b[4:8], kind)
	copy(b[8:], payload)
	return b
}
func TestMovieMetadataAndCorruptAtoms(t *testing.T) {
	want := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[4:8], uint32(want.Unix()+2082844800))
	movie := append(atom("mdat", []byte("不解析媒体内容")), atom("moov", atom("mvhd", payload))...)
	got, ok := movieDate(bytes.NewReader(movie), 0, int64(len(movie)), 0)
	if !ok || !got.Equal(want) {
		t.Fatalf("视频时间错误：%v %v", got, ok)
	}
	payload[0] = 1
	binary.BigEndian.PutUint64(payload[4:12], uint64(want.Unix()+2082844800))
	movie = atom("moov", atom("mvhd", payload))
	got, ok = movieDate(bytes.NewReader(movie), 0, int64(len(movie)), 0)
	if !ok || !got.Equal(want) {
		t.Fatal("64 位时间错误")
	}
	for _, data := range [][]byte{{0, 0, 0, 1, 'm', 'o', 'o', 'v'}, atom("mvhd", []byte{0, 0})} {
		if _, ok := movieDate(bytes.NewReader(data), 0, int64(len(data)), 0); ok {
			t.Fatal("接受损坏元数据")
		}
	}
}
func TestEXIFDate(t *testing.T) {
	// 构造只含 DateTime 的最小小端 TIFF，以验证相机本地时间解析。
	data := make([]byte, 46)
	copy(data, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(data[8:10], 1)
	binary.LittleEndian.PutUint16(data[10:12], 0x132)
	binary.LittleEndian.PutUint16(data[12:14], 2)
	binary.LittleEndian.PutUint32(data[14:18], 20)
	binary.LittleEndian.PutUint32(data[18:22], 26)
	copy(data[26:], "2026:10:08 09:24:12\x00")
	path := filepath.Join(t.TempDir(), "date.tif")
	os.WriteFile(path, data, 0644)
	f, _ := os.Open(path)
	defer f.Close()
	got, source := mediaDate(f, "photo", "tif", time.Now())
	if source != "EXIF 拍摄时间" || got.Format("2006-01-02T15:04:05") != "2026-10-08T09:24:12" {
		t.Fatalf("EXIF 解析错误：%v %s", got, source)
	}
}

func TestCopyProgressAndCancelVerification(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	fixture(t, root, "large.MP4", bytes.Repeat([]byte("a"), 3<<20), time.Now().Add(-time.Hour))
	s := scanFixture(t, root)
	f := s.QueryFiles(Filter{}).Files[0]
	src, _ := os.OpenRoot(root)
	defer src.Close()
	dst, _ := os.OpenRoot(out)
	defer dst.Close()
	phases := map[string]int64{}
	err := copyMedia(context.Background(), src, dst, f, func(phase string, done int64) {
		if done < phases[phase] || done > f.Size {
			t.Errorf("进度范围错误：%s %d", phase, done)
		}
		phases[phase] = done
	})
	if err != nil || phases["copying"] != f.Size || phases["verifying"] != f.Size {
		t.Fatalf("复制与校验进度不完整：%+v %v", phases, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = copyMedia(ctx, src, dst, f, func(phase string, done int64) {
		if phase == "verifying" && done > 0 {
			cancel()
		}
	})
	if err != context.Canceled {
		t.Fatalf("校验阶段取消失败：%v", err)
	}
	ext := filepath.Ext(f.Name)
	cancelled := filepath.Join(filepath.Dir(targetPath(f)), strings.TrimSuffix(f.Name, ext)+"_001"+ext)
	if _, err := dst.Stat(cancelled); !os.IsNotExist(err) {
		t.Fatal("校验取消留下未完成文件")
	}
	if _, err := s.StartExport(out, s.MatchingIDs(Filter{})); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s)
	if job.WorkDone != job.TotalWork || job.TotalWork != f.Size*2 || job.StartedAt == 0 || job.FinishedAt < job.StartedAt {
		t.Fatalf("最终复制状态错误：%+v", job)
	}
}
