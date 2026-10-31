package diagnostics

import (
	"os"
	"path/filepath"
	"sync"
)

var sharedStores = struct {
	sync.Mutex
	stores map[string]*Store
}{stores: map[string]*Store{}}

func DefaultBase() string {
	if base := os.Getenv("CARDATLAS_DEVICE_DATA"); base != "" {
		return base
	}
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "CardAtlas", "devices")
}

// Shared 让界面、框架和设备共用写入锁、脱敏词典与实时设置。New 仍用于独立存储和测试。
func Shared(base string) *Store {
	if absolute, err := filepath.Abs(base); err == nil {
		base = absolute
	}
	sharedStores.Lock()
	defer sharedStores.Unlock()
	if s := sharedStores.stores[base]; s != nil {
		return s
	}
	s := New(base)
	sharedStores.stores[base] = s
	return s
}
