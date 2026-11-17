package persist

import (
	"testing"
)

// fakeStore 是带 Close 计数的测试用 Store，验证 LayeredStore 是否正确地
// 把 Close 透传到每一层。
type fakeStore struct {
	m      map[string][]byte
	closed int
}

func newFakeStore() *fakeStore { return &fakeStore{m: make(map[string][]byte)} }

func (f *fakeStore) Get(p string) ([]byte, bool) {
	b, ok := f.m[p]
	return b, ok
}
func (f *fakeStore) Put(p string, d []byte) error {
	f.m[p] = d
	return nil
}
func (f *fakeStore) Delete(p string) error {
	delete(f.m, p)
	return nil
}
func (f *fakeStore) Close() error {
	f.closed++
	return nil
}

// TestLayeredStoreWriteBoth 验证写会落到最终层和缓存层。
func TestLayeredStoreWriteBoth(t *testing.T) {
	cache := NewMemoryStore()
	final := NewMemoryStore()
	ls := NewLayeredStore(cache, final)

	if err := ls.Put("a", []byte("X")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if b, ok := cache.Get("a"); !ok || string(b) != "X" {
		t.Fatalf("缓存层应命中, got %q ok=%v", b, ok)
	}
	if b, ok := final.Get("a"); !ok || string(b) != "X" {
		t.Fatalf("最终层应命中, got %q ok=%v", b, ok)
	}
}

// TestLayeredStoreReadBackfill 验证缓存未命中时查最终层并回填缓存。
func TestLayeredStoreReadBackfill(t *testing.T) {
	cache := NewMemoryStore()
	final := NewMemoryStore()
	ls := NewLayeredStore(cache, final)

	ls.Put("a", []byte("X"))
	cache.Delete("a") // 模拟缓存被逐出

	b, ok := ls.Get("a")
	if !ok || string(b) != "X" {
		t.Fatalf("最终层应兜住, got %q ok=%v", b, ok)
	}
	// 回填：缓存现在又有了，下次读走缓存。
	if b2, ok2 := cache.Get("a"); !ok2 || string(b2) != "X" {
		t.Fatalf("应回填缓存, got %q ok=%v", b2, ok2)
	}
}

// TestLayeredStoreDeleteBoth 验证删除会清掉两层。
func TestLayeredStoreDeleteBoth(t *testing.T) {
	cache := NewMemoryStore()
	final := NewMemoryStore()
	ls := NewLayeredStore(cache, final)

	ls.Put("a", []byte("X"))
	if err := ls.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := cache.Get("a"); ok {
		t.Fatal("缓存层应已删除")
	}
	if _, ok := final.Get("a"); ok {
		t.Fatal("最终层应已删除")
	}
}

// TestLayeredStoreCacheMiss 验证两层都没有时返回 (nil, false)。
func TestLayeredStoreCacheMiss(t *testing.T) {
	ls := NewLayeredStore(NewMemoryStore(), NewMemoryStore())
	if b, ok := ls.Get("nope"); ok || b != nil {
		t.Fatalf("两层都空应返回 (nil,false), got %q ok=%v", b, ok)
	}
}

// TestLayeredStoreCloseClosesBoth 验证 Close 透传到两层。
func TestLayeredStoreCloseClosesBoth(t *testing.T) {
	c := newFakeStore()
	f := newFakeStore()
	ls := NewLayeredStore(c, f)
	if err := ls.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if c.closed != 1 || f.closed != 1 {
		t.Fatalf("两层都应被 Close 一次, cache=%d final=%d", c.closed, f.closed)
	}
}
