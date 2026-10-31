package upnpav

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"cardatlas/internal/diagnostics"
)

const ssdpAddr = "239.255.255.250:1900"

// Discovery 将 SSDP 发送接口和接收网段绑定到实际 Wi-Fi 地址；Accept 用于筛选目标机型。
type Discovery struct {
	Subnet            netip.Prefix
	LocalIP           netip.Addr
	Control           func(string, string, syscall.RawConn) error
	Accept            func(*Server) bool
	Timeout           time.Duration
	FallbackLocations []string
}

func Discover(ctx context.Context, client *http.Client, subnet netip.Prefix) (*Server, error) {
	return DiscoverOn(ctx, client, Discovery{Subnet: subnet})
}
func DiscoverOn(ctx context.Context, client *http.Client, config Discovery) (*Server, error) {
	return discoverAt(ctx, client, config, ssdpAddr)
}
func discoverAt(ctx context.Context, client *http.Client, config Discovery, target string) (*Server, error) {
	trace := diagnostics.From(ctx)
	locations, err := searchAt(ctx, config, target, contentDirST, mediaServerST, pushListST)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && len(config.FallbackLocations) == 0 {
		return nil, err
	}
	searchErr := err
	seen := map[string]bool{}
	var descriptionErr error
	rejected := false
	for source, candidates := range [][]string{locations, config.FallbackLocations} {
		for _, location := range candidates {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if seen[location] || !discoveryLocationOnLink(config, location) {
				continue
			}
			seen[location] = true
			method := "ssdp"
			if source == 1 {
				method = "known_address"
			}
			trace.Log("info", "upnp", "upnp.description_attempt", "正在读取相机素材服务描述", map[string]string{"location": location, "method": method})
			server, err := Describe(ctx, client, location)
			if err != nil {
				descriptionErr = err
				trace.Log("warn", "upnp", "upnp.description_failed", "相机服务描述读取失败", map[string]string{"location": location, "error": err.Error()})
				continue
			}
			if config.Accept != nil && !config.Accept(server) {
				rejected = true
				trace.Log("warn", "upnp", "upnp.model_rejected", "素材服务的机型信息不匹配", map[string]string{"location": location, "model": server.ModelName, "manufacturer": server.Manufacturer, "friendly_name": server.FriendlyName})
				continue
			}
			trace.Log("info", "upnp", "upnp.service_ready", "已识别相机素材服务", map[string]string{"location": location, "model": server.ModelName})
			return server, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if rejected {
		return nil, errors.New("已收到素材服务，但设备描述未匹配 Sony ZV-E10 一代，请查看诊断日志中的机型信息")
	}
	if len(locations) > 0 && descriptionErr != nil {
		return nil, fmt.Errorf("已发现设备服务，但无法读取素材服务描述：%w", descriptionErr)
	}
	if searchErr != nil {
		return nil, fmt.Errorf("通过 Wi-Fi 发现相机服务失败：%w", searchErr)
	}
	return nil, errors.New("未收到 Sony 素材服务响应。ZV-E10 一代请先按回放键显示照片，再进入 MENU → 网络 → 发送到智能手机功能 → 发送到智能手机 → 这个影像（或多个影像），停在该入口的二维码界面；「智能手机连接功能 → 连接」是通用配网入口，当前客户端需使用前述素材传输入口。若仍无响应，请确认 CardAtlas 的本地网络权限，并查看诊断日志")
}

// 固定地址回退只接受适配器提供的链路内 HTTP 字面地址，沿用描述与资源的同机约束。
func discoveryLocationOnLink(config Discovery, location string) bool {
	u, err := cameraURL(location)
	if err != nil {
		return false
	}
	ip, _ := netip.ParseAddr(u.Hostname())
	return config.Subnet.Contains(ip.Unmap()) && ip.Unmap() != config.LocalIP.Unmap()
}

func searchAt(ctx context.Context, config Discovery, target string, sts ...string) ([]string, error) {
	trace := diagnostics.From(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !config.Subnet.IsValid() || !config.Subnet.Addr().Is4() {
		return nil, errors.New("相机发现网段无效")
	}
	local := &net.UDPAddr{}
	if config.LocalIP.IsValid() {
		if !config.Subnet.Contains(config.LocalIP) {
			return nil, errors.New("相机发现接口与网段不一致")
		}
		local.IP = net.IP(config.LocalIP.AsSlice())
	}
	listener := net.ListenConfig{Control: config.Control}
	packet, err := listener.ListenPacket(ctx, "udp4", local.String())
	if err != nil {
		trace.Log("warn", "upnp", "upnp.ssdp_listen_failed", "相机发现套接字初始化失败", map[string]string{"error": err.Error()})
		return nil, err
	}
	conn := packet.(*net.UDPConn)
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	addr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return nil, err
	}
	trace.Log("info", "upnp", "upnp.ssdp_start", "开始发送相机素材服务发现请求", map[string]string{"client_ipv4": config.LocalIP.String(), "subnet": config.Subnet.String(), "target": target})
	for _, st := range sts {
		payload := fmt.Sprintf("M-SEARCH * HTTP/1.1\r\nHOST: %s\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: %s\r\n\r\n", ssdpAddr, st)
		// 每个搜索目标单独使用一个 UDP 数据报；拼接多个请求不符合 SSDP。
		if _, err := conn.WriteToUDP([]byte(payload), addr); err != nil {
			trace.Log("warn", "upnp", "upnp.ssdp_send_failed", "相机发现请求发送失败", map[string]string{"error": err.Error(), "search_target": st})
			return nil, err
		}
	}
	timeout := config.Timeout
	if timeout <= 0 || timeout > 3*time.Second {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	locations := []string{}
	received, offLink, invalid, foreign := 0, 0, 0, 0
	defer func() {
		trace.Log("info", "upnp", "upnp.ssdp_complete", "相机发现响应接收结束", map[string]string{"datagrams": fmt.Sprint(received), "accepted": fmt.Sprint(len(locations)), "off_link": fmt.Sprint(offLink), "invalid": fmt.Sprint(invalid), "foreign_location": fmt.Sprint(foreign)})
	}()
	buf := make([]byte, 8192)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				return locations, nil
			}
			trace.Log("warn", "upnp", "upnp.ssdp_receive_failed", "接收相机发现响应失败", map[string]string{"error": err.Error()})
			return nil, err
		}
		received++
		ip, ok := netip.AddrFromSlice(from.IP)
		if !ok || !config.Subnet.Contains(ip.Unmap()) {
			offLink++
			continue
		}
		location, ok := ssdpLocation(buf[:n])
		if !ok {
			invalid++
			continue
		}
		if seen[location] {
			continue
		}
		u, err := cameraURL(location)
		if err != nil {
			continue
		}
		endpoint, _ := netip.ParseAddr(u.Hostname())
		if endpoint.Unmap() != ip.Unmap() {
			foreign++
			continue
		}
		seen[location] = true
		locations = append(locations, location)
	}
}

func ssdpLocation(data []byte) (string, bool) {
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(data))), nil)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if _, err := cameraURL(location); err != nil {
		return "", false
	}
	return location, true
}
