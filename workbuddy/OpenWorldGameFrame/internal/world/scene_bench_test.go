package world

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/aoi"
	"openworld/internal/observability"
)

// sink 用来强制编码结果逃逸。
//
// 不加这句，编译器能证明 body 不逃逸（Send 是空函数），把 Encode 的切片
// 直接分配到栈上，allocs 测出来是假的——曾经就是 1 allocs/op，
// 而真实环境每个包都要分配一次。
var sink []byte

// noopClient 不记录任何消息，只模拟"把包交给写队列"这个动作。
type noopClient struct{}

func (noopClient) Send(_ uint16, body []byte) error {
	sink = body
	return nil
}

func (noopClient) Kick(uint8, string) error { return nil }

// newBenchScene 在 span×span 的方形区域里均匀放置 n 个实体。
//
// 为什么要指定 span：广播成本取决于"3×3 格内有多少实体"，而不是总实体数。
// 同样 2000 人，散落在 1000m 世界和挤在 200m 主城，开销差一个数量级。
func newBenchScene(b *testing.B, n int, span float64, opts ...Option) (*Scene, []*Entity) {
	b.Helper()

	s := NewScene(1, 50*time.Millisecond, observability.NewMetrics(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)

	side := 1
	for side*side < n {
		side++
	}
	gap := span / float64(side)

	now := time.Now()
	ents := make([]*Entity, 0, n)
	for i := 0; i < n; i++ {
		// 网格布点，保证密度可预测（随机散落会有热区，测出来不稳定）。
		x := float32(float64(i%side) * gap)
		z := float32(float64(i/side) * gap)
		e := &Entity{
			id:            uint64(i + 1),
			player:        "bench",
			x:             x,
			z:             z,
			speed:         DefaultSpeed,
			client:        noopClient{},
			lastHeartbeat: now,
			lastMoveAt:    now,
		}
		s.entities[e.id] = e
		e.cell = s.grid.Insert(e.id, aoi.Pos{X: x, Z: z}, e)
		s.entityCount.Add(1)
		ents = append(ents, e)
	}
	return s, ents
}

// BenchmarkMoveBroadcast 测单条移动消息的处理成本（含视野广播）。
//
// 结果换算：支持的实体数 ≈ 每秒可处理消息数 / 实体移动频率。
//
// 最后三组是同一份代码、不同 AOI 参数的对比，用来回答
// "格子尺寸该怎么定"——它不改变代码，只改变视野覆盖范围。
func BenchmarkMoveBroadcast(b *testing.B) {
	cases := []struct {
		name string
		n    int
		span float64
		opts []Option
	}{
		{"散落_1000m_2000人", 2000, 1000, nil},
		{"集中_300m_2000人", 2000, 300, nil},
		{"主城_100m_2000人", 2000, 100, nil},
		{"单场景_5000人_1000m", 5000, 1000, nil},
		{"主城_2000人_旧配置格32m视野96m", 2000, 100, []Option{WithAOI(32, 1)}},
		{"主城_2000人_格8m视野40m", 2000, 100, []Option{WithAOI(8, 2)}},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			s, ents := newBenchScene(b, c.n, c.span, c.opts...)
			n := len(ents)
			// 固定时间戳：这里测的是"一次位移 + 广播"的 CPU 成本，
			// 不希望把 time.Now() 的系统调用算进去（真实路径里一帧只取一次）。
			now := time.Now()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e := ents[i%n]
				// 位移 1mm，不触发超速钳制，只走广播路径。
				s.applyMove(e.id, e.x+0.001, 0, e.z, now)
			}
		})
	}
}

// BenchmarkMoveIntake 测"接收一条移动意图"的成本：分片锁 + 一次 map 写入。
//
// 这个数字决定了 Move() 能被多少个连接的读协程同时调用而不互相拖慢。
// 它和 BenchmarkMoveBroadcast 相加才是完整的单条移动成本。
func BenchmarkMoveIntake(b *testing.B) {
	s, ents := newBenchScene(b, 2000, 1000)
	n := len(ents)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := ents[i%n]
		s.Move(e.id, e.x+0.001, 0, e.z)
	}
}

// BenchmarkTickCoalesced 测"一帧完整消费"的成本：
// 2000 个实体，每个实体在本帧内被写入 3 次移动意图，然后统一结算。
//
// 这是合并机制真正省下来的东西：3 倍的上行消息，只产生 1 倍的位移计算。
func BenchmarkTickCoalesced(b *testing.B) {
	s, ents := newBenchScene(b, 2000, 1000)
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for rep := 0; rep < 3; rep++ {
			for _, e := range ents {
				s.Move(e.id, e.x+0.001, 0, e.z)
			}
		}
		b.StartTimer()
		s.applyIntents(now)
	}
}

// BenchmarkEnter 测玩家进入场景的成本（双向视野补齐，O(邻域人数)）。
//
// 每一轮必须成对地进出：只进不出会让场景规模随 b.N 膨胀，
// 测出来的其实是"实体越来越多时 enter 有多慢"，不是单次 enter 的成本。
func BenchmarkEnter(b *testing.B) {
	s, _ := newBenchScene(b, 2000, 1000)
	var next uint64 = 100000
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next++
		s.onEnter(&msgEnter{id: next, player: "new", client: noopClient{}})

		// 退出时的广播不计入 enter 成本。
		b.StopTimer()
		s.onLeave(&msgLeave{entityID: next})
		b.StartTimer()
	}
}
