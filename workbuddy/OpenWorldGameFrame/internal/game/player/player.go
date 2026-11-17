// Package player 是玩家档案的读写：把 creature 的属性 + combat 的战斗
// 数据按玩家名落库，重连时恢复。
//
// 它同时依赖 creature 和 combat（读它们的组件），而这两个包反过来
// 不依赖 player，所以放在独立的包，避免 creature↔combat 之间的循环。
//
// 分层边界：world 不知道"玩家有档案"；creature/combat 各自管自己的
// 组件，不知道"档案会被持久化"；player 包把它们的数据聚合起来落库，
// 是唯一知道"玩家档案长什么样"的地方。
package player

import (
	"time"

	"openworld/internal/ecs"
	"openworld/internal/game/combat"
	"openworld/internal/game/creature"
	"openworld/internal/persist"
	"openworld/internal/world"
)

// EnablePlayerSave 挂上玩家档案的进场恢复 + 离场落库钩子。
//
// 必须在 creature.Register 和 combat.Register 之后调用——进场钩子按
// 注册顺序执行，player 的钩子要在最后跑，才能用存档值覆盖默认值。
func EnablePlayerSave(s *world.Scene, store persist.Store) {
	if store == nil {
		return
	}
	s.OnEntityEnter(func(ctx *world.Ctx, e *world.Entity) {
		if !e.IsPlayer() {
			return
		}
		load(ctx, e, store)
	})
	s.OnEntityLeave(func(ctx *world.Ctx, e *world.Entity) {
		if !e.IsPlayer() {
			return
		}
		save(ctx, e, store)
	})
}

// EnablePeriodicSave 挂一个周期批量回写系统，定期把在线玩家的档案落库。
//
// 与离场落库互补：离场是"精确的最后一笔"（玩家下线时必写），周期回写是
// "高频改属性时的兜底"——进程随时可能崩溃，如果只在离场写，一个在线
// 玩了一小时的玩家，进程崩溃就丢一小时的属性变化。周期回写把丢失窗口
// 压到 interval 以内。
//
// 为什么是周期扫描而不是"变化即写"：属性变化是高频的（每帧都可能扣血），
// 每次都同步落盘（FileStore 是 tmp+rename 两次磁盘 IO）会把 tick 协程拖垮。
// 周期批量写把 N 次变化合并成一次 IO，代价是 interval 内的变化可能丢失。
//
// interval 建议 5~30 秒，权衡持久化窗口和 IO 压力。0 或负数用默认值。
func EnablePeriodicSave(s *world.Scene, store persist.Store, interval time.Duration) {
	if store == nil {
		return
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	st := &periodicSaver{store: store, interval: interval, next: time.Time{}}
	_ = s.AddSystemFunc("player.periodic_save", world.StageLogic, st.tick)
}

// periodicSaver 是周期回写的状态。每个场景一个实例，扫自己的在线玩家。
//
// 时间闸门 + 全量扫描：只有闸门到了才扫一遍。扫描成本是 O(在线玩家数)，
// 且只在闸门触发的那一帧发生，稳态（闸门没到）代价是一次时间比较。
type periodicSaver struct {
	store    persist.Store
	interval time.Duration
	next     time.Time
}

// tick 是周期回写系统。闸门到了就把本场景所有在线玩家的档案批量落库。
//
// 落库失败静默（和离场落库同一个理由）：尽力而为，不阻塞帧。
func (p *periodicSaver) tick(ctx *world.Ctx) {
	if !p.next.IsZero() && ctx.Now.Before(p.next) {
		return
	}
	p.next = ctx.Now.Add(p.interval)

	// 遍历所有带属性的玩家实体。用 CAttr 池遍历（只扫有属性的实体，
	// 跳过纯环境实体），再过滤出玩家。
	ecs.Get(ctx.Store, creature.CAttr).Each(func(id uint64, _ *creature.Attributes) {
		e := ctx.Scene.Entity(id)
		if e == nil || !e.IsPlayer() {
			return
		}
		save(ctx, e, p.store)
	})
}

// save 把玩家当前状态写入 store。失败静默（日志），不阻塞离场——
// 落库是尽力而为，玩家不会因为存储抖动而被卡在离场。
func save(ctx *world.Ctx, e *world.Entity, store persist.Store) {
	rec := persist.PlayerRecord{Player: e.Name(), SavedAt: ctx.Now}

	// 属性段。
	if a := ecs.Get(ctx.Store, creature.CAttr).Get(e.ID()); a != nil {
		rec.HP, rec.MaxHP = a.HP, a.MaxHP
		rec.MP, rec.MaxMP = a.MP, a.MaxMP
		rec.Level = a.Level
		rec.XP = a.XP
	}

	// 战斗段。
	if c := ecs.Get(ctx.Store, combat.CCombat).Get(e.ID()); c != nil {
		rec.Attack, rec.Defense = c.Attack, c.Defense
		rec.CritRate, rec.CritMult = c.CritRate, c.CritMult
		rec.Learned = c.Learned
	}

	data, err := rec.Marshal()
	if err != nil {
		return
	}
	_ = store.Put(e.Name(), data)
}

// load 从 store 读档案并恢复到玩家组件上。无档案（新玩家）则保持
// 进场钩子给的默认值，不覆盖。
func load(ctx *world.Ctx, e *world.Entity, store persist.Store) {
	data, ok := store.Get(e.Name())
	if !ok {
		return
	}
	rec, err := persist.UnmarshalPlayerRecord(data)
	if err != nil {
		return
	}

	if a := ecs.Get(ctx.Store, creature.CAttr).Get(e.ID()); a != nil {
		a.HP, a.MaxHP = rec.HP, rec.MaxHP
		a.MP, a.MaxMP = rec.MP, rec.MaxMP
		a.Level = rec.Level
		a.XP = rec.XP
		a.Dirty = true // 恢复的属性要推给客户端
	}
	if c := ecs.Get(ctx.Store, combat.CCombat).Get(e.ID()); c != nil {
		c.Attack, c.Defense = rec.Attack, rec.Defense
		c.CritRate, c.CritMult = rec.CritRate, rec.CritMult
		c.Learned = rec.Learned
	}
}
