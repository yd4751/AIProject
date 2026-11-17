// Package aoi 实现空间分区与视野查询。
//
// 这是开放世界最核心的模块：它决定"谁能看见谁"，进而决定 CPU 和带宽。
//
// # 三条设计原则
//
//  1. 格子内用 slice 而不是 map。同格实体在内存里连续，遍历快 3~5 倍；
//     删除用 swap-remove + 索引表，保持 O(1)。
//  2. 格子尺寸和视野范围解耦。格子多大是存储问题，视野多大是玩法问题，
//     两者绑死会导致"想改视野就得重建索引"。
//  3. 差集查询不构造临时集合。跨格时新旧视野重叠 90% 以上，
//     遍历新视野 + InView 判旧视野即可，省掉每次移动的 map 分配。
//
// # 并发
//
// Grid 不是并发安全的，由 Scene 的单 goroutine 独占。
// 跨 goroutine 的读取只能走 Stats 这类快照接口。
package aoi

import (
	"math"
	"sync/atomic"
)

// Cell 是格子坐标。
type Cell struct {
	X, Z int32
}

// Pos 是平面坐标。AOI 只管平面，高度由业务自己处理。
type Pos struct {
	X, Z float32
}

// Grid 是一个方格空间索引。
//
// 类型参数 T 是存进去的值，通常是实体指针。用泛型而不是 any，
// 是为了避免热路径上的类型断言和接口装箱。
type Grid[T any] struct {
	cellSize float32
	radius   int32
	cells    map[Cell]*bucket[T]

	// 计数用原子量维护，这样 Stats 可以在别的 goroutine 里读，
	// 不必去遍历正在被改写的 map。
	cellCount   atomic.Int64
	entityCount atomic.Int64
}

type entry[T any] struct {
	id uint64
	v  T
}

type bucket[T any] struct {
	items []entry[T]
	// idx 记录 id 在 items 中的位置，删除时用于 swap-remove。
	idx map[uint64]int
}

// NewGrid 创建索引。
//
// cellSize 是格子边长，radius 是视野半径（单位：格）。
// radius=1 表示 3×3，radius=2 表示 5×5。
func NewGrid[T any](cellSize float32, radius int) *Grid[T] {
	if cellSize <= 0 {
		panic("aoi: cellSize must be positive")
	}
	if radius < 1 {
		panic("aoi: radius must be >= 1")
	}
	return &Grid[T]{
		cellSize: cellSize,
		radius:   int32(radius),
		cells:    make(map[Cell]*bucket[T], 64),
	}
}

// CellSize 返回格子边长。
func (g *Grid[T]) CellSize() float32 { return g.cellSize }

// Radius 返回视野半径（格）。
func (g *Grid[T]) Radius() int { return int(g.radius) }

// CellOf 把坐标换算成格子。
func (g *Grid[T]) CellOf(p Pos) Cell {
	return Cell{
		X: int32(math.Floor(float64(p.X / g.cellSize))),
		Z: int32(math.Floor(float64(p.Z / g.cellSize))),
	}
}

// Insert 放入一个实体并返回它所在的格子。
// 同一个 id 重复 Insert 没有防护——调用方保证生命周期正确。
func (g *Grid[T]) Insert(id uint64, p Pos, v T) Cell {
	c := g.CellOf(p)
	g.insertInto(c, id, v)
	return c
}

func (g *Grid[T]) insertInto(c Cell, id uint64, v T) {
	b := g.cells[c]
	if b == nil {
		b = &bucket[T]{idx: make(map[uint64]int, 4)}
		g.cells[c] = b
		g.cellCount.Add(1)
	}
	b.idx[id] = len(b.items)
	b.items = append(b.items, entry[T]{id: id, v: v})
	g.entityCount.Add(1)
}

// Remove 从指定格子移除实体。格子必须和 Insert/Move 返回的一致。
func (g *Grid[T]) Remove(id uint64, c Cell) {
	b := g.cells[c]
	if b == nil {
		return
	}
	i, ok := b.idx[id]
	if !ok {
		return
	}
	last := len(b.items) - 1
	if i != last {
		b.items[i] = b.items[last]
		b.idx[b.items[i].id] = i
	}
	b.items = b.items[:last]
	delete(b.idx, id)
	g.entityCount.Add(-1)
	// 空格子必须删掉，否则 map 会随玩家走过的路径无界增长。
	if last == 0 {
		delete(g.cells, c)
		g.cellCount.Add(-1)
	}
}

// Move 更新实体位置。返回新格子和是否跨格。
// 未跨格时只返回旧格子，不碰索引——绝大多数移动都是这种情况。
func (g *Grid[T]) Move(id uint64, v T, from Cell, to Pos) (Cell, bool) {
	nc := g.CellOf(to)
	if nc == from {
		return from, false
	}
	g.Remove(id, from)
	g.insertInto(nc, id, v)
	return nc, true
}

// ForEachNear 遍历 center 视野内的所有实体，包含 center 自身所在格的实体。
//
// 回调里禁止调用 Insert/Remove/Move——items 正在被遍历。
// 需要"边遍历边删"时，先收集再处理。
func (g *Grid[T]) ForEachNear(center Cell, fn func(id uint64, v T)) {
	r := g.radius
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			b := g.cells[Cell{X: center.X + dx, Z: center.Z + dz}]
			if b == nil {
				continue
			}
			for i := range b.items {
				fn(b.items[i].id, b.items[i].v)
			}
		}
	}
}

// InView 判断格子 c 是否在 center 的视野内。
//
// 这是差集查询的关键：遍历新视野时用它排除"原本就看得见"的实体，
// 就不必为了求差集去构造两个临时集合。
func (g *Grid[T]) InView(center, c Cell) bool {
	dx := c.X - center.X
	dz := c.Z - center.Z
	r := g.radius
	return dx >= -r && dx <= r && dz >= -r && dz <= r
}

// NearCount 返回视野内的实体数量，用于指标和调优。
// 内部走一遍遍历，不要在每帧调。
func (g *Grid[T]) NearCount(center Cell) int {
	n := 0
	g.ForEachNear(center, func(uint64, T) { n++ })
	return n
}

// Stats 是索引的规模快照。
type Stats struct {
	Cells    int `json:"cells"`
	Entities int `json:"entities"`
}

// Stats 返回当前格子数与实体数。并发安全，可在其他 goroutine 调用。
func (g *Grid[T]) Stats() Stats {
	return Stats{
		Cells:    int(g.cellCount.Load()),
		Entities: int(g.entityCount.Load()),
	}
}
