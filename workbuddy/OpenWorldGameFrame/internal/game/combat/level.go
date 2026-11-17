package combat

import (
	"openworld/internal/ecs"
	"openworld/internal/game/creature"
	"openworld/internal/world"
)

// 升级系统：击杀怪物给经验，经验累积升级，升级提升属性并解锁高级技能。
//
// 这补上了 M4 战斗里"打怪升级"闭环缺的最后一块：之前 Skill.Level 门槛
// 只是文档字段（玩家等级恒为 1，高级技能永远放不出来），现在击杀→经验→
// 升级→属性成长→解锁技能，整条链路闭合。
//
// 分层：经验值放在 creature.Attributes（等级在那，XP 是等级的伴生字段），
// 升级逻辑放在 combat（击杀发生在 combat 的 dealDamage，且 combat 已经
// import creature，可以读属性）。两个包不互相依赖，无环。

// xpForKill 是击杀一只怪给的经验。当前固定值，后续可改成按怪物等级/类型
// 区分（精英怪给更多）。固定值让测试确定。
const xpForKill int32 = 10

// xpToLevel 返回从 level 升到 level+1 所需的经验值。
//
// 用简单线性增长（每级 +10），让早期升级快、后期变慢，符合直觉。
// level 从 1 开始：1→2 要 20，2→3 要 30，以此类推。
func xpToLevel(level uint8) int32 {
	return int32(level) * 20
}

// maxLevel 是等级上限。到顶后不再累积经验。
const maxLevel uint8 = 20

// awardKillXP 击杀后给攻击方经验，处理升级。
//
// 只在攻击方是玩家且目标被击杀时调用（由 dealDamage 触发）。经验加到
// creature.Attributes.XP 上，累积到阈值就升级：清空 XP、等级 +1、提升
// HP/攻击，并重新计算属性。升级是无副作用的纯属性变化，不涉及技能解锁
// （技能解锁仍走 Learned 位图，由外部系统显式授予），这样等级和技能树
// 解耦——等级决定"能不能用"（skillAvailable 的等级门槛），Learned 决定
// "会不会"（有没有解锁）。
func awardKillXP(s *world.Scene, attackerID uint64) {
	a := ecs.Get(s.Store(), creature.CAttr).Get(attackerID)
	if a == nil {
		return
	}
	a.XP += xpForKill
	a.Dirty = true

	// 可能连续升级（一次给的经验跨多级），循环处理。
	for a.Level < maxLevel {
		need := xpToLevel(a.Level)
		if a.XP < need {
			break
		}
		a.XP -= need
		a.Level++
		applyLevelUp(s, attackerID, a)
	}
}

// applyLevelUp 应用一次升级的属性成长。
//
// 每级 +100 MaxHP（并回满血）、+10 攻击。数值从简，重点是"升级→变强"
// 的闭环成立，具体成长曲线后续再调。升级也会顺带回蓝（MP 补满），
// 让升级成为"满血复活"式的正向反馈。
func applyLevelUp(s *world.Scene, id uint64, a *creature.Attributes) {
	a.MaxHP += 100
	a.HP = a.MaxHP
	a.MaxMP += 20
	a.MP = a.MaxMP

	if c := ecs.Get(s.Store(), CCombat).Get(id); c != nil {
		c.Attack += 10
		ecs.Get(s.Store(), CCombat).Add(id, *c)
	}
}

// playerLevel 返回玩家的当前等级，没属性组件时返回 1（兜底）。
func playerLevel(s *world.Scene, id uint64) uint8 {
	if a := ecs.Get(s.Store(), creature.CAttr).Get(id); a != nil {
		return a.Level
	}
	return 1
}
