package world

import (
	"sync"
	"testing"
	"time"
)

// 所有用例都用显式传入的时间，不依赖 time.Sleep。
// 冷却机制是"时间比较"，能直接喂时间就没必要真的等。

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestIDAllocFreshSequential(t *testing.T) {
	a := newIDAlloc()
	for i := uint64(1); i <= 100; i++ {
		if got := a.alloc(t0); got != i {
			t.Fatalf("第 %d 次分配得到 %d", i, got)
		}
	}
	if st := a.stat(); st.HighWater != 100 || st.Live != 100 || st.Reused != 0 {
		t.Fatalf("统计不对: %+v", st)
	}
}

// TestIDNotReusedBeforeCooldown 是最关键的一条：
// 刚释放的 ID 一旦立刻复用，客户端在途的旧包会打到新实体身上。
func TestIDNotReusedBeforeCooldown(t *testing.T) {
	a := newIDAlloc()
	id := a.alloc(t0)
	a.release(id, t0)

	// 冷却期内分配，必须拿新号，不能拿到刚释放的那个。
	half := t0.Add(idCoolDown / 2)
	if got := a.alloc(half); got == id {
		t.Fatalf("冷却期内复用了 ID %d", id)
	}
	// 冷却期内的 ID 还没进 free，所以仍然是"新号"。
	if st := a.stat(); st.Reused != 0 || st.Free != 0 {
		t.Fatalf("冷却期内不该有可复用 ID: %+v", st)
	}
}

func TestIDReusedAfterCooldown(t *testing.T) {
	a := newIDAlloc()
	id := a.alloc(t0)
	a.release(id, t0)

	// 推进到冷却期满，再分配应当回到最初那个 ID。
	later := t0.Add(idCoolDown + time.Millisecond)
	if got := a.alloc(later); got != id {
		t.Fatalf("冷却期满应复用 %d，实际 %d", id, got)
	}
	if st := a.stat(); st.Reused != 1 || st.HighWater != 1 {
		t.Fatalf("复用时不该推高高水位: %+v", st)
	}
}

// TestIDRecycleKeepsHighWater 是回收机制存在的理由：
// 大量实体生灭之后，高水位应该等于"同时存活数"，而不是"累计数"。
func TestIDRecycleKeepsHighWater(t *testing.T) {
	const (
		concurrent = 100
		rounds     = 50
	)
	a := newIDAlloc()
	now := t0

	// 先铺满一轮，建立峰值。
	live := make([]uint64, 0, concurrent)
	for i := 0; i < concurrent; i++ {
		live = append(live, a.alloc(now))
	}

	for r := 0; r < rounds; r++ {
		// 全部释放，然后等冷却期满。
		now = now.Add(50 * time.Millisecond)
		for _, id := range live {
			a.release(id, now)
		}
		live = live[:0]
		now = now.Add(idCoolDown)

		// 再铺满，应该全部来自 free，高水位不动。
		for i := 0; i < concurrent; i++ {
			live = append(live, a.alloc(now))
		}
	}

	st := a.stat()
	if st.HighWater != concurrent {
		t.Fatalf("高水位应钉在 %d（同时存活数），实际 %d（累计 %d 个 ID）",
			concurrent, st.HighWater, concurrent*(rounds+1))
	}
	if st.Live != concurrent {
		t.Fatalf("存活数应为 %d，实际 %d", concurrent, st.Live)
	}
	if st.Reused != int64(concurrent*rounds) {
		t.Fatalf("复用次数应为 %d，实际 %d", concurrent*rounds, st.Reused)
	}
}

// TestIDExhaustion 确认上限生效：到了上限返回 0，而不是把稀疏数组撑爆。
func TestIDExhaustion(t *testing.T) {
	a := newIDAlloc()
	a.limit = 3
	for i := 0; i < 3; i++ {
		if got := a.alloc(t0); got == 0 {
			t.Fatalf("第 %d 次分配不该失败", i+1)
		}
	}
	if got := a.alloc(t0); got != 0 {
		t.Fatalf("超限应返回 0，实际 %d", got)
	}
	if st := a.stat(); st.Exhausted != 1 {
		t.Fatalf("应记录 1 次耗尽，实际 %+v", st)
	}

	// 归还一个并等冷却，应该又能分配出来。
	a.release(2, t0)
	if got := a.alloc(t0.Add(idCoolDown + time.Second)); got != 2 {
		t.Fatalf("回收后应能再分配，实际 %d", got)
	}
}

// TestIDAllocConcurrent 模拟真实的两个调用方：
// Gate 读协程分配玩家 ID，tick 协程分配 NPC ID。
func TestIDAllocConcurrent(t *testing.T) {
	a := newIDAlloc()
	const workers, per = 8, 2000

	var wg sync.WaitGroup
	seen := make([][]uint64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			out := make([]uint64, 0, per)
			for i := 0; i < per; i++ {
				id := a.alloc(t0)
				if id == 0 {
					t.Errorf("worker %d 第 %d 次分配失败", w, i)
					return
				}
				out = append(out, id)
			}
			seen[w] = out
		}(w)
	}
	wg.Wait()

	all := make(map[uint64]struct{}, workers*per)
	for _, ids := range seen {
		for _, id := range ids {
			if _, dup := all[id]; dup {
				t.Fatalf("并发下分配出重复 ID %d", id)
			}
			all[id] = struct{}{}
		}
	}
	if len(all) != workers*per {
		t.Fatalf("应分配 %d 个唯一 ID，实际 %d", workers*per, len(all))
	}
}

// TestIDReleaseZeroIgnored 防一手：0 是"没有实体"的哨兵，
// 被回收进 free 就会被当成正常 ID 发出去。
func TestIDReleaseZeroIgnored(t *testing.T) {
	a := newIDAlloc()
	a.release(0, t0)
	if st := a.stat(); st.Cooling != 0 {
		t.Fatalf("0 不该进冷却队列: %+v", st)
	}
	if got := a.alloc(t0.Add(idCoolDown * 2)); got != 1 {
		t.Fatalf("首个 ID 应为 1，实际 %d", got)
	}
}

// TestIDCoolQueueBounded 盯的是冷却队列自身的内存。
//
// 游标只进不退，如果 promote 不做原地压缩，cool 的长度会跟着
// 累计释放次数一路涨——那就等于把"ID 空间泄漏"换成了"队列泄漏"。
func TestIDCoolQueueBounded(t *testing.T) {
	a := newIDAlloc()
	now := t0
	// 每毫秒一次生灭，冷却 2 秒 → 稳态下应有约 2000 项在冷却。
	for i := 0; i < 20000; i++ {
		id := a.alloc(now)
		if id == 0 {
			t.Fatalf("第 %d 次分配失败", i)
		}
		a.release(id, now)
		now = now.Add(time.Millisecond)
	}

	// 理论值 2000 项，给 4 倍余量；不压缩的话这里会是 20001。
	if n := len(a.cool); n > 8192 {
		t.Fatalf("冷却队列涨到 %d 项，说明没做压缩", n)
	}
	// 两万次生灭只用掉 2000 个 ID 号：正好是"2 秒冷却窗口内的周转量"。
	// 这个数就是稀疏数组的长度，它不随累计次数增长，回收才算真的成立。
	st := a.stat()
	if st.HighWater != 2000 {
		t.Fatalf("ID 高水位应为 2000（冷却窗口内周转量），实际 %+v", st)
	}
	if st.Reused != 18000 {
		t.Fatalf("应复用 18000 次，实际 %+v", st)
	}
}

func BenchmarkIDAllocReuse(b *testing.B) {
	a := newIDAlloc()
	a.coolDown = 0
	id := a.alloc(t0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.release(id, t0)
		id = a.alloc(t0)
	}
}

func BenchmarkIDAllocFresh(b *testing.B) {
	a := newIDAlloc()
	a.limit = 1 << 40
	now := t0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.alloc(now)
	}
}
