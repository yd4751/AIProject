// Package control 实现控制平面：区服与实例的生命周期编排。
//
// 控制平面和数据平面是两套东西，必须分开部署：
// 如果控制台跑在 World 进程里，World 挂掉的时候恰好就是最需要看状态的时刻，
// 而那时控制台也一起没了。
//
// 这个包的职责边界：
//   - 它管"进程起没起、健不健康、能不能接客"
//   - 它不管游戏逻辑，也不碰玩家数据
package control

// State 是实例的生命周期状态。
//
// 七态而不是三态（运行/停止/异常），是因为开关服中间有很多"正在进行中"
// 的时刻。如果只用一个布尔量表示，UI 就无法区分"正在开服"和"开服失败"，
// 运维只能靠刷新碰运气。
type State string

const (
	// StatePending 已登记但从未启动过。
	StatePending State = "Pending"
	// StateStarting 进程已拉起（或正在拉起），但还没通过就绪探针。
	StateStarting State = "Starting"
	// StateRunning 就绪探针通过，可以接客。
	StateRunning State = "Running"
	// StateDraining 正在关服：已摘流，正在等玩家下线。
	StateDraining State = "Draining"
	// StateMaintenance 人工置为维护态，不参与自动拉起。
	StateMaintenance State = "Maintenance"
	// StateStopped 正常停止。
	StateStopped State = "Stopped"
	// StateFailed 异常。注意它不是终态——允许人工重启回到 Starting，
	// 很多控制台把异常做成死状态，结果只能删除重建，非常难用。
	StateFailed State = "Failed"

	// StateDegraded 是区服聚合状态专用：部分实例在线、部分不在。
	// 实例本身永远不会处于这个状态。
	StateDegraded State = "Degraded"
)

// transitions 定义允许的状态迁移。
//
// 显式列出而不是"什么都能转"：状态机最大的价值不是允许，而是拒绝。
// 比如 Running 不能直接跳到 Starting（必须先经过 Draining 或 Stopped），
// 否则会出现"旧进程还没退、新进程已经占了同一个端口"。
var transitions = map[State][]State{
	StatePending:     {StateStarting, StateMaintenance, StateStopped},
	StateStarting:    {StateRunning, StateFailed, StateStopped},
	StateRunning:     {StateDraining, StateFailed, StateStopped, StateMaintenance},
	StateDraining:    {StateStopped, StateFailed},
	StateMaintenance: {StateStarting, StateStopped},
	StateStopped:     {StateStarting, StateMaintenance},
	StateFailed:      {StateStarting, StateStopped, StateMaintenance},
	StateDegraded:    {},
}

// CanTransition 判断 s -> to 是否合法。
func (s State) CanTransition(to State) bool {
	for _, allowed := range transitions[s] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Terminal 表示该状态下进程不应再存活。
func (s State) Terminal() bool {
	return s == StateStopped || s == StateFailed || s == StatePending || s == StateMaintenance
}

// Active 表示进程应该活着。
func (s State) Active() bool {
	return s == StateStarting || s == StateRunning || s == StateDraining
}

// Label 返回给人看的中文名，控制台直接用。
func (s State) Label() string {
	switch s {
	case StatePending:
		return "未启动"
	case StateStarting:
		return "启动中"
	case StateRunning:
		return "运行中"
	case StateDraining:
		return "排水关闭中"
	case StateMaintenance:
		return "维护中"
	case StateStopped:
		return "已停止"
	case StateFailed:
		return "异常"
	case StateDegraded:
		return "部分可用"
	default:
		return string(s)
	}
}
