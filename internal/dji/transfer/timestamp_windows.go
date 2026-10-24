//go:build windows

package transfer

import (
	"golang.org/x/sys/windows"
	"os"
	"time"
)

// 通过已打开的句柄设置时间，避免文件路径被替换。
func SetCaptureTime(file *os.File, cameraDate string) error {
	stamp, err := time.ParseInLocation("2006-01-02 15:04:05", cameraDate, time.Local)
	if err != nil {
		return err
	}
	modified := windows.NsecToFiletime(stamp.UnixNano())
	return windows.SetFileTime(windows.Handle(file.Fd()), nil, nil, &modified)
}
