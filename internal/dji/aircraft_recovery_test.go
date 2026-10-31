package camera

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 故障替身模拟 macOS 撤销 socket 后的永久 EPIPE；同一连接再写不能恢复。
type handshakeConn struct {
	failure        error
	writes, closes int
	session        uint16
}

func (c *handshakeConn) Write(p []byte) (int, error) {
	c.writes++
	if c.failure != nil {
		return 0, &net.OpError{Op: "write", Net: "udp4", Err: c.failure}
	}
	c.session = le.Uint16(p[2:])
	return len(p), nil
}
func (c *handshakeConn) Read(p []byte) (int, error) {
	return copy(p, Packet(0, c.session, 0, []byte{1})), nil
}
func (c *handshakeConn) Close() error { c.closes++; return nil }
func (c *handshakeConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9003}
}
func (c *handshakeConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 9003}
}
func (c *handshakeConn) SetDeadline(time.Time) error      { return nil }
func (c *handshakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *handshakeConn) SetWriteDeadline(time.Time) error { return nil }

func TestAircraftRecreatesSocketAfterEPIPE(t *testing.T) {
	first := &handshakeConn{failure: syscall.EPIPE}
	second := &handshakeConn{}
	dials := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials++
		if dials == 1 {
			return first, nil
		}
		if first.closes != 1 {
			t.Fatal("重建前未关闭旧连接")
		}
		return second, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	link, err := openAircraftTransportWithRecovery(ctx, "127.0.0.2:9003", strings.Repeat("a", 32), dial)
	if err != nil {
		t.Fatal(err)
	}
	defer link.cancel()
	defer link.conn.Close()
	if dials != 2 || first.writes != 1 || second.writes != 1 {
		t.Fatalf("未通过新 socket 恢复握手：%d/%d/%d", dials, first.writes, second.writes)
	}
}

func TestAircraftEPIPERecoveryIsBoundedAndPreservesError(t *testing.T) {
	var connections []*handshakeConn
	dial := func(context.Context, string, string) (net.Conn, error) {
		c := &handshakeConn{failure: syscall.EPIPE}
		connections = append(connections, c)
		return c, nil
	}
	link, err := openAircraftTransportWithRecovery(context.Background(), "127.0.0.2:9003", strings.Repeat("a", 32), dial)
	if link != nil || !errors.Is(err, syscall.EPIPE) || !strings.Contains(err.Error(), "本地网络") || len(connections) != 2 {
		t.Fatalf("持续失败应给出本机权限指引，且只重建一次：%v %d", err, len(connections))
	}
	for _, c := range connections {
		if c.closes != 1 || c.writes != 1 {
			t.Fatal("失败连接未关闭，或在坏连接上持续重试")
		}
	}
}

func TestAircraftRecoveryStopsOnCancellation(t *testing.T) {
	dials := 0
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials++
		time.AfterFunc(20*time.Millisecond, cancel)
		return &handshakeConn{failure: syscall.EPIPE}, nil
	}
	defer cancel()
	started := time.Now()
	_, err := openAircraftTransportWithRecovery(ctx, "127.0.0.2:9003", strings.Repeat("a", 32), dial)
	if !errors.Is(err, context.Canceled) || dials != 1 || time.Since(started) >= 400*time.Millisecond {
		t.Fatalf("取消后仍尝试重连：%v %d", err, dials)
	}
}

func TestAircraftDoesNotRetryUnrelatedSocketErrors(t *testing.T) {
	for _, failure := range []error{syscall.EACCES, syscall.EADDRINUSE, syscall.ECONNREFUSED} {
		t.Run(failure.Error(), func(t *testing.T) {
			dials := 0
			dial := func(context.Context, string, string) (net.Conn, error) {
				dials++
				return &handshakeConn{failure: failure}, nil
			}
			_, err := openAircraftTransportWithRecovery(context.Background(), "127.0.0.2:9003", strings.Repeat("a", 32), dial)
			if !errors.Is(err, failure) || dials != 1 {
				t.Fatalf("不应重试非 EPIPE 错误：%v %d", err, dials)
			}
		})
	}
}
