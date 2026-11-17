// Package creature 是 ECS 的参考玩法实现：属性、回血、游荡 AI、属性同步。
//
// 它同时是一个"分层示范"：这个包只依赖 world 和 ecs，world 完全不知道
// 有"怪物"这回事。要加战斗，就在这个包里加一个 combat 包和一个
// StageLogic 系统；要加 Buff，再加一个组件池。Scene 一行都不用改。
//
// 真正的玩法层（战斗管线、技能、任务）按 docs/ARCHITECTURE.md 4.6 的
// 目录拆分，这里只做最小闭环：证明玩法能以 System 形式挂上去、能跑、能被观测。
package creature

import (
	"math"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

// Attributes 是实体的基础属性。
//
// 字段都是定长数值，没有任何指针：这样 10 万个实体就是一段连续的
// 2.4MB 内存，遍历时不会因为追指针把 cache 打满。
// 需要变长数据（背包、Buff 列表）时挂另一个组件，不要塞进这里。
type Attributes struct {
	HP    int32
	MaxHP int32
	MP    int32
	MaxMP int32
	// Level 是玩家/怪的等级。XP 是当前等级内的经验值（累积到阈值就升级）。
	// 等级门槛只约束玩家施放高级技能（见 combat.skillAvailable），怪不升级。
	Level uint8
	XP    int32
	// Dirty 表示属性已变化、等待下发给客户端。
	//
	// 脏标记放在组件里而不是集中维护一张"待同步表"：
	// 集中表要么每帧扫全表（等于没优化），要么维护一个去重的集合
	// （每次改属性都要查一次 map）。放组件里就是一次 bool 赋值，
	// 同步系统遍历时顺手清掉，代价最低。
	Dirty bool
	_     [3]byte
}

// Brain 是怪物 AI 的状态。
type Brain struct {
	State   State
	HomeX   float32
	HomeZ   float32
	TargetX float32
	TargetZ float32
	// NextThink 是下一次"思考"的时刻。AI 不需要每帧决策：
	// 20Hz × 1 万只怪 = 每秒 20 万次决策，而这些决策 99% 都是
	// "还没到我该动的时候"。用下次思考时间做闸门，成本降到 1/40。
	NextThink time.Time
	// seed 是每只怪独立的随机种子。不用全局 rand 是因为它内部有锁，
	// 1 万只怪每帧抽一次会把 tick 协程卡在锁上。
	seed uint32
}

// State 是 AI 的粗粒度状态机。
//
// 刻意做得很小：更复杂的行为（追击、逃跑、协同）属于 M4 的
// 行为树/状态机，这里只够让怪"看起来活着"。
type State uint8

const (
	// StateIdle 站着不动，等下一次思考。
	StateIdle State = iota
	// StateWalk 朝 TargetX/TargetZ 走。
	StateWalk
)

// 组件描述符。包级变量，全进程唯一。
var (
	CAttr  = ecs.Define[Attributes]("creature.attr")
	CBrain = ecs.Define[Brain]("creature.brain")
)

// Kind 是生成时的实体类别名，避免调用方到处写 world.KindMob。
const KindMob = world.KindMob

// Spawn 生成一只带属性和 AI 的怪物。
//
// 只能在 tick 协程内或场景 Start 之前调用（见 world.Scene.Spawn）。
func Spawn(s *world.Scene, name string, x, z float32, maxHP int32) *world.Entity {
	e := s.Spawn(KindMob, name, x, z)
	if e == nil {
		// ID 空间耗尽（理论上限 100 万，真碰到就是有实体没释放）。
		// 返回 nil 让调用方知道"这次没刷出来"，而不是拿到一个空指针往里挂组件。
		return nil
	}
	if maxHP <= 0 {
		maxHP = 100
	}
	// 出生的怪只有 60% 血：回血系统会把它补满，属性同步系统会把
	// 每一次变化推给附近的玩家——这样"组件 -> System -> 网络"整条链路
	// 在不起战斗系统的情况下也能跑起来，方便联调和观察。
	attr := ecs.Get(s.Store(), CAttr)
	attr.Add(e.ID(), Attributes{
		HP:    maxHP * 6 / 10,
		MaxHP: maxHP,
		MP:    100,
		MaxMP: 100,
		Level: 1,
		Dirty: true,
	})

	ecs.Get(s.Store(), CBrain).Add(e.ID(), Brain{
		State: StateIdle,
		HomeX: x,
		HomeZ: z,
		// 用 ID 取模把思考时刻打散到 0~3 秒里。如果所有怪都在同一帧
		// 思考，tick 会周期性出现尖刺（1000 只怪同帧决策 ≈ 1ms），
		// 打散之后每帧只有 1/60。
		NextThink: time.Now().Add(time.Duration(e.ID()%3000) * time.Millisecond),
		seed:      uint32(e.ID())*2654435761 + 12345,
	})
	return e
}

// SpawnHorde 在半径 radius 的圆内撒 n 只怪。
//
// 用固定的黄金角螺旋布点而不是随机撒：随机撒会有重叠和空洞，
// 压测密度不可控，测出来的广播成本每次都不一样。
func SpawnHorde(s *world.Scene, n int, cx, cz, radius float32) int {
	if n <= 0 {
		return 0
	}
	const goldenAngle = 2.399963
	spawned := 0
	for i := 0; i < n; i++ {
		// sqrt 让点在圆内均匀分布，否则会全挤在圆心。
		r := radius * float32(math.Sqrt(float64(float32(i)/float32(n))))
		a := goldenAngle * float32(i)
		x := cx + r*float32(math.Cos(float64(a)))
		z := cz + r*float32(math.Sin(float64(a)))
		if Spawn(s, "mob", x, z, 100) != nil {
			spawned++
		}
	}
	return spawned
}

// Register 把玩法挂到场景上：三个 System + 一个进场钩子。
//
// 顺序即语义：同阶段的 System 按注册顺序执行。回血、AI 都在 StageLogic，
// 属性同步在 StageReplicate（只发消息、不改状态），所以它看到的永远是
// 本帧逻辑跑完之后的结果。
func Register(s *world.Scene) error {
	st := &systems{
		nextRegen: time.Time{},
		nextSync:  time.Time{},
	}
	if err := s.AddSystemFunc("creature.regen", world.StageLogic, st.regen); err != nil {
		return err
	}
	if err := s.AddSystemFunc("creature.wander", world.StageLogic, st.wander); err != nil {
		return err
	}
	if err := s.AddSystemFunc("creature.attr_sync", world.StageReplicate, st.syncAttr); err != nil {
		return err
	}

	// 玩家进场时补一份属性。放在钩子里而不是让 Scene 直接建，
	// 是因为"玩家有血条"属于玩法设定，不是场景运行时的必备条件。
	s.OnEntityEnter(func(ctx *world.Ctx, e *world.Entity) {
		if !e.IsPlayer() {
			return
		}
		ecs.Get(ctx.Store, CAttr).Add(e.ID(), Attributes{
			HP: 1000, MaxHP: 1000, MP: 200, MaxMP: 200, Level: 1, Dirty: true,
		})
	})
	return nil
}
