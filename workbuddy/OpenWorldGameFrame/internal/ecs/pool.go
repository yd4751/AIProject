// Package ecs 提供按类型连续存储的组件容器。
//
// 这里刻意不用反射，也不用"万能 Component 接口"：
//
//	type Health struct{ ... }
//	var CHealth = ecs.Define[Health]("health")
//	pool := ecs.Get(scene.Store(), CHealth)   // *ecs.Pool[Health]
//	pool.Add(id, Health{HP: 100})
//
// 泛型池带来的最大好处是遍历时拿到的是 []Health 而不是 []any，
// 一个 24 字节的组件在连续内存里走，CPU 预取能命中。
// 实测（1 万个组件整表只读遍历）：15.8µs vs []any 的 29.1µs，约 1.8 倍。
// 差距没有想象中大，是因为遍历的回调调用本身盖住了一部分；
// 真正拉开差距的是写入路径——往 []any 里写要装箱，每次一个堆分配。
//
// 数据布局是 sparse set（稀疏集），不是 archetype：
//
//	sparse: [实体 ID] -> dense 下标 + 1（0 表示没挂这个组件）
//	dense:  []T       —— 真正连续存放的组件
//	ids:    []uint64  —— dense 下标 -> 实体 ID
//
// 为什么不用 archetype（flecs / bevy 那种按组件组合分表）：
// archetype 的强项是"按组件组合批量遍历"，代价是增删组件要把实体
// 从一张表搬到另一张表（O(组合大小)），并且搬完所有指针全部失效。
// 开放世界的实体是长尾的：玩家进出、怪物刷新、掉落物生成，
// 增删频率远高于"同一组合的大批量遍历"，稀疏集的 O(1) 增删更合适。
// 代价是查询要多一次稀疏数组间接寻址，见 Each2。
package ecs

// Pool 是一种组件的连续存储。
//
// 零值不可用，必须通过 Get 创建，因为稀疏数组需要按组件类型独立维护。
type Pool[T any] struct {
	name   string
	sparse []int32 // 实体 ID -> dense 下标 + 1；0 表示未挂载
	dense  []T
	ids    []uint64
}

// Add 挂载组件。已存在时按覆盖处理（后写覆盖先写）。
//
// 覆盖语义是刻意的：属性、位置这类组件在服务端每个 tick 都可能被
// 多个来源修改，要求调用方先 Has 再 Add 只会让代码变啰嗦，
// 并且两步之间没有任何原子性保证。
func (p *Pool[T]) Add(id uint64, v T) {
	if id == 0 {
		// 0 保留给"无效实体"。放行的话，稀疏数组的 0 值哨兵就会
		// 把 ID 0 误判成"没有组件"，这种 bug 极难排查。
		panic("ecs: entity id 0 is reserved")
	}
	if i := p.index(id); i >= 0 {
		p.dense[i] = v
		return
	}
	p.ensureSparse(id)
	p.ids = append(p.ids, id)
	p.dense = append(p.dense, v)
	p.sparse[id] = int32(len(p.dense))
}

// Get 返回组件指针，未挂载时返回 nil。
//
// 指针指向 dense 切片内部，因此只在"没有对同一 Pool 做 Add/Remove"
// 的期间有效。System 里常见的用法是拿到指针当场改完，不要存起来。
func (p *Pool[T]) Get(id uint64) *T {
	if i := p.index(id); i >= 0 {
		return &p.dense[i]
	}
	return nil
}

// Has 判断是否挂载了该组件。
func (p *Pool[T]) Has(id uint64) bool { return p.index(id) >= 0 }

// Remove 卸载组件。未挂载时返回 false。
//
// 采用 swap-remove：把最后一个元素搬到被删的位置。
// 这让删除是 O(1) 且不需要在 dense 里留空洞（留空洞会让后续遍历
// 变成"跳过空位"，分支预测不友好，也破坏连续性）。
// 代价是 dense 的顺序不保证稳定，遍历顺序在删除后会变化。
func (p *Pool[T]) Remove(id uint64) bool {
	i := p.index(id)
	if i < 0 {
		return false
	}
	last := len(p.dense) - 1
	if i != last {
		p.dense[i] = p.dense[last]
		p.ids[i] = p.ids[last]
		// 被搬过来的那个元素，它的稀疏项指向新位置。
		p.sparse[p.ids[i]] = int32(i) + 1
	}
	var zero T
	// 必须清零：T 里可能有指针（比如 *Buff 列表），
	// 不清零会让 GC 一直认为那个已死组件还活着。
	p.dense[last] = zero
	p.ids = p.ids[:last]
	p.dense = p.dense[:last]
	p.sparse[id] = 0
	return true
}

// Len 返回已挂载该组件的实体数。
func (p *Pool[T]) Len() int { return len(p.dense) }

// SparseLen 返回稀疏数组的长度，等于「历史最大实体 ID + 1」。
//
// 这个数 × 4 字节就是该池仅用于寻址的开销。它跟 Len 完全脱钩：
// Len 是 10 的池也可能有 100 万长度的稀疏数组，取决于用过的 ID 有多大。
// 所以实体 ID 必须回收（见 world.idalloc），否则这个数会一直涨。
func (p *Pool[T]) SparseLen() int { return len(p.sparse) }

// Name 返回组件名，用于指标和调试输出。
func (p *Pool[T]) Name() string { return p.name }

// Each 按 dense 顺序遍历所有组件。
//
// 遍历期间的 fn 里不要对同一个 Pool 做 Add/Remove——那会让 dense
// 在脚下变化（swap-remove 会把还没遍历到的元素搬到你身后）。
// 需要边遍历边删时，先用 IDs() 取一份快照再逐个处理。
func (p *Pool[T]) Each(fn func(id uint64, v *T)) {
	for i := range p.dense {
		fn(p.ids[i], &p.dense[i])
	}
}

// IDs 返回实体 ID 的副本，用于"边遍历边改集合"的场景。
func (p *Pool[T]) IDs() []uint64 {
	out := make([]uint64, len(p.ids))
	copy(out, p.ids)
	return out
}

// index 返回实体在 dense 里的下标，未挂载返回 -1。
func (p *Pool[T]) index(id uint64) int {
	if id >= uint64(len(p.sparse)) {
		return -1
	}
	s := p.sparse[id]
	if s == 0 {
		return -1
	}
	return int(s) - 1
}

// ensureSparse 保证稀疏数组能容纳 id。
//
// 按 2 倍扩容而不是精确到 id+1：实体 ID 是单调递增的，逐个扩容会让
// 前 N 次插入变成 O(N²) 的拷贝。2 倍扩容的摊还成本是 O(1)。
func (p *Pool[T]) ensureSparse(id uint64) {
	if id < uint64(len(p.sparse)) {
		return
	}
	n := len(p.sparse)*2 + 16
	if uint64(n) <= id {
		n = int(id) + 1
	}
	ns := make([]int32, n)
	copy(ns, p.sparse)
	p.sparse = ns
}

// 下面三个方法只为了在 Store 里以统一方式操作"某个具体类型的池"。
// Go 的方法不能带类型参数，所以只能用这种不带泛型的桥接方法，
// 而不是把 Pool 抽成一个 interface{T any}。
func (p *Pool[T]) compName() string     { return p.name }
func (p *Pool[T]) compLen() int         { return len(p.dense) }
func (p *Pool[T]) compSparseLen() int   { return len(p.sparse) }
func (p *Pool[T]) compRemove(id uint64) { p.Remove(id) }
