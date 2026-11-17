package control

import "time"

// Config 是控制平面的启动配置，通常来自 configs/zones.json。
type Config struct {
	// Binary 是被托管的服务端可执行文件路径。
	Binary string `json:"binary"`
	// WorkDir 是子进程的工作目录。
	WorkDir string `json:"workDir"`
	// LogDir 是子进程 stdout/stderr 的落盘目录。
	LogDir string `json:"logDir"`
	// TickMs 透传给服务端的逻辑帧间隔。
	TickMs int `json:"tickMs"`
	// LogLevel 透传给服务端的日志级别。
	LogLevel string `json:"logLevel"`
	// GraceSeconds 是关服时给玩家的倒计时秒数。
	GraceSeconds int `json:"graceSeconds"`
	// ReadyTimeoutSeconds 是等待 /readyz 通过的最长时间。
	ReadyTimeoutSeconds int `json:"readyTimeoutSeconds"`
	// Zones 是区服定义。
	Zones []ZoneConfig `json:"zones"`
}

// ZoneConfig 描述一个区服要起几个实例、端口从哪开始。
//
// 端口分配是约定式的：实例 i 用 gatePort = PortBase+i，
// adminPort = PortBase+1000+i。约定优于配置，端口表不容易写错。
type ZoneConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Instances int    `json:"instances"`
	PortBase  int    `json:"portBase"`
}

// Instance 是一个被托管的服务进程。
//
// 注意 Role 目前只有 world 一种真正落地：M1/M2 的服务端把 Gate 和 World
// 放在同一个进程里，拆成独立角色需要跨进程 RPC（M3）。
// 编排器按 Role 排序的逻辑已经就位，届时只要给配置加角色，代码不用改。
type Instance struct {
	ID       string `json:"id"`
	ZoneID   string `json:"zoneId"`
	ZoneName string `json:"zoneName,omitempty"`
	Role     string `json:"role"`
	Index    int    `json:"index"`

	GateAddr  string `json:"gateAddr"`
	AdminAddr string `json:"adminAddr"`

	PID      int       `json:"pid"`
	State    State     `json:"state"`
	Since    time.Time `json:"since"`
	ExitCode int       `json:"exitCode"`

	LastError string `json:"lastError,omitempty"`

	// 通过 /admin/state 采到的运行态。只在 Running 时有值。
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	// ProbeAt 是最近一次成功探测的时间。
	ProbeAt time.Time `json:"probeAt,omitempty"`
}

// Snapshot 是从服务端 /admin/state 摘出来的关键指标。
//
// 只取控制台真正会展示的字段。整份 state 有几十个字段，
// 原样转发会让控制台的轮询包变大几十倍，而且字段一改前端就崩。
type Snapshot struct {
	UptimeSeconds int64   `json:"uptimeSeconds"`
	Online        int64   `json:"online"`
	Entities      int64   `json:"entities"`
	TickLagMs     float64 `json:"tickLagMs"`
	Broadcasts    int64   `json:"broadcasts"`
	MoveIntents   int64   `json:"moveIntents"`
	MoveApplied   int64   `json:"moveApplied"`
	HeapMB        float64 `json:"heapMB"`
	Goroutines    int     `json:"goroutines"`
	Draining      bool    `json:"draining"`
}

// MergeRatio 返回移动合并率：同一帧内被合并掉的意图占比。
// 0 表示客户端发得比 tick 慢（正常），越高说明客户端在重发。
func (s *Snapshot) MergeRatio() float64 {
	if s == nil || s.MoveIntents == 0 {
		return 0
	}
	if s.MoveApplied >= s.MoveIntents {
		return 0
	}
	return 1 - float64(s.MoveApplied)/float64(s.MoveIntents)
}

// ZoneView 是区服聚合视图，控制台的总览卡片直接用这个。
type ZoneView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	State        State   `json:"state"`
	StateLabel   string  `json:"stateLabel"`
	Instances    int     `json:"instances"`
	Running      int     `json:"running"`
	Online       int64   `json:"online"`
	Entities     int64   `json:"entities"`
	MaxTickLagMs float64 `json:"maxTickLagMs"`
	// DesiredRunning 是运维意图：true 表示"这个区应该是开着的"。
	// 和实际状态分开存，才能在页面上一眼看出"想开但开不起来"。
	DesiredRunning bool `json:"desiredRunning"`
}

// InstanceView 是实例列表项。
type InstanceView struct {
	Instance
	StateLabel string  `json:"stateLabel"`
	UptimeStr  string  `json:"uptimeStr"`
	MergeRatio float64 `json:"mergeRatio"`
}

// Task 是一次开服/关服操作的执行记录。
//
// 所有开关服接口都必须异步：一次开服要跑十几秒到几分钟，
// 同步接口必然超时、必然被重试、必然重复开服。
// 接口返回 taskId，前端拿它轮询进度。
type Task struct {
	ID        string     `json:"id"`
	ZoneID    string     `json:"zoneId"`
	Op        string     `json:"op"`    // start / stop
	State     string     `json:"state"` // running / done / failed
	Steps     []TaskStep `json:"steps"`
	Message   string     `json:"message,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   time.Time  `json:"endedAt,omitempty"`
}

// TaskStep 是任务里的一个步骤。
type TaskStep struct {
	Name   string    `json:"name"`
	State  string    `json:"state"` // ok / warn / failed / running
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}
