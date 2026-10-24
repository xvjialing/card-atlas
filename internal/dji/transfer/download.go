package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cardatlas/internal/diagnostics"
	"cardatlas/internal/dji"
)

type Progress struct {
	Bytes int64   `json:"bytes"`
	Total int64   `json:"total"`
	Speed float64 `json:"speed"`
	Phase string  `json:"phase"`
}
type Result struct {
	Path    string
	SHA256  string
	Bytes   int64
	Warning string
}

// Client 禁止代理和跨地址重定向，确保素材只从当前相机直连读取。
func Client() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 8 * time.Second, DisableCompression: true}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("不允许相机下载重定向") }}
}

type identity struct {
	Camera  string
	Path    string
	Storage int
	Size    int64
	Date    string
}

func Download(ctx context.Context, client *http.Client, host, device, root string, m camera.Media, progress func(Progress)) (Result, error) {
	var result Result
	if !filepath.IsAbs(root) || !camera.SafePath(m.Path) || m.Storage < 0 || m.Storage > 1 {
		return result, errors.New("无效的下载位置或相机文件路径")
	}
	key := sha256.Sum256([]byte(device))
	relative := filepath.Join(hex.EncodeToString(key[:6]), map[int]string{0: "SD卡", 1: "内置存储"}[m.Storage], filepath.FromSlash(m.Path))
	return DownloadTo(ctx, client, host, device, root, relative, m, progress)
}

// DownloadTo 接收通用导入规划生成的相对路径，协议层只负责安全传输。
func DownloadTo(ctx context.Context, client *http.Client, host, device, root, relative string, m camera.Media, progress func(Progress)) (Result, error) {
	trace := diagnostics.From(ctx)
	var result Result
	if !filepath.IsAbs(root) || !filepath.IsLocal(relative) || !camera.SafePath(m.Path) || m.Storage < 0 || m.Storage > 1 {
		return result, errors.New("无效的下载位置或相机文件路径")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return result, err
	}
	// os.Root 限制符号链接和路径跳转，不允许相机文件名逃逸用户选择的目录。
	r, err := os.OpenRoot(root)
	if err != nil {
		return result, err
	}
	defer r.Close()
	if err = r.MkdirAll(filepath.Dir(relative), 0755); err != nil {
		return result, err
	}
	if _, err = r.Stat(relative); err == nil {
		return result, errors.New("目标文件已存在，为避免覆盖，请更换导入位置或检查历史记录")
	}
	part, meta := relative+".part", relative+".part.json"
	for _, name := range []string{part, meta} {
		if st, e := r.Lstat(name); e == nil && !st.Mode().IsRegular() {
			return result, errors.New("续传文件不是普通文件，已停止以保护本地数据")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return result, e
		}
	}
	id := identity{device, m.Path, m.Storage, m.Size, m.Date}
	encoded, _ := json.Marshal(id)
	if old, e := r.ReadFile(meta); e == nil {
		var existing identity
		if json.Unmarshal(old, &existing) != nil || existing != id {
			return result, errors.New("已有临时文件与当前素材不一致，已停止续传")
		}
	} else {
		if _, e = r.Stat(part); e == nil {
			return result, errors.New("临时文件缺少来源记录，无法安全续传")
		}
		if e = r.WriteFile(meta, encoded, 0600); e != nil {
			return result, e
		}
	}
	f, err := r.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer f.Close()
	var lastErr error
	var total int64
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		stat, e := f.Stat()
		if e != nil {
			return result, e
		}
		offset := stat.Size()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, camera.MediaURL(host, m.Storage, m.Path), nil)
		if e != nil {
			return result, e
		}
		trace.Log("debug", "transfer", "http.request", "请求原始文件数据", map[string]string{"attempt": fmt.Sprint(attempt + 1), "offset": fmt.Sprint(offset)})
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("Accept-Encoding", "identity")
		resp, e := client.Do(req)
		if e != nil {
			lastErr = e
			trace.Log("warn", "transfer", "http.request_failed", "相机 HTTP 请求失败，将尝试重试", map[string]string{"attempt": fmt.Sprint(attempt + 1), "error": e.Error()})
			if !retryWait(ctx, attempt) {
				return result, ctx.Err()
			}
			continue
		}
		trace.Log("debug", "transfer", "http.response", "收到相机 HTTP 响应", map[string]string{"status": fmt.Sprint(resp.StatusCode), "content_length": fmt.Sprint(resp.ContentLength), "content_range": resp.Header.Get("Content-Range")})
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			n, e := strconv.ParseInt(strings.TrimPrefix(resp.Header.Get("Content-Range"), "bytes */"), 10, 64)
			resp.Body.Close()
			if e == nil && n > 0 && offset == n {
				total = n
				if !sameLength(m.Size, total) {
					return result, errors.New("远端文件长度已变化，停止续传")
				}
				lastErr = nil
				break
			}
			return result, errors.New("相机拒绝续传范围，请检查素材是否被修改")
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			resp.Body.Close()
			lastErr = fmt.Errorf("相机返回 HTTP %d", resp.StatusCode)
			if resp.StatusCode == http.StatusNotFound {
				lastErr = errors.New("相机未找到原文件（HTTP 404），请刷新素材列表后重试；此错误发生在文件读取阶段")
			}
			trace.Log("warn", "transfer", "http.status_error", "相机返回错误状态", map[string]string{"status": fmt.Sprint(resp.StatusCode), "attempt": fmt.Sprint(attempt + 1)})
			if resp.StatusCode < 500 {
				return result, lastErr
			}
			if !retryWait(ctx, attempt) {
				return result, ctx.Err()
			}
			continue
		}
		if resp.StatusCode == 206 {
			var start, end int64
			if _, e = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); e != nil || start != offset || end < start || total <= end || resp.ContentLength != end-start+1 {
				resp.Body.Close()
				return result, errors.New("相机续传范围与本地文件不一致，已停止以防文件损坏")
			}
		} else {
			total = resp.ContentLength
			if total <= 0 {
				resp.Body.Close()
				return result, errors.New("相机未提供原文件长度，无法确认完整下载")
			}
			if offset > 0 {
				trace.Log("warn", "transfer", "http.range_ignored", "相机未接受续传，已安全从头开始", map[string]string{"previous_bytes": fmt.Sprint(offset)})
			}
			// 服务端忽略 Range 时重新开始，禁止将完整文件追加到旧片段。
			if e = f.Truncate(0); e != nil {
				resp.Body.Close()
				return result, e
			}
			offset = 0
		}
		if !sameLength(m.Size, total) {
			trace.Log("error", "transfer", "http.length_changed", "文件长度与清单不符", map[string]string{"expected_bytes": fmt.Sprint(m.Size), "actual_bytes": fmt.Sprint(total)})
			resp.Body.Close()
			return result, errors.New("相机文件长度与素材列表不符，请刷新列表后重试")
		}
		if _, e = f.Seek(offset, io.SeekStart); e != nil {
			resp.Body.Close()
			return result, e
		}
		startTime := time.Now()
		written := offset
		lastProgress := time.Time{}
		buf := make([]byte, 256*1024)
		// 读超时关闭响应，避免热点丢失后永久停留在下载中。
		idle := time.AfterFunc(12*time.Second, func() { resp.Body.Close() })
		for {
			n, readErr := resp.Body.Read(buf)
			idle.Reset(12 * time.Second)
			if n > 0 {
				if written+int64(n) > total {
					lastErr = errors.New("相机响应超过声明长度")
					break
				}
				wn, we := f.Write(buf[:n])
				written += int64(wn)
				if we != nil {
					lastErr = we
					break
				}
				if time.Since(lastProgress) > 150*time.Millisecond {
					progress(Progress{written, total, float64(written-offset) / time.Since(startTime).Seconds(), "downloading"})
					lastProgress = time.Now()
				}
			}
			if readErr != nil {
				lastErr = readErr
				if errors.Is(readErr, io.EOF) && written == total {
					lastErr = nil
				}
				break
			}
			if ctx.Err() != nil {
				lastErr = ctx.Err()
				break
			}
		}
		idle.Stop()
		resp.Body.Close()
		if e = f.Sync(); e != nil {
			return result, e
		}
		if lastErr == nil {
			progress(Progress{written, total, 0, "fingerprinting"})
			break
		}
		trace.Log("warn", "transfer", "http.body_interrupted", "传输中断，保留片段并尝试续传", map[string]string{"attempt": fmt.Sprint(attempt + 1), "bytes": fmt.Sprint(written), "error": lastErr.Error()})
		if !retryWait(ctx, attempt) {
			return result, ctx.Err()
		}
	}
	if lastErr != nil {
		return result, fmt.Errorf("下载中断，已保留临时文件，可重试：%w", lastErr)
	}
	stat, err := f.Stat()
	if err != nil {
		return result, err
	}
	if total <= 0 || stat.Size() != total {
		return result, errors.New("文件长度不完整，已保留临时文件")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	progress(Progress{total, total, 0, "fingerprinting"})
	hash := sha256.New()
	if _, err = io.Copy(hash, &contextReader{ctx: ctx, reader: f}); err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	warning := ""
	if err := SetCaptureTime(f, m.Date); err != nil {
		warning = "原文件已完整保存，但无法保留相机拍摄时间，请查看诊断日志"
		trace.Log("warn", "transfer", "transfer.timestamp_failed", "无法保留相机拍摄时间", map[string]string{"error": err.Error()})
	}
	if err = f.Close(); err != nil {
		return result, err
	}
	// 优先用硬链接原子发布；外接 exFAT 等不支持硬链接时，独占创建并读回核验。
	if err = r.Link(part, relative); err != nil {
		if errors.Is(err, os.ErrExist) {
			return result, err
		}
		progress(Progress{total, total, 0, "publishing"})
		if err = publishCopy(ctx, r, part, relative, hex.EncodeToString(hash.Sum(nil))); err != nil {
			return result, err
		}
	}
	_ = r.Remove(part)
	_ = r.Remove(meta)
	return Result{Path: filepath.Join(root, relative), SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: total, Warning: warning}, nil
}

// publishCopy 不覆盖现有文件，失败清理本次目标，完整的 .part 始终保留以供重试。
func publishCopy(ctx context.Context, root *os.Root, part, target, digest string) (err error) {
	source, err := root.Open(part)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	out, err := root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		if err != nil {
			_ = root.Remove(target)
		}
	}()
	n, err := io.Copy(out, &contextReader{ctx: ctx, reader: source})
	if err != nil {
		return err
	}
	if n != info.Size() {
		return errors.New("最终文件写入长度不完整，已保留续传文件")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err = io.Copy(h, &contextReader{ctx: ctx, reader: out}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("最终文件读回校验失败，已保留续传文件")
	}
	if err = SetCaptureTime(out, info.ModTime().Format("2006-01-02 15:04:05")); err != nil {
		return err
	}
	return out.Close()
}

func retryWait(ctx context.Context, attempt int) bool {
	t := time.NewTimer(time.Duration(attempt+1) * 300 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// 清单长度在部分机型上只有 32 位，允许大文件按该位宽回绕。
func sameLength(manifest, total int64) bool {
	return manifest == 0 || total == manifest || total%(1<<32) == manifest
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}
