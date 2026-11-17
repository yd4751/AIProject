package ecs

import (
	"testing"
)

type benchComp struct {
	HP, MP   int32
	Level    uint8
	_        [3]byte
	TargetID uint64
}

var (
	cBenchA = Define[benchComp]("bench_a")
	cBenchB = Define[benchComp]("bench_b")
)

// BenchmarkAdd 测组件挂载成本（含 2 倍扩容的摊还）。
func BenchmarkAdd(b *testing.B) {
	s := NewStore()
	p := Get(s, cBenchA)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 每个实体只加一次，ID 从 1 递增。
		p.Add(uint64(i+1), benchComp{HP: int32(i)})
	}
}

// BenchmarkAddRemove 测"挂载 + 卸载"一个来回，这是实体进出场的热路径。
func BenchmarkAddRemove(b *testing.B) {
	s := NewStore()
	p := Get(s, cBenchA)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Add(uint64(i%8192)+1, benchComp{HP: 1})
		p.Remove(uint64(i%8192) + 1)
	}
}

// BenchmarkGet 测指针查询。
func BenchmarkGet(b *testing.B) {
	s := NewStore()
	p := Get(s, cBenchA)
	const n = 10000
	for i := 0; i < n; i++ {
		p.Add(uint64(i+1), benchComp{HP: int32(i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := p.Get(uint64(i%n) + 1); got == nil {
			b.Fatal("miss")
		}
	}
}

// benchSink 防止编译器把整段遍历优化掉。
var benchSink int64

// BenchmarkEach 测 1 万个组件的整表遍历。
func BenchmarkEach(b *testing.B) {
	s := NewStore()
	p := Get(s, cBenchA)
	const n = 10000
	for i := 0; i < n; i++ {
		p.Add(uint64(i+1), benchComp{HP: int32(i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		p.Each(func(_ uint64, c *benchComp) { sum += int64(c.HP) })
		benchSink = sum
	}
}

// BenchmarkEach2 测双组件交集：1 万个 A，其中 3000 个同时有 B。
//
// 这是"玩家有属性、附近的怪有 AI"这类关系的典型形状：
// 从 3000 的池子出发查 1 万的池子，而不是反过来。
func BenchmarkEach2(b *testing.B) {
	s := NewStore()
	pa, pb := Get(s, cBenchA), Get(s, cBenchB)
	const n = 10000
	for i := 0; i < n; i++ {
		pa.Add(uint64(i+1), benchComp{HP: int32(i)})
	}
	for i := 0; i < 3000; i++ {
		// ID 取偶数，保证两个池子的遍历顺序不同，避免测出缓存的假象。
		pb.Add(uint64(i*2+1), benchComp{MP: int32(i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Each2(s, cBenchA, cBenchB, func(_ uint64, a *benchComp, c *benchComp) {
			a.HP++
			a.MP += c.MP
		})
	}
}

// BenchmarkEachAny 是对照组：同样的遍历，但组件存在 []any 里。
//
// 两边都只读不改，排除"写回 interface 触发装箱分配"的干扰——
// 第一次写这个基准时忘了这点，测出 []any 慢 40 倍，
// 其中 39 倍是每轮 1 万次装箱分配，不是布局的锅。
func BenchmarkEachAny(b *testing.B) {
	const n = 10000
	data := make([]any, n)
	for i := 0; i < n; i++ {
		data[i] = benchComp{HP: int32(i)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for j := range data {
			c := data[j].(benchComp)
			sum += int64(c.HP)
		}
		benchSink = sum
	}
}
