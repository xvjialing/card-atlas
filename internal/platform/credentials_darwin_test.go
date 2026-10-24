package platform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
)

// 显式启用的本机集成测试：只创建并删除随机测试条目，不读取任何用户凭据。
func TestCredentialsNativeRoundTrip(t *testing.T) {
	if os.Getenv("OSMO_TEST_KEYCHAIN") != "1" {
		t.Skip("通过 OSMO_TEST_KEYCHAIN=1 显式启用本机钥匙串集成测试")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	account := "osmo-test-" + hex.EncodeToString(random[:])
	store := Credentials{}
	t.Cleanup(func() {
		if err := store.Delete(account); err != nil {
			t.Errorf("清理随机测试条目失败：%v", err)
		}
	})
	if found, err := store.Has(account); err != nil || found {
		t.Fatalf("测试条目初始状态异常：%v", err)
	}
	if err := store.Set(account, "FixtureOnly42"); err != nil {
		t.Fatal(err)
	}
	if found, err := store.Has(account); err != nil || !found {
		t.Fatalf("未找到已保存的测试条目：%v", err)
	}
	// 新存储实例读取，验证数据不依赖 Go 内存缓存。
	if value, err := (Credentials{}).Get(account); err != nil || value != "FixtureOnly42" {
		t.Fatalf("读取测试条目失败：%v", err)
	}
	if err := store.Set(account, "UpdatedFixture42"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get(account); err != nil || value != "UpdatedFixture42" {
		t.Fatalf("更新测试条目失败：%v", err)
	}
	if err := store.Delete(account); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(account); !errors.Is(err, ErrCredentialMissing) {
		t.Fatalf("删除后应不存在：%v", err)
	}
}
