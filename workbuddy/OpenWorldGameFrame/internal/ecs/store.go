package ecs

import (
	"sort"
	"sync/atomic"
)

// Component 是一个组件类型描述符。
//
// 描述符是进程级单例（包级变量），存储是场景级的。两者分开是因为：
// 描述符只是"类型 -> 一个小整数 ID"的映射，全局唯一才能让不同场景
// 用同一份代码；而数据必须每个场景一份，否则两个场景的实体互相串味。
//
// 用法：
//
//	var CHealth = ecs.Define[Health]("health")
//	p := ecs.Get(scene.Store(), CHealth)
type Component[T any] struct {
	name string
	id   uint32
}

// Name 返回组件名。
func (c *Component[T]) Name() string { return c.name }

// ID 返回组件在全进程内的编号。
func (c *Component[T]) ID() uint32 { return c.id }

// nextComponentID 全局自增。只在包初始化阶段被调用，用原子量是为了
// 万一有人在 goroutine 里 Define（比如插件热加载）也不会撞号。
var nextComponentID atomic.Uint32

// Define 创建一个组件描述符。
//
// 同一个类型调两次 Define 会得到两个不相干的 ID，数据不共享。
// 约定是每个组件类型只在包级 var 里 Define 一次，不要放进函数里。
func Define[T any](name string) *Component[T] {
	return &Component[T]{name: name, id: nextComponentID.Add(1) - 1}
}

// pool 是 Pool[T] 去掉类型参数后的视图，只保留与 T 无关的操作。
type pool interface {
	compName() string
	compLen() int
	compSparseLen() int
	compRemove(id uint64)
}

// Store 是一个场景的全部组件存储。
//
// 懒注册：某个场景如果从来没用过"背包"组件，就一个 []Item 都不会分配。
// 开放世界有几十个场景，每个场景的玩法组件集合差别很大，
// 提前为所有组件分配空间会浪费大量内存。
type Store struct {
	pools []pool
}

// NewStore 创建空的组件存储。
func NewStore() *Store { return &Store{} }

// Get 返回某个组件在 store 里的池，不存在则创建。
//
// 一次 Get 的成本是"切片索引 + 接口类型断言"，约 2ns。
// 不要在热循环里每次迭代都调 Get——System 的惯用写法是在 Tick 开头
// 取一次池指针，循环里直接用。
func Get[T any](s *Store, c *Component[T]) *Pool[T] {
	if int(c.id) < len(s.pools) {
		if p, ok := s.pools[c.id].(*Pool[T]); ok {
			return p
		}
	}
	p := &Pool[T]{name: c.name}
	for len(s.pools) <= int(c.id) {
		s.pools = append(s.pools, nil)
	}
	s.pools[c.id] = p
	return p
}

// Destroy 卸载某个实体的全部组件。
//
// 实体离场时必须调一次，否则池里会留下一个"永远没人读、但一直占着
// 内存、还持有指针"的僵尸组件。开销是 O(组件种类数)，与实体数无关。
func (s *Store) Destroy(id uint64) {
	for _, p := range s.pools {
		if p != nil {
			p.compRemove(id)
		}
	}
}

// PoolStat 是一个池的规模快照。
type PoolStat struct {
	Name string `json:"name"`
	Len  int    `json:"len"`
	// Sparse 是稀疏数组长度，等于「历史最大实体 ID + 1」，与 Len 无关。
	// 它 × 4 字节 = 这个池仅用于 ID 寻址的内存。
	// 实体 ID 不回收的话，这个数会随累计实体数一直涨，而 Len 纹丝不动——
	// 看起来"组件没变多"，实际内存一直在漏。
	Sparse int `json:"sparse"`
}

// Stats 返回所有已注册池的规模，按名字排序。
//
// 只能在 tick 协程里调用（它读 len(dense)，跨 goroutine 读是数据竞争）。
// 对外暴露走 Scene 每帧存下来的快照。
func (s *Store) Stats() []PoolStat {
	out := make([]PoolStat, 0, len(s.pools))
	for _, p := range s.pools {
		if p == nil {
			continue
		}
		out = append(out, PoolStat{Name: p.compName(), Len: p.compLen(), Sparse: p.compSparseLen()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Each2 遍历同时拥有 A、B 两种组件的实体。
//
// 关键在"挑小池子遍历，在大池子里查"：
// 1000 个玩家有属性、5000 只怪有 AI，求交集时从属性池出发只做 1000 次
// 查询，而不是 5000 次。这个选择让查询成本 = min(|A|, |B|)，而不是 |A|+|B|。
//
// 组件多了以后（3 种以上）没有做"自动选最小池"的通用查询器，
// 因为那需要把类型参数塞进运行时结构，最终会退化成反射或者 map 查表，
// 反而比手写 Each2/Each3 慢。玩法层的组合查询本来就不多，手写更划算。
func Each2[A, B any](s *Store, ca *Component[A], cb *Component[B], fn func(id uint64, a *A, b *B)) {
	pa, pb := Get(s, ca), Get(s, cb)
	if pa.compLen() <= pb.compLen() {
		pa.Each(func(id uint64, a *A) {
			if b := pb.Get(id); b != nil {
				fn(id, a, b)
			}
		})
		return
	}
	pb.Each(func(id uint64, b *B) {
		if a := pa.Get(id); a != nil {
			fn(id, a, b)
		}
	})
}

// Each3 遍历同时拥有三种组件的实体。策略同 Each2。
func Each3[A, B, C any](s *Store, ca *Component[A], cb *Component[B], cc *Component[C],
	fn func(id uint64, a *A, b *B, c *C)) {
	pa, pb, pc := Get(s, ca), Get(s, cb), Get(s, cc)

	// 选最小的池作为驱动，其余两个用 Has 过滤后再 Get（多一次下标查找，
	// 但省掉一次「取指针又判空」的分支）。
	switch {
	case pa.compLen() <= pb.compLen() && pa.compLen() <= pc.compLen():
		pa.Each(func(id uint64, a *A) {
			if pc.Has(id) {
				if b := pb.Get(id); b != nil {
					fn(id, a, b, pc.Get(id))
				}
			}
		})
	case pb.compLen() <= pc.compLen():
		pb.Each(func(id uint64, b *B) {
			if pc.Has(id) {
				if a := pa.Get(id); a != nil {
					fn(id, a, b, pc.Get(id))
				}
			}
		})
	default:
		pc.Each(func(id uint64, c *C) {
			if b := pb.Get(id); b != nil {
				if a := pa.Get(id); a != nil {
					fn(id, a, b, c)
				}
			}
		})
	}
}
