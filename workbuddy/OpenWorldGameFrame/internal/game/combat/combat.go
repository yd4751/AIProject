// Package combat 是战斗管线：技能、伤害结算、Buff。
//
// 它承接 creature 包打的底（属性、回血、AI），补上"打怪升级"闭环里
// 缺的那块：玩家怎么造成伤害、伤害怎么结算、Buff 怎么随时间生效。
//
// 与 creature 一样，这个包只依赖 world 和 ecs，world 完全不知道有
// "战斗"这回事。攻击指令从客户端进来后，由 world 投进场景 mailbox，
// 再回调这里注入的处理器（见 SetAttackHandler / Register）。
package combat

import (
	"time"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

// Skill 是技能的静态描述。
//
// 静态描述是进程级共享的（不随实体变化），所以放在 Skills 表里查表，
// 而不是每个实体复制一份。实体只存"这个技能的 CD 到什么时候"这种
// 运行时状态。
type Skill struct {
	ID     uint8
	Name   string
	Damage int32 // 基础伤害，结算时乘攻击方攻击力/100
	Range  float32
	// Cooldown 是技能冷却。0 表示无冷却（普攻）。
	Cooldown time.Duration
	// AOE 表示对目标周围所有敌人结算（含目标）。false 是单体。
	AOE    bool
	AOERad float32 // 仅 AOE 有效，超出该范围的敌人不受影响
	// Level 是解锁所需等级。攻击方等级低于此值则拒绝。
	// 0 表示无等级门槛（出生就能用）。
	Level uint8
	// Requires 是前置技能 ID：必须先解锁该技能才能施放当前技能。
	// 0 表示无前置。这是"技能树"的最小表达——技能之间有解锁依赖，
	// 形成一棵树而非平铺列表。
	Requires uint8
	// ComboFrom 是连招起点：如果上一次施放的是这个技能且时间窗口内，
	// 本次技能伤害 × ComboScale（连招加成）。0 表示不参与连招。
	ComboFrom  uint8
	ComboScale int32 // 百分比，默认 150 表示 ×1.5
	// ComboWindow 是连招判定的时间窗口：上次施放 ComboFrom 到现在
	// 不超过这个窗口才算连招。0 用默认值。
	ComboWindow time.Duration
	// Dispel 表示本次技能命中后移除目标所有 Buff（驱散）。
	Dispel bool
}

// Skills 是进程级技能表。用 ID 当下标，ID 就是协议里的 SkillID。
//
// 空槽（nil）表示该 ID 没定义，攻击会被拒绝。
var Skills = [...]*Skill{
	1: {ID: 1, Name: "普通攻击", Damage: 100, Range: 3.0},
	2: {ID: 2, Name: "重击", Damage: 250, Range: 3.0, Cooldown: 3 * time.Second},
	3: {ID: 3, Name: "旋风斩", Damage: 120, Range: 4.0, Cooldown: 6 * time.Second, AOE: true, AOERad: 5.0},
	// 技能树示例：连击、处决、净化。
	// 4 连击：普攻 → 连击，连招加成 150%。
	4: {ID: 4, Name: "连击", Damage: 150, Range: 3.0, Cooldown: 2 * time.Second,
		Requires: 1, ComboFrom: 1, ComboScale: 150, ComboWindow: 3 * time.Second},
	// 5 处决：需先解锁重击，且等级 >= 2，对低血目标伤害翻倍（连招来自重击）。
	5: {ID: 5, Name: "处决", Damage: 300, Range: 3.0, Cooldown: 8 * time.Second,
		Requires: 2, Level: 2, ComboFrom: 2, ComboScale: 200, ComboWindow: 4 * time.Second},
	// 6 净化之刃：命中后驱散目标所有 Buff。
	6: {ID: 6, Name: "净化之刃", Damage: 80, Range: 3.0, Cooldown: 5 * time.Second, Dispel: true},
}

// Combat 是实体的战斗属性 + 技能 CD 状态 + 连招追踪。
//
// 和 creature.Attributes 分开：属性是"血蓝等级"，战斗是"攻防 + 冷却"。
// 分开让 creature 的回血系统只扫 Attributes（不碰 CD），战斗系统只扫 Combat。
type Combat struct {
	Attack  int32 // 攻击力，伤害 = 技能Damage * Attack / 100
	Defense int32 // 防御力，减免伤害 = Defense
	// CritRate 是暴击率，百分比（0-100）。CritMult 是暴击倍率，百分比，
	// 默认 150 表示暴击伤害 ×1.5。0 的 CritMult 在结算里按 150 兜底。
	CritRate uint8
	CritMult int32
	// CDs 是每个技能的下次可施放时刻。用固定长度数组而非 map：
	// 技能种类少（个位数）、ID 连续，数组直接下标寻址零分配，
	// 而且能塞进 Combat 这个定长结构里，保持连续内存。
	CDs [8]time.Time
	// LastSkill 是上一次施放的技能 ID，LastCast 是其时刻。连招判定用：
	// 如果本次技能的 ComboFrom == LastSkill 且窗口内，触发连招加成。
	// 这是"连招"的最小状态——只记"上一次"，不记整条连招历史，
	// 因为当前只有两段式连招（A→B），记一段状态足够。
	LastSkill uint8
	LastCast  time.Time
	// Learned 是已解锁的技能位图，bit i 表示技能 i 已解锁。
	// 单独用位图而不是复用 CDs 判解锁：CD 是"下次能施放"，解锁是
	// "会不会这个技能"，两者语义不同（学了但 CD 中 ≠ 没学）。
	Learned uint64
}

// CCombat 是战斗组件描述符。
var CCombat = ecs.Define[Combat]("combat.stats")

// DefaultCombat 是玩家/怪进场的默认战斗属性。
const (
	playerAttack  = 120
	playerDefense = 20
	// 玩家默认无暴击（CritRate=0），暴击是进阶属性，通过装备/技能/Buff
	// 获得。默认给 0 也让测试确定性（不会随机触发暴击干扰断言）。
	playerCritRate = 0
	playerCritMult = 150
	mobAttack      = 30
	mobDefense     = 10
)

// CombatOf 返回实体的战斗组件，没有则按实体类型给默认值。
//
// 攻击方/受击方都可能没有挂组件（比如没走 Register 进场钩子的测试实体），
// 这里兜一个默认值，让结算逻辑对"没属性的实体"也稳。
func CombatOf(s *world.Scene, id uint64) Combat {
	if c := ecs.Get(s.Store(), CCombat).Get(id); c != nil {
		return *c
	}
	e := s.Entity(id)
	if e != nil && e.IsPlayer() {
		return Combat{Attack: playerAttack, Defense: playerDefense, CritRate: playerCritRate, CritMult: playerCritMult, Learned: 1 << 1}
	}
	return Combat{Attack: mobAttack, Defense: mobDefense}
}

// learned 判断技能是否已解锁。
func learned(c *Combat, skill uint8) bool {
	if skill == 0 || skill >= 64 {
		return false
	}
	return c.Learned&(1<<skill) != 0
}

// BuffKind 是 Buff 的效果类型。
//
// 在"增伤 / 减伤 / 中毒 / 回血"四类基础上补"护盾 / 驱散"两类，
// 覆盖"吸收伤害"和"清除负面状态"这两个最常见的进阶机制。
type BuffKind uint8

const (
	// BuffDamageUp 增伤：伤害 ×(1 + Value/100)。
	BuffDamageUp BuffKind = iota + 1
	// BuffDamageDown 减伤：伤害 ×(1 - Value/100)，下限 0。
	BuffDamageDown
	// BuffPoison 中毒：每 tick 掉 Value 点血。
	BuffPoison
	// BuffRegen 持续回血：每 tick 回 Value 点血。
	BuffRegen
	// BuffShield 护盾：吸收 Value 点伤害，吸满即破。盾是"先于血量"
	// 的伤害缓冲，扣血前先扣盾。不是比例减伤，而是固定吸收量。
	BuffShield
	// BuffDispel 驱散：挂上后立即移除目标身上所有 Buff（含这个驱散
	// 本身，所以它是一次性生效、不驻留的效果）。Value 无意义。
	BuffDispel
)

// Buff 是一条生效中的持续效果。
type Buff struct {
	Kind   BuffKind
	Value  int32 // 幅度：百分比（增伤/减伤/暴击）或固定值（中毒/回血/护盾吸收量）
	Until  time.Time
	Source uint64 // 施加者，仅用于日志/统计，不参与结算
}

// CBuff 是 Buff 组件描述符。Buff 是变长列表，必须单独一个组件，
// 不能塞进 Combat（那样 Combat 就有切片字段，破坏连续内存）。
var CBuff = ecs.Define[[]Buff]("combat.buffs")

// buffTickInterval 是 Buff 的结算周期。持续伤害/回血不需要每帧算，
// 250ms 一次对玩法体验足够，且把 20Hz 的帧降成 4Hz 的结算。
const buffTickInterval = 250 * time.Millisecond

// damageScale 是伤害公式的攻击力缩放：伤害 = 技能Damage * Attack / damageScale。
const damageScale = 100
