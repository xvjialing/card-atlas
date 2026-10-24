package diagnostics

import "syscall"

func systemVersion() string {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return "未知"
	}
	return v
}
