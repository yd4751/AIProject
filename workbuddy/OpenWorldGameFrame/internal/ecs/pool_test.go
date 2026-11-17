package ecs

import (
	"math/rand"
	"testing"
)

type testComp struct {
	HP  int32
	Tag string
}

var (
	cTest  = Define[testComp]("test")
	cOther = Define[int]("other")
)

func TestPoolAddGetRemove(t *testing.T) {
	s := NewStore()
	p := Get(s, cTest)

	if p.Len() != 0 {
		t.Fatalf("新池应为空，实际 %d", p.Len())
	}
	if p.Get(1) != nil || p.Has(1) {
		t.Fatal("未挂载时 Get/Has 应为空")
	}

	p.Add(1, testComp{HP: 100})
	p.Add(2, testComp{HP: 50})

	if !p.Has(2) || p.Len() != 2 {
		t.Fatalf("添加后应有 2 个组件，实际 %d", p.Len())
	}
	if got := p.Get(1); got == nil || got.HP != 100 {
		t.Fatalf("实体 1 的组件应为 HP=100，实际 %+v", got)
	}

	// 覆盖语义：第二次 Add 不应该多出一个元素。
	p.Add(1, testComp{HP: 30})
	if p.Len() != 2 {
		t.Fatalf("重复 Add 不应增加数量，实际 %d", p.Len())
	}
	if got := p.Get(1); got.HP != 30 {
		t.Fatalf("重复 Add 应覆盖旧值，实际 %+v", got)
	}

	if !p.Remove(1) {
		t.Fatal("Remove 已存在的组件应返回 true")
	}
	if p.Remove(1) {
		t.Fatal("重复 Remove 应返回 false")
	}
	if p.Len() != 1 || p.Has(1) {
		t.Fatalf("删除后应只剩实体 2，实际 len=%d has(1)=%v", p.Len(), p.Has(1))
	}
}

// TestPoolRemoveKeepsSparseConsistent 验证 swap-remove 之后，
// 被搬到删除位置的那个元素仍能被正确找到。
//
// 这是稀疏集实现里最容易错的一处：只更新 dense 忘了更新 sparse，
// 表现是"删完 A 之后，B 的组件读出来是别人的数据"，线上极难定位。
func TestPoolRemoveKeepsSparseConsistent(t *testing.T) {
	s := NewStore()
	p := Get(s, cTest)

	for i := uint64(1); i <= 100; i++ {
		p.Add(i, testComp{HP: int32(i)})
	}
	// 从中间删，保证每次都触发 swap。
	for i := uint64(1); i <= 50; i++ {
		p.Remove(i)
	}
	for i := uint64(51); i <= 100; i++ {
		got := p.Get(i)
		if got == nil {
			t.Fatalf("实体 %d 不应被删掉", i)
		}
		if got.HP != int32(i) {
			t.Fatalf("实体 %d 的组件被串成 HP=%d", i, got.HP)
		}
	}
	if p.Len() != 50 {
		t.Fatalf("应剩 50 个，实际 %d", p.Len())
	}
}

// TestPoolRandomOpsMatchMap 用随机操作与 map 做对拍。
//
// 单点用例覆盖不到"扩容 + swap + 覆盖"交错出现的组合，
// 对拍是最省事也最可靠的办法：只要求最终状态一致。
func TestPoolRandomOpsMatchMap(t *testing.T) {
	const (
		rounds = 20
		maxID  = 300
	)
	for r := 0; r < rounds; r++ {
		s := NewStore()
		p := Get(s, cTest)
		ref := make(map[uint64]testComp)

		rng := rand.New(rand.NewSource(int64(r)))
		for step := 0; step < 2000; step++ {
			id := uint64(rng.Intn(maxID)) + 1
			switch rng.Intn(3) {
			case 0, 1:
				v := testComp{HP: int32(rng.Intn(1000))}
				p.Add(id, v)
				ref[id] = v
			default:
				removed := p.Remove(id)
				_, existed := ref[id]
				if removed != existed {
					t.Fatalf("第 %d 轮第 %d 步：Remove(%d) 返回 %v，期望 %v", r, step, id, removed, existed)
				}
				delete(ref, id)
			}
		}

		if p.Len() != len(ref) {
			t.Fatalf("第 %d 轮：池内 %d 个，map 内 %d 个", r, p.Len(), len(ref))
		}
		for id, want := range ref {
			got := p.Get(id)
			if got == nil || *got != want {
				t.Fatalf("第 %d 轮：实体 %d 期望 %+v，实际 %+v", r, id, want, got)
			}
		}
		// 遍历出来的集合必须和 map 完全一致（不多不漏）。
		seen := make(map[uint64]bool, len(ref))
		p.Each(func(id uint64, v *testComp) {
			if seen[id] {
				t.Fatalf("第 %d 轮：遍历重复输出实体 %d", r, id)
			}
			seen[id] = true
			if want, ok := ref[id]; !ok || *v != want {
				t.Fatalf("第 %d 轮：遍历到实体 %d 的数据不符", r, id)
			}
		})
		if len(seen) != len(ref) {
			t.Fatalf("第 %d 轮：遍历出 %d 个，期望 %d 个", r, len(seen), len(ref))
		}
	}
}

func TestPoolRejectsZeroID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("实体 ID 0 应该 panic")
		}
	}()
	Get(NewStore(), cTest).Add(0, testComp{})
}

// TestGetIsIdempotent 保证同一个 Store 上重复 Get 不会重置数据。
func TestGetIsIdempotent(t *testing.T) {
	s := NewStore()
	Get(s, cTest).Add(7, testComp{HP: 42})
	if got := Get(s, cTest).Get(7); got == nil || got.HP != 42 {
		t.Fatal("重复 Get 不应清空已存数据")
	}
}

// TestStoreIsolation 两个 Store 之间数据必须完全隔离。
func TestStoreIsolation(t *testing.T) {
	a, b := NewStore(), NewStore()
	Get(a, cTest).Add(1, testComp{HP: 1})
	if Get(b, cTest).Has(1) {
		t.Fatal("不同 Store 的组件池不应共享数据")
	}
}

func TestStoreDestroyRemovesAllComponents(t *testing.T) {
	s := NewStore()
	Get(s, cTest).Add(9, testComp{HP: 9})
	Get(s, cOther).Add(9, 99)
	Get(s, cOther).Add(10, 100)

	s.Destroy(9)

	if Get(s, cTest).Has(9) || Get(s, cOther).Has(9) {
		t.Fatal("Destroy 应清掉该实体的所有组件")
	}
	if !Get(s, cOther).Has(10) {
		t.Fatal("Destroy 不应影响其他实体")
	}
}

func TestEach2YieldsIntersection(t *testing.T) {
	s := NewStore()
	pa, pb := Get(s, cTest), Get(s, cOther)
	// A: 1,2,3   B: 2,3,4  → 交集 2,3
	pa.Add(1, testComp{HP: 1})
	pa.Add(2, testComp{HP: 2})
	pa.Add(3, testComp{HP: 3})
	pb.Add(2, 20)
	pb.Add(3, 30)
	pb.Add(4, 40)

	got := map[uint64]int32{}
	Each2(s, cTest, cOther, func(id uint64, a *testComp, b *int) {
		if _, dup := got[id]; dup {
			t.Fatalf("实体 %d 被遍历两次", id)
		}
		got[id] = a.HP
		if *b != int(id*10) {
			t.Fatalf("实体 %d 的第二个组件值不对：%d", id, *b)
		}
	})
	if len(got) != 2 || got[2] != 2 || got[3] != 3 {
		t.Fatalf("交集应为 {2,3}，实际 %v", got)
	}

	// 交换参数顺序，结果必须一致（内部会挑小池子，两边的代码路径不同）。
	got2 := 0
	Each2(s, cOther, cTest, func(id uint64, b *int, a *testComp) { got2++ })
	if got2 != 2 {
		t.Fatalf("交换顺序后交集应仍为 2，实际 %d", got2)
	}
}

func TestEach3(t *testing.T) {
	s := NewStore()
	Get(s, cTest).Add(1, testComp{HP: 1})
	Get(s, cTest).Add(2, testComp{HP: 2})
	Get(s, cOther).Add(1, 10)
	Get(s, cOther).Add(2, 20)
	cThird := Define[float32]("third")
	Get(s, cThird).Add(2, 2.5)

	n := 0
	Each3(s, cTest, cOther, cThird, func(id uint64, a *testComp, b *int, c *float32) {
		n++
		if id != 2 {
			t.Fatalf("只有实体 2 同时有三种组件，实际遍历到 %d", id)
		}
		if *c != 2.5 {
			t.Fatalf("第三个组件值不对：%v", *c)
		}
	})
	if n != 1 {
		t.Fatalf("应遍历到 1 个实体，实际 %d", n)
	}
}

func TestStoreStats(t *testing.T) {
	s := NewStore()
	Get(s, cTest).Add(1, testComp{})
	Get(s, cOther).Add(1, 1)

	stats := s.Stats()
	if len(stats) != 2 {
		t.Fatalf("应有两个池，实际 %d", len(stats))
	}
	for _, st := range stats {
		if st.Len != 1 {
			t.Fatalf("池 %s 应有 1 个组件，实际 %d", st.Name, st.Len)
		}
	}
}
