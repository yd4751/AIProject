package world

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/protocol"
)

// switchClient 是能感知场景切换的测试客户端。
type switchClient struct {
	mockClient
	scene atomic.Pointer[Scene]
}

func (c *switchClient) SetScene(s *Scene) { c.scene.Store(s) }

// hpComp / hpCodec 用来验证"玩法数据跟着实体一起搬走"。
// 玩法包不能在这里引用（creature 依赖 world，会造成循环），
// 所以测试自带一个最小的 codec。
type hpComp struct{ HP int32 }

var CHP = ecs.Define[hpComp]("test.transfer.hp")

type hpCodec struct{}

func (hpCodec) Encode(ctx *Ctx, e *Entity) []byte {
	c := ecs.Get(ctx.Store, CHP).Get(e.ID())
	if c == nil {
		return nil
	}
	return []byte{byte(c.HP)}
}

func (hpCodec) Decode(ctx *Ctx, e *Entity, d []byte) {
	if len(d) < 1 {
		return
	}
	ecs.Get(ctx.Store, CHP).Add(e.ID(), hpComp{HP: int32(d[0])})
}

// enterPlayer 在场景里放一个玩家，返回它的实体 ID。
func enterPlayer(t *testing.T, s *Scene, name string, x, z float32) (uint64, *switchClient) {
	t.Helper()
	c := &switchClient{}
	id := s.ids.alloc(time.Now())
	if id == 0 {
		t.Fatal("分配实体 ID 失败")
	}
	s.onEnter(&msgEnter{id: id, player: name, client: c, hasPos: true, x: x, z: z})
	if s.Entity(id) == nil {
		t.Fatal("玩家没有进入场景")
	}
	return id, c
}

// tickAll 让所有场景各跑一帧。迁移是异步的，需要两个场景交替推进。
func tickAll(n int, scenes ...*Scene) {
	for i := 0; i < n; i++ {
		for _, s := range scenes {
			s.doTick()
		}
	}
}

func TestHandoverAcrossBoundary(t *testing.T) {
	_, a, b := newTestWorld(t)
	a.SetTransferCodec(hpCodec{})
	b.SetTransferCodec(hpCodec{})

	id, c := enterPlayer(t, a, "p", 500, 100)
	ecs.Get(a.Store(), CHP).Add(id, hpComp{HP: 42})
	// 把上次移动时间往前推，让 100 米的位移能通过速度校验。
	// （速度钳制是 8m/s × 1.5 + 0.5，不推时间的话 100 米会被夹成半米。）
	a.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)
	c.reset()

	a.Move(id, 600, 0, 100)
	tickAll(4, a, b)

	if a.Entity(id) != nil {
		t.Fatal("迁移完成后实体不应还留在源场景")
	}
	if got := a.entityCount.Load(); got != 0 {
		t.Fatalf("源场景人数应为 0，实际 %d", got)
	}
	e := b.Entity(id)
	if e == nil {
		t.Fatal("实体没有落到目的场景")
	}
	if e.ID() != id {
		t.Fatalf("迁移后 ID 必须保持不变（客户端靠它认自己），实际 %d → %d", id, e.ID())
	}
	if e.x != 600 || e.z != 100 {
		t.Fatalf("落点应为请求的 (600,100)，实际 (%.1f,%.1f)", e.x, e.z)
	}
	if got := ecs.Get(b.Store(), CHP).Get(id); got == nil || got.HP != 42 {
		t.Fatalf("属性没跟着搬过来: %+v", got)
	}
	if c.scene.Load() != b {
		t.Fatal("会话没有切到目的场景，之后的 Move 会打到旧场景上")
	}
	if c.count(protocol.MsgS2CEnterScene) != 1 {
		t.Fatalf("客户端应收到 1 条 EnterSceneResp（场景已切换），实际 %d", c.count(protocol.MsgS2CEnterScene))
	}
	if st := a.Stats(); st.HandoversOut != 1 || st.HandoverFails != 0 {
		t.Fatalf("源场景迁移统计不对: out=%d fail=%d", st.HandoversOut, st.HandoverFails)
	}
	if st := b.Stats(); st.HandoversIn != 1 {
		t.Fatalf("目的场景迁移统计不对: in=%d", st.HandoversIn)
	}
}

func TestHandoverRejectedWhenDestinationFull(t *testing.T) {
	_, a, b := newTestWorld(t)
	b.maxEntities = 1
	enterPlayer(t, b, "occupant", 600, 100) // 占满 B

	id, c := enterPlayer(t, a, "p", 500, 100)
	a.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)
	c.reset()

	a.Move(id, 600, 0, 100)
	tickAll(4, a, b)

	e := a.Entity(id)
	if e == nil {
		t.Fatal("被拒绝后实体必须留在源场景，不能凭空消失")
	}
	if e.migrating {
		t.Fatal("被拒绝后应该解除迁移标记，否则这个玩家永远动不了")
	}
	if e.x >= 512 {
		t.Fatalf("被拒绝后应夹回边界内，实际 x=%.1f", e.x)
	}
	if got := b.entityCount.Load(); got != 1 {
		t.Fatalf("目的场景人数不该变化，实际 %d", got)
	}
	if st := a.Stats(); st.HandoverFails != 1 {
		t.Fatalf("应记录 1 次迁移失败，实际 %d", st.HandoverFails)
	}
}

func TestHandoverTimeoutKeepsEntityLocal(t *testing.T) {
	_, a, _ := newTestWorld(t)

	id, _ := enterPlayer(t, a, "p", 500, 100)
	a.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)

	a.Move(id, 600, 0, 100)
	// 只推进源场景：目的场景永远不处理 Prepare，等超时。
	a.doTick()
	if _, pending := a.transfers[id]; !pending {
		t.Fatal("迁移应该已经发起")
	}
	if !a.Entity(id).migrating {
		t.Fatal("迁移期间实体应被冻结")
	}

	// 把截止时间拨到过去，模拟 3 秒超时。
	a.transfers[id].deadline = time.Now().Add(-time.Second)
	a.doTick()

	e := a.Entity(id)
	if e == nil {
		t.Fatal("超时后实体必须留在源场景")
	}
	if e.migrating {
		t.Fatal("超时后必须解冻，否则玩家会永久卡住")
	}
	if e.x >= 512 {
		t.Fatalf("超时后应夹回边界内，实际 x=%.1f", e.x)
	}
	if st := a.Stats(); st.HandoverFails != 1 {
		t.Fatalf("应记录 1 次超时，实际 %d", st.HandoverFails)
	}
	if a.Stats().Migrating != 0 {
		t.Fatal("在途迁移数应归零")
	}
}

func TestHandoverTwiceReturnsHome(t *testing.T) {
	_, a, b := newTestWorld(t)
	a.SetTransferCodec(hpCodec{})
	b.SetTransferCodec(hpCodec{})

	id, _ := enterPlayer(t, a, "p", 500, 100)
	ecs.Get(a.Store(), CHP).Add(id, hpComp{HP: 7})

	// 往东跨界
	a.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)
	a.Move(id, 600, 0, 100)
	tickAll(4, a, b)
	if b.Entity(id) == nil {
		t.Fatal("第一次迁移失败")
	}

	// 再往西跨回来
	b.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)
	b.Move(id, 400, 0, 100)
	tickAll(4, a, b)

	if b.Entity(id) != nil {
		t.Fatal("实体应已回到场景 A")
	}
	if a.Entity(id) == nil {
		t.Fatal("实体没有回到场景 A")
	}
	if got := ecs.Get(a.Store(), CHP).Get(id); got == nil || got.HP != 7 {
		t.Fatalf("往返后属性应保持不变(7)，实际 %+v", got)
	}
	// ID 空间是全局共享的，往返两次不该产生新的 ID。
	if hw := a.ids.stat().HighWater; hw != id {
		t.Fatalf("往返不应推高 ID 高水位：期望 %d，实际 %d", id, hw)
	}
}

func TestHandoverDisabledWithoutWorld(t *testing.T) {
	// 独立场景（没有 World）不该尝试迁移，只在边界处夹住。
	s := NewScene(1, 50*time.Millisecond, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), WithBounds(Rect{0, 0, 512, 512}))
	id, _ := enterPlayer(t, s, "p", 500, 100)
	s.Entity(id).lastMoveAt = time.Now().Add(-30 * time.Second)
	s.Move(id, 600, 0, 100)
	s.doTick()

	e := s.Entity(id)
	if e == nil {
		t.Fatal("没有 World 时实体不该消失")
	}
	if e.x >= 512 {
		t.Fatalf("应被夹在边界内，实际 x=%.1f", e.x)
	}
}

func TestSpawnStaysInsideBounds(t *testing.T) {
	_, a, _ := newTestWorld(t)
	// 撒 200 个怪，全部必须落在场景矩形内。
	for i := 0; i < 200; i++ {
		e := a.Spawn(KindMob, "mob", 0, 0)
		if e == nil {
			t.Fatal("spawn 失败")
		}
		x, z := a.spawnPos(e.ID())
		if !a.bounds.Contains(x, z) {
			t.Fatalf("ID %d 的出生点 (%.1f,%.1f) 落在边界 %s 之外", e.ID(), x, z, a.bounds)
		}
	}
}
