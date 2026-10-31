package camera

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type captureSession struct {
	AppName     string `json:"app_name"`
	Serial      string `json:"serial"`
	AppAddress  string `json:"app_address"`
	PeerAddress string `json:"peer_address"`
	Frames      map[string]struct {
		Frame   string
		Payload string
		Packet  int
		Tail    string
	}
}

func readCaptureSession(t *testing.T) captureSession {
	t.Helper()
	data, err := os.ReadFile("testdata/mini5pro-session.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture captureSession
	if err = json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	return capture
}

func (c captureSession) message(t *testing.T, name string) Message {
	t.Helper()
	data, err := hex.DecodeString(c.Frames[name].Frame)
	if err != nil {
		t.Fatal(err)
	}
	var r Reader
	messages := r.Feed(data)
	if len(messages) != 1 {
		t.Fatalf("抓包 #%d 的 DUML CRC 不正确", c.Frames[name].Packet)
	}
	return messages[0]
}

func TestMini5ProCapturedSessionLayouts(t *testing.T) {
	c := readCaptureSession(t)
	if !bytes.Equal(aircraftOpenRequest(), c.message(t, "open").Payload) {
		t.Fatal("会话开启必须匹配 #12811 的11字节布局")
	}
	challenge := c.message(t, "challenge").Payload
	app := aircraftAppIdentity(c.AppName)
	if !bytes.Equal(aircraftIdentityRequest(app, challenge[:23]), c.message(t, "identity_request").Payload) {
		t.Fatal("身份请求未匹配 #12834 的51字节布局")
	}
	if bytes.Equal(c.message(t, "identity_ack").Payload, append(challenge[:23:23], 0)) {
		t.Fatal("确认包应返回客户端身份，不能把它当作设备身份回显")
	}
	var appID, peerID [4]byte
	copy(appID[:], hx(c.AppAddress))
	copy(peerID[:], hx(c.PeerAddress))
	if !bytes.Equal(aircraftCapabilityOffer(appID), c.message(t, "capability_offer").Payload) {
		t.Fatal("能力通告布局未匹配抓包")
	}
	for _, name := range []string{"basic", "full"} {
		got, bit := aircraftCapabilityReply(c.message(t, "capability_request_"+name).Payload, appID, peerID)
		if bit == 0 || !bytes.Equal(got, c.message(t, "capability_reply_"+name).Payload) {
			t.Fatalf("%s 能力回复与抓包不一致：%x", name, got)
		}
	}
	if !bytes.Equal(aircraftEndpointAnnouncement(c.AppName), c.message(t, "endpoint_announcement").Payload) || !bytes.Equal(aircraftAbilityAnnouncement(c.AppName, false), c.message(t, "identity_announcement").Payload) {
		t.Fatal("缺少或改变了初始端点/身份公告")
	}
	if !bytes.Equal(aircraftAckPayload(0xbc88, 0xbc88, 0xbcc8, 0xbcd0), hx("88bc88bc0000000088bc88bc00000000c8bcd0bc000000000000")) {
		t.Fatal("控制 ACK 窗口未匹配抓包")
	}
}

type capturedWriteConn struct {
	handshakeConn
	packet []byte
}

func (c *capturedWriteConn) Write(p []byte) (int, error) {
	c.packet = append([]byte{}, p...)
	return len(p), nil
}

func TestMini5ProBeaconStateMatchesCapture(t *testing.T) {
	c := readCaptureSession(t)
	for _, ready := range []bool{false, true} {
		conn := &capturedWriteConn{}
		l := &AircraftLink{conn: conn, appName: c.AppName, sessionReady: ready, started: time.Now()}
		if err := l.sendBeacon(); err != nil {
			t.Fatal(err)
		}
		var reader Reader
		outer := reader.Feed(conn.packet[20:])
		var nested Reader
		inner := nested.Feed(outer[0].Payload)
		name := "beacon_before"
		if ready {
			name = "beacon_after"
		}
		want := c.message(t, name).Payload
		got := inner[0].Payload
		// 计数、运行时间与未映射的设备环境字段不复用手机抓包值。
		if len(got) != 55 || !bytes.Equal(got[:39], want[:39]) || !bytes.Equal(got[49:], want[49:]) {
			t.Fatal("身份广播未表达实际快传状态")
		}
	}
}

func TestAircraftBeaconDoesNotConsumeWLMSequence(t *testing.T) {
	c := readCaptureSession(t)
	conn := &capturedWriteConn{}
	l := &AircraftLink{conn: conn, appName: c.AppName, started: time.Now()}
	for _, step := range []struct {
		beacon bool
		seq    uint32
	}{{true, 1}, {true, 1}, {true, 1}, {false, 1}, {true, 2}, {false, 2}, {true, 3}} {
		var err error
		if step.beacon {
			err = l.sendBeacon()
		} else {
			err = l.sendTunnel(Message{Sender: 0xee, Receiver: 0xe9, Flags: 0x40, Set: 0x51, ID: 0x41, Payload: aircraftCapabilityOffer(l.app)}, true)
		}
		if err != nil {
			t.Fatal(err)
		}
		var r Reader
		outer := r.Feed(conn.packet[20:])[0]
		n := int(le.Uint16(outer.Payload[1:]) & 0x3ff)
		if outer.Seq != uint16(step.seq) || le.Uint32(outer.Payload[n+5:]) != step.seq {
			t.Fatalf("广播消耗了正式请求序号：%+v", step)
		}
	}
}

func TestAircraftIdentityIsStableAndPreservesPairingPrefix(t *testing.T) {
	identity := "0123456789abcdef0123456789abcdef"
	name, address := aircraftLocalIdentity(identity)
	againName, againAddress := aircraftLocalIdentity(identity)
	if name != identity[:19] || name != againName || address != againAddress {
		t.Fatal("重连改变了已授权客户端的身份或地址")
	}
	_, other := aircraftLocalIdentity("fedcba9876543210fedcba9876543210")
	if other == address || address == [4]byte{} || address == [4]byte{255, 255, 255, 255} {
		t.Fatal("不同安装身份不能共用保留地址")
	}
}

func TestAircraftSendWindowStartsAtPeerAcknowledgement(t *testing.T) {
	conn := &capturedWriteConn{}
	l := &AircraftLink{conn: conn, seq: 0x1260, controlAck: 0x1230}
	if err := l.writeLocked(Message{Sender: 2, Receiver: 7, Seq: 1, Flags: 0x40, Set: 7, ID: 0x93}); err != nil {
		t.Fatal(err)
	}
	if le.Uint16(conn.packet[8:]) != 0x1230 || le.Uint16(conn.packet[10:]) != 0x1260 {
		t.Fatal("发送窗口误用了上一包序号，而非实际已确认边界")
	}
}

func TestMini5ProCapturedManifests(t *testing.T) {
	for _, tc := range []struct {
		name                string
		storage, count, dng int
	}{{"sd", 0, 45, 39}, {"internal", 1, 45, 38}, {"burst", 1, 57, 57}, {"panorama", 1, 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/mini5pro-" + tc.name + "-manifest.bin")
			if err != nil {
				t.Fatal(err)
			}
			if int(le.Uint32(raw[4:])) == len(raw) {
				t.Fatal("样本未覆盖逻辑长度与线上长度的差异")
			}
			files, err := ParseAircraftManifest(raw, tc.storage)
			if err != nil || len(files) != tc.count {
				t.Fatalf("捕获目录解析失败：%d %v", len(files), err)
			}
			dng, embedded := 0, 0
			for _, m := range files {
				if m.Storage != tc.storage || !SafePath(m.Path) || m.Date != "2000-01-01 00:00:00" {
					t.Fatal("存储、路径或脱敏日期解析错误")
				}
				if strings.HasSuffix(m.Name, ".DNG") {
					dng++
				}
				if m.ThumbnailSize > 0 {
					embedded++
				}
			}
			if dng != tc.dng {
				t.Fatalf("遗漏 DNG：%d", dng)
			}
			if embedded == 0 && tc.name != "panorama" {
				t.Fatal("未读出内嵌缩略图范围")
			}
			if _, err := ParseAircraftManifest(raw[:len(raw)-1], tc.storage); err == nil {
				t.Fatal("接受截断记录")
			}
			if _, err := ParseAircraftManifest(append(append([]byte{}, raw...), 0), tc.storage); err == nil {
				t.Fatal("接受无效尾部数据")
			}
			if _, err := ParseAircraftManifest(raw, 1-tc.storage); err == nil {
				t.Fatal("接受错误存储编号")
			}
		})
	}
}
