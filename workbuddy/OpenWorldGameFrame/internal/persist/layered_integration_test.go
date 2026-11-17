package persist

import (
	"os"
	"testing"
)

// TestLayeredStoreRealBackends 用真实 MySQL + Redis 验证分层语义里最关键的
// 一条：缓存被逐出后，读请求要能从最终层取回数据并回填缓存。
//
// MemoryStore 的 fake 测试（layered_test.go）只能证明调用顺序，证明不了
// 两个网络后端连起来真的能工作——键前缀、Ping、连接池、字符编码都得在
// 真环境里跑一遍。任一服务不可用即 skip：本机开发能跑，CI 无外部依赖
// 时自动略过，不给流水线添堵。
func TestLayeredStoreRealBackends(t *testing.T) {
	dsn := mysqlDSN(t) // 连不上 MySQL 会自行 skip，并尝试自举建库
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	cache, err := NewRedisStore(addr, "", 0, "ow-layered-test:")
	if err != nil {
		t.Skipf("跳过真实后端分层测试（连不上 Redis %s）: %v", addr, err)
	}
	defer cache.Close()
	final, err := NewMySQLStore(dsn)
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	defer final.Close()

	ls := NewLayeredStore(cache, final)
	const p = "layered-real-probe"
	_ = ls.Delete(p)
	defer ls.Delete(p)

	payload := []byte(`{"hp":42,"level":7}`)
	if err := ls.Put(p, payload); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 模拟缓存逐出（Redis 内存淘汰 / 运维手工清 key）。
	if err := cache.Delete(p); err != nil {
		t.Fatalf("evict cache: %v", err)
	}
	if b, ok := final.Get(p); !ok || string(b) != string(payload) {
		t.Fatalf("最终层应始终保留数据, got %q ok=%v", b, ok)
	}

	// 分层读取：缓存未命中 → 查最终层 → 回填缓存。
	b, ok := ls.Get(p)
	if !ok || string(b) != string(payload) {
		t.Fatalf("缓存逐出后应从最终层读回, got %q ok=%v", b, ok)
	}
	if b2, ok2 := cache.Get(p); !ok2 || string(b2) != string(payload) {
		t.Fatalf("应回填缓存, got %q ok=%v", b2, ok2)
	}
}
