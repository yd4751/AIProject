package persist

import (
	"os"
	"testing"
)

// redisAddr 从环境变量读测试用的 Redis 地址。CI 或本机没装 Redis 时
// 返回空，测试自动 skip——Redis 后端是可选能力，不该让没装 Redis 的
// 环境跑不过测试。
func redisAddr(t *testing.T) string {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	s, err := NewRedisStore(addr, "", 15, "test:persist:")
	if err != nil {
		t.Skipf("跳过 Redis 集成测试（连不上 %s）: %v", addr, err)
	}
	s.Close()
	return addr
}

// TestRedisStoreRoundTrip 验证 Redis 后端的读写删。连不上 Redis 自动 skip。
func TestRedisStoreRoundTrip(t *testing.T) {
	addr := redisAddr(t)
	s, err := NewRedisStore(addr, "", 15, "test:roundtrip:")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	// 清空测试键，避免上次残留。
	_ = s.Delete("alice")

	if _, ok := s.Get("alice"); ok {
		t.Fatal("新键不该有数据")
	}
	if err := s.Put("alice", []byte(`{"hp":100}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	b, ok := s.Get("alice")
	if !ok || string(b) != `{"hp":100}` {
		t.Fatalf("get 应返回写入值, got %q ok=%v", b, ok)
	}
	if err := s.Delete("alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.Get("alice"); ok {
		t.Fatal("删除后不该再读到")
	}
}

// TestRedisStorePrefix 验证 key 前缀隔离：不同前缀的 store 互不干扰。
func TestRedisStorePrefix(t *testing.T) {
	addr := redisAddr(t)
	a, err := NewRedisStore(addr, "", 15, "test:prefix-a:")
	if err != nil {
		t.Fatalf("connect a: %v", err)
	}
	defer a.Close()
	b, err := NewRedisStore(addr, "", 15, "test:prefix-b:")
	if err != nil {
		t.Fatalf("connect b: %v", err)
	}
	defer b.Close()

	_ = a.Delete("p1")
	_ = b.Delete("p1")

	if err := a.Put("p1", []byte(`{"hp":1}`)); err != nil {
		t.Fatalf("put a: %v", err)
	}
	if _, ok := b.Get("p1"); ok {
		t.Fatal("前缀隔离失效：b 读到了 a 的键")
	}
	_ = a.Delete("p1")
	_ = b.Delete("p1")
}

// TestRedisStoreConnectFail 验证连不上时 NewRedisStore 返回错误。
func TestRedisStoreConnectFail(t *testing.T) {
	// 用 3 秒超时 + 一个几乎不可能监听的端口。故意用很短的超时——但
	// NewRedisStore 内部 Ping 超时固定 3 秒，这里只验证"连不上报错"的
	// 语义，用一个不存在的端口。
	_, err := NewRedisStore("127.0.0.1:1", "", 0, "test:")
	if err == nil {
		t.Fatal("连不上 Redis 应返回错误")
	}
}
