package world

import (
	"context"
	"testing"
	"time"

	"openworld/internal/mesh"
)

// digestOf 造一份只用于测试的摘要，直接投给 World.Deliver。
func digestOf(t *testing.T, w *World, from string, entries ...mesh.TopoEntry) {
	t.Helper()
	body := mesh.EncodeTopoDigest(&mesh.TopoDigest{From: from, Round: 1, Entries: entries})
	w.Deliver(mesh.MsgTopoDigest, body)
}

func entry(scene uint32, owner string, ver uint64, minX, maxX float32) mesh.TopoEntry {
	return mesh.TopoEntry{
		Scene: scene, Owner: owner, Ver: ver,
		Rect: [4]float32{minX, 0, maxX, 512},
	}
}

// setTopo 把一个场景的归属直接写成指定状态，模拟"对账之前"的本地视图。
func setTopo(t *testing.T, w *World, e mesh.TopoEntry) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	p := mesh.Peer{Node: e.Owner, Scene: e.Scene}
	if _, ok := w.topo.PeerOf(e.Scene); ok {
		w.topo.relocate(e.Scene, e.Owner, e.Ver)
	} else {
		w.topo.bind(p, Rect{MinX: e.Rect[0], MinZ: e.Rect[1], MaxX: e.Rect[2], MaxZ: e.Rect[3]})
		w.topo.setVersion(e.Scene, e.Ver)
	}
}

func ownerAt(t *testing.T, w *World, scene uint32, x, z float32) mesh.Peer {
	t.Helper()
	p, ok := w.PeerAt(x, z)
	if !ok {
		t.Fatalf("坐标 (%.0f,%.0f) 没有归属", x, z)
	}
	return p
}

// TestReconcileAdoptsNewerVersion 验证对账能把"别人的新视图"吃下来。
//
// 这是对账存在的理由：一条 Move 广播丢了，本节点手里还是旧路由，
// 定期互换摘要时对方版本更高，本地必须无条件改成对方的。
func TestReconcileAdoptsNewerVersion(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(3, "n2", 0, 1024, 1536))

	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n2" {
		t.Fatalf("初始归属应该是 n2, got %s", p)
	}

	// n2 在别处把这块地交给了 n1（本节点漏收了那条广播）。
	digestOf(t, w, "n2", entry(3, "n1", 7, 1024, 1536))

	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n1" {
		t.Fatalf("对账之后归属应该改成 n1, got %s", p)
	}
	if st := w.TopoStat(); st.Applied != 1 {
		t.Fatalf("applied=%d, want 1 (%+v)", st.Applied, st)
	}
}

// TestReconcileStaleIsIgnored 验证过期声明不会把新版覆盖回去。
//
// 没有这条，对账会变成"永远相信对端"：谁发得晚谁赢，而网络没有顺序。
func TestReconcileStaleIsIgnored(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(3, "n1", 5, 1024, 1536))

	digestOf(t, w, "n2", entry(3, "n2", 3, 1024, 1536))

	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n1" {
		t.Fatalf("过期声明不该改写归属, got %s", p)
	}
	if st := w.TopoStat(); st.Stale != 1 || st.Pushed != 1 {
		t.Fatalf("应该记一次 stale + 一次 push: %+v", st)
	}
}

// TestReconcileConflictConverges 验证"同版本不同归属"能自动收敛。
//
// 这段逻辑的正确性不在"谁赢"，而在**两边独立算出同一个结果**：
// 双方都取字典序小的节点名，并各自把版本加一，于是下一轮摘要相同，
// 不再有任何动作。如果改成"谁收到谁改"，两边会互相改写，永远不收敛。
func TestReconcileConflictConverges(t *testing.T) {
	n1 := NewWorld(quiet(), WithNodeID("n1"))
	n2 := NewWorld(quiet(), WithNodeID("n2"))
	setTopo(t, n1, entry(3, "n1", 2, 1024, 1536))
	setTopo(t, n2, entry(3, "n2", 2, 1024, 1536))

	// 各自把自己的视图发给对方（模拟一次同时发生的对账）。
	digestOf(t, n1, "n2", entry(3, "n2", 2, 1024, 1536))
	digestOf(t, n2, "n1", entry(3, "n1", 2, 1024, 1536))

	p1 := ownerAt(t, n1, 3, 1100, 100)
	p2 := ownerAt(t, n2, 3, 1100, 100)
	if p1.Node != p2.Node {
		t.Fatalf("两边裁决结果不一致: n1→%s n2→%s", p1.Node, p2.Node)
	}
	if p1.Node != "n1" {
		t.Fatalf("字典序小的应该赢, got %s", p1.Node)
	}
	if v1, v2 := n1.TopoStat().Versions[3], n2.TopoStat().Versions[3]; v1 != 3 || v2 != 3 {
		t.Fatalf("裁决后版本应该都是 3, got n1=%d n2=%d", v1, v2)
	}

	// 再对一轮：两边已经是 (n1, 3)，不该再产生任何裁决。
	digestOf(t, n1, "n2", entry(3, "n1", 3, 1024, 1536))
	digestOf(t, n2, "n1", entry(3, "n1", 3, 1024, 1536))
	if st := n1.TopoStat(); st.Conflicts != 1 {
		t.Fatalf("第二轮不该再产生冲突, conflicts=%d", st.Conflicts)
	}
}

// TestReconcileSkipsMovingScene 验证对账不插手正在搬迁的地。
//
// 搬迁的中间态（目标已接、源还没摘）在拓扑上看起来"两边都说自己是主人"，
// 对账如果按版本号裁决，会把这一刻坐实成永久状态。
func TestReconcileSkipsMovingScene(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	s := w.NewScene(3, 20*time.Millisecond, nil, quiet(),
		WithBounds(Rect{MinX: 1024, MaxX: 1536, MaxZ: 512}))
	setTopo(t, w, entry(3, "n1", 1, 1024, 1536))
	s.movingFlag.Store(true)

	digestOf(t, w, "n2", entry(3, "n2", 9, 1024, 1536))

	if _, ok := w.Scene(3); !ok {
		t.Fatal("搬迁中的场景不该被对账摘掉")
	}
	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n1" {
		t.Fatalf("搬迁中的归属不该被对账改写, got %s", p)
	}
}

// TestReconcileRetiresLocalScene 验证"归属被别人拿走后本地场景要停掉"。
//
// 触发场景是节点崩溃后重启：它按配置重建了自己原来的地，而那块地
// 在这期间已经被别人接管。留着就是双写者——比丢一份刚造出来的副本严重得多。
//
// 同时钉住一个容易漏的账目：退休必须**回收 ID**。第一版直接 Stop，
// 结果一个重启节点退休 100 只怪之后 live 永远停在 100（实测数据），
// M3.1 的冷却回收在这条路径上等于没做。
func TestReconcileRetiresLocalScene(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewWorld(quiet(), WithNodeID("n1"))
	s := w.NewScene(3, 20*time.Millisecond, nil, quiet(),
		WithBounds(Rect{MinX: 1024, MaxX: 1536, MaxZ: 512}))
	setTopo(t, w, entry(3, "n1", 0, 1024, 1536))
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop(context.Background())

	cli := &testClient{ref: mesh.ClientRef{Node: "n1", Session: 11}}
	if id := s.EnterAt("p1", cli, 1100, 100); id == 0 {
		t.Fatal("enter failed")
	}
	waitFor(t, 2*time.Second, func() bool { return s.entityCount.Load() == 1 })

	digestOf(t, w, "n2", entry(3, "n2", 5, 1024, 1536))

	if _, ok := w.Scene(3); ok {
		t.Fatal("归属已经变了，本地场景应该被摘掉")
	}
	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n2" {
		t.Fatalf("归属应该改成 n2, got %s", p)
	}
	waitFor(t, 2*time.Second, func() bool { return w.TopoStat().Retired == 1 })
	// 场景里的实体必须被清掉，ID 必须回到分配器。
	waitFor(t, 2*time.Second, func() bool { return s.entityCount.Load() == 0 })
	if st := w.IDStat(); st.Live != 0 {
		t.Fatalf("退休场景的实体 ID 没有回收, live=%d", st.Live)
	}
	if !s.draining() {
		t.Fatal("退休的场景应该处于 draining 状态")
	}
}

// TestRemoteMoveIgnoresStale 验证广播侧的版本比较。
func TestRemoteMoveIgnoresStale(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(3, "n1", 4, 1024, 1536))

	// 一条迟到的旧广播：版本 3，说这块地归 n2。
	body := mesh.EncodeSceneCtl(&mesh.SceneCtl{
		Op: mesh.SceneOpMove, Scene: 3, From: "n2", To: "n2", Ver: 3,
	})
	w.Deliver(mesh.MsgSceneCtl, body)

	if p := ownerAt(t, w, 3, 1100, 100); p.Node != "n1" {
		t.Fatalf("迟到的旧广播不该改写归属, got %s", p)
	}
}

// TestReconcileOverMesh 是端到端：两个真实节点通过 TCP 互换摘要，
// 把"漏收了一条广播"的节点纠正过来。
func TestReconcileOverMesh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := newTestNode(t, ctx, "n1", 2, 0)
	n2 := newTestNode(t, ctx, "n2", 2, 1)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())

	n1.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.AddPeer("n1", n1.msh.Addr())
	n1.msh.SetHandler(n1.world)
	n2.msh.SetHandler(n2.world)

	// n1 认为 scene3 属于一个已经死掉、配置里也不存在的节点 n0；
	// n2 则已经把它接管了（版本 5）。两边都没有本地场景对象，
	// 所以这条路径只动路由表——正是"漏收广播"之后要修的东西。
	setTopo(t, n1.world, entry(3, "n0", 0, 1024, 1536))
	setTopo(t, n2.world, entry(3, "n2", 5, 1024, 1536))

	if p := ownerAt(t, n1.world, 3, 1100, 100); p.Node != "n0" {
		t.Fatalf("初始归属应该是 n0, got %s", p)
	}

	go n1.world.Reconcile(ctx)

	// 一个周期 2 秒，给足余量。
	waitFor(t, 6*time.Second, func() bool {
		p, ok := n1.world.PeerAt(1100, 100)
		return ok && p.Node == "n2"
	})
	if st := n1.world.TopoStat(); st.Applied == 0 {
		t.Fatalf("应该是靠对账纠正的, stat=%+v", st)
	}
}
