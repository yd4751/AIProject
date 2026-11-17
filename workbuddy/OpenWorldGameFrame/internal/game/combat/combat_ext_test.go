package combat

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/game/creature"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

// newSceneExtended 复用 combat_test.go 的 newScene 逻辑，但这里独立一份
// 避免测试文件之间的耦合（这个文件是 M4 扩展的专项测试）。
func newSceneExtended(t *testing.T) *world.Scene {
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

// learnSkill 直接给玩家的 Combat 组件打上学过某个技能的位。
func learnSkill(s *world.Scene, id uint64, skill uint8) {
	pool := ecs.Get(s.Store(), CCombat)
	c := pool.Get(id)
	if c == nil {
		return
	}
	c.Learned |= 1 << skill
	pool.Add(id, *c)
}

// hitCount 统计 recClient 收到的 CombatHit 数量。
func hitCount(c *recClient) int { return c.count(protocol.MsgS2CCombatHit) }

// TestSkillTreeRequiresPrerequisite 验证技能树：未学前置不能施放连击。
func TestSkillTreeRequiresPrerequisite(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 1000)
	tick(s)

	// 连击 skill 4 需要前置 skill 1（普攻），但玩家默认只解锁普攻本身，
	// 没解锁 skill 4。未学 skill 4 时应拒绝。
	s.Attack(pid, mob.ID(), 4)
	tick(s)
	if hitCount(pClient) != 0 {
		t.Fatal("未学连击（skill 4）前施放应被拒绝")
	}

	// 学会 skill 4 后应能施放。
	learnSkill(s, pid, 4)
	s.Attack(pid, mob.ID(), 4)
	tick(s)
	if hitCount(pClient) == 0 {
		t.Fatal("学会连击后应能施放并结算")
	}
}

// TestComboBoostsDamage 验证连招：普攻→连击触发加成。
func TestComboBoostsDamage(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 100000) // 血超厚，避免死亡干扰
	tick(s)
	learnSkill(s, pid, 4)

	attr := ecs.Get(s.Store(), creature.CAttr)

	// 第一发：先普攻（建立连招起点 LastSkill=1）。
	s.Attack(pid, mob.ID(), 1)
	tick(s)
	hpAfterNormal := attr.Get(mob.ID()).HP

	// 第二发：连击 skill 4，ComboFrom=1，应触发 ×1.5 加成。
	s.Attack(pid, mob.ID(), 4)
	tick(s)
	hpAfterCombo := attr.Get(mob.ID()).HP

	// 连击伤害 = (150 * 120/100 - 10) × 1.5 = 170 × 1.5 = 255。
	comboDrop := hpAfterNormal - hpAfterCombo
	if comboDrop < 240 || comboDrop > 270 {
		t.Fatalf("连招加成后连击应掉约 255 血，实际 %d", comboDrop)
	}
}

// TestDispelRemovesBuffs 验证驱散：净化之刃清空目标 Buff。
func TestDispelRemovesBuffs(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 1000)
	tick(s)

	// 给怪挂两个 Buff（增伤 + 中毒）。
	now := time.Now()
	addBuff(s, mob.ID(), Buff{Kind: BuffDamageUp, Value: 50, Until: now.Add(10 * time.Second)})
	addBuff(s, mob.ID(), Buff{Kind: BuffPoison, Value: 20, Until: now.Add(10 * time.Second)})

	pool := ecs.Get(s.Store(), CBuff)
	if got := len(*pool.Get(mob.ID())); got != 2 {
		t.Fatalf("初始应有 2 个 Buff，实际 %d", got)
	}

	// 净化之刃 skill 6 带驱散。
	s.Attack(pid, mob.ID(), 6)
	tick(s)

	if b := pool.Get(mob.ID()); b != nil && len(*b) != 0 {
		t.Fatalf("驱散后 Buff 应清空，实际 %d 个", len(*b))
	}
}

// TestCritAppliesMultiplier 验证暴击：暴击伤害高于普通伤害。
//
// 暴击率是概率性的，这里用确定性的方法验证：直接构造一个 CritRate=100
// 的攻击者，确保必暴击，再比较暴击 vs 非暴击的伤害。
func TestCritAppliesMultiplier(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 100000)
	tick(s)

	// 把玩家暴击率设为 100、倍率 200，确保必暴击且 ×2。
	pool := ecs.Get(s.Store(), CCombat)
	c := pool.Get(pid)
	c.CritRate = 100
	c.CritMult = 200
	pool.Add(pid, *c)

	attr := ecs.Get(s.Store(), creature.CAttr)
	before := attr.Get(mob.ID()).HP

	s.Attack(pid, mob.ID(), 1) // 普攻 100*120/100 - 10 = 110，暴击 ×2 = 220
	tick(s)

	drop := before - attr.Get(mob.ID()).HP
	if drop < 200 || drop > 240 {
		t.Fatalf("100%% 暴击 ×2 后普攻应掉约 220 血，实际 %d", drop)
	}
}

// TestShieldAbsorbsDamage 验证护盾吸收伤害。
func TestShieldAbsorbsDamage(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 100000)
	tick(s)

	// 给怪挂 50 点护盾。
	now := time.Now()
	addBuff(s, mob.ID(), Buff{Kind: BuffShield, Value: 50, Until: now.Add(10 * time.Second)})

	attr := ecs.Get(s.Store(), creature.CAttr)
	before := attr.Get(mob.ID()).HP

	// 普攻 110 伤害，护盾吸收 50，实际掉血 60。
	s.Attack(pid, mob.ID(), 1)
	tick(s)

	drop := before - attr.Get(mob.ID()).HP
	if drop != 60 {
		t.Fatalf("护盾吸收 50 后普攻 110 应掉 60 血，实际 %d", drop)
	}
}

// TestCombatTransferRoundTrip 验证新 Combat 字段（暴击/连招/学习位图）能往返编解码。
func TestCombatTransferRoundTrip(t *testing.T) {
	s := newSceneExtended(t)
	pid := s.EnterAt("p1", &recClient{}, 0, 0)
	tick(s)

	pool := ecs.Get(s.Store(), CCombat)
	c := pool.Get(pid)
	c.CritRate = 30
	c.CritMult = 180
	c.Learned = 1<<1 | 1<<4 | 1<<6
	c.LastSkill = 4
	c.LastCast = time.Now().Add(-time.Second)
	c.CDs[4] = time.Now().Add(2 * time.Second)
	pool.Add(pid, *c)
	addBuff(s, pid, Buff{Kind: BuffShield, Value: 100, Until: time.Now().Add(5 * time.Second)})

	enc := codec{}
	blob := enc.Encode(&world.Ctx{Store: s.Store()}, s.Entity(pid))
	if blob == nil {
		t.Fatal("Encode 应返回非空 blob")
	}

	// 覆盖成默认，再 Decode 回来。
	pool.Add(pid, Combat{Attack: 1, Defense: 1})
	ecs.Get(s.Store(), CBuff).Remove(pid)
	enc.Decode(&world.Ctx{Store: s.Store()}, s.Entity(pid), blob)

	got := pool.Get(pid)
	if got.CritRate != 30 || got.CritMult != 180 {
		t.Fatalf("暴击属性未还原: rate=%d mult=%d", got.CritRate, got.CritMult)
	}
	if got.Learned != 1<<1|1<<4|1<<6 {
		t.Fatalf("Learned 位图未还原: %b", got.Learned)
	}
	if got.LastSkill != 4 || got.LastCast.IsZero() {
		t.Fatalf("连招状态未还原: lastSkill=%d lastCast=%v", got.LastSkill, got.LastCast)
	}
	if got.CDs[4].IsZero() {
		t.Fatal("CD 未还原")
	}
	if buffs := ecs.Get(s.Store(), CBuff).Get(pid); buffs == nil || len(*buffs) != 1 {
		t.Fatalf("护盾 Buff 未还原")
	}
}
