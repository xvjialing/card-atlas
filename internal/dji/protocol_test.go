package camera

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestDocumentedDUMLFrames(t *testing.T) {
	for _, golden := range []string{"550d0433020700a04007077472", "55110492020100a040020c01010001b63b", "550f04a202f01bcb40002b04009ab9"} {
		b, _ := hex.DecodeString(golden)
		var r Reader
		msgs := r.Feed(b)
		if len(msgs) != 1 {
			t.Fatalf("协议固定报文未解析：%s", golden)
		}
		if !bytes.Equal(msgs[0].Encode(), b) {
			t.Fatal("编码与协议固定报文不符")
		}
	}
}

func TestReaderFragmentationNoiseAndCorruption(t *testing.T) {
	b, _ := hex.DecodeString("550d0433020700a04007077472")
	broken := append([]byte{}, b...)
	broken[len(broken)-1] ^= 1
	stream := append([]byte{1, 2, 0x55, 0xff, 0, 0}, broken...)
	stream = append(stream, b...)
	var r Reader
	var got []Message
	for _, v := range stream {
		got = append(got, r.Feed([]byte{v})...)
	}
	if len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("分片或 CRC 处理错误：%+v", got)
	}
}

func manifestRecord(name string, storage int, size uint32) []byte {
	// 采用协议记录布局构造不同文件类型、存储和自定义文件夹的报文。
	b := make([]byte, 36)
	le.PutUint32(b[4:], size)
	h := uint32(0x00100040)
	if storage == 1 {
		h |= 0x40000000
	}
	le.PutUint32(b[8:], h)
	le.PutUint16(b[12:], 63)
	b[16] = 3
	if name[len(name)-3:] == "JPG" {
		b[16] = 0
	}
	b[17] = 0xff
	b[18] = 0x19
	b[19] = 6
	b = append(b, 0x0d, byte(len(name)))
	b = append(b, name...)
	stem := name[:len(name)-4]
	p := "DCIM/CUSTOM_001/" + stem
	b = append(b, 0x1a, byte(len(p)+6), 0, 0, 0, 1)
	b = append(b, p...)
	p = "MISC/THM/CUSTOM_001/" + stem
	b = append(b, 0x1a, byte(len(p)+6), 0, 0, 0, 2)
	b = append(b, p...)
	return b
}

func TestManifestRecordIsolationAndStorage(t *testing.T) {
	b := append(manifestRecord("ONE.MP4", 1, 987654), manifestRecord("TWO.JPG", 1, 123)...)
	files, e := ParseManifest(b, 1)
	if e != nil || len(files) != 2 {
		t.Fatalf("解析失败 %v %+v", e, files)
	}
	if files[0].Name != "ONE.MP4" || files[0].Size != 987654 || files[0].Duration != 63 || files[1].Name != "TWO.JPG" || files[1].Size != 123 || files[1].Kind != "photo" {
		t.Fatalf("记录串扰：%+v", files)
	}
	if files[0].Storage != 1 || files[0].Path != "DCIM/CUSTOM_001/ONE.MP4" {
		t.Fatal(files[0])
	}
	if files[0].Thumb != "MISC/THM/CUSTOM_001/ONE.scr" || files[1].Thumb != "MISC/THM/CUSTOM_001/TWO.scr" {
		t.Fatalf("缩略图路径扩展名错误：%+v", files)
	}
	sd, _ := ParseManifest(manifestRecord("ONE.MP4", 0, 987654), 0)
	if sd[0].ID == files[0].ID {
		t.Fatal("不同存储不能共用素材 ID")
	}
}

func TestSafeMediaPaths(t *testing.T) {
	for _, p := range []string{"../secret", "DCIM/../../secret", "DCIM/a/../b", "/DCIM/file.MP4", "DCIM/x\\y", "DCIM/a\x00.mp4"} {
		if SafePath(p) {
			t.Fatalf("接受非法路径 %q", p)
		}
	}
	if !SafePath("DCIM/DJI_001/clip.MP4") {
		t.Fatal("拒绝正常路径")
	}
}

func FuzzFrameReader(f *testing.F) {
	f.Add([]byte{0x55, 13, 4, 0x33})
	f.Fuzz(func(t *testing.T, b []byte) {
		var r Reader
		for _, m := range r.Feed(b) {
			if len(m.Encode()) > 1023 {
				t.Fatal("超长报文")
			}
		}
	})
}

// 固定 HTTP 请求布局对应 Action 6 路径式读取协议，而非解码后的 Query 映射。
func TestCameraMediaURLWireFormat(t *testing.T) {
	for _, tc := range []struct {
		storage    int
		path, want string
	}{
		{1, "DCIM/DJI_001/DJI_20261021131408_0086_D.MP4", "http://192.168.2.1/v2?storage=1&path=DCIM/DJI_001/DJI_20261021131408_0086_D.MP4"},
		{0, "MISC/THM/DJI_001/test.scr", "http://192.168.2.1/v2?storage=0&path=MISC/THM/DJI_001/test.scr"},
		{1, "DCIM/a b+&x?#%.JPG", "http://192.168.2.1/v2?storage=1&path=DCIM/a%20b%2B%26x%3F%23%25.JPG"},
	} {
		if got := MediaURL("192.168.2.1", tc.storage, tc.path); got != tc.want {
			t.Fatalf("HTTP 请求布局错误：%q", got)
		}
	}
}
