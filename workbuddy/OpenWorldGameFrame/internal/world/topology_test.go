package world

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

func newTestWorld(t *testing.T) (*World, *Scene, *Scene) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log)
	// Chunk 默认 512m，所以场景边界必须按 512 对齐。
	a := w.NewScene(1, 50*time.Millisecond, nil, log, WithBounds(Rect{MinX: 0, MinZ: 0, MaxX: 512, MaxZ: 512}))
	b := w.NewScene(2, 50*time.Millisecond, nil, log, WithBounds(Rect{MinX: 512, MinZ: 0, MaxX: 1024, MaxZ: 512}))
	return w, a, b
}

func TestPartitionCreatesScenesWithBounds(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log)
	scenes := w.Partition(2, 3, 512, 50*time.Millisecond, nil, log, 10)
	if len(scenes) != 6 {
		t.Fatalf("应创建 6 个场景，实际 %d", len(scenes))
	}
	if got := w.Count(); got != 6 {
		t.Fatalf("World 应有 6 个场景，实际 %d", got)
	}
	// 行优先编号：10..15
	for i, s := range scenes {
		want := uint32(10 + i)
		if s.ID() != want {
			t.Fatalf("第 %d 个场景 ID 应为 %d，实际 %d", i, want, s.ID())
		}
	}
	// 右下角那个场景应该是 [1024,1536) × [512,1024)
	last := scenes[5].Bounds()
	if last.MinX != 1024 || last.MinZ != 512 || last.MaxX != 1536 || last.MaxZ != 1024 {
		t.Fatalf("最后一个场景边界不对: %s", last)
	}
}

func TestRouteByChunk(t *testing.T) {
	_, a, b := newTestWorld(t)

	cases := []struct {
		x, z float32
		want *Scene
	}{
		{0, 0, a},
		{511.9, 511.9, a},
		{512, 0, b}, // 半开区间：边界点属于右边那个场景
		{1023, 511, b},
		{1024, 100, nil}, // 界外
		{-1, 100, nil},
	}
	for _, c := range cases {
		got, ok := a.world.Route(c.x, c.z)
		if c.want == nil {
			if ok {
				t.Fatalf("(%v,%v) 不该路由到任何场景，实际 %d", c.x, c.z, got.ID())
			}
			continue
		}
		if !ok || got != c.want {
			t.Fatalf("(%v,%v) 路由错误：期望场景 %d，实际 ok=%v", c.x, c.z, c.want.ID(), ok)
		}
	}
}

func TestRouteFallsBackToDefaultWithoutBounds(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log)
	s := w.NewScene(1, 50*time.Millisecond, nil, log) // 不设边界
	for _, p := range [][2]float32{{0, 0}, {99999, -99999}} {
		got, ok := w.Route(p[0], p[1])
		if !ok || got != s {
			t.Fatalf("没有拓扑时应回落到默认场景，(%v,%v) 失败", p[0], p[1])
		}
	}
}

func TestTopologyRejectsMisalignedBounds(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log)
	// 300 不是 512 的整数倍：一个 Chunk 会横跨两个场景。
	s := NewScene(1, 50*time.Millisecond, nil, log, WithBounds(Rect{0, 0, 300, 300}))
	w.Add(s)

	if got := w.Chunks(); len(got) != 0 {
		t.Fatalf("未对齐的边界不该登记进拓扑，实际登记了 %d 个 Chunk", len(got))
	}
	// 降级而不是崩：拓扑为空时路由回落到默认场景，进程照常服务。
	// 代价是所有玩家都挤在场景 1——这正是"Chunks 为空"要在控制台上
	// 显眼展示的原因：配错拓扑不会报错，只会让负载分布变得很怪。
	got, ok := w.Route(10, 10)
	if !ok || got.ID() != 1 {
		t.Fatalf("拓扑为空时应降级到默认场景 1，实际 ok=%v", ok)
	}
}

func TestTopologyRejectsOverlap(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorld(log)
	w.NewScene(1, 50*time.Millisecond, nil, log, WithBounds(Rect{0, 0, 512, 512}))
	w.NewScene(2, 50*time.Millisecond, nil, log, WithBounds(Rect{0, 0, 512, 512})) // 完全重叠

	// 第二个场景被拒绝登记：它那块地的归属仍然是场景 1。
	got, ok := w.Route(100, 100)
	if !ok || got.ID() != 1 {
		t.Fatalf("重叠边界应被拒绝，路由应仍指向场景 1，实际 ok=%v", ok)
	}
}

func TestChunksCoverWholeRect(t *testing.T) {
	w, _, _ := newTestWorld(t)
	chunks := w.Chunks()
	// 两个 512×512 的场景，Chunk 也是 512，所以各 1 个 Chunk。
	if len(chunks) != 2 {
		t.Fatalf("应有 2 个 Chunk，实际 %d", len(chunks))
	}
	if chunks[0].SceneID != 1 || chunks[1].SceneID != 2 {
		t.Fatalf("Chunk 归属不对: %+v", chunks)
	}
}

func TestAssignDistributesPlayers(t *testing.T) {
	_, a, b := newTestWorld(t)
	w := a.world

	counts := map[uint32]int{}
	for i := 0; i < 300; i++ {
		name := fmt.Sprintf("player-%d", i)
		s, x, z := w.Assign(name)
		if s == nil {
			t.Fatal("Assign 返回了空场景")
		}
		if !s.bounds.Contains(x, z) {
			t.Fatalf("%s 的落点 (%.1f,%.1f) 不在场景 %d 的 %s 内", name, x, z, s.ID(), s.bounds)
		}
		counts[s.ID()]++
	}
	if len(counts) != 2 {
		t.Fatalf("300 个玩家应该被分到两个场景，实际只用到 %d 个", len(counts))
	}
	// 散列分布不该严重倾斜：2 个场景各 150，允许 ±30% 偏差。
	for id, n := range counts {
		if n < 105 || n > 195 {
			t.Fatalf("场景 %d 分到 %d 人，分布太偏", id, n)
		}
	}
	// 同一个玩家两次分配必须落在同一个场景、同一个落点。
	s1, x1, z1 := w.Assign("alice")
	s2, x2, z2 := w.Assign("alice")
	if s1 != s2 || x1 != x2 || z1 != z2 {
		t.Fatalf("同一玩家两次分配结果不一致：%d/%v vs %d/%v",
			s1.ID(), [2]float32{x1, z1}, s2.ID(), [2]float32{x2, z2})
	}
	_ = b
}
