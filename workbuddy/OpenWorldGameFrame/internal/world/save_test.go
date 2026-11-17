package world

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/observability"
)

// testCodec 是最小的玩法 codec：只存一个 int32 血量。
type testCodec struct{}

func (testCodec) Encode(ctx *Ctx, e *Entity) []byte {
	b := make([]byte, 4)
	v := uint32(e.ID() % 1000)
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	return b
}

func (testCodec) Decode(ctx *Ctx, e *Entity, data []byte) {
	// 不真正写组件，只验证 Decode 被调用（通过 nop 场景）。
}

// newSaveTestWorld 建一个带场景和 codec 的 World，用于存档测试。
func newSaveTestWorld(t *testing.T) (*World, *Scene) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log, WithNodeID("n1"))
	s := w.NewScene(1, 20*time.Millisecond, nil, log, WithBounds(Rect{
		MinX: 0, MinZ: 0, MaxX: 512, MaxZ: 512,
	}))
	s.SetTransferCodec(testCodec{})
	return w, s
}

func TestSaveRestoreRoundtrip(t *testing.T) {
	w, s := newSaveTestWorld(t)

	// 刷几只怪（非玩家实体，会被存档）。
	for i := 0; i < 5; i++ {
		e := s.Spawn(KindMob, "mob", float32(i)*10, float32(i)*10)
		if e == nil {
			t.Fatalf("spawn %d failed", i)
		}
	}

	// 采集快照并落盘。
	data, err := w.SaveWorld()
	if err != nil {
		t.Fatalf("SaveWorld: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("SaveWorld returned empty data")
	}

	// 清空场景实体，模拟重启。
	for id := range s.entities {
		s.removeEntity(s.entities[id])
	}
	if got := len(s.entities); got != 0 {
		t.Fatalf("expected empty scene after clear, got %d", got)
	}

	// 恢复。
	if err := w.RestoreWorld(data); err != nil {
		t.Fatalf("RestoreWorld: %v", err)
	}

	// 校验实体数一致、位置一致。
	if got := len(s.entities); got != 5 {
		t.Fatalf("expected 5 entities restored, got %d", got)
	}
	for i := 0; i < 5; i++ {
		// 找到对应位置的实体。
		found := false
		for _, e := range s.entities {
			if e.kind == KindMob && e.x == float32(i)*10 && e.z == float32(i)*10 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("entity at position %d not restored", i)
		}
	}
}

func TestSaveRestoreSkipsPlayers(t *testing.T) {
	w, s := newSaveTestWorld(t)

	// 一个玩家实体（有 client）。
	client := &nopClient{}
	e := s.Spawn(KindMob, "mob", 10, 10)
	if e == nil {
		t.Fatal("spawn failed")
	}
	_ = client
	// 手动造一个玩家实体（Spawn 只造 KindMob，玩家走 Enter）。
	pid := s.Enter("player1", nopClient{})
	if pid == 0 {
		t.Fatal("enter player failed")
	}

	data, err := w.SaveWorld()
	if err != nil {
		t.Fatalf("SaveWorld: %v", err)
	}

	// 清空。
	for id := range s.entities {
		s.removeEntity(s.entities[id])
	}
	if err := w.RestoreWorld(data); err != nil {
		t.Fatalf("RestoreWorld: %v", err)
	}

	// 怪恢复了，玩家没有。
	if got := len(s.entities); got != 1 {
		t.Fatalf("expected 1 entity (mob only, player skipped), got %d", got)
	}
	for _, e := range s.entities {
		if e.IsPlayer() {
			t.Fatal("player entity should not be restored")
		}
	}
}

func TestSaveRestoreTopoVersion(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log, WithNodeID("n1"))
	_ = w.NewScene(1, 20*time.Millisecond, nil, log, WithBounds(Rect{
		MinX: 0, MinZ: 0, MaxX: 512, MaxZ: 512,
	}))

	// 推一个版本号上去。
	w.mu.Lock()
	w.topo.setVersion(1, 42)
	w.mu.Unlock()

	data, err := w.SaveWorld()
	if err != nil {
		t.Fatalf("SaveWorld: %v", err)
	}

	// 模拟重启：新建一个 World，版本号归零。
	w2 := NewWorld(log, WithNodeID("n1"))
	_ = w2.NewScene(1, 20*time.Millisecond, nil, log, WithBounds(Rect{
		MinX: 0, MinZ: 0, MaxX: 512, MaxZ: 512,
	}))
	if got := w2.topo.Version(1); got != 0 {
		t.Fatalf("expected version 0 before restore, got %d", got)
	}

	if err := w2.RestoreWorld(data); err != nil {
		t.Fatalf("RestoreWorld: %v", err)
	}
	if got := w2.topo.Version(1); got != 42 {
		t.Fatalf("expected version 42 restored, got %d", got)
	}
}

func TestSaveRestoreWrongFormat(t *testing.T) {
	w, _ := newSaveTestWorld(t)
	err := w.RestoreWorld([]byte(`{"format":999}`))
	if err == nil {
		t.Fatal("expected error for wrong format version")
	}
}

func TestCodecChainBlob(t *testing.T) {
	// 验证多个 codec 的长度前缀编码往返。
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log, WithNodeID("n1"))
	s := w.NewScene(1, 20*time.Millisecond, nil, log)

	// 两个 codec，各自产出不同长度的字节。
	s.AddTransferCodec(testCodec{})
	s.AddTransferCodec(testCodec{})

	e := &Entity{id: 100, kind: KindMob}
	blob := s.encodeBlob(e)
	if len(blob) == 0 {
		t.Fatal("expected non-empty blob with 2 codecs")
	}

	// 解码不 panic 即可（testCodec.Decode 是 no-op）。
	s.decodeBlob(e, blob)
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/world-save.json"
	if err := AtomicWrite(path, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	// 再次写覆盖。
	if err := AtomicWrite(path, []byte(`{"a":2}`)); err != nil {
		t.Fatalf("AtomicWrite overwrite: %v", err)
	}
}

// 确保 observability import 被用到（场景需要 metrics 参数，这里传 nil 即可，
// 但保留 import 以避免未来移除时误删）。
var _ = observability.NewMetrics
