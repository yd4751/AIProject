package world

import (
	"testing"
	"time"

	"openworld/internal/ecs"
)

// testComp 是回收测试用的组件。用一个私有类型而不是复用玩法包的，
// 是为了让"稀疏数组长度"这组数字不受玩法配置变化影响。
type testComp struct{ V int32 }

var CTest = ecs.Define[testComp]("test.recycle")

// churnScene 让场景反复"铺满 -> 全部销毁"，返回 ID 统计和组件池稀疏数组长度。
//
// coolDown 决定实验臂：
//   - 传 1 小时 = 冷却永远不到期，等价于"不回收"（改造前的行为）
//   - 传默认值 = 正常回收
func churnScene(t *testing.T, rounds, per int, coolDown time.Duration) (IDStat, int) {
	t.Helper()
	s := newTestScene()
	s.ids.coolDown = coolDown
	s.sysCtx.Now = t0

	pool := ecs.Get(s.Store(), CTest)
	// 每轮推进 3 秒：比默认冷却（2 秒）长，回收臂才能复用；
	// 又远小于"不回收"臂的 1 小时冷却，那一臂始终不会复用。
	// 步长必须与 coolDown 解耦，否则两臂会一起跟着冷却走，实验就失效了。
	const step = 3 * time.Second

	for r := 0; r < rounds; r++ {
		live := make([]*Entity, 0, per)
		for i := 0; i < per; i++ {
			e := s.Spawn(KindMob, "mob", float32(i%80)*2, float32(i/80)*2)
			if e == nil {
				t.Fatalf("第 %d 轮第 %d 只刷不出来，ID 空间耗尽了", r, i)
			}
			pool.Add(e.ID(), testComp{V: int32(i)})
			live = append(live, e)
		}
		if got := s.entityCount.Load(); got != int64(per) {
			t.Fatalf("第 %d 轮应有 %d 个实体，实际 %d", r, per, got)
		}

		for _, e := range live {
			s.removeEntity(e)
		}
		if got := s.entityCount.Load(); got != 0 {
			t.Fatalf("第 %d 轮结束后应为空，实际 %d", r, got)
		}
		if got := pool.Len(); got != 0 {
			t.Fatalf("第 %d 轮结束后组件应清空，实际 %d", r, got)
		}
		// 推进时间，让本轮释放的 ID 满足冷却条件。
		s.sysCtx.Now = s.sysCtx.Now.Add(step)
	}
	return s.ids.stat(), pool.SparseLen()
}

// TestSceneRecyclesEntityIDs 验证场景层真的走了回收路径。
func TestSceneRecyclesEntityIDs(t *testing.T) {
	const per = 200
	st, sparse := churnScene(t, 10, per, idCoolDown)

	if st.HighWater > per {
		t.Fatalf("高水位应钉在同时存活数 %d，实际 %d（说明没有复用）", per, st.HighWater)
	}
	// 稀疏数组按 2 倍扩容，钉在 ID 高水位附近，不会跟着累计量走。
	if sparse > 4*per {
		t.Fatalf("稀疏数组长度 %d 远超同时存活数 %d", sparse, per)
	}
	if st.Reused != int64(per*9) {
		t.Fatalf("10 轮里应复用 %d 次，实际 %d", per*9, st.Reused)
	}
}

// TestSparseArrayGrowth 把"不回收会怎样"量化出来。
//
// 这个用例的价值在于它是可复现的证据：同一段代码、同一个组件池，
// 唯一变量是 ID 冷却策略，稀疏数组长度差两个数量级。
func TestSparseArrayGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("会刷两万个实体，-short 下跳过")
	}
	const rounds, per = 10, 2000 // 累计 2 万个实体，峰值 2000

	// 实验臂 A：不回收（冷却期设成 1 小时，等价于改造前的单调递增）
	stNo, sparseNo := churnScene(t, rounds, per, time.Hour)
	// 实验臂 B：正常回收
	stYes, sparseYes := churnScene(t, rounds, per, idCoolDown)

	t.Logf("不回收: 高水位=%d sparse=%d (%d KB/池)", stNo.HighWater, sparseNo, sparseNo*4/1024)
	t.Logf("回收后: 高水位=%d sparse=%d (%d KB/池)", stYes.HighWater, sparseYes, sparseYes*4/1024)

	if stNo.HighWater != rounds*per {
		t.Fatalf("不回收时高水位应等于累计数 %d，实际 %d", rounds*per, stNo.HighWater)
	}
	if stYes.HighWater > per {
		t.Fatalf("回收后高水位应不超过峰值 %d，实际 %d", per, stYes.HighWater)
	}
	// 2 倍扩容的余量，允许 sparse 是峰值的若干倍，但必须远小于累计数。
	if sparseYes*3 > sparseNo {
		t.Fatalf("回收没有明显降低稀疏数组：%d vs %d", sparseYes, sparseNo)
	}
}

// TestSpawnFailsWhenExhausted 确认 ID 耗尽时 Spawn 返回 nil 而不是造出 ID 0 的实体。
func TestSpawnFailsWhenExhausted(t *testing.T) {
	s := newTestScene()
	s.ids.limit = 2
	s.sysCtx.Now = t0

	if e := s.Spawn(KindMob, "a", 0, 0); e == nil {
		t.Fatal("第 1 只应刷得出来")
	}
	if e := s.Spawn(KindMob, "b", 0, 0); e == nil {
		t.Fatal("第 2 只应刷得出来")
	}
	if e := s.Spawn(KindMob, "c", 0, 0); e != nil {
		t.Fatalf("ID 耗尽应返回 nil，实际拿到 id=%d", e.ID())
	}
	if got := s.entityCount.Load(); got != 2 {
		t.Fatalf("应有 2 个实体，实际 %d", got)
	}
}

// TestEnterReleasesIDWhenMailboxFull 防一个静默泄漏：
// 邮箱满导致 msgEnter 被丢时，已经分出去的 ID 必须还回去。
func TestEnterReleasesIDWhenMailboxFull(t *testing.T) {
	s := newTestScene()
	// 不启动 tick 循环，直接把邮箱塞满。
	for i := 0; i < mailboxSize; i++ {
		s.mailbox <- &msgHeartbeat{entityID: 0}
	}

	if id := s.Enter("a", &mockClient{}); id != 0 {
		t.Fatalf("邮箱满时 Enter 应返回 0，实际 %d", id)
	}
	st := s.ids.stat()
	if st.Live != 0 {
		t.Fatalf("被丢弃的进场指令不该留下存活 ID: %+v", st)
	}
	if st.Cooling != 1 {
		t.Fatalf("ID 应进冷却队列等待复用: %+v", st)
	}
}

func BenchmarkEntityChurn(b *testing.B) {
	s := newTestScene()
	s.ids.coolDown = 0
	s.sysCtx.Now = t0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := s.Spawn(KindMob, "mob", 0, 0)
		if e == nil {
			b.Fatal("spawn failed")
		}
		s.removeEntity(e)
	}
}
