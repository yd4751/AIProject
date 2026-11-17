package aoi

import (
	"math/rand"
	"testing"
)

func TestCellOfHandlesNegativeCoordinates(t *testing.T) {
	g := NewGrid[int](10, 1)

	cases := []struct {
		pos  Pos
		want Cell
	}{
		{Pos{X: 0, Z: 0}, Cell{0, 0}},
		{Pos{X: 9.9, Z: 9.9}, Cell{0, 0}},
		{Pos{X: 10, Z: 10}, Cell{1, 1}},
		{Pos{X: -0.1, Z: -0.1}, Cell{-1, -1}},
		{Pos{X: -10, Z: -10}, Cell{-1, -1}},
		{Pos{X: -10.1, Z: -10.1}, Cell{-2, -2}},
	}
	for _, c := range cases {
		if got := g.CellOf(c.pos); got != c.want {
			t.Fatalf("CellOf(%v) = %v, want %v", c.pos, got, c.want)
		}
	}
}

func TestInsertAndRemoveCleansEmptyCell(t *testing.T) {
	g := NewGrid[string](10, 1)

	c := g.Insert(1, Pos{X: 5, Z: 5}, "a")
	if s := g.Stats(); s.Cells != 1 || s.Entities != 1 {
		t.Fatalf("after insert: %+v, want 1 cell / 1 entity", s)
	}

	g.Remove(1, c)
	if s := g.Stats(); s.Cells != 0 || s.Entities != 0 {
		t.Fatalf("after remove: %+v, want empty grid", s)
	}

	// 重复删除不应该 panic 或破坏其他数据。
	g.Remove(1, c)
}

func TestRemoveUsesSwapAndKeepsIndexConsistent(t *testing.T) {
	g := NewGrid[int](10, 1)
	c := Cell{0, 0}

	for i := 1; i <= 5; i++ {
		g.Insert(uint64(i), Pos{X: 1, Z: 1}, i)
	}
	// 删中间那个，最后一个会被换到它的位置。
	g.Remove(3, c)

	seen := map[uint64]bool{}
	g.ForEachNear(c, func(id uint64, v int) {
		seen[id] = true
		if int(id) != v {
			t.Fatalf("id %d 携带的值是 %d，索引错乱", id, v)
		}
	})
	if seen[3] || len(seen) != 4 {
		t.Fatalf("删除后剩余 %v，期望 4 个且不含 3", seen)
	}
}

func TestMoveAcrossCell(t *testing.T) {
	g := NewGrid[string](10, 1)

	c1 := g.Insert(1, Pos{X: 5, Z: 5}, "a")
	if c1 != (Cell{0, 0}) {
		t.Fatalf("初始格子 = %v", c1)
	}

	// 同格内移动不换格。
	c2, moved := g.Move(1, "a", c1, Pos{X: 6, Z: 6})
	if moved || c2 != c1 {
		t.Fatalf("同格移动不应跨格，moved=%v cell=%v", moved, c2)
	}

	// 跨格。
	c3, moved := g.Move(1, "a", c2, Pos{X: 25, Z: 5})
	if !moved || c3 != (Cell{2, 0}) {
		t.Fatalf("跨格失败，moved=%v cell=%v", moved, c3)
	}

	if s := g.Stats(); s.Cells != 1 || s.Entities != 1 {
		t.Fatalf("跨格后统计 = %+v，期望 1 格 1 实体（旧格应被清掉）", s)
	}

	found := 0
	g.ForEachNear(c3, func(uint64, string) { found++ })
	if found != 1 {
		t.Fatalf("新格视野内实体数 = %d", found)
	}
}

func TestForEachNearCoversSquareAround(t *testing.T) {
	for _, radius := range []int{1, 2} {
		g := NewGrid[int](10, radius)
		center := Cell{0, 0}

		// 在半径边界内外各放一个。
		g.Insert(1, Pos{X: 5, Z: 5}, 1) // (0,0) 中心
		in := float32(radius)*10 + 5
		g.Insert(2, Pos{X: in, Z: in}, 2) // 边界内
		out := float32(radius+1)*10 + 5
		g.Insert(3, Pos{X: out, Z: out}, 3) // 边界外

		seen := map[uint64]bool{}
		g.ForEachNear(center, func(id uint64, _ int) { seen[id] = true })

		if !seen[1] || !seen[2] {
			t.Fatalf("radius=%d 漏掉了视野内实体: %v", radius, seen)
		}
		if seen[3] {
			t.Fatalf("radius=%d 包含了视野外实体", radius)
		}
	}
}

func TestInViewMatchesForEachNear(t *testing.T) {
	g := NewGrid[int](10, 1)

	center := Cell{0, 0}
	// 把 -3..3 的所有格子都放一个实体，再用两种方式比对。
	id := uint64(0)
	owner := map[uint64]Cell{}
	for x := int32(-3); x <= 3; x++ {
		for z := int32(-3); z <= 3; z++ {
			id++
			g.Insert(id, Pos{X: float32(x)*10 + 1, Z: float32(z)*10 + 1}, 0)
			owner[id] = Cell{x, z}
		}
	}

	byWalk := map[uint64]bool{}
	g.ForEachNear(center, func(eid uint64, _ int) { byWalk[eid] = true })

	for eid, c := range owner {
		want := g.InView(center, c)
		if want != byWalk[eid] {
			t.Fatalf("实体 %v 在格 %v：InView=%v 但 ForEachNear=%v", eid, c, want, byWalk[eid])
		}
	}
}

// TestAgainstBruteForce 用随机操作做性质测试：
// 每一步之后，索引给出的视野集合必须和"遍历全部实体按坐标过滤"的结果完全一致。
// 这条能抓住 swap-remove 写坏索引、跨格漏删、空格子没清等所有指针类 bug。
func TestAgainstBruteForce(t *testing.T) {
	const (
		cellSize = 10
		radius   = 1
		entities = 200
		steps    = 3000
	)
	g := NewGrid[int](cellSize, radius)
	rng := rand.New(rand.NewSource(42))

	pos := make(map[uint64]Pos)
	cell := make(map[uint64]Cell)
	alive := make(map[uint64]bool)
	var nextID uint64 = 1

	for step := 0; step < steps; step++ {
		switch op := rng.Intn(10); {
		case op < 3: // 进
			id := nextID
			nextID++
			p := Pos{X: float32(rng.Intn(200) - 100), Z: float32(rng.Intn(200) - 100)}
			cell[id] = g.Insert(id, p, int(id))
			pos[id] = p
			alive[id] = true

		case op < 5: // 出
			ids := keys(alive)
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			g.Remove(id, cell[id])
			delete(alive, id)
			delete(pos, id)
			delete(cell, id)

		default: // 走
			ids := keys(alive)
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			p := Pos{X: float32(rng.Intn(200) - 100), Z: float32(rng.Intn(200) - 100)}
			nc, _ := g.Move(id, int(id), cell[id], p)
			cell[id] = nc
			pos[id] = p
		}

		if step%97 != 0 {
			continue
		}
		verifyView(t, g, alive, pos, Cell{X: int32(rng.Intn(9) - 4), Z: int32(rng.Intn(9) - 4)})
	}
}

func verifyView(t *testing.T, g *Grid[int], alive map[uint64]bool, pos map[uint64]Pos, center Cell) {
	t.Helper()

	got := map[uint64]bool{}
	g.ForEachNear(center, func(id uint64, _ int) {
		if got[id] {
			t.Fatalf("视野内实体 %d 出现两次", id)
		}
		got[id] = true
	})

	want := map[uint64]bool{}
	for id := range alive {
		if g.InView(center, g.CellOf(pos[id])) {
			want[id] = true
		}
	}

	if len(got) != len(want) {
		t.Fatalf("视野集合大小不一致: got %d want %d (center=%v)", len(got), len(want), center)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("漏掉了实体 %d (center=%v)", id, center)
		}
	}
}

func keys(m map[uint64]bool) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
