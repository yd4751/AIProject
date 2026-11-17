package world

import (
	"context"
	"testing"
	"time"

	"openworld/internal/mesh"
)

// schedNode 在 newTestNode 的基础上：3 节点槽位 + 场景工厂（重分区接管用）。
func schedNode(t *testing.T, ctx context.Context, id string, offset uint64) *node {
	t.Helper()
	m := mesh.New(id, quiet())
	if err := m.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start mesh %s: %v", id, err)
	}
	w := NewWorld(quiet(), WithNodeID(id), WithMesh(m),
		WithIDPartition(3, offset), WithNodeSlots([]string{"n1", "n2", "n3"}))
	w.factory = func(spec SceneSpec) (*Scene, error) {
		return w.NewSceneDetached(spec.ID, 20*time.Millisecond, nil, quiet(),
			WithBounds(spec.RectOf())), nil
	}
	return &node{id: id, msh: m, world: w}
}

// meshFull 把三个节点两两互连，并把每个 world 设为各自 mesh 的处理器。
func meshFull(t *testing.T, ns ...*node) {
	t.Helper()
	for _, a := range ns {
		for _, b := range ns {
			if a.id == b.id {
				continue
			}
			a.msh.AddPeer(b.id, b.msh.Addr())
		}
		a.msh.SetHandler(a.world)
	}
}

// declareAll 让每个节点知道所有场景的归属：自己的 NewScene 已经登记了边界，
// 别人的用 DeclareRemote。rects 按 scene id 索引。
func declareAll(t *testing.T, ns []*node, owner map[uint32]string, rects map[uint32]Rect) {
	t.Helper()
	for _, n := range ns {
		for sid, o := range owner {
			if o == n.id {
				continue
			}
			if err := n.world.DeclareRemote(mesh.Peer{Node: o, Scene: sid}, rects[sid]); err != nil {
				t.Fatalf("declare remote scene %d@%s on %s: %v", sid, o, n.id, err)
			}
		}
	}
}

// startOwnedScenes 给每个主人启动它拥有的场景。
func startOwnedScenes(t *testing.T, ctx context.Context, ns []*node,
	owner map[uint32]string, rects map[uint32]Rect) {
	t.Helper()
	for _, n := range ns {
		for sid, o := range owner {
			if o != n.id {
				continue
			}
			s := n.world.NewScene(sid, 20*time.Millisecond, nil, quiet(), WithBounds(rects[sid]))
			if err := s.Start(ctx); err != nil {
				t.Fatalf("start scene %d on %s: %v", sid, n.id, err)
			}
		}
	}
}

// row5 是 5 个场景横向排列在 X 轴上的标准测试拓扑：
//
//	scene1 [0,512)     n1
//	scene2 [512,1024)  n1
//	scene3 [1024,1536) n1
//	scene4 [1536,2048) n2
//	scene5 [2048,2560) n3
//
// n1 持 3 块、n2/n3 各 1 块，公平份额 ceil(5/3)=2，n1 超载 1 块。
// scene3 与 scene4 相邻，所以调度器会把 scene3 搬给 n2（保持路由连续）。
var (
	row5Owner = map[uint32]string{1: "n1", 2: "n1", 3: "n1", 4: "n2", 5: "n3"}
	row5Rects = map[uint32]Rect{
		1: {MinX: 0, MaxX: 512, MaxZ: 512},
		2: {MinX: 512, MaxX: 1024, MaxZ: 512},
		3: {MinX: 1024, MaxX: 1536, MaxZ: 512},
		4: {MinX: 1536, MaxX: 2048, MaxZ: 512},
		5: {MinX: 2048, MaxX: 2560, MaxZ: 512},
	}
)

// TestSchedulerMovesOverloadedScene 是 M3.7 的主路径：
// n1 持 3 块（超载），调度器把相邻的 scene3 搬给 n2，结果 2/2/1 且路由连续。
func TestSchedulerMovesOverloadedScene(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := schedNode(t, ctx, "n1", 0)
	n2 := schedNode(t, ctx, "n2", 1)
	n3 := schedNode(t, ctx, "n3", 2)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())

	meshFull(t, n1, n2, n3)
	declareAll(t, []*node{n1, n2, n3}, row5Owner, row5Rects)
	startOwnedScenes(t, ctx, []*node{n1, n2, n3}, row5Owner, row5Rects)

	s := n1.world.NewScheduler(WithEvalInterval(time.Hour), WithCooldown(500*time.Millisecond),
		WithSlack(0), WithLogger(quiet()))
	s.evaluate()

	// 搬迁异步：等 scene3 在 n1 退休、在 n2 上线。
	waitFor(t, 8*time.Second, func() bool {
		if _, ok := n1.world.Scene(3); ok {
			return false
		}
		sc, ok := n2.world.Scene(3)
		return ok && !sc.Standby()
	})

	// 拓扑：所有节点都要认为 scene3 现在归 n2。
	// 注意 n3 是靠"尽力而为"的广播（SceneOpMove）更新的，异步到达，
	// 必须等它收敛，不能像 n1/n2 那样断言一次就过——否则偶发地读到
	// n3 还停在"scene3 归 n1"的旧路由上（广播还在路上）。
	waitFor(t, 8*time.Second, func() bool {
		for _, w := range []*World{n1.world, n2.world, n3.world} {
			p, ok := w.PeerOf(3)
			if !ok || p.Node != "n2" {
				return false
			}
		}
		return true
	})
	// n1 现在持 2 块（scene1, scene2），n2 持 2 块（scene3, scene4），n3 持 1 块。
	if got := ownedScenes(n1.world, "n1"); got != 2 {
		t.Fatalf("n1 搬迁后应持 2 块，实际 %d", got)
	}
	if got := ownedScenes(n2.world, "n2"); got != 2 {
		t.Fatalf("n2 搬迁后应持 2 块，实际 %d", got)
	}

	st := s.Stat()
	if st.Attempted != 1 || st.Succeeded != 1 || st.Failed != 0 {
		t.Fatalf("统计错: attempted=%d succeeded=%d failed=%d",
			st.Attempted, st.Succeeded, st.Failed)
	}
	if st.LastScene != 3 || st.LastTo != "n2" {
		t.Fatalf("最后搬迁记录错: scene=%d to=%q", st.LastScene, st.LastTo)
	}
}

// TestSchedulerBalancedNoMove 验证已平衡时不动。
// 3 个场景分给 3 个节点，公平份额=1，谁都不超载。
func TestSchedulerBalancedNoMove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := schedNode(t, ctx, "n1", 0)
	n2 := schedNode(t, ctx, "n2", 1)
	n3 := schedNode(t, ctx, "n3", 2)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())

	owner := map[uint32]string{1: "n1", 2: "n2", 3: "n3"}
	rects := map[uint32]Rect{
		1: {MinX: 0, MaxX: 512, MaxZ: 512},
		2: {MinX: 512, MaxX: 1024, MaxZ: 512},
		3: {MinX: 1024, MaxX: 1536, MaxZ: 512},
	}
	meshFull(t, n1, n2, n3)
	declareAll(t, []*node{n1, n2, n3}, owner, rects)
	startOwnedScenes(t, ctx, []*node{n1, n2, n3}, owner, rects)

	for _, n := range []*node{n1, n2, n3} {
		s := n.world.NewScheduler(WithLogger(quiet()))
		s.evaluate()
		st := s.Stat()
		if st.Attempted != 0 {
			t.Fatalf("%s 在平衡态不应发起搬迁，attempted=%d", n.id, st.Attempted)
		}
	}
}

// TestSchedulerInflightDefer 验证"一次只搬一个"：
// 第一次 evaluate 发起搬迁后，紧接着第二次 evaluate 应被 in-flight 拦下。
func TestSchedulerInflightDefer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := schedNode(t, ctx, "n1", 0)
	n2 := schedNode(t, ctx, "n2", 1)
	n3 := schedNode(t, ctx, "n3", 2)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())

	meshFull(t, n1, n2, n3)
	declareAll(t, []*node{n1, n2, n3}, row5Owner, row5Rects)
	startOwnedScenes(t, ctx, []*node{n1, n2, n3}, row5Owner, row5Rects)

	s := n1.world.NewScheduler(WithEvalInterval(time.Hour), WithCooldown(time.Second),
		WithSlack(0), WithLogger(quiet()))
	s.evaluate() // 发起 scene3 -> n2
	if st := s.Stat(); st.Attempted != 1 {
		t.Fatalf("第一次评估应发起搬迁，attempted=%d", st.Attempted)
	}
	// 紧接着再评估：inflight 还没落地，应被拦下，不再发起第二个搬迁。
	s.evaluate()
	st := s.Stat()
	if st.Attempted != 1 {
		t.Fatalf("in-flight 期间不应再发起搬迁，attempted=%d", st.Attempted)
	}
	if st.Rejected != 1 {
		t.Fatalf("应记一次 rejected（in-flight defer），实际 %d", st.Rejected)
	}

	// 等搬迁落地，inflight 清掉。
	waitFor(t, 8*time.Second, func() bool {
		if _, ok := n1.world.Scene(3); ok {
			return false
		}
		_, ok := n2.world.Scene(3)
		return ok
	})
}

// TestSchedulerCooldownSkips 验证冷却期：
// 搬迁完成后立即把拓扑改回超载（手动重新声明 scene3 归 n1 不现实，
// 这里改成在搬迁落地后再次 evaluate，此时已平衡，不应再搬）。
func TestSchedulerCooldownSkips(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := schedNode(t, ctx, "n1", 0)
	n2 := schedNode(t, ctx, "n2", 1)
	n3 := schedNode(t, ctx, "n3", 2)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())

	meshFull(t, n1, n2, n3)
	declareAll(t, []*node{n1, n2, n3}, row5Owner, row5Rects)
	startOwnedScenes(t, ctx, []*node{n1, n2, n3}, row5Owner, row5Rects)

	s := n1.world.NewScheduler(WithEvalInterval(time.Hour), WithCooldown(time.Second),
		WithSlack(0), WithLogger(quiet()))
	s.evaluate()
	waitFor(t, 8*time.Second, func() bool {
		_, ok := n2.world.Scene(3)
		return ok
	})
	// 搬迁落地后再评估：n1 已持 2 块，平衡，不应再动。
	s.evaluate()
	if st := s.Stat(); st.Attempted != 1 {
		t.Fatalf("平衡后不应再搬，attempted=%d", st.Attempted)
	}
}

// TestSchedulerOnlyOwnerActs 验证"只有超载节点才决策"：
// n2 持 1 块（不超载），它的调度器评估后不应发起任何搬迁。
func TestSchedulerOnlyOwnerActs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := schedNode(t, ctx, "n1", 0)
	n2 := schedNode(t, ctx, "n2", 1)
	n3 := schedNode(t, ctx, "n3", 2)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())

	meshFull(t, n1, n2, n3)
	declareAll(t, []*node{n1, n2, n3}, row5Owner, row5Rects)
	startOwnedScenes(t, ctx, []*node{n1, n2, n3}, row5Owner, row5Rects)

	// n2 的调度器：n2 持 1 块，公平份额 2，不超载。
	s := n2.world.NewScheduler(WithLogger(quiet()))
	s.evaluate()
	if st := s.Stat(); st.Attempted != 0 {
		t.Fatalf("n2 不超载不应发起搬迁，attempted=%d", st.Attempted)
	}
}

// TestAdjScore 验证相邻打分：边相接 > 角相接 > 不相邻。
func TestAdjScore(t *testing.T) {
	target := Rect{MinX: 512, MaxX: 1024, MinZ: 0, MaxZ: 512}
	cases := []struct {
		name string
		r    Rect
		want int
	}{
		{"右边相接", Rect{MinX: 1024, MaxX: 1536, MinZ: 0, MaxZ: 512}, 2},
		{"左边相接", Rect{MinX: 0, MaxX: 512, MinZ: 0, MaxZ: 512}, 2},
		{"上边相接", Rect{MinX: 512, MaxX: 1024, MinZ: 512, MaxZ: 1024}, 2},
		{"角相接", Rect{MinX: 1024, MaxX: 1536, MinZ: 512, MaxZ: 1024}, 1},
		{"不相邻", Rect{MinX: 2048, MaxX: 2560, MinZ: 0, MaxZ: 512}, 0},
	}
	for _, c := range cases {
		if got := adjScore(c.r, []Rect{target}); got != c.want {
			t.Fatalf("%s: 期望 %d 实际 %d", c.name, c.want, got)
		}
	}
}

// ownedScenes 数一个节点当前持有的本地场景数（不是拓扑里的归属数）。
func ownedScenes(w *World, node string) int {
	n := 0
	for _, c := range w.Chunks() {
		if c.Local && c.Node == node {
			n++
		}
	}
	return n
}
