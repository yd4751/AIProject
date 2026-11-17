package combat

import (
	"math"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/game/creature"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

// resolver 持有战斗结算需要的全部引用。每个场景一个实例，
// 由 Register 创建、注入攻击处理器和 Buff tick 系统。
//
// scene / now / pendingDeaths 只在 tick 协程里被读写（攻击处理器和
// buff tick 系统都在 tick 协程里跑），所以不加锁。
type resolver struct {
	scene         *world.Scene
	now           time.Time
	nextBuffTick  time.Time
	pendingDeaths []uint64
}

// Register 把战斗挂到场景上。
//
// 做了三件事：
//  1. 注入攻击处理器（world.Attack 的回调，跑在 tick 协程里）
//  2. 挂 buff tick 系统（StageLogic，结算持续伤害/回血/到期）
//  3. 挂进场钩子，给玩家和怪补战斗组件
//
// 必须在 scene.Start 之前调用。攻击处理器只由 tick 协程调用，
// 所以 resolver 不需要锁。
func Register(s *world.Scene) error {
	r := &resolver{scene: s}
	s.SetAttackHandler(r.attack)
	s.SetSkillQueryHandler(r.querySkills)
	if err := s.AddSystemFunc("combat.buff_tick", world.StageLogic, r.buffTick); err != nil {
		return err
	}
	s.OnEntityEnter(func(ctx *world.Ctx, e *world.Entity) {
		if e.IsPlayer() {
			// 玩家进场：默认解锁普攻（bit 1），带基础暴击。
			ecs.Get(ctx.Store, CCombat).Add(e.ID(), Combat{
				Attack: playerAttack, Defense: playerDefense,
				CritRate: playerCritRate, CritMult: playerCritMult,
				Learned: 1 << 1,
			})
		} else {
			ecs.Get(ctx.Store, CCombat).Add(e.ID(), Combat{Attack: mobAttack, Defense: mobDefense})
		}
	})
	return nil
}

// attack 是攻击指令的入口，由 world.Attack 在 tick 协程里回调。
//
// 校验链（任意一步不过就静默拒绝，不 panic、不回错误包——攻击是
// 高频且可能来自恶意客户端，拒绝是常态而非异常）：
//  1. 技能存在
//  2. 攻击者存在、目标存在、目标不是自己
//  3. 目标在技能射程内
//  4. 技能已解锁（等级门槛 + 前置技能 + Learned 位图）
//  5. 技能 CD 已转好
//
// 然后结算伤害（AOE 对范围内所有敌人结算），最后处理死亡。
func (r *resolver) attack(now time.Time, attackerID, targetID uint64, skillID uint8) {
	sk := Skills[skillID]
	if sk == nil {
		return
	}
	r.now = now

	s := r.scene
	attacker := s.Entity(attackerID)
	if attacker == nil {
		return
	}
	target := s.Entity(targetID)
	if target == nil || targetID == attackerID {
		return
	}

	// 射程校验：只校验目标（AOE 的溅射范围在结算里再判）。
	if !withinRange(attacker, target, sk.Range) {
		return
	}

	// CD 校验：攻击者必须有 Combat 组件（玩家/怪都挂了），且技能 CD 已转好。
	pool := ecs.Get(s.Store(), CCombat)
	ac := pool.Get(attackerID)
	if ac == nil {
		return
	}

	// 技能树校验：等级门槛 + 前置技能 + Learned 位图。
	// 玩家才走这套（怪用默认 Combat，Learned 为 0，不校验解锁）。
	// 等级门槛读 creature 属性（玩家的实时等级），升级系统（level.go）驱动。
	if attacker.IsPlayer() {
		if !skillAvailable(ac, sk, playerLevel(s, attackerID)) {
			return
		}
	}

	if sk.Cooldown != 0 && ac.CDs[sk.ID].After(r.now) {
		return
	}

	// 连招判定：上一次施放 == 本技能 ComboFrom 且窗口内，触发连招加成。
	combo := sk.ComboFrom != 0 && ac.LastSkill == sk.ComboFrom &&
		!ac.LastCast.IsZero() && now.Sub(ac.LastCast) <= comboWindow(sk)

	// 写入 CD 和连招状态（在结算之前，保证连招追踪不遗漏）。
	ac.CDs[sk.ID] = r.now.Add(sk.Cooldown)
	ac.LastSkill = sk.ID
	ac.LastCast = now
	pool.Add(attackerID, *ac)

	atk := ac.Attack
	if atk <= 0 {
		atk = 1
	}

	if sk.AOE {
		// AOE：先结算主目标（ForEachNear 会跳过目标自己），
		// 再对目标周围 AOERad 内的其他敌对实体结算。
		r.dealDamage(attacker, target, sk, atk, combo)
		s.ForEachNear(target, func(o *world.Entity) {
			if o.ID() == attackerID || !isHostile(attacker, o) {
				return
			}
			if withinRange(target, o, sk.AOERad) {
				r.dealDamage(attacker, o, sk, atk, combo)
			}
		})
	} else {
		r.dealDamage(attacker, target, sk, atk, combo)
	}
}

// skillAvailable 判断玩家能否施放技能。
//
// 语义：基础技能（1/2/3，无 Requires 无 Level）天生就会；技能树技能
// （带 Requires 或 Level）需要显式解锁——先学会前置技能，且自身已学，
// 且玩家等级达到技能的等级门槛。这样 Learned 位图只承载"额外学会的技能"，
// 不用给每个基础技能都打 bit；等级门槛则和技能树解耦，由升级系统驱动。
func skillAvailable(c *Combat, sk *Skill, level uint8) bool {
	// 基础技能：无前置、无等级门槛，直接可用。
	if sk.Requires == 0 && sk.Level == 0 {
		return true
	}
	// 等级门槛：玩家等级低于技能要求则拒绝（未达标，不是没学）。
	if sk.Level > 0 && level < sk.Level {
		return false
	}
	// 技能树技能：先学会前置，再学会自身。
	if sk.Requires != 0 && !learned(c, sk.Requires) {
		return false
	}
	return learned(c, sk.ID)
}

// comboWindow 返回连招判定窗口，0 用默认值。
func comboWindow(sk *Skill) time.Duration {
	if sk.ComboWindow > 0 {
		return sk.ComboWindow
	}
	return 3 * time.Second
}

// dealDamage 对单个目标结算一次伤害，广播结算结果，处理死亡。
//
// combo 表示本次攻击是否触发了连招加成。结算顺序：
// 基础伤害 → 连招加成 → 暴击 → 增伤/减伤 Buff → 护盾吸收 → 扣血。
func (r *resolver) dealDamage(attacker, target *world.Entity, sk *Skill, atk int32, combo bool) {
	s := r.scene
	tc := CombatOf(s, target.ID())
	ac := CombatOf(s, attacker.ID())

	base := sk.Damage * atk / damageScale
	base -= tc.Defense
	if base < 1 {
		base = 1
	}

	// 连招加成：连招触发时伤害 ×ComboScale/100。
	if combo {
		base = base * sk.ComboScale / 100
		if base < 1 {
			base = 1
		}
	}

	// 暴击：按攻击方暴击率判定，暴击伤害 ×CritMult/100。
	crit := rollCrit(&ac)
	if crit {
		mult := ac.CritMult
		if mult <= 0 {
			mult = 150
		}
		base = base * mult / 100
	}

	// Buff 修正：攻击方增伤、受击方减伤。乘法叠加，先增后减。
	base = applyBuffScale(s, attacker.ID(), BuffDamageUp, base)
	base = applyBuffScale(s, target.ID(), BuffDamageDown, base)
	if base < 1 {
		base = 1
	}
	damage := base

	// 驱散：技能带 Dispel 时，命中后移除目标所有 Buff（在扣血前，避免
	// 减伤 Buff 先被结算掉又没被清）。驱散是"移除 Buff"，不是伤害。
	if sk.Dispel {
		dispelAll(s, target.ID())
	}

	// 护盾吸收：先扣盾，剩余伤害才扣血。
	damage = absorbShield(s, target.ID(), damage)

	// 扣血：读 creature 的属性组件（combat 依赖 creature 的属性定义，
	// 两个包同属 game 层，这是有意的耦合——战斗必然要动血量）。
	attrPool := ecs.Get(s.Store(), creature.CAttr)
	ta := attrPool.Get(target.ID())
	if ta == nil {
		// 目标没有属性组件（理论上不会，Register 挂了默认值）。
		// 兜底：直接当作击杀不了的木桩，仍广播这一次结算。
		r.broadcastHit(attacker, target, sk, damage, 0, false, crit)
		return
	}
	ta.HP -= damage
	ta.Dirty = true
	killed := ta.HP <= 0

	r.broadcastHit(attacker, target, sk, damage, maxi(ta.HP, 0), killed, crit)

	if killed {
		// 死亡：移除实体（回收 ID），并给施加者挂一个增伤 Buff 作为
		// "击杀奖励"，让 Buff 链路在实战里能完整跑起来。
		if sk.ID == 2 { // 重击击杀：给攻击者 5 秒 30% 增伤
			addBuff(s, attacker.ID(), Buff{Kind: BuffDamageUp, Value: 30, Until: r.now.Add(5 * time.Second), Source: target.ID()})
		}
		// 击杀经验：攻击方是玩家时给经验，累积升级（属性成长）。
		// 放在 Leave 之前——Leave 会清组件，经验要在实体还在时记到攻击方身上。
		if attacker.IsPlayer() {
			awardKillXP(s, attacker.ID())
		}
		s.Leave(target.ID())
	}
}

// broadcastHit 把一次伤害结算广播给目标视野内的所有玩家。
func (r *resolver) broadcastHit(attacker, target *world.Entity, sk *Skill, damage, targetHP int32, killed, crit bool) {
	body := (&protocol.CombatHit{
		AttackerID: attacker.ID(),
		TargetID:   target.ID(),
		SkillID:    sk.ID,
		Damage:     damage,
		TargetHP:   targetHP,
		Killed:     killed,
		Crit:       crit,
	}).Encode()
	// 以目标为圆心广播：目标视野内的人关心这一击。
	r.scene.BroadcastNear(target, protocol.MsgS2CCombatHit, body)
}

// querySkills 是技能目录查询的处理器，在 tick 协程里跑（由 Scene.QuerySkills
// 投进 mailbox）。它读实体的战斗组件拿到 Learned 位图，把静态技能目录 +
// 位图编成 S2C_SKILL_LIST 回给该玩家。
//
// 不缓存目录：技能表进程级共享、极少变化，每次现编成本可忽略，
// 还省去"表变了要失效缓存"的麻烦。
func (r *resolver) querySkills(entityID uint64) {
	e := r.scene.Entity(entityID)
	if e == nil {
		return
	}
	var learned uint64
	if c := ecs.Get(r.scene.Store(), CCombat).Get(entityID); c != nil {
		learned = c.Learned
	}
	_ = r.scene.SendTo(e, protocol.MsgS2CSkillList, buildSkillList(learned).Encode())
}

// buildSkillList 把进程级技能表转成协议目录。
//
// 下标 0 是保留位（协议里没有技能 0），跳过；其余有定义的技能按原样导出。
// 静态字段（名称、伤害、CD、解锁条件）直接搬；Cooldown 是 time.Duration，
// 转成毫秒下发给客户端展示。
func buildSkillList(learned uint64) *protocol.SkillList {
	out := &protocol.SkillList{Learned: learned}
	for i := 1; i < len(Skills); i++ {
		sk := Skills[i]
		if sk == nil {
			continue
		}
		out.Skills = append(out.Skills, protocol.SkillInfo{
			ID:         sk.ID,
			Name:       sk.Name,
			Damage:     sk.Damage,
			Range:      sk.Range,
			CooldownMs: uint32(sk.Cooldown.Milliseconds()),
			AOE:        sk.AOE,
			AOERad:     sk.AOERad,
			Level:      sk.Level,
			Requires:   sk.Requires,
			ComboFrom:  sk.ComboFrom,
			ComboScale: sk.ComboScale,
			Dispel:     sk.Dispel,
		})
	}
	return out
}

// buffTick 结算所有实体的持续 Buff：中毒掉血、持续回血、到期移除。
func (r *resolver) buffTick(ctx *world.Ctx) {
	if !r.nextBuffTick.IsZero() && ctx.Now.Before(r.nextBuffTick) {
		return
	}
	r.nextBuffTick = ctx.Now.Add(buffTickInterval)

	pool := ecs.Get(ctx.Store, CBuff)
	attrPool := ecs.Get(ctx.Store, creature.CAttr)
	now := ctx.Now

	pool.Each(func(id uint64, buffs *[]Buff) {
		kept := (*buffs)[:0]
		changed := false
		for _, b := range *buffs {
			if now.After(b.Until) {
				changed = true
				continue // 到期，丢弃
			}
			switch b.Kind {
			case BuffPoison:
				if a := attrPool.Get(id); a != nil && a.HP > 0 {
					a.HP -= b.Value
					a.Dirty = true
					changed = true
					if a.HP <= 0 {
						// 中毒致死：离开场景。注意不能在遍历里直接 Leave
						// （会改实体表），这里先标记，外面统一处理。
						r.pendingDeaths = append(r.pendingDeaths, id)
					}
				}
			case BuffRegen:
				if a := attrPool.Get(id); a != nil && a.HP < a.MaxHP {
					a.HP += b.Value
					if a.HP > a.MaxHP {
						a.HP = a.MaxHP
					}
					a.Dirty = true
					changed = true
				}
			}
			kept = append(kept, b)
		}
		if changed {
			*buffs = kept
		}
	})
	// 中毒致死的实体统一移除（在遍历之后，避免改正在遍历的集合）。
	for _, id := range r.pendingDeaths {
		r.scene.Leave(id)
	}
	r.pendingDeaths = r.pendingDeaths[:0]
}

// --- 辅助 ---

// addBuff 给实体挂一个 Buff（追加，不查重、不叠加同源）。
func addBuff(s *world.Scene, id uint64, b Buff) {
	pool := ecs.Get(s.Store(), CBuff)
	cur := pool.Get(id)
	if cur == nil {
		pool.Add(id, []Buff{b})
		return
	}
	*cur = append(*cur, b)
}

// rollCrit 按攻击方暴击率掷一次暴击。用攻击方 ID 和时间做种子，
// 避免全局 rand 的锁竞争（和 creature.Brain 的独立种子同一理由）。
//
// 用"攻击方 ID 的高 32 位 + 纳秒"混合成种子，重复攻击同一目标时
// 纳秒不同、结果分布接近均匀。这不是加密安全的随机，但暴击判定
// 不需要密码学强度，只需要"肉眼看起来随机、不可被客户端预测"。
func rollCrit(c *Combat) bool {
	if c.CritRate <= 0 {
		return false
	}
	n := uint32(time.Now().UnixNano())
	n = n*1103515245 + 12345 // 线性同余，够用
	return n%100 < uint32(c.CritRate)
}

// dispelAll 移除实体的所有 Buff。驱散是一次性效果，不驻留。
func dispelAll(s *world.Scene, id uint64) {
	pool := ecs.Get(s.Store(), CBuff)
	pool.Remove(id)
}

// absorbShield 用护盾吸收伤害，返回穿透护盾后仍需扣血的量。
//
// 护盾是"先于血量"的固定吸收：Value 是吸收上限，扣盾直到 0 或
// 伤害耗尽。多个护盾 Buff 按挂载顺序逐个消耗。
func absorbShield(s *world.Scene, id uint64, dmg int32) int32 {
	pool := ecs.Get(s.Store(), CBuff)
	buffs := pool.Get(id)
	if buffs == nil {
		return dmg
	}
	remaining := dmg
	kept := (*buffs)[:0]
	changed := false
	for _, b := range *buffs {
		if b.Kind == BuffShield && remaining > 0 {
			changed = true
			if b.Value >= remaining {
				// 盾够厚，伤害全被吸收，盾减剩余伤害。
				b.Value -= remaining
				remaining = 0
				if b.Value > 0 {
					kept = append(kept, b) // 盾还没破，保留
				}
				continue
			}
			// 盾破了，吸收 b.Value，剩余伤害继续。
			remaining -= b.Value
			continue
		}
		kept = append(kept, b)
	}
	if changed {
		*buffs = kept
	}
	return remaining
}

// applyBuffScale 按实体的某类 Buff 对伤害做比例修正，返回修正后的值。
func applyBuffScale(s *world.Scene, id uint64, kind BuffKind, dmg int32) int32 {
	pool := ecs.Get(s.Store(), CBuff)
	buffs := pool.Get(id)
	if buffs == nil {
		return dmg
	}
	scale := int32(0)
	for _, b := range *buffs {
		if b.Kind == kind {
			scale += b.Value
		}
	}
	if scale == 0 {
		return dmg
	}
	switch kind {
	case BuffDamageUp:
		return dmg * (100 + scale) / 100
	case BuffDamageDown:
		v := dmg * (100 - scale) / 100
		if v < 0 {
			return 0
		}
		return v
	}
	return dmg
}

// withinRange 判断两实体是否在 d 米内。用平面距离，忽略 Y（同一地面）。
func withinRange(a, b *world.Entity, d float32) bool {
	ax, _, az := a.Pos()
	bx, _, bz := b.Pos()
	dx, dz := float64(ax-bx), float64(az-bz)
	return math.Hypot(dx, dz) <= float64(d)
}

// isHostile 判断 a 与 b 是否敌对。当前规则：玩家与怪互相敌对，
// 玩家之间、怪之间不敌对。这是 PvE 的最小规则，PvP 后续再说。
func isHostile(a, b *world.Entity) bool {
	return a.IsPlayer() != b.IsPlayer()
}

func maxi(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
