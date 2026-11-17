package creature

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

func benchScene(b *testing.B, mobs int, withSystems bool) *world.Scene {
	b.Helper()
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if withSystems {
		if err := Register(s); err != nil {
			b.Fatal(err)
		}
	}
	SpawnHorde(s, mobs, 0, 0, 300)
	return s
}

// BenchmarkSceneTickMobs 比较"有玩法系统"和"只有移动"的单帧成本。
//
// 这是 ECS 接入后最该盯的数字：它直接告诉你 2000 只怪会让服务器的
// 帧预算多用掉多少。差值就是 AI + 回血 + 属性同步的总开销。
func BenchmarkSceneTickMobs(b *testing.B) {
	cases := []struct {
		name    string
		mobs    int
		systems bool
	}{
		{"2000怪_无系统", 2000, false},
		{"2000怪_含AI", 2000, true},
		{"5000怪_含AI", 5000, true},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			s := benchScene(b, c.mobs, c.systems)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.TickOnce()
			}
		})
	}
}

// BenchmarkWander3k 单测游荡系统：3000 个 Brain 的全表扫描。
func BenchmarkWander3k(b *testing.B) {
	s := benchScene(b, 3000, false)
	st := &systems{}
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: time.Now(), Delta: 50 * time.Millisecond}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.wander(ctx)
	}
}

// BenchmarkRegen3k 单测回血系统。
//
// 注意闸门：不重置 nextRegen 的话，第 2 次调用起都是"秒返回"，
// 测出来会变成一次时间比较的成本。这里每次重置，测的是真实的全表回血。
func BenchmarkRegen3k(b *testing.B) {
	s := benchScene(b, 3000, false)
	st := &systems{}
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.nextRegen = time.Time{}
		st.regen(ctx)
	}
}

// BenchmarkSyncAttr3k 测 3000 个属性组件的脏检查成本（全部不脏时）。
//
// 稳态下绝大多数实体不脏，所以"扫一遍什么都没发"才是常态成本。
func BenchmarkSyncAttr3k(b *testing.B) {
	s := benchScene(b, 3000, false)
	st := &systems{}
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()}
	// 先清一次脏，进入"没有变化"的稳态。
	st.syncAttr(ctx)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.nextSync = time.Time{}
		st.syncAttr(ctx)
	}
}

// BenchmarkComponentAddRemove 是实体进出场的组件开销：
// 每只怪 2 个组件，进 + 出各一遍。
func BenchmarkComponentAddRemove(b *testing.B) {
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	attr := ecs.Get(s.Store(), CAttr)
	brain := ecs.Get(s.Store(), CBrain)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint64(i%8192) + 1
		attr.Add(id, Attributes{HP: 60, MaxHP: 100})
		brain.Add(id, Brain{HomeX: 0, HomeZ: 0})
		attr.Remove(id)
		brain.Remove(id)
	}
}
