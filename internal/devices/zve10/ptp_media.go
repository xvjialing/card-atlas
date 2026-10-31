package zve10

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cardatlas/internal/deviceimport"
	"cardatlas/internal/diagnostics"
	"cardatlas/internal/upnpav"

	"github.com/knallli/alpha2go/ptpip"
)

type ptpObject struct {
	info *ptpip.ObjectInfo
	size int64
}

var errPTPModel = errors.New("PTP/IP 返回的相机型号不是 Sony ZV-E10 一代")

// PTP 对象通过会话内受控传输器接入既有预览与续传；这些内部 URL 不会发给任意 HTTP 主机。
type ptpMedia struct {
	c       *ptpip.Client
	di      *ptpip.DeviceInfo
	host    string
	ctx     context.Context
	cancel  context.CancelFunc
	mode    bool
	session bool
	mu      sync.RWMutex
	objects map[uint32]ptpObject
	gate    chan struct{}
}

func openPTPMedia(ctx context.Context, server *upnpav.Server, dial func(context.Context, string, string) (net.Conn, error)) (*upnpLink, error) {
	u, err := url.Parse(server.Location)
	if err != nil {
		return nil, err
	}
	host := net.JoinHostPort(u.Hostname(), "15740")
	trace := diagnostics.From(ctx)
	trace.Log("info", "ptp", "ptp.connect", "正在连接相机 PTP/IP 素材服务", nil)
	c, err := ptpip.Dial(ctx, host, ptpip.Options{Name: "CardAtlas", DialContext: dial, IOTimeout: 8 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("连接相机 PTP/IP 失败：%w", err)
	}
	p := &ptpMedia{c: c, host: host, objects: map[uint32]ptpObject{}, gate: make(chan struct{}, 1)}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	good := false
	defer func() {
		if !good {
			p.close()
		}
	}()
	p.di, err = c.GetDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	if !p.di.IsSony() || !strings.EqualFold(strings.TrimSpace(p.di.Model), "ZV-E10") {
		return nil, errPTPModel
	}
	trace.Log("info", "ptp", "ptp.device_ready", "已确认真机型号与 PTP/IP 协议", map[string]string{"model": p.di.Model, "firmware": p.di.DeviceVersion})
	if err = c.OpenSession(ctx, 1); err != nil {
		return nil, err
	}
	p.session = true
	base := "http://" + host + "/ptp/"
	link := newMediaLink(&upnpav.Server{Location: base, BaseURL: base, Manufacturer: p.di.Manufacturer, ModelName: p.di.Model}, &http.Client{Transport: p}, p)
	good = true
	return link, nil
}

func (p *ptpMedia) alive() bool {
	select {
	case <-p.c.Done():
		return false
	default:
		return true
	}
}

func (p *ptpMedia) close() {
	p.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if p.alive() && p.session {
		if p.mode {
			_ = p.c.SonyDisableContentsTransferMode(ctx)
		}
		_ = p.c.CloseSession(ctx)
	}
	_ = p.c.Close()
}

func (p *ptpMedia) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case p.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}
func (p *ptpMedia) unlock() { <-p.gate }

func (l *upnpLink) listPTP(ctx context.Context) ([]deviceimport.Media, string, error) {
	p := l.ptp
	if err := p.lock(ctx); err != nil {
		return nil, "", err
	}
	defer p.unlock()
	storages, err := p.c.GetStorageIDs(ctx)
	if ptpip.IsCode(err, ptpip.RespStoreNotAvailable) {
		err = nil
	}
	// 存储在切换素材模式后异步发布，不能把最初的空数组当成空相册。
	for try := 0; err == nil && len(storages) == 0 && try < 200; try++ {
		// 原生发送模式已经开放存储时保留其会话；只有没有存储时才协商 Sony 素材模式。
		if try == 10 && !p.mode && p.di.Supports(ptpip.OpSonySDIOConnect) && p.di.Supports(ptpip.OpSonySetContentsTransferMode) {
			if _, err = p.c.SonyHandshake(ctx); err != nil {
				return nil, "", fmt.Errorf("建立 Sony 素材会话失败：%w", err)
			}
			// 固件 2.01 在三阶段握手后还需先读取一次状态，随后才会发布素材存储。
			if p.di.Supports(ptpip.OpSonyGetAllExtDevicePropInfo) {
				if _, err = p.c.Transaction(ctx, ptpip.OpSonyGetAllExtDevicePropInfo, []uint32{0}, nil, io.Discard); err != nil {
					return nil, "", fmt.Errorf("同步 Sony 素材会话状态失败：%w", err)
				}
			}
			if err = p.c.SonySetContentsTransferMode(ctx); err != nil {
				return nil, "", fmt.Errorf("开启相机素材读取失败：%w", err)
			}
			p.mode = true
		}
		if try%20 == 0 {
			diagnostics.From(ctx).Log("info", "ptp", "ptp.storage_wait", "正在等待相机发布存储", map[string]string{"elapsed_ms": fmt.Sprint(try * 150)})
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		storages, err = p.c.GetStorageIDs(ctx)
		if ptpip.IsCode(err, ptpip.RespStoreNotAvailable) {
			err = nil
		}
	}
	if err != nil {
		return nil, "", err
	}
	if len(storages) == 0 {
		return nil, "", errors.New("相机尚未发布可读取的存储，请确认存储卡和素材传输界面")
	}
	objects := map[uint32]ptpObject{}
	invalid := 0
	type objectRef struct{ handle, storage uint32 }
	var refs []objectRef
	for _, storage := range storages {
		handles, err := p.c.GetObjectHandles(ctx, storage, 0, ptpip.ParentAll)
		if err != nil {
			return nil, "", err
		}
		if len(handles) > 100000 {
			return nil, "", errors.New("相机对象数量超过读取限制")
		}
		for _, handle := range handles {
			refs = append(refs, objectRef{handle, storage})
		}
	}
	for index, ref := range refs {
		handle := ref.handle
		var object ptpObject
		{
			info, err := p.c.GetObjectInfo(ctx, handle)
			if ptpip.IsCode(err, ptpip.RespInvalidObjectHandle) {
				invalid++
				continue
			}
			if err != nil {
				return nil, "", err
			}
			size := int64(info.CompressedSize)
			if info.CompressedSize == 0xffffffff && !info.IsFolder() {
				value, err := p.c.GetObjectPropValue(ctx, handle, 0xDC04, 8)
				if err != nil || value.Int < 0 {
					return nil, "", errors.New("相机未提供大文件的准确大小，已停止以避免截断素材")
				}
				size = value.Int
			}
			object = ptpObject{info, size}
		}
		if object.info.StorageID == 0 {
			object.info.StorageID = ref.storage
		}
		objects[handle] = object
		if index%200 == 0 {
			diagnostics.From(ctx).Log("info", "ptp", "ptp.list_progress", "正在读取相机对象信息", map[string]string{"read": fmt.Sprint(index + 1), "total": fmt.Sprint(len(refs))})
		}
	}
	out := []deviceimport.Media{}
	items := map[string]upnpav.Item{}
	medias := map[string]deviceimport.Media{}
	for handle, object := range objects {
		info := object.info
		if info.IsFolder() || classify("", info.Filename) == "" {
			continue
		}
		folders := []string{}
		seen := map[uint32]bool{handle: true}
		for parent := info.ParentObject; parent != 0 && parent != 0xffffffff; {
			if seen[parent] || len(folders) > 32 {
				return nil, "", errors.New("相机对象目录存在循环或层级过深")
			}
			seen[parent] = true
			folder, ok := objects[parent]
			if !ok {
				break
			}
			folders = append([]string{folder.info.Filename}, folders...)
			parent = folder.info.ParentObject
		}
		date := ""
		if parsed, err := ptpip.ParseDate(info.CaptureDate, time.Local); err == nil {
			date = parsed.Format("2006-01-02T15:04:05")
		}
		base := "http://" + p.host + "/ptp/"
		item := upnpav.Item{ID: fmt.Sprintf("%08x:%08x", info.StorageID, handle), Title: info.Filename, Res: base + "object/" + strconv.FormatUint(uint64(handle), 10), Thumbnail: base + "thumb/" + strconv.FormatUint(uint64(handle), 10), Size: object.size, Date: date, Folders: folders}
		media, ok := normalize(item, upnpav.Storage{ID: "0", Name: "存储卡"})
		if !ok {
			invalid++
			continue
		}
		if media.Date != "" {
			media.DateSource = "相机媒体服务"
		}
		items[media.ID], medias[media.ID] = item, media
		out = append(out, media)
	}
	p.mu.Lock()
	p.objects = objects
	p.mu.Unlock()
	l.mu.Lock()
	l.items = items
	l.medias = medias
	l.lastOK = time.Now()
	l.mu.Unlock()
	warning := ""
	if invalid > 0 {
		warning = fmt.Sprintf("相机有 %d 个对象已失效或路径无效，已跳过", invalid)
	}
	diagnostics.From(ctx).Log("info", "ptp", "ptp.list_ready", "PTP/IP 素材清单读取完成", map[string]string{"count": fmt.Sprint(len(out)), "skipped": fmt.Sprint(invalid)})
	return out, warning, nil
}

func (p *ptpMedia) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil, errors.New("PTP 素材仅允许读取")
	}
	if r.URL.Scheme != "http" || r.URL.Host != p.host || r.URL.User != nil || r.URL.RawQuery != "" {
		return nil, errors.New("无效的 PTP 素材地址")
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ptp/"), "/")
	if len(parts) != 2 || (parts[0] != "object" && parts[0] != "thumb") || !strings.HasPrefix(r.URL.Path, "/ptp/") {
		return nil, errors.New("无效的 PTP 素材对象")
	}
	id, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	object, ok := p.objects[uint32(id)]
	p.mu.RUnlock()
	response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: r}
	if !ok || object.info.IsFolder() {
		response.StatusCode = http.StatusNotFound
		return response, nil
	}
	if parts[0] == "thumb" {
		if err := p.lock(r.Context()); err != nil {
			return nil, err
		}
		defer p.unlock()
		buffer := &boundedPTPBuffer{max: 8 << 20}
		ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
		defer cancel()
		if _, err := p.c.Transaction(ctx, ptpip.OpGetThumb, []uint32{uint32(id)}, nil, buffer); err != nil {
			return nil, err
		}
		response.Header.Set("Content-Type", http.DetectContentType(buffer.Bytes()))
		response.ContentLength = int64(buffer.Len())
		response.Header.Set("Content-Length", strconv.Itoa(buffer.Len()))
		if r.Method != http.MethodHead {
			response.Body = io.NopCloser(bytes.NewReader(buffer.Bytes()))
		}
		return response, nil
	}
	start, end, status, err := ptpRange(r.Header.Get("Range"), object.size)
	if err != nil {
		response.StatusCode = 416
		response.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", object.size))
		return response, nil
	}
	response.StatusCode = status
	response.ContentLength = end - start
	response.Header.Set("Content-Length", strconv.FormatInt(end-start, 10))
	response.Header.Set("Accept-Ranges", "bytes")
	if kind := previewKind(object.info.Filename); kind != "" {
		response.Header.Set("Content-Type", kind)
	} else {
		response.Header.Set("Content-Type", "application/octet-stream")
	}
	if status == http.StatusPartialContent {
		response.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, object.size))
	}
	if r.Method != http.MethodHead {
		response.Body = &ptpObjectReader{p: p, ctx: r.Context(), handle: uint32(id), offset: start, end: end}
	}
	return response, nil
}

func ptpRange(raw string, size int64) (int64, int64, int, error) {
	if size < 0 {
		return 0, 0, 0, errors.New("无效的素材大小")
	}
	if raw == "" {
		return 0, size, http.StatusOK, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, 0, errors.New("无效的预览范围")
	}
	a, b, ok := strings.Cut(strings.TrimPrefix(raw, "bytes="), "-")
	if !ok || size == 0 {
		return 0, 0, 0, errors.New("预览范围为空")
	}
	var start, end int64
	var err error
	if a == "" {
		count, e := strconv.ParseInt(b, 10, 64)
		if e != nil || count <= 0 {
			return 0, 0, 0, errors.New("无效的后缀范围")
		}
		start, end = max(0, size-count), size
	} else {
		start, err = strconv.ParseInt(a, 10, 64)
		if err != nil || start < 0 || start >= size {
			return 0, 0, 0, errors.New("预览范围超出文件")
		}
		end = size
		if b != "" {
			last, e := strconv.ParseInt(b, 10, 64)
			if e != nil || last < start {
				return 0, 0, 0, errors.New("预览范围无效")
			}
			if last < size-1 {
				end = last + 1
			}
		}
	}
	return start, end, http.StatusPartialContent, nil
}

type boundedPTPBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedPTPBuffer) Write(data []byte) (int, error) {
	if len(data) > b.max-b.Len() {
		return 0, errors.New("相机数据超过本次读取上限")
	}
	return b.Buffer.Write(data)
}

type ptpObjectReader struct {
	p           *ptpMedia
	ctx         context.Context
	handle      uint32
	offset, end int64
	buffer      bytes.Buffer
	closed      atomic.Bool
}

func (r *ptpObjectReader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if r.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.buffer.Len() > 0 {
		return r.buffer.Read(out)
	}
	if r.offset >= r.end {
		return 0, io.EOF
	}
	count := uint32(min(r.end-r.offset, 512<<10))
	buffer := &boundedPTPBuffer{max: int(count)}
	if err := r.p.lock(r.ctx); err != nil {
		return 0, err
	}
	defer r.p.unlock()
	// 每块独立完成事务；浏览器关闭预览时不因取消半包而破坏相机共享会话。
	ctx, cancel := context.WithTimeout(r.p.ctx, 8*time.Second)
	defer cancel()
	var n uint32
	var err error
	if r.p.di.Supports(ptpip.OpSonyGetPartialLargeObject) {
		n, err = r.p.c.SonyGetPartialLargeObject(ctx, r.handle, r.offset, count, buffer)
	} else if r.p.di.Supports(ptpip.OpGetPartialObject) && r.offset <= 0xffffffff {
		n, err = r.p.c.GetPartialObject(ctx, r.handle, uint32(r.offset), count, buffer)
	} else {
		return 0, errors.New("相机未提供可用的分段读取接口")
	}
	if err != nil {
		return 0, err
	}
	if n == 0 || int(n) != buffer.Len() {
		return 0, io.ErrUnexpectedEOF
	}
	r.offset += int64(n)
	r.buffer = *bytes.NewBuffer(buffer.Bytes())
	return r.buffer.Read(out)
}

func (r *ptpObjectReader) Close() error { r.closed.Store(true); return nil }

// 保留编译期接口核对；素材地址的最后一段始终是受控对象句柄。
var _ http.RoundTripper = (*ptpMedia)(nil)
