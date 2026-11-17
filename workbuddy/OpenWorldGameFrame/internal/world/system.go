package world

import (
	"fmt"
	"sync/atomic"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/protocol"
)

// Stage 是 System 的运行阶段。
//
// 分阶段而不是"按注册顺序一把梭"，是因为不同性质的逻辑对"看到的是
// 本帧还是上帧的状态"有明确要求。写死阶段名，能让新加的系统一眼看出
// 自己该往哪儿插，而不用去数 addSystem 的调用顺序。
//
// 一帧内的执行顺序：
//
//	drainCmds      控制指令（进入/离开/心跳）落地
//	StageCommand   需要"实体刚进场景就能看到"的逻辑（如属性初始化）
//	StageInput     位移意图结算（内置 moveSystem）
//	StageLogic     玩法逻辑：AI、战斗、Buff、任务（写下一帧的意图）
//	reapIdle       心跳超时踢人
//	StageReplicate 对外同步：属性包、大范围状态、统计快照
type Stage uint8

const (
	// StageCommand 在控制指令落地后、位移结算前执行。
	StageCommand Stage = iota
	// StageInput 是位移结算阶段，通常只有内置的 moveSystem。
	StageInput
	// StageLogic 是玩法逻辑阶段，绝大多数自定义 System 应该挂在这里。
	StageLogic
	// StageReplicate 是对外同步阶段，只发消息、不改游戏状态。
	StageReplicate
	stageCount
)

func (s Stage) String() string {
	switch s {
	case StageCommand:
		return "command"
	case StageInput:
		return "input"
	case StageLogic:
		return "logic"
	case StageReplicate:
		return "replicate"
	default:
		return "unknown"
	}
}

// maxSystemFailures 是单个 System 连续 panic 多少次后被停用。
//
// 一个每帧都 panic 的系统会刷爆日志、并且让同一阶段后面的系统永远跑不到。
// 停用它是"把故障限制在局部"：场上其他玩法继续运行，运维能从容重启实例。
const maxSystemFailures = 3

// Ctx 是 System 拿到的上下文。
//
// 它只在 tick 协程内有效，不要保存到其他 goroutine 里使用——
// Scene 内部的所有集合都是单写者模型，跨 goroutine 读就是数据竞争。
type Ctx struct {
	// Scene 是所属场景，提供视野遍历、发消息、生成实体等动作。
	Scene *Scene
	// Store 是组件存储，玩法数据都放这里，不要往 Entity 里加字段。
	Store *ecs.Store
	// Now 是本帧开始时刻。一帧内所有系统看到的时间必须一致，
	// 否则同一个 tick 里"谁先跑"会影响结果，回放和压测都不可复现。
	Now time.Time
	// Delta 是距上一帧的间隔，用于积分（回血、CD 递减）。
	Delta time.Duration
}

// System 是一段挂到场景上的玩法逻辑。
//
// 用结构体而不是 interface，是为了让注册写在一行里：
//
//	scene.AddSystem(&world.System{
//		Name: "combat", Stage: world.StageLogic, Tick: combat.Tick,
//	})
//
// Tick 必须是非阻塞的：它在 tick 协程里同步执行，里面做一次 IO
// 就会把整帧拖长，所有玩家的操作延迟都跟着涨。
type System struct {
	Name  string
	Stage Stage
	Tick  func(*Ctx)

	// 下面几个字段由 Scene 维护，玩法层不要碰。
	lastNanos atomic.Int64
	disabled  atomic.Bool
	fails     int
}

// TickOnce 手动推进一帧。
//
// 只给测试和离线模拟用：正常运行时帧由 tick 协程驱动，外部再调一次
// 会让同一帧的逻辑跑两遍。注意场景 Start 之后调用会和 tick 协程竞争，
// 只能在未启动的场景上（或测试里停掉 tick 后）使用。
func (s *Scene) TickOnce() { s.doTick() }

// Store 返回场景的组件存储。
func (s *Scene) Store() *ecs.Store { return s.store }

// AddSystem 注册一个 System。必须在 Start 之前调用。
//
// 允许在 Start 前注册是为了让注册表在运行期完全只读——
// 这样 /admin/state 这类跨 goroutine 的读取不需要任何锁，
// 也不会有"注册到一半被 tick 看到"的中间态。
func (s *Scene) AddSystem(sys *System) error {
	if s.started.Load() {
		return fmt.Errorf("world: AddSystem(%q) after scene start", sys.Name)
	}
	if sys == nil || sys.Tick == nil {
		return fmt.Errorf("world: nil system or nil tick")
	}
	if sys.Name == "" {
		return fmt.Errorf("world: system name is required")
	}
	if sys.Stage >= stageCount {
		return fmt.Errorf("world: system %q has invalid stage %d", sys.Name, sys.Stage)
	}
	for st := range s.systems {
		for _, e := range s.systems[st] {
			if e.Name == sys.Name {
				return fmt.Errorf("world: duplicate system %q", sys.Name)
			}
		}
	}
	s.systems[sys.Stage] = append(s.systems[sys.Stage], sys)
	return nil
}

// AddSystemFunc 是 AddSystem 的简写。
func (s *Scene) AddSystemFunc(name string, stage Stage, fn func(*Ctx)) error {
	return s.AddSystem(&System{Name: name, Stage: stage, Tick: fn})
}

// runSystems 执行一个阶段的全部 System。
//
// 每个 System 单独包一层 recover：一个系统的 bug 不该让整帧后续逻辑
// 全部跳过——那样表现是"某个玩法卡住后，全服玩家都不动了"，
// 比单个功能出错严重得多。
func (s *Scene) runSystems(stage Stage) {
	for _, sys := range s.systems[stage] {
		if sys.disabled.Load() {
			continue
		}
		s.runSystem(sys)
	}
}

func (s *Scene) runSystem(sys *System) {
	start := time.Now()
	defer func() {
		sys.lastNanos.Store(time.Since(start).Nanoseconds())
		if r := recover(); r != nil {
			sys.fails++
			s.sysPanics.Add(1)
			if sys.fails >= maxSystemFailures {
				sys.disabled.Store(true)
				s.log.Error("system disabled after repeated panics",
					"scene", s.id, "system", sys.Name, "stage", sys.Stage.String(), "panic", r)
				return
			}
			s.log.Error("system panic",
				"scene", s.id, "system", sys.Name, "stage", sys.Stage.String(),
				"fails", sys.fails, "panic", r)
		}
	}()
	sys.Tick(&s.sysCtx)
}

// OnEntityEnter 注册实体进入场景的回调，用于给实体挂初始组件。
//
// 有了钩子，world 包就不需要知道"玩家有属性、怪物有 AI"这些玩法概念，
// 玩法包自己决定给新实体挂什么。这是 ECS 与场景解耦的关键一步：
// world 只管生命周期，game 只管数据。
func (s *Scene) OnEntityEnter(fn func(*Ctx, *Entity)) {
	s.enterHooks = append(s.enterHooks, fn)
}

// OnEntityLeave 注册实体离开场景的回调。
//
// 调用时机在"组件被销毁之前"，所以可以在这里读组件做存档、掉落、结算。
// 反过来，这里不能往 grid / entities 里塞东西——那是场景的账。
func (s *Scene) OnEntityLeave(fn func(*Ctx, *Entity)) {
	s.leaveHooks = append(s.leaveHooks, fn)
}

func (s *Scene) fireEnter(e *Entity) {
	for _, fn := range s.enterHooks {
		fn(&s.sysCtx, e)
	}
}

func (s *Scene) fireLeave(e *Entity) {
	for _, fn := range s.leaveHooks {
		fn(&s.sysCtx, e)
	}
}

// Entity 按 ID 查实体，不存在返回 nil。只能在 tick 协程内调用。
func (s *Scene) Entity(id uint64) *Entity { return s.entities[id] }

// ForEachNear 遍历实体视野内的其他实体（含自己）。
//
// 只读：回调里不要增删实体，否则会改变正在遍历的格子内容。
// 需要"遍历中生成/删除"时，先收集 ID 再统一处理。
func (s *Scene) ForEachNear(e *Entity, fn func(*Entity)) {
	s.grid.ForEachNear(e.cell, func(id uint64, o *Entity) {
		if id == e.id {
			return
		}
		fn(o)
	})
}

// SendTo 给指定实体发消息。非玩家实体（NPC）不持有连接，直接返回 false。
func (s *Scene) SendTo(e *Entity, msgID uint16, body []byte) bool {
	if e.kind != KindPlayer {
		return false
	}
	return e.client.Send(msgID, body) == nil
}

// BroadcastNear 把同一条消息发给视野内的其他玩家，返回成功条数。
//
// body 只编码一次再广播：这是视野广播路径上唯一的分配点，
// 千万别在循环里重新 Encode。
//
// 只发给玩家：NPC 有出口但那是空实现，把它算进收件人只会让
// 密集刷怪区（2000 只怪互相 3×3 相邻）白跑一半以上的循环。
func (s *Scene) BroadcastNear(e *Entity, msgID uint16, body []byte) int {
	n := 0
	s.grid.ForEachNear(e.cell, func(id uint64, o *Entity) {
		if id == e.id || o.kind != KindPlayer {
			return
		}
		if o.client.Send(msgID, body) == nil {
			n++
		}
	})
	return n
}

// MoveEntity 让服务端驱动的实体（NPC）移动到指定位置。
//
// 走和玩家完全相同的校验与广播路径：NPC 也会被速度钳制，
// 也会触发跨格视野刷新。这样"怪卡在墙里瞬移"之类的问题
// 在开发期就会暴露，而不是上线后由玩家发现。
func (s *Scene) MoveEntity(e *Entity, x, y, z float32, now time.Time) {
	s.applyMove(e.id, x, y, z, now)
}

// syncAttr 编码并下发一条属性包：先给自己，再给视野内其他人。
//
// 属性是低频数据（1Hz 或变化时下发），所以这里不省那一次 Encode。
func (s *Scene) SyncAttr(e *Entity, a protocol.EntityAttr) int {
	body := a.Encode()
	n := 0
	if e.kind == KindPlayer && e.client.Send(protocol.MsgS2CEntityAttr, body) == nil {
		n++
	}
	n += s.BroadcastNear(e, protocol.MsgS2CEntityAttr, body)
	s.attrSyncs.Add(int64(n))
	return n
}

// SystemStat 是单个 System 的运行状态快照。
type SystemStat struct {
	Name     string `json:"name"`
	Stage    string `json:"stage"`
	LastNs   int64  `json:"lastNs"`
	Fails    int    `json:"fails"`
	Disabled bool   `json:"disabled,omitempty"`
}

// systemStats 汇总所有 System 的耗时。只能读运行期不变的结构（见 AddSystem）。
func (s *Scene) systemStats() []SystemStat {
	out := make([]SystemStat, 0, 8)
	for st := Stage(0); st < stageCount; st++ {
		for _, sys := range s.systems[st] {
			out = append(out, SystemStat{
				Name:     sys.Name,
				Stage:    st.String(),
				LastNs:   sys.lastNanos.Load(),
				Fails:    sys.fails,
				Disabled: sys.disabled.Load(),
			})
		}
	}
	return out
}
