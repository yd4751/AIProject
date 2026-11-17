package player

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/game/combat"
	"openworld/internal/game/creature"
	"openworld/internal/persist"
	"openworld/internal/world"
)

// newScene 建一个挂了 creature + combat + player 的场景。
func newScene(t *testing.T, store persist.Store) *world.Scene {
	t.Helper()
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := creature.Register(s); err != nil {
		t.Fatalf("creature register: %v", err)
	}
	if err := combat.Register(s); err != nil {
		t.Fatalf("combat register: %v", err)
	}
	EnablePlayerSave(s, store)
	return s
}

// tick 推进一帧。
func tick(s *world.Scene) { s.TickOnce() }

// TestPlayerSaveAndRestore 验证玩家离场落库、重连恢复。
func TestPlayerSaveAndRestore(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	// 玩家进场，改血量，然后离场（触发落库）。
	cli := &testClient{}
	id := s.EnterAt("p1", cli, 0, 0)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(id)
	a.HP = 300 // 从默认 1000 扣到 300
	a.Level = 7
	attr.Add(id, *a)

	// 离场：触发 OnEntityLeave 钩子落库。
	s.Leave(id)
	tick(s)

	// 档案应已写入。
	if _, ok := store.Get("p1"); !ok {
		t.Fatal("玩家离场后应落库")
	}

	// 重连：新实体进场，应从档案恢复 HP=300 Level=7。
	id2 := s.EnterAt("p1", cli, 0, 0)
	tick(s)

	a2 := attr.Get(id2)
	if a2.HP != 300 || a2.Level != 7 {
		t.Fatalf("重连应从档案恢复 HP=300 Level=7, got HP=%d Level=%d", a2.HP, a2.Level)
	}
}

// TestPlayerSavePreservesCombat 验证战斗数据（暴击/技能解锁）也随档案走。
func TestPlayerSavePreservesCombat(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	cli := &testClient{}
	id := s.EnterAt("p1", cli, 0, 0)
	tick(s)

	// 改战斗属性：学技能、加暴击。
	cp := ecs.Get(s.Store(), combat.CCombat)
	c := cp.Get(id)
	c.CritRate = 50
	c.CritMult = 200
	c.Learned = 1<<1 | 1<<4 | 1<<6
	cp.Add(id, *c)

	s.Leave(id)
	tick(s)

	id2 := s.EnterAt("p1", cli, 0, 0)
	tick(s)

	c2 := cp.Get(id2)
	if c2.CritRate != 50 || c2.CritMult != 200 || c2.Learned != 1<<1|1<<4|1<<6 {
		t.Fatalf("战斗数据未恢复: %+v", c2)
	}
}

// TestNewPlayerGetsDefaults 验证新玩家（无档案）不覆盖默认值。
func TestNewPlayerGetsDefaults(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	cli := &testClient{}
	id := s.EnterAt("brand-new", cli, 0, 0)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(id)
	if a.HP != 1000 || a.Level != 1 {
		t.Fatalf("新玩家应得默认属性 HP=1000 Level=1, got HP=%d Level=%d", a.HP, a.Level)
	}
}

// testClient 是最小 Client 实现。
type testClient struct{}

func (c *testClient) Send(uint16, []byte) error { return nil }
func (c *testClient) Kick(uint8, string) error  { return nil }

// TestPeriodicSaveWritesOnlinePlayers 验证周期回写：在线玩家的属性变化
// 不依赖离场，也会被周期系统落库。
//
// 直接构造 periodicSaver 并手动控制 ctx.Now，避免依赖真实时间精度
// （Windows 上 time.Now() 粒度约 1ms，纳秒级间隔在两次 tick 之间不可靠）。
func TestPeriodicSaveWritesOnlinePlayers(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	cli := &testClient{}
	id := s.EnterAt("p1", cli, 0, 0)
	tick(s) // 进场，挂默认属性

	// 改属性但不离场（模拟玩家在线时扣血/升级）。
	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(id)
	a.HP = 500
	a.Level = 3
	a.XP = 40
	attr.Add(id, *a)

	// 手动构造 periodicSaver，闸门设成过去（已到期），立即触发落库。
	saver := &periodicSaver{store: store, interval: time.Second, next: time.Now().Add(-time.Second)}
	saver.tick(&world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()})

	data, ok := store.Get("p1")
	if !ok {
		t.Fatal("周期回写应落库（玩家未离场）")
	}
	rec, err := persist.UnmarshalPlayerRecord(data)
	if err != nil {
		t.Fatalf("解析档案失败: %v", err)
	}
	if rec.HP != 500 || rec.Level != 3 || rec.XP != 40 {
		t.Fatalf("周期回写应落最新属性 HP=500 Level=3 XP=40, got HP=%d Level=%d XP=%d",
			rec.HP, rec.Level, rec.XP)
	}
}

// TestPeriodicSaveGate 验证周期闸门：未到期不落库。
func TestPeriodicSaveGate(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	cli := &testClient{}
	_ = s.EnterAt("p1", cli, 0, 0)
	tick(s)

	saver := &periodicSaver{store: store, interval: time.Hour, next: time.Now().Add(time.Hour)}
	saver.tick(&world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()})

	if _, ok := store.Get("p1"); ok {
		t.Fatal("闸门未到期时不应落库")
	}
}

// TestPeriodicSaveSkipsNonPlayers 验证周期回写只写玩家，不写怪。
func TestPeriodicSaveSkipsNonPlayers(t *testing.T) {
	store := persist.NewMemoryStore()
	s := newScene(t, store)

	// 刷一只怪（带属性，但不是玩家）。
	creature.Spawn(s, "mob", 5, 5, 100)

	saver := &periodicSaver{store: store, interval: time.Second, next: time.Now().Add(-time.Second)}
	saver.tick(&world.Ctx{Scene: s, Store: s.Store(), Now: time.Now()})

	// 怪的档案不应该存在（按玩家名 "mob" 查）。
	if _, ok := store.Get("mob"); ok {
		t.Fatal("周期回写不应为怪物落档案")
	}
}
