package world

import (
	"context"
	"fmt"
	"testing"
	"time"

	"openworld/internal/mesh"
	"openworld/internal/protocol"
)

// repartitionNode 在 newTestNode 的基础上补一个场景工厂：
// 重分区要在运行期原地造场景，而 World 不知道 tick 间隔和玩法。
func repartitionNode(t *testing.T, ctx context.Context, id string, stride, offset uint64) *node {
	t.Helper()
	n := newTestNode(t, ctx, id, stride, offset)
	n.world.factory = func(spec SceneSpec) (*Scene, error) {
		return n.world.NewSceneDetached(spec.ID, 20*time.Millisecond, nil, quiet(),
			WithBounds(spec.RectOf())), nil
	}
	return n
}

// TestMoveSceneAcrossNodes 是 M3.5 的主路径：把 n1 的 scene3 整块搬到 n2，
// 地上的玩家跟着走，两边的拓扑与路由都要一致。
func TestMoveSceneAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := repartitionNode(t, ctx, "n1", 2, 0)
	n2 := repartitionNode(t, ctx, "n2", 2, 1)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())

	n1.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.AddPeer("n1", n1.msh.Addr())
	n1.msh.SetHandler(n1.world)
	n2.msh.SetHandler(n2.world)

	const players = 20
	clients := make([]*testClient, players)
	bySession := make(map[uint64]*testClient, players)
	for i := range clients {
		cli := &testClient{ref: mesh.ClientRef{Node: "n1", Session: uint64(100 + i)}}
		clients[i] = cli
		bySession[cli.ref.Session] = cli
	}
	resolver := func(ref mesh.ClientRef) Client {
		if ref.Node != "n1" {
			return nil
		}
		if c, ok := bySession[ref.Session]; ok {
			return c
		}
		return nil
	}
	n1.world.resolve = resolver
	n2.world.resolve = resolver

	// 三块地：n1 两块、n2 一块。每个节点都要知道另外两块的归属，
	// 否则玩家走到边界时路由落空，也就不会被迁过去。
	b1 := Rect{MinX: 0, MaxX: 512, MaxZ: 512}
	b2 := Rect{MinX: 512, MaxX: 1024, MaxZ: 512}
	b3 := Rect{MinX: 1024, MaxX: 1536, MaxZ: 512}
	if err := n1.world.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, b2); err != nil {
		t.Fatal(err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 3}, b3); err != nil {
		t.Fatal(err)
	}

	s1 := n1.world.NewScene(1, 20*time.Millisecond, nil, quiet(), WithBounds(b1))
	s3 := n1.world.NewScene(3, 20*time.Millisecond, nil, quiet(), WithBounds(b3))
	s2 := n2.world.NewScene(2, 20*time.Millisecond, nil, quiet(), WithBounds(b2))
	for _, s := range []*Scene{s1, s3, s2} {
		if err := s.Start(ctx); err != nil {
			t.Fatalf("start scene %d: %v", s.ID(), err)
		}
		defer s.Stop(context.Background())
	}

	for i := 0; i < players; i++ {
		if id := s3.EnterAt(fmt.Sprintf("p%d", i), clients[i], 1100+float32(i), 100); id == 0 {
			t.Fatalf("第 %d 个玩家进场失败", i)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return s3.entityCount.Load() == players })

	if err := n1.world.MoveScene(3, "n2"); err != nil {
		t.Fatalf("发起搬迁失败: %v", err)
	}

	// 等的是"源节点退休"而不是"目标收了 20 个实体"：后者会早几十毫秒发生，
	// 实体搬完和拓扑切换之间还隔着一次 Activate/Activated 往返。
	waitFor(t, 10*time.Second, func() bool {
		if _, ok := n1.world.Scene(3); ok {
			return false
		}
		s, ok := n2.world.Scene(3)
		return ok && s.entityCount.Load() == players
	})

	// 源节点不应该再持有这个场景：它已经退休，tick 协程退出，
	// 拓扑里也只留一块"别人的地"。
	if s, ok := n1.world.Scene(3); ok {
		t.Fatalf("搬迁完成后 n1 仍然持有 scene3（entities=%d）", s.entityCount.Load())
	}
	if p, ok := n1.world.PeerOf(3); !ok || p.Node != "n2" || p.Scene != 3 {
		t.Fatalf("n1 的拓扑没切过去: %+v ok=%v", p, ok)
	}
	if p, ok := n2.world.PeerOf(3); !ok || p.Node != "n2" {
		t.Fatalf("n2 的拓扑没接上: %+v ok=%v", p, ok)
	}

	// 路由必须跟着归属走：从两个节点看这块地，答案都得是 n2。
	for _, w := range []*World{n1.world, n2.world} {
		p, ok := w.PeerAt(1100, 100)
		if !ok || p.Node != "n2" || p.Scene != 3 {
			t.Fatalf("%s 的路由仍然指向 %+v ok=%v", w.Node(), p, ok)
		}
	}

	// 客户端全部收到新的场景号：玩家过界之后看到的是"另一块地"，
	// 不是"原地消失又在原地出现"。
	n2s, _ := n2.world.Scene(3)
	for i, cli := range clients {
		if !cli.got(protocol.MsgS2CEnterScene) {
			t.Fatalf("第 %d 个客户端没收到 EnterSceneResp", i)
		}
		if p, ok := cli.routeOf(); !ok || p.Node != "n2" || p.Scene != 3 {
			t.Fatalf("第 %d 个客户端的路由没切过去: %+v ok=%v", i, p, ok)
		}
	}
	if n2s.Bounds().MinX != 1024 {
		t.Fatalf("接管后的场景边界不对: %s", n2s.Bounds())
	}

	if out := s3.Stats().HandoversOut; out != players {
		t.Fatalf("源场景 handoversOut=%d, want %d", out, players)
	}
	if in := n2s.Stats().HandoversIn; in != players {
		t.Fatalf("目标场景 handoversIn=%d, want %d", in, players)
	}
	if f := s3.Stats().HandoverFails + n2s.Stats().HandoverFails; f != 0 {
		t.Fatalf("搬迁过程有 %d 次失败", f)
	}
	if st := s3.Stats(); st.Move != nil {
		t.Fatalf("搬迁完成后还留着进度快照: %+v", st.Move)
	}
}

// TestMoveSceneRejectsLiveOwner 保证第三方不能替一个活着的节点搬地。
//
// 这条约束看着保守，其实是正确性要求：活着的节点还在往这块地上迁玩家、
// 写实体，把地抽走等于让它继续往一个已经不归它的场景里写——两个写者。
func TestMoveSceneRejectsLiveOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := repartitionNode(t, ctx, "n1", 2, 0)
	n2 := repartitionNode(t, ctx, "n2", 2, 1)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	n1.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.AddPeer("n1", n1.msh.Addr())
	n1.msh.SetHandler(n1.world)
	n2.msh.SetHandler(n2.world)

	b1 := Rect{MinX: 0, MaxX: 512, MaxZ: 512}
	b2 := Rect{MinX: 512, MaxX: 1024, MaxZ: 512}
	if err := n1.world.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, b2); err != nil {
		t.Fatal(err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}
	s1 := n1.world.NewScene(1, 20*time.Millisecond, nil, quiet(), WithBounds(b1))
	if err := s1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s1.Stop(context.Background())

	// n2 眼里 scene1 归 n1，而 n1 是活的：拒绝。
	if err := n2.world.MoveScene(1, "n2"); err == nil {
		t.Fatal("应该拒绝接管一个活着的节点上的场景")
	}
	if p, _ := n1.world.PeerOf(1); p.Node != "n1" {
		t.Fatalf("拓扑被改动了: %+v", p)
	}
}

// TestReclaimDeadScene 是掉线接管路径：主人已经死了，地上没人，
// 只需要**改归属**，一次实体迁移都不用发。
func TestReclaimDeadScene(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := repartitionNode(t, ctx, "n1", 2, 0)
	n2 := repartitionNode(t, ctx, "n2", 2, 1)
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	n1.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.AddPeer("n1", n1.msh.Addr())
	n1.msh.SetHandler(n1.world)
	n2.msh.SetHandler(n2.world)

	b1 := Rect{MinX: 0, MaxX: 512, MaxZ: 512}
	b2 := Rect{MinX: 512, MaxX: 1024, MaxZ: 512}
	if err := n1.world.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, b2); err != nil {
		t.Fatal(err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}
	s2 := n2.world.NewScene(2, 20*time.Millisecond, nil, quiet(), WithBounds(b2))
	if err := s2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s2.Stop(context.Background())

	// n1 挂了：它的地块从 n2 的路由里消失，玩家被夹在边界上。
	n2.world.SetNodeAlive("n1", false)
	if _, ok := n2.world.PeerAt(100, 100); ok {
		t.Fatal("死掉的节点不该还能路由")
	}

	// 让 n2 接管。是"本节点自己接"，所以没有 Prepare/Activate 往返，
	// 但要广播给其它节点。
	if err := n2.world.MoveScene(1, "n2"); err != nil {
		t.Fatalf("接管失败: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		s, ok := n2.world.Scene(1)
		return ok && !s.Standby()
	})

	if p, ok := n2.world.PeerOf(1); !ok || p.Node != "n2" {
		t.Fatalf("接管后拓扑没切过去: %+v ok=%v", p, ok)
	}
	// 接管之后这块地必须重新可达：这是"节点崩了玩家还能过来"的全部意义。
	if p, ok := n2.world.PeerAt(100, 100); !ok || p.Node != "n2" || p.Scene != 1 {
		t.Fatalf("接管后仍然不可达: %+v ok=%v", p, ok)
	}
}

// TestReclaimByThirdParty 覆盖"由第三节点发起接管"的路径。
//
// 它和 selfReclaim 走的是完全不同的代码：Prepare/Activate 要经过两次
// 跨节点往返，而且发起方自己也要改路由。这条路径上曾经漏设一个
// OK 标记，结果是目标节点已经把地接管了、发起方却一直以为被拒绝——
// 两边对"这块地归谁"记着不同的答案，而且没有任何报错。
func TestReclaimByThirdParty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n2 := repartitionNode(t, ctx, "n2", 2, 1)
	n3 := repartitionNode(t, ctx, "n3", 2, 0)
	defer n2.msh.Stop(context.Background())
	defer n3.msh.Stop(context.Background())
	n2.msh.AddPeer("n3", n3.msh.Addr())
	n3.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.SetHandler(n2.world)
	n3.msh.SetHandler(n3.world)

	// scene1 原本归 n1，而 n1 已经死了（测试里不需要真的起它）。
	b1 := Rect{MinX: 0, MaxX: 512, MaxZ: 512}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}
	if err := n3.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}
	n2.world.SetNodeAlive("n1", false)
	n3.world.SetNodeAlive("n1", false)

	if err := n2.world.MoveScene(1, "n3"); err != nil {
		t.Fatalf("发起接管失败: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		p, ok := n2.world.PeerOf(1)
		s, ok2 := n3.world.Scene(1)
		return ok && ok2 && p.Node == "n3" && !s.Standby()
	})

	// 发起方必须自己把路由改掉：它是从自己的 admin 接口接的单，
	// 不代表它就知道结果——这条断言就是那次"漏设 OK"的直接回归。
	if p, ok := n2.world.PeerAt(100, 100); !ok || p.Node != "n3" {
		t.Fatalf("发起方的路由没有更新: %+v ok=%v", p, ok)
	}
	if p, ok := n3.world.PeerAt(100, 100); !ok || p.Node != "n3" {
		t.Fatalf("接管方的路由不对: %+v ok=%v", p, ok)
	}
}

// TestMoveSceneAbortOnReject 验证拒绝路径能干净回滚。
//
// 目标节点没有场景工厂 → Prepare 被拒 → 源场景必须恢复接客，
// 一个实体都不能少。这条路径是"还没搬人之前可以干净回头"的证据。
func TestMoveSceneAbortOnReject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := repartitionNode(t, ctx, "n1", 2, 0)
	n2 := newTestNode(t, ctx, "n2", 2, 1) // 故意不配工厂
	defer n1.msh.Stop(context.Background())
	defer n2.msh.Stop(context.Background())
	n1.msh.AddPeer("n2", n2.msh.Addr())
	n2.msh.AddPeer("n1", n1.msh.Addr())
	n1.msh.SetHandler(n1.world)
	n2.msh.SetHandler(n2.world)

	b1 := Rect{MinX: 0, MaxX: 512, MaxZ: 512}
	b2 := Rect{MinX: 512, MaxX: 1024, MaxZ: 512}
	if err := n1.world.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, b2); err != nil {
		t.Fatal(err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, b1); err != nil {
		t.Fatal(err)
	}

	cli := &testClient{ref: mesh.ClientRef{Node: "n1", Session: 1}}
	n1.world.resolve = func(mesh.ClientRef) Client { return cli }

	s1 := n1.world.NewScene(1, 20*time.Millisecond, nil, quiet(), WithBounds(b1))
	if err := s1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s1.Stop(context.Background())

	if id := s1.EnterAt("p", cli, 100, 100); id == 0 {
		t.Fatal("进场失败")
	}
	waitFor(t, 2*time.Second, func() bool { return s1.entityCount.Load() == 1 })

	if err := n1.world.MoveScene(1, "n2"); err != nil {
		t.Fatalf("发起搬迁失败: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		st := s1.Stats()
		return st.Move == nil && !st.Draining
	})
	if got := s1.entityCount.Load(); got != 1 {
		t.Fatalf("回滚之后实体丢了: entities=%d", got)
	}
	if p, _ := n1.world.PeerOf(1); p.Node != "n1" {
		t.Fatalf("回滚之后拓扑不该变: %+v", p)
	}
	// 回滚不能算失败迁移：那个计数器是给"玩家跨界失败"用的，
	// 混进重分区的拒绝会让它失去意义。
	if f := s1.Stats().HandoverFails; f != 0 {
		t.Fatalf("回滚被计成了 %d 次迁移失败", f)
	}
}
