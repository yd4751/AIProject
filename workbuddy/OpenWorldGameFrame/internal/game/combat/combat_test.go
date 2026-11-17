package combat

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/game/creature"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

// recClient 记录场景下发的消息。
type recClient struct {
	mu   sync.Mutex
	msgs []uint16
	hits []protocol.CombatHit
}

func (c *recClient) Send(id uint16, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, id)
	if id == protocol.MsgS2CCombatHit {
		var m protocol.CombatHit
		if m.Decode(body) == nil {
			c.hits = append(c.hits, m)
		}
	}
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

// newScene 建一个挂了 creature + combat 的场景，返回场景和攻击处理器驱动用的闭包。
func newScene(t *testing.T) *world.Scene {
	t.Helper()
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := creature.Register(s); err != nil {
		t.Fatalf("creature register: %v", err)
	}
	if err := Register(s); err != nil {
		t.Fatalf("combat register: %v", err)
	}
	return s
}

// tick 手动推进一帧，并在此之前触发一次攻击指令处理（走 Scene.Attack 投 mailbox）。
func tick(s *world.Scene) {
	s.TickOnce()
}

// TestAttackDamagesTarget 验证普攻扣血 + 广播结算结果。
func TestAttackDamagesTarget(t *testing.T) {
	s := newScene(t)
	// 玩家进场（钩子挂 Combat），怪进场（钩子挂 Combat + creature 挂属性）。
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 1000) // 血厚，避免一击秒杀
	tick(s)                                     // 落地进场

	attr := ecs.Get(s.Store(), creature.CAttr)
	before := attr.Get(mob.ID()).HP

	// 玩家攻击怪（普攻 skill 1：伤害 100 * 玩家攻击120/100 - 怪防御10 = 110）。
	s.Attack(pid, mob.ID(), 1)
	tick(s)

	after := attr.Get(mob.ID()).HP
	if after >= before {
		t.Fatalf("攻击后怪应掉血，before=%d after=%d", before, after)
	}
	if pClient.count(protocol.MsgS2CCombatHit) == 0 {
		t.Fatal("攻击应广播结算结果，玩家（视野内）应收到 CombatHit")
	}
}

// TestAttackCooldownBlocksRapidCast 验证有 CD 的技能不能连发。
func TestAttackCooldownBlocksRapidCast(t *testing.T) {
	s := newScene(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 1000)
	tick(s)

	// 重击 skill 2 有 3 秒 CD。
	s.Attack(pid, mob.ID(), 2)
	tick(s)
	first := pClient.count(protocol.MsgS2CCombatHit)

	// 同一帧（其实 CD 未到）再打，应被 CD 拒绝。
	s.Attack(pid, mob.ID(), 2)
	tick(s)
	if got := pClient.count(protocol.MsgS2CCombatHit); got != first {
		t.Fatalf("CD 内不应再次结算，第一次 %d 次，第二次后 %d 次", first, got)
	}
}

// TestAttackOutOfRangeRejected 验证射程校验。
func TestAttackOutOfRangeRejected(t *testing.T) {
	s := newScene(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 100, 0, 100) // 相距 100 米，远超普攻 3 米
	tick(s)

	s.Attack(pid, mob.ID(), 1)
	tick(s)
	if pClient.count(protocol.MsgS2CCombatHit) != 0 {
		t.Fatal("射程外攻击应被拒绝，不应广播结算")
	}
}

// TestKillRemovesEntity 验证击杀移除实体并广播 killed。
func TestKillRemovesEntity(t *testing.T) {
	s := newScene(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 20) // 20 血，一击必杀
	tick(s)

	s.Attack(pid, mob.ID(), 2) // 重击：250*120/100 - 10 = 290，秒杀
	tick(s)

	// 结算广播里 killed 应为 true。
	pClient.mu.Lock()
	killed := false
	for _, h := range pClient.hits {
		if h.TargetID == mob.ID() && h.Killed {
			killed = true
		}
	}
	pClient.mu.Unlock()
	if !killed {
		t.Fatal("击杀应广播 killed=true")
	}

	// 实体应被移除（回收 ID）。
	if s.Entity(mob.ID()) != nil {
		t.Fatal("被击杀的怪应从场景移除")
	}
}

// TestBuffDamageUpScalesDamage 验证增伤 Buff 提高伤害。
func TestBuffDamageUpScalesDamage(t *testing.T) {
	s := newScene(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 1000) // 血厚，不会一击死
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	before := attr.Get(mob.ID()).HP

	// 直接给攻击者挂 100% 增伤 Buff。
	addBuff(s, pid, Buff{Kind: BuffDamageUp, Value: 100, Until: time.Now().Add(10 * time.Second)})

	s.Attack(pid, mob.ID(), 1)
	tick(s)

	// 普攻：100 * 120/100 - 10 = 110 基础，×2（100% 增伤）= 220。
	want := int32(220)
	after := attr.Get(mob.ID()).HP
	if before-after != want {
		t.Fatalf("100%% 增伤后普攻应掉 %d 血，实际掉 %d", want, before-after)
	}
}

// TestBuffPoisonDrainsOverTime 验证中毒 Buff 持续掉血。
//
// 直接驱动 buffTick 方法并传入可控时间，避免真实时间闸门的干扰。
func TestBuffPoisonDrainsOverTime(t *testing.T) {
	s := newScene(t)
	mob := creature.Spawn(s, "mob", 0, 0, 1000)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	before := attr.Get(mob.ID()).HP

	now := time.Now()
	addBuff(s, mob.ID(), Buff{Kind: BuffPoison, Value: 30, Until: now.Add(time.Second)})

	r := &resolver{scene: s}
	for i := 0; i < 4; i++ { // 4 个 250ms tick，每个掉 30
		now = now.Add(buffTickInterval)
		r.buffTick(&world.Ctx{Scene: s, Store: s.Store(), Now: now, Delta: buffTickInterval})
	}
	after := attr.Get(mob.ID()).HP
	if before-after != 30*4 {
		t.Fatalf("中毒应掉 %d 血，实际掉 %d", 30*4, before-after)
	}
}

// TestBuffRegenHeals 验证回血 Buff。
func TestBuffRegenHeals(t *testing.T) {
	s := newScene(t)
	mob := creature.Spawn(s, "mob", 0, 0, 1000)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(mob.ID())
	a.HP = 10
	attr.Add(mob.ID(), *a)

	now := time.Now()
	addBuff(s, mob.ID(), Buff{Kind: BuffRegen, Value: 50, Until: now.Add(time.Second)})

	r := &resolver{scene: s}
	for i := 0; i < 4; i++ {
		now = now.Add(buffTickInterval)
		r.buffTick(&world.Ctx{Scene: s, Store: s.Store(), Now: now, Delta: buffTickInterval})
	}
	if got := attr.Get(mob.ID()).HP; got <= 10 {
		t.Fatalf("回血 Buff 应让血量上升，实际 %d", got)
	}
}

// TestAOEHitsMultipleTargets 验证 AOE 技能对范围内多个敌人结算。
func TestAOEHitsMultipleTargets(t *testing.T) {
	s := newScene(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	m1 := creature.Spawn(s, "mob", 1, 0, 1000)
	m2 := creature.Spawn(s, "mob", -1, 0, 1000)
	m3 := creature.Spawn(s, "mob", 0, 1, 1000)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	b1, b2, b3 := attr.Get(m1.ID()).HP, attr.Get(m2.ID()).HP, attr.Get(m3.ID()).HP

	// 旋风斩 skill 3：AOE，范围 5 米，目标 m1，三个怪都在 5 米内。
	s.Attack(pid, m1.ID(), 3)
	tick(s)

	if attr.Get(m1.ID()).HP >= b1 || attr.Get(m2.ID()).HP >= b2 || attr.Get(m3.ID()).HP >= b3 {
		t.Fatalf("AOE 应对范围内所有敌人结算，HP %d/%d/%d -> %d/%d/%d",
			b1, b2, b3, attr.Get(m1.ID()).HP, attr.Get(m2.ID()).HP, attr.Get(m3.ID()).HP)
	}
}
