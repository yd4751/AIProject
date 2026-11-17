package world

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"openworld/internal/mesh"
	"openworld/internal/protocol"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testClient 是跨节点测试用的玩家出口。
//
// 它同时实现 Client、Locator、Router：Locator 给出"连接挂在哪个节点"，
// 任意节点都能靠它把下行包转回这里——真实实现里这一层是 gate.remoteClient，
// 这里用一个共享对象代替，验证的是世界层的契约：payload 只带地址、
// 落地侧用 resolver 还原、落地后回调 RouteTo。
type testClient struct {
	ref mesh.ClientRef

	mu       sync.Mutex
	msgs     []uint16
	route    mesh.Peer
	routedTo bool
}

func (c *testClient) Send(msgID uint16, body []byte) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, msgID)
	c.mu.Unlock()
	return nil
}

func (c *testClient) Kick(uint8, string) error { return nil }

func (c *testClient) Locate() mesh.ClientRef { return c.ref }

func (c *testClient) RouteTo(p mesh.Peer, entity uint64, x, y, z float32) {
	c.mu.Lock()
	c.route = p
	c.routedTo = true
	c.mu.Unlock()
}

func (c *testClient) got(msgID uint16) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.msgs {
		if m == msgID {
			return true
		}
	}
	return false
}

func (c *testClient) routeOf() (mesh.Peer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.route, c.routedTo
}

// node 把"一个进程"需要的东西打包：链路 + 世界 + 若干场景。
type node struct {
	id    string
	msh   *mesh.Mesh
	world *World
}

func newTestNode(t *testing.T, ctx context.Context, id string, stride, offset uint64) *node {
	t.Helper()
	m := mesh.New(id, quiet())
	if err := m.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start mesh %s: %v", id, err)
	}
	w := NewWorld(quiet(), WithNodeID(id), WithMesh(m),
		WithIDPartition(stride, offset), WithNodeSlots([]string{"n1", "n2"}))
	return &node{id: id, msh: m, world: w}
}

// TestCrossNodeHandover 验证实体从一个进程的场景走到另一个进程的场景。
//
// 这是 M3.3 的核心路径：三阶段握手全部走真实 TCP，只有客户端出口
// 用共享对象代替（真实的 gate.remoteClient 干的是同一件事）。
func TestCrossNodeHandover(t *testing.T) {
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

	// 拓扑：n1 管 [0,512)，n2 管 [512,1024)。两边都要知道对方的存在，
	// 否则玩家走到边界时 Route 落空，会被夹在原地。
	if err := n1.world.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, Rect{MinX: 512, MaxX: 1024, MaxZ: 512}); err != nil {
		t.Fatalf("n1 declare remote: %v", err)
	}
	if err := n2.world.DeclareRemote(mesh.Peer{Node: "n1", Scene: 1}, Rect{MinX: 0, MaxX: 512, MaxZ: 512}); err != nil {
		t.Fatalf("n2 declare remote: %v", err)
	}

	// 客户端挂在 n1 上（TCP 连的是 n1 的 Gate），所以 ref.Node = "n1"。
	cli := &testClient{ref: mesh.ClientRef{Node: "n1", Session: 7}}

	// 两个节点都要能把这个 (node= n1, session=7) 还原成出口：
	// n1 用本地会话，n2 用代理——这里都用同一个对象代替。
	resolver := func(ref mesh.ClientRef) Client {
		if ref.Node == "n1" && ref.Session == 7 {
			return cli
		}
		return nil
	}
	n1.world.resolve = resolver
	n2.world.resolve = resolver

	s1 := n1.world.NewScene(1, 20*time.Millisecond, nil, quiet(), WithBounds(Rect{MinX: 0, MaxX: 512, MaxZ: 512}))
	s2 := n2.world.NewScene(2, 20*time.Millisecond, nil, quiet(), WithBounds(Rect{MinX: 512, MaxX: 1024, MaxZ: 512}))

	if err := s1.Start(ctx); err != nil {
		t.Fatalf("start s1: %v", err)
	}
	defer s1.Stop(context.Background())
	if err := s2.Start(ctx); err != nil {
		t.Fatalf("start s2: %v", err)
	}
	defer s2.Stop(context.Background())

	id := s1.EnterAt("player-a", cli, 100, 100)
	if id == 0 {
		t.Fatal("enter failed")
	}
	waitFor(t, 2*time.Second, func() bool { return s1.entityCount.Load() == 1 })
	// 直接跳到边界外需要足够的速度额度，否则会被服务端的超速钳制拦住
	// （那才是正确行为：客户端不能靠一帧报一个远点来免费传送）。
	s1.SetSpeed(id, 2000)
	waitFor(t, time.Second, func() bool { return s1.entities[id].Speed() == 2000 })

	// 穿过边界。每帧只能走"速度 × 帧长"那么远，所以要连续几帧才到边界——
	// 真实客户端也是这样一包一包走的，一包直接报 520 会被超速钳制。
	for i := 0; i < 40 && s1.entityCount.Load() > 0; i++ {
		s1.Move(id, 520, 100, 100)
		time.Sleep(30 * time.Millisecond)
	}

	// 期望：n1 的实体数归零，n2 的实体数变成 1，ID 不变。
	waitFor(t, 3*time.Second, func() bool {
		return s1.entityCount.Load() == 0 && s2.entityCount.Load() == 1
	})

	if got := s2.entityCount.Load(); got != 1 {
		t.Fatalf("n2 的场景没有接管这个实体, entities=%d", got)
	}
	// 迁入的实体沿用源节点的 ID：n2 的分配器不会把它算进自己的 live
	// （那不是它发的号），所以这里查的是"实体在不在"，不是"ID 空间统计"。
	if e, ok := s2.entities[id]; !ok {
		t.Fatalf("实体 %d 没有落在 n2 的 scene 2 里", id)
	} else if e.x < 512 {
		t.Fatalf("落地坐标 %.1f 不在目标场景范围内", e.x)
	}
	// 路由变更回调（RouteTo）可能在实体落地后的下一帧才触发，
	// 和 entityCount 的变化不在同一拍，这里用 waitFor 等它收敛。
	waitFor(t, 2*time.Second, func() bool {
		if p, ok := cli.routeOf(); ok && p.Node == "n2" && p.Scene == 2 {
			return true
		}
		return false
	})
	if p, ok := cli.routeOf(); !ok || p.Node != "n2" || p.Scene != 2 {
		t.Fatalf("客户端没收到跨节点路由变更: %+v ok=%v", p, ok)
	}
	if !cli.got(protocol.MsgS2CEnterScene) {
		t.Fatal("客户端没收到新的 EnterSceneResp")
	}

	// 迁出/迁入必须配平，失败必须为 0：一次成功迁移只该各记一次。
	if out := s1.Stats().HandoversOut; out != 1 {
		t.Fatalf("s1 handoversOut=%d, want 1", out)
	}
	if in := s2.Stats().HandoversIn; in != 1 {
		t.Fatalf("s2 handoversIn=%d, want 1", in)
	}
	if f := s1.Stats().HandoverFails + s2.Stats().HandoverFails; f != 0 {
		t.Fatalf("handover fails=%d, want 0", f)
	}
	if st := n1.msh.Stat(); st.Sent == 0 {
		t.Fatal("节点间链路没有任何发送记录")
	}

	// ID 归属：这个 ID 是 n1 发的，实体在 n2 死掉时必须通知 n1 回收，
	// 否则跨区玩家的 ID 会永久泄漏（M3.1 的冷却回收在跨节点场景下失效）。
	if got := n1.world.IDOwner(id); got != "n1" {
		t.Fatalf("ID %d 的所属节点算出来是 %q, want n1", id, got)
	}
	s2.Leave(id)
	waitFor(t, 2*time.Second, func() bool {
		return n1.world.IDStat().Cooling == 1
	})
	if st := n1.world.IDStat(); st.Live != 0 {
		t.Fatalf("实体死后 n1 仍认为有 %d 个 ID 在用", st.Live)
	}
	if st := n2.world.IDStat(); st.Foreign != 1 {
		t.Fatalf("n2 应该记下 1 个外来 ID, got %d", st.Foreign)
	}
}

// TestCrossNodeHandoverWithoutMesh 验证"没配链路就跨节点"时的行为：
// 必须留在原地并计入失败，而不是把实体送进虚空。
func TestCrossNodeHandoverWithoutMesh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewWorld(quiet(), WithNodeID("n1"))
	// 声明一块属于 n2 的地，但没有 mesh。
	if err := w.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, Rect{MinX: 512, MaxX: 1024, MaxZ: 512}); err != nil {
		t.Fatalf("declare remote: %v", err)
	}
	cli := &testClient{ref: mesh.ClientRef{Node: "n1", Session: 1}}
	w.resolve = func(mesh.ClientRef) Client { return cli }

	s1 := w.NewScene(1, 20*time.Millisecond, nil, quiet(), WithBounds(Rect{MinX: 0, MaxX: 512, MaxZ: 512}))
	if err := s1.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s1.Stop(context.Background())

	id := s1.EnterAt("p", cli, 500, 100)
	waitFor(t, 2*time.Second, func() bool { return s1.entityCount.Load() == 1 })
	s1.SetSpeed(id, 2000)
	waitFor(t, time.Second, func() bool { return s1.entities[id].Speed() == 2000 })
	s1.Move(id, 600, 100, 100)
	time.Sleep(120 * time.Millisecond)

	if s1.entityCount.Load() != 1 {
		t.Fatal("实体不应该在没链路的情况下消失")
	}
	if f := s1.Stats().HandoverFails; f == 0 {
		t.Fatal("跨节点但没有链路，应该记一次失败")
	}
	if e, ok := s1.entities[id]; ok {
		if e.x > 512 {
			t.Fatalf("实体被放到了边界外 x=%.1f，属于没有任何场景的地", e.x)
		}
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("条件在 %s 内没有满足", d)
}
