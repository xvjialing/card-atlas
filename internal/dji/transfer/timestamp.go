//go:build !windows

package transfer

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// SetCaptureTime 将相机清单的本地墙上时间写入修改日期，不改变素材内容。
// 清单不带时区，按当前 Mac 的本地时区解释，保持 Finder 与素材列表显示一致。
// 对已打开的文件描述符操作，避免路径在校验后被换成符号链接。
func SetCaptureTime(file *os.File, cameraDate string) error {
	stamp, err := time.ParseInLocation("2006-01-02 15:04:05", cameraDate, time.Local)
	if err != nil || stamp.Year() < 1980 || stamp.Year() > 2107 {
		return fmt.Errorf("相机未提供有效的拍摄时间")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	err = raw.Control(func(fd uintptr) {
		setErr = unix.Futimes(int(fd), []unix.Timeval{
			unix.NsecToTimeval(time.Now().UnixNano()),
			unix.NsecToTimeval(stamp.UnixNano()),
		})
	})
	if err != nil {
		return err
	}
	if setErr != nil {
		return setErr
	}
	return file.Sync()
}
