package persist

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMemoryStoreRoundTrip 验证内存存储的读写删。
func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	if _, ok := s.Get("alice"); ok {
		t.Fatal("新 store 不该有数据")
	}
	if err := s.Put("alice", []byte(`{"hp":100}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	b, ok := s.Get("alice")
	if !ok || string(b) != `{"hp":100}` {
		t.Fatalf("get 应返回写入值, got %q ok=%v", b, ok)
	}
	// Get 返回副本，修改不影响内部。
	b[0] = 'X'
	if b2, _ := s.Get("alice"); b2[0] == 'X' {
		t.Fatal("Get 应返回副本")
	}
	if err := s.Delete("alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.Get("alice"); ok {
		t.Fatal("删除后不该再读到")
	}
}

// TestFileStoreRoundTrip 验证文件存储的读写删 + 原子写。
func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir)

	if _, ok := s.Get("bob"); ok {
		t.Fatal("新目录不该有档案")
	}
	if err := s.Put("bob", []byte(`{"hp":250}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	// 落盘文件应存在。
	if _, err := os.Stat(filepath.Join(dir, "bob.json")); err != nil {
		t.Fatalf("档案文件应落盘: %v", err)
	}
	b, ok := s.Get("bob")
	if !ok || string(b) != `{"hp":250}` {
		t.Fatalf("get 应返回写入值, got %q ok=%v", b, ok)
	}
	if err := s.Delete("bob"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.Get("bob"); ok {
		t.Fatal("删除后不该再读到")
	}
}

// TestPlayerRecordMarshal 验证档案的 JSON 往返。
func TestPlayerRecordMarshal(t *testing.T) {
	r := PlayerRecord{
		Player: "carol", HP: 800, MaxHP: 1000, MP: 150, MaxMP: 200,
		Level: 5, Attack: 130, Defense: 25, CritRate: 20, CritMult: 160,
		Learned: 1<<1 | 1<<4,
	}
	b, err := r.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalPlayerRecord(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HP != 800 || got.Level != 5 || got.Learned != 1<<1|1<<4 {
		t.Fatalf("往返后字段不一致: %+v", got)
	}
}
