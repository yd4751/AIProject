package creature

import (
	"math"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

const (
	// regenInterval 是回血的结算间隔。
	//
	// 回血不需要每帧算：它是一个 1Hz 量级的玩法数值，20Hz 结算只是
	// 把同样的结果算 20 遍。低频逻辑放在 tick 里用"下次执行时刻"闸门，
	// 是这套框架的通用做法（属性同步同理）。
	regenInterval = time.Second
	// syncInterval 是属性下发的间隔。
	//
	// 和位置不同，属性变化慢、且通常是"跳变"（打到 0 再回满），
	// 1Hz 的延迟玩家感觉不出来；而每帧下发会让每个怪每秒多发 20 个包，
	// 按 2000 只怪 × 视野内 20 人算就是 80 万包/秒的纯浪费。
	syncInterval = time.Second
	// wanderRadius 是怪物游荡半径（米）。离巢太远会让刷怪点被慢慢搬空。
	wanderRadius = 10.0
)

type systems struct {
	nextRegen time.Time
	nextSync  time.Time
}

// regen 是每秒一次的回血结算。
//
// 这里演示了"低频 System"的标准写法：用一个时间闸门把 20Hz 的帧
// 降成 1Hz 的逻辑，闸门没到就直接返回，代价是一次时间比较。
func (st *systems) regen(ctx *world.Ctx) {
	if !st.nextRegen.IsZero() && ctx.Now.Before(st.nextRegen) {
		return
	}
	st.nextRegen = ctx.Now.Add(regenInterval)

	ecs.Get(ctx.Store, CAttr).Each(func(_ uint64, a *Attributes) {
		if a.HP >= a.MaxHP {
			return
		}
		heal := a.MaxHP / 50 // 每秒 2%
		if heal < 1 {
			heal = 1
		}
		a.HP += heal
		if a.HP > a.MaxHP {
			a.HP = a.MaxHP
		}
		a.Dirty = true
	})
}

// wander 是游荡 AI：走到目标点 -> 停一会儿 -> 再选一个新目标。
//
// 每帧要扫一遍所有 Brain，看起来是 O(N)，但每次只做"比较时间 +
// 判断状态"两件事，1 万只怪约 15µs/帧（占 50ms 帧预算的 0.03%）。
// 换成定时器堆（每只怪一个 time.Timer）反而更贵：
// 1 万个定时器每秒要触发 1 万次，还要维护堆结构。
func (st *systems) wander(ctx *world.Ctx) {
	ecs.Get(ctx.Store, CBrain).Each(func(id uint64, b *Brain) {
		e := ctx.Scene.Entity(id)
		if e == nil {
			// 实体已销毁但组件还在，说明离场清理漏了。
			// 正常路径不会走到这里（Scene.removeEntity 会 Destroy 组件），
			// 保留这个判断是为了让这类 bug 表现为"怪不动"，而不是 panic 一片。
			return
		}

		if b.State == StateWalk {
			if stepToward(ctx, e, b) {
				return
			}
			// 到点了，站着发呆 1~3 秒。
			b.State = StateIdle
			b.NextThink = ctx.Now.Add(time.Duration(1000+nextRand(&b.seed)%2000) * time.Millisecond)
			return
		}

		if ctx.Now.Before(b.NextThink) {
			return
		}
		// 在巢穴附近选一个点。角度和半径都用同一个随机数拆出来，
		// 一只怪一次抽签，省一次 xorshift。
		r := nextRand(&b.seed)
		angle := float64(r%3600) / 3600 * 2 * math.Pi
		radius := float64(r>>12%100) / 100 * wanderRadius
		b.TargetX = b.HomeX + float32(math.Cos(angle)*radius)
		b.TargetZ = b.HomeZ + float32(math.Sin(angle)*radius)
		b.State = StateWalk
	})
}

// stepToward 朝目标走一帧，返回是否还在路上。
//
// 每帧走 speed*delta，交给 Scene.MoveEntity 做速度校验与视野广播——
// 和玩家移动走同一条路径，所以怪也会触发跨格视野刷新，
// 不会出现"怪走到隔壁格子后玩家看不见它"这种只在 NPC 上出现的问题。
func stepToward(ctx *world.Ctx, e *world.Entity, b *Brain) bool {
	x, _, z := e.Pos()
	dx := float64(b.TargetX - x)
	dz := float64(b.TargetZ - z)
	dist := math.Hypot(dx, dz)
	if dist < 0.2 {
		return false
	}

	step := float64(e.Speed()) * ctx.Delta.Seconds()
	if step <= 0 {
		return true
	}
	if step > dist {
		step = dist
	}
	ctx.Scene.MoveEntity(e, float32(float64(x)+dx/dist*step), 0, float32(float64(z)+dz/dist*step), ctx.Now)
	return true
}

// syncAttr 把脏属性下发给客户端。
//
// 只发 Dirty 的实体：静置的怪在回满血之后再也不会产生流量，
// 这比"每帧全量广播属性"省下的是几个数量级的带宽。
func (st *systems) syncAttr(ctx *world.Ctx) {
	if !st.nextSync.IsZero() && ctx.Now.Before(st.nextSync) {
		return
	}
	st.nextSync = ctx.Now.Add(syncInterval)

	attrs := ecs.Get(ctx.Store, CAttr)
	attrs.Each(func(id uint64, a *Attributes) {
		if !a.Dirty {
			return
		}
		e := ctx.Scene.Entity(id)
		if e == nil {
			return
		}
		// 先清脏标记再发：发包失败（写队列满）时这一帧的数据就丢了，
		// 但下一帧的数值一定比这一帧新，所以丢一帧比"卡在脏状态反复重发"更好。
		a.Dirty = false
		ctx.Scene.SyncAttr(e, protocol.EntityAttr{
			EntityID: id,
			HP:       a.HP,
			MaxHP:    a.MaxHP,
			Level:    a.Level,
		})
	})
}

// nextRand 是 xorshift32。
//
// 不用 math/rand 的全局实例：它有原子操作和锁，1 万只怪每帧抽签会把
// tick 协程的开销抬上去。xorshift 是 3 次移位异或，约 1ns，
// 随机质量对"往哪个方向走"这种用途绰绰有余。
func nextRand(seed *uint32) uint32 {
	s := *seed
	if s == 0 {
		s = 2463534242
	}
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*seed = s
	return s
}
