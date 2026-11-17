package creature

import (
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

// recClient 记录场景下发的消息。
type recClient struct {
	mu   sync.Mutex
	msgs []uint16
}

func (c *recClient) Send(id uint16, _ []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, id)
	return nil
}

func (c *recClient) Kick(uint8, string) error { return nil }

func (c *recClient) count(id uint16) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if m == id {
			n++
		}
	}
	return n
}

func newScene(t *testing.T) *world.Scene {
	t.Helper()
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	return s
}

func TestSpawnAttachesComponents(t *testing.T) {
	s := newScene(t)
	e := Spawn(s, "mob", 5, 5, 200)

	attr := ecs.Get(s.Store(), CAttr).Get(e.ID())
	if attr == nil {
		t.Fatal("生成的怪应带属性组件")
	}
	if attr.MaxHP != 200 || attr.HP != 120 {
		t.Fatalf("出生属性应为 120/200，实际 %d/%d", attr.HP, attr.MaxHP)
	}
	if !attr.Dirty {
		t.Fatal("出生属性应标记为脏，保证第一次同步能发出去")
	}
	b := ecs.Get(s.Store(), CBrain).Get(e.ID())
	if b == nil || b.HomeX != 5 || b.HomeZ != 5 {
		t.Fatalf("AI 组件应记录巢穴位置，实际 %+v", b)
	}
}

func TestSpawnHordeCount(t *testing.T) {
	s := newScene(t)
	n := SpawnHorde(s, 500, 0, 0, 100)
	if n != 500 {
		t.Fatalf("应生成 500 只，实际 %d", n)
	}
	if got := ecs.Get(s.Store(), CBrain).Len(); got != 500 {
		t.Fatalf("Brain 池应有 500 个，实际 %d", got)
	}
}

// TestRegenHealsUntilFull 验证回血的上限与 1Hz 闸门。
func TestRegenHealsUntilFull(t *testing.T) {
	s := newScene(t)
	mob := Spawn(s, "mob", 0, 0, 100) // 出生 60 血
	attr := ecs.Get(s.Store(), CAttr)

	st := &systems{}
	now := time.Now()
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: now, Delta: 50 * time.Millisecond}

	st.regen(ctx)
	if got := attr.Get(mob.ID()).HP; got != 62 {
		t.Fatalf("1 秒后应回 2 点到 62，实际 %d", got)
	}
	if !attr.Get(mob.ID()).Dirty {
		t.Fatal("回血应把属性标记为脏")
	}

	// 同一帧再跑一次：闸门没到，不应继续回血。
	st.regen(ctx)
	if got := attr.Get(mob.ID()).HP; got != 62 {
		t.Fatalf("同帧重复执行不应回血，实际 %d", got)
	}

	// 跑够次数必须停在满血，不能溢出。
	for i := 0; i < 100; i++ {
		ctx.Now = ctx.Now.Add(regenInterval)
		st.regen(ctx)
	}
	if got := attr.Get(mob.ID()).HP; got != 100 {
		t.Fatalf("最终应为满血 100，实际 %d", got)
	}
}

// TestWanderKeepsMobNearHome 验证游荡不会把怪越带越远。
//
// 这是刷怪点最容易被写坏的地方：目标点选在"当前位置"周围而不是
// "巢穴"周围，几百次游荡之后整片怪就漂到地图另一头去了。
func TestWanderKeepsMobNearHome(t *testing.T) {
	s := newScene(t)
	const homeX, homeZ float32 = 30, -20
	mob := Spawn(s, "mob", homeX, homeZ, 100)

	st := &systems{}
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: time.Now(), Delta: 50 * time.Millisecond}

	walked := 0
	lastX, _, lastZ := mob.Pos()
	for i := 0; i < 4000; i++ {
		ctx.Now = ctx.Now.Add(ctx.Delta)
		st.wander(ctx)
		x, _, z := mob.Pos()
		if x != lastX || z != lastZ {
			walked++
		}
		lastX, lastZ = x, z

		dx, dz := float64(x-homeX), float64(z-homeZ)
		if d := math.Hypot(dx, dz); d > wanderRadius+1 {
			t.Fatalf("第 %d 帧：怪离巢 %.1f 米，超出游荡半径 %.0f", i, d, wanderRadius)
		}
	}
	if walked == 0 {
		t.Fatal("4000 帧里怪一次都没动，AI 没有生效")
	}
}

// TestPlayerGetsAttributesAndSync 是端到端链路验证：
// 玩家进场 -> 钩子挂属性 -> 同步 System 发属性包 -> 脏标记被清掉。
func TestPlayerGetsAttributesAndSync(t *testing.T) {
	s := newScene(t)
	c := &recClient{}

	s.Enter("p1", c)
	s.TickOnce() // 处理进场 + 跑一帧系统

	if n := c.count(protocol.MsgS2CEntityAttr); n != 1 {
		t.Fatalf("进场后应收到 1 条属性包，实际 %d", n)
	}
	// 玩家实体 ID 从 1 开始，这里直接从池里确认组件存在。
	pool := ecs.Get(s.Store(), CAttr)
	if pool.Len() != 1 {
		t.Fatalf("应有 1 个属性组件，实际 %d", pool.Len())
	}
	var dirty bool
	pool.Each(func(_ uint64, a *Attributes) { dirty = a.Dirty })
	if dirty {
		t.Fatal("同步之后脏标记应被清掉，否则每帧都会重发")
	}

	// 再跑一帧：没有属性变化，1 秒闸门也没过，不该再发包。
	s.TickOnce()
	if n := c.count(protocol.MsgS2CEntityAttr); n != 1 {
		t.Fatalf("属性未变化时不应重复下发，实际累计 %d 条", n)
	}
}

// TestProjectileAttrSyncRespectsOneSecondGate 确认闸门确实按时间放行。
func TestAttrSyncRespectsOneSecondGate(t *testing.T) {
	s := newScene(t)
	mob := Spawn(s, "mob", 0, 0, 100)

	st := &systems{}
	ctx := &world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()}
	st.syncAttr(ctx)

	attr := ecs.Get(s.Store(), CAttr).Get(mob.ID())
	if attr.Dirty {
		t.Fatal("同步后脏标记应清除")
	}

	// 手动弄脏，但时间没到：不该发。
	attr.Dirty = true
	st.syncAttr(ctx)
	if !attr.Dirty {
		t.Fatal("1 秒闸门内的同步不应执行")
	}

	// 时间推过 1 秒：应该发出去并清脏。
	ctx.Now = ctx.Now.Add(syncInterval)
	st.syncAttr(ctx)
	if attr.Dirty {
		t.Fatal("闸门到期后应完成同步")
	}
}
