package combat

import (
	"testing"

	"openworld/internal/ecs"
	"openworld/internal/game/creature"
)

// TestKillAwardsXP 验证击杀怪物给经验。
func TestKillAwardsXP(t *testing.T) {
	s := newSceneExtended(t)
	pid := s.EnterAt("p1", &recClient{}, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 100) // 血少，一次普攻击杀
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	if got := attr.Get(pid).XP; got != 0 {
		t.Fatalf("初始 XP 应为 0，实际 %d", got)
	}

	s.Attack(pid, mob.ID(), 1)
	tick(s)

	if got := attr.Get(pid).XP; got != xpForKill {
		t.Fatalf("击杀后 XP 应为 %d，实际 %d", xpForKill, got)
	}
}

// TestXpAccumulatesAndLevelsUp 验证经验累积升级，属性成长。
//
// 升级阈值 xpToLevel(1)=20，击杀 3 只怪（3×10=30）应升到 2 级，
// 剩余 30-20=10 XP。升级后 MaxHP +100 且回满，攻击 +10。
func TestXpAccumulatesAndLevelsUp(t *testing.T) {
	s := newSceneExtended(t)
	pid := s.EnterAt("p1", &recClient{}, 0, 0)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	combat := ecs.Get(s.Store(), CCombat)

	beforeHP := attr.Get(pid).MaxHP
	beforeAtk := combat.Get(pid).Attack

	// 击杀 3 只怪，每只 10 XP，共 30 XP。
	for i := 0; i < 3; i++ {
		mob := creature.Spawn(s, "mob", float32(1+i), 0, 100)
		s.Attack(pid, mob.ID(), 1)
		tick(s)
	}

	a := attr.Get(pid)
	if a.Level != 2 {
		t.Fatalf("击杀 3 只怪后应升到 2 级，实际 %d", a.Level)
	}
	if a.XP != 10 {
		t.Fatalf("升级后剩余 XP 应为 10，实际 %d", a.XP)
	}
	if a.MaxHP != beforeHP+100 {
		t.Fatalf("升级后 MaxHP 应 +100（%d），实际 %d", beforeHP+100, a.MaxHP)
	}
	if got := combat.Get(pid).Attack; got != beforeAtk+10 {
		t.Fatalf("升级后攻击应 +10（%d），实际 %d", beforeAtk+10, got)
	}
}

// TestSkillLevelGate 验证技能等级门槛：等级不够放不出处决（skill 5，Level=2）。
func TestSkillLevelGate(t *testing.T) {
	s := newSceneExtended(t)
	pClient := &recClient{}
	pid := s.EnterAt("p1", pClient, 0, 0)
	mob := creature.Spawn(s, "mob", 1, 0, 100000) // 厚血，避免死亡
	tick(s)

	// 处决 skill 5 要求 Level>=2。玩家等级 1，即使学了技能也放不出。
	learnSkill(s, pid, 5)
	learnSkill(s, pid, 2) // 前置重击也学会
	s.Attack(pid, mob.ID(), 5)
	tick(s)
	if hitCount(pClient) != 0 {
		t.Fatal("等级 1 施放需 Level>=2 的处决应被拒绝")
	}

	// 手动升到 2 级（直接改属性，绕过击杀流程）。
	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(pid)
	a.Level = 2
	attr.Add(pid, *a)

	s.Attack(pid, mob.ID(), 5)
	tick(s)
	if hitCount(pClient) == 0 {
		t.Fatal("等级 2 应能施放处决")
	}
}

// TestLevelCap 验证等级封顶：到 maxLevel 后不再升级。
func TestLevelCap(t *testing.T) {
	s := newSceneExtended(t)
	pid := s.EnterAt("p1", &recClient{}, 0, 0)
	tick(s)

	attr := ecs.Get(s.Store(), creature.CAttr)
	a := attr.Get(pid)
	a.Level = maxLevel
	a.XP = 10000
	attr.Add(pid, *a)

	awardKillXP(s, pid) // 击杀给经验，但已满级，不应再升级

	if got := attr.Get(pid).Level; got != maxLevel {
		t.Fatalf("满级后不应再升级，实际 %d", got)
	}
}
