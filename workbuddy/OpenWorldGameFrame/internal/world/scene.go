// Package world 实现世界运行时。
//
// 单进程内：一个 Scene 一个 goroutine，实体无锁，控制指令走 mailbox。
// 跨进程（M3.3）：场景归属按 Chunk 记在拓扑里，属于别的进程的地只知道地址，
// 跨界迁移与远端会话转发经 internal/mesh 的节点间链路完成。
package world

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/aoi"
	"openworld/internal/ecs"
	"openworld/internal/mesh"
	"openworld/internal/observability"
	"openworld/internal/protocol"
)

const (
	// DefaultCellSize 是 AOI 格子边长的默认值（米）。
	//
	// 它只影响索引的粒度，不影响视野大小——这两个概念分开配置，
	// 才能在不重建索引的前提下调整视野。
	DefaultCellSize = 16.0
	// DefaultViewRadius 是视野半径的默认值（格），1 表示 3×3。
	//
	// 16m × 3 = 48m 见方视野，这是 MMO 的常见取值。
	// 早期版本用 32m 格，视野达到 96m 见方——主城场景下邻域人数是现在的 4 倍，
	// 实测单条移动处理成本 15.1µs vs 6.3µs。视野不是越大越好。
	DefaultViewRadius = 1
	// DefaultSpeed 是实体默认移动速度（米/秒）。
	DefaultSpeed = 6.0
	// HeartbeatTimeout 超过这个时间没收到心跳就踢人。
	HeartbeatTimeout = 30 * time.Second
	mailboxSize      = 8192
)

// Option 是场景的构造选项。
type Option func(*sceneConfig)

type sceneConfig struct {
	cellSize   float32
	viewRadius int
	bounds     Rect
	capacity   int
	alloc      *idAlloc
	world      *World
	node       string
	msh        *mesh.Mesh
	resolve    ClientResolver
}

// WithAOI 覆盖 AOI 参数。
//
// 格子大小和视野半径要一起看：视野范围 = cellSize × (2×radius+1)。
// 主城人多，适合小格（如 16m/1 → 48m 视野）以压低邻域人数；
// 野外空旷，可以用大格减少格子数量和索引开销。
// 无论怎么配，单个实体的广播成本都只和"视野内有多少实体"有关，
// 而视野内实体的数量取决于玩法密度，不取决于格子大小本身。
func WithAOI(cellSize float32, viewRadius int) Option {
	return func(c *sceneConfig) {
		if cellSize > 0 {
			c.cellSize = cellSize
		}
		if viewRadius > 0 {
			c.viewRadius = viewRadius
		}
	}
}

// WithBounds 指定场景占据的世界矩形。不设则不限范围（单场景部署时的情形）。
//
// 矩形必须按 Chunk 对齐，否则 World.Add 会报错，理由见 topology.go。
func WithBounds(r Rect) Option {
	return func(c *sceneConfig) { c.bounds = r }
}

// WithCapacity 设置场景的软人数上限，0 表示不限。
//
// 只在跨场景迁入时校验：它是"别把隔壁区的压力吸过来"的闸门，
// 不是登录排队。登录限流属于 Gate 的职责，放在场景里会变成
// "人进不来但不知道为什么"。
func WithCapacity(n int) Option {
	return func(c *sceneConfig) { c.capacity = n }
}

// withIDAllocator 让场景共用 World 的 ID 分配器。
func withIDAllocator(a *idAlloc) Option {
	return func(c *sceneConfig) { c.alloc = a }
}

// withOwner 记录场景所属的 World，跨场景迁移要靠它路由。
func withOwner(w *World) Option {
	return func(c *sceneConfig) { c.world = w }
}

// withNodeID 记录本进程的节点标识，决定"这次迁移是进程内还是跨进程"。
func withNodeID(id string) Option {
	return func(c *sceneConfig) { c.node = id }
}

// withMesh 挂上节点间链路。为 nil 时跨节点迁移会被拒绝而不是静默失败。
func withMesh(m *mesh.Mesh) Option {
	return func(c *sceneConfig) { c.msh = m }
}

// withClientResolver 设置会话还原函数，跨节点迁移落地时要用。
func withClientResolver(fn ClientResolver) Option {
	return func(c *sceneConfig) { c.resolve = fn }
}

// Client 是场景向玩家发消息的出口。由 Gate 的 Session 实现。
type Client interface {
	Send(msgID uint16, body []byte) error
	Kick(code uint8, reason string) error
}

// SceneSwitcher 由持有连接的实现方按需实现：实体跨场景迁移落地后，
// 通知它后续的 Move / Heartbeat 该投给哪个场景。
//
// 做成可选接口而不是塞进 Client，是为了不强迫 nopClient、测试桩
// 这些不关心迁移的实现方都去写一个空方法。
type SceneSwitcher interface {
	SetScene(s *Scene)
}

// Locator 让 Client 说明自己的全局地址（哪个节点的哪条连接）。
//
// 迁移时必须带上它：落地节点要靠这个地址把下行消息写回玩家的 socket，
// 而实体 ID 换不了这个信息——实体会搬家，连接不会。
type Locator interface {
	Locate() mesh.ClientRef
}

// Router 接收"我这个实体换到哪个场景/节点了"的通知。
//
// 本地会话用它更新自己的场景指针；跨进程时由代理实现，
// 收到通知就发一条 Rebind 回接入节点，让那边改转发方向。
//
// 和 SceneSwitcher 分开的理由：SetScene 传的是 *Scene，只有同进程才有；
// RouteTo 传的是地址，跨进程也成立。两者都要，因为本地路径需要指针
// 才不用每次 Move 都做一次 map 查找。
type Router interface {
	RouteTo(p mesh.Peer, entity uint64, x, y, z float32)
}

// Kind 区分实体由谁驱动。同一套移动/AOI 代码同时服务两种实体，
// 差别只在"位置从哪来"：玩家上报，怪物由 AI 算。
type Kind uint8

const (
	// KindPlayer 是玩家实体：持有连接，靠心跳保活。
	KindPlayer Kind = iota + 1
	// KindMob 是服务端驱动的实体（NPC、怪物、掉落物容器）。
	KindMob
)

// Entity 是场景内的一个可移动实体。
//
// 这里只放"高频且所有实体都有"的字段：位置、速度、所在格。
// 属性、Buff、AI 状态这类玩法数据一律放 ECS 组件（见 internal/ecs），
// 否则每加一个玩法就要改这个结构体，并且所有实体都要为用不到的字段
// 付内存代价。
type Entity struct {
	id      uint64
	kind    Kind
	player  string
	x, y, z float32
	speed   float32
	client  Client
	// cell 是实体当前所在 AOI 格子，由索引在 Insert/Move 时返回。
	// 业务不能自己算，否则和索引里的归属会不一致。
	cell aoi.Cell

	lastHeartbeat time.Time
	lastMoveAt    time.Time

	// migrating 表示实体正在跨场景迁移，此时本地不再接受位移。
	// 短暂的冻结是刻意的：迁移只有 1~2 帧，而"两边都能改位置"
	// 会让落点取决于两个场景的调度顺序，压测时表现为偶发的瞬移。
	migrating bool
}

// ID 返回实体 ID。
func (e *Entity) ID() uint64 { return e.id }

// Kind 返回实体类型。
func (e *Entity) Kind() Kind { return e.kind }

// IsPlayer 判断是否玩家实体。
func (e *Entity) IsPlayer() bool { return e.kind == KindPlayer }

// Name 返回实体名（玩家 ID 或 NPC 名），仅用于日志和调试。
func (e *Entity) Name() string { return e.player }

// Pos 返回坐标。只读快照，要改位置请走 Scene.MoveEntity，
// 这样才会触发索引更新和视野广播。
func (e *Entity) Pos() (x, y, z float32) { return e.x, e.y, e.z }

// Speed 返回移动速度（米/秒）。
func (e *Entity) Speed() float32 { return e.speed }

// SetSpeed 修改移动速度。速度影响的是"合法位移额度"，
// 改完立刻生效，不需要刷新索引。
func (e *Entity) SetSpeed(v float32) {
	if v > 0 {
		e.speed = v
	}
}

// nopClient 是 NPC 使用的空出口：场上广播会照常走到这里，但什么也不做。
//
// 用空实现而不是让 client 为 nil，是为了让广播路径少一个分支。
// 移动广播每帧要跑上百万次，热循环里每多一个 nil 判断都要付代价，
// 而空接口调用的代价（约 2ns）本来就在。
type nopClient struct{}

func (nopClient) Send(uint16, []byte) error { return nil }
func (nopClient) Kick(uint8, string) error  { return nil }

var npcClient Client = nopClient{}

// 场景内部消息。所有对实体的写操作都必须走 mailbox，
// 保证只有一个 goroutine 能碰到 entities/cells。
type msgEnter struct {
	id     uint64
	player string
	client Client
	// hasPos 为 true 时使用指定坐标，否则用 ID 散列出生的位置。
	// 指定坐标是给复活点、传送、测试用的。
	hasPos bool
	x      float32
	z      float32
}

type msgLeave struct{ entityID uint64 }

type msgHeartbeat struct{ entityID uint64 }

// msgSetSpeed 改实体速度。走 mailbox 而不是直接改字段：
// 速度参与超速校验，只能由 tick 协程写。
type msgSetSpeed struct {
	entityID uint64
	speed    float32
}

// msgAttack 是一次攻击指令。attacker 是攻击者，target 是受击者，skill 是技能 ID。
//
// 攻击不进 System 而是走 mailbox：它需要读实体表、改属性、可能移除实体，
// 全部是单写者模型下的写操作，必须由 tick 协程串行执行。战斗结算本身
// 由玩法包（internal/game/combat）通过 SetAttackHandler 注入。
type msgAttack struct {
	attacker uint64
	target   uint64
	skill    uint8
}

// msgQuerySkills 是一次技能目录查询。和攻击一样走 mailbox 串行化——
// 它要读实体的战斗组件（Learned 位图），只能由 tick 协程做，避免
// 和场景内的写操作（CD 更新、解锁授予）竞争。实际读取与回包由
// 玩法包（internal/game/combat）通过 SetSkillQueryHandler 注入。
type msgQuerySkills struct {
	entityID uint64
}

// Scene 是一个场景实例。
type Scene struct {
	id  uint32
	log *slog.Logger
	m   *observability.Metrics

	tickDur time.Duration
	mailbox chan any

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// started 之后注册表、钩子都进入只读状态，跨 goroutine 读取才不用加锁。
	started atomic.Bool

	entities map[uint64]*Entity
	grid     *aoi.Grid[*Entity]
	// ids 分配并回收实体 ID。回收是为了让组件池的稀疏数组
	// 钉在「同时在线实体数」而不是「历史累计实体数」上，见 idalloc.go。
	//
	// 多场景部署时它指向 World 的分配器，而不是场景自己的：
	// 迁移出去的实体在目的场景必须沿用同一个 ID（否则客户端要重建本地实体），
	// ID 空间因此必须全局唯一，回收也只能有一个所有者。
	ids *idAlloc

	// world 是所属世界，跨场景迁移靠它路由。单场景直连（测试）时为 nil。
	world *World
	// node 是本进程的节点标识：目标场景的 Node 等于它才是进程内迁移。
	node string
	// msh 是节点间链路，跨进程迁移与远端会话转发走它。单进程部署为 nil。
	msh *mesh.Mesh
	// resolve 把 (节点, 会话) 还原成 Client，跨进程落地时用。
	resolve ClientResolver
	// bounds 是本场景负责的世界矩形。零值表示不限范围。
	bounds Rect
	// maxEntities 是迁入的软上限，0 表示不限。
	maxEntities int
	// codecs 打包/恢复实体的玩法数据，迁移与存档时使用。
	// 玩法层通过 AddTransferCodec 逐个注入（creature 存属性/AI，combat 存
	// 战斗/Buff），每个 codec 的字节用长度前缀拼成一段 blob。
	codecs []TransferCodec
	// transfers 是正在迁移中的实体，key 是实体 ID。
	transfers   map[uint64]*pendingTransfer
	transferSeq uint64

	// store 是玩法数据的组件存储。world 包只负责生命周期，
	// 具体挂什么组件由玩法包通过 OnEntityEnter 钩子决定。
	store      *ecs.Store
	enterHooks []func(*Ctx, *Entity)
	leaveHooks []func(*Ctx, *Entity)

	// onAttack 是攻击指令的处理器，由玩法包（combat）注入。
	// 只能由 tick 协程调用，因为它会读写实体表、属性和组件。
	// 第一个参数是本帧时间，保证 CD / Buff 到期用同一把时钟。
	onAttack func(now time.Time, attacker, target uint64, skill uint8)

	// onQuerySkills 是技能目录查询的处理器，由玩法包（combat）注入。
	// 只能由 tick 协程调用，因为它会读实体的战斗组件。
	onQuerySkills func(entityID uint64)

	// systems 按阶段分桶。Start 之后不再变化，所以读它不需要锁。
	systems [stageCount][]*System
	sysCtx  Ctx

	// compStats 是组件规模的快照，每秒刷新一次。
	// 不直接在 HTTP 线程里读 store，是因为那会读到正在被 tick 修改的切片头。
	compStats atomic.Pointer[[]ecs.PoolStat]
	lastStats time.Time

	// intents 是移动意图缓冲，不进 mailbox。理由见 intent.go。
	// moveScratch 复用它，避免每帧为 drain 出来的意图分配切片。
	intents     intentBuffer
	moveScratch []moveEntry

	entityCount   atomic.Int64
	tickTotal     atomic.Int64
	lastTickNanos atomic.Int64
	tickLagNanos  atomic.Int64
	lastTickAt    time.Time

	// 移动意图 / 实际位移 / 合并掉的意图，用来观察合并效果。
	// 合并率 = 1 - applied/written，正常情况下应接近 0（客户端 10Hz、服务端 20Hz），
	// 只在客户端发得比 tick 快、或者有批量重发时才会明显上升。
	moveIntents *atomic.Int64
	moveApplied *atomic.Int64
	attrSyncs   *atomic.Int64

	dropped       *atomic.Int64
	rejectedMoves *atomic.Int64
	// blockedMoves 是"走到场景边界被夹住"的次数，与超速钳制分开计。
	// 两者在旧实现里共用一个计数器，结果是一个缺席的邻居让作弊指标
	// 涨了一千多，排障时先把人引到错误的方向。
	blockedMoves *atomic.Int64
	broadcasts   *atomic.Int64
	sysPanics    *atomic.Int64

	// 迁移计数：迁出、迁入、失败（被拒 / 超时 / 投递失败）。
	// 失败数持续非零说明拓扑或容量配置有问题，比单个玩家的瞬移更早暴露。
	handoversOut  *atomic.Int64
	handoversIn   *atomic.Int64
	handoverFails *atomic.Int64
	drainingFlag  atomic.Bool
	// migratingCount 是在途迁移数，供 Stats 跨 goroutine 读。
	migratingCount atomic.Int64

	// evac 是重分区（整场景搬迁）的状态机，只在 tick 协程里读写。
	evac *evacuation
	// evacStat 是它的不可变快照，供 /admin/state 读。
	evacStat atomic.Pointer[EvacInfo]
	// retired 表示本场景已经被搬走，loop 应该在下一帧退出。
	// 只由 tick 协程写、由同一个协程里的 loop 读，不需要原子。
	retired bool
	// standbyFlag 表示本场景是"为接管准备的壳"：能收 Commit，
	// 但还没进路由表。由 World 在接管的两个阶段之间翻转。
	standbyFlag atomic.Bool
	// movingFlag 表示本场景正在搬迁（整块地换节点）。
	//
	// 和 evac != nil 是同一件事，但这个字段是原子的：对账协程要用它
	// 判断"这块地正在被搬，别插手"，而 evac 由 tick 协程独占。
	// 少一个原子字段，就会在迁移的中间态上读到半截状态。
	movingFlag atomic.Bool
}

// NewScene 创建场景并注册指标。
func NewScene(id uint32, tickDur time.Duration, m *observability.Metrics, log *slog.Logger, opts ...Option) *Scene {
	cfg := sceneConfig{cellSize: DefaultCellSize, viewRadius: DefaultViewRadius}
	for _, opt := range opts {
		opt(&cfg)
	}

	s := &Scene{
		id:          id,
		log:         log,
		m:           m,
		tickDur:     tickDur,
		mailbox:     make(chan any, mailboxSize),
		entities:    make(map[uint64]*Entity),
		grid:        aoi.NewGrid[*Entity](cfg.cellSize, cfg.viewRadius),
		store:       ecs.NewStore(),
		ids:         cfg.alloc,
		world:       cfg.world,
		node:        cfg.node,
		msh:         cfg.msh,
		resolve:     cfg.resolve,
		bounds:      cfg.bounds,
		maxEntities: cfg.capacity,
		transfers:   make(map[uint64]*pendingTransfer, 4),
	}
	if s.ids == nil {
		s.ids = newIDAlloc()
	}
	s.sysCtx = Ctx{Scene: s, Store: s.store}

	// 位移结算是内置 System。它是最热的路径（每条位移都要广播），
	// 留在 world 包里省一层间接，也避免"玩法包改了移动逻辑"这种事故。
	// 之所以还是做成 System，是为了让"移动在哪个阶段生效"显式可见：
	// 它在 StageInput，玩法系统在 StageLogic，所以玩法写的意图下一帧结算。
	s.systems[StageInput] = append(s.systems[StageInput], &System{
		Name:  "world.move",
		Stage: StageInput,
		Tick:  func(ctx *Ctx) { s.applyIntents(ctx.Now) },
	})

	if m != nil {
		// 所有指标都带 {scene="N"} 标签。同名指标在多个场景之间必须用标签区分，
		// 否则注册表会按名字去重：计数器变成全局共享（三个场景报同一个数），
		// 仪表只保留第一个注册的场景（另外两个的人数永远不更新）。
		lbl := s.metricLabels()
		m.GaugeNamed("scene_entity_count", lbl, "current entity count in scene", func() float64 {
			return float64(s.entityCount.Load())
		})
		m.GaugeNamed("scene_tick_duration_nanoseconds", lbl, "last tick cpu duration in nanoseconds", func() float64 {
			return float64(s.lastTickNanos.Load())
		})
		m.GaugeNamed("scene_tick_lag_nanoseconds", lbl, "delay between ideal and actual tick time", func() float64 {
			return float64(s.tickLagNanos.Load())
		})
		// 这两个数决定组件池稀疏数组的长度，也就是"ID 空间占了多少内存"。
		// 高水位持续上涨 = 有实体没释放，是排查泄漏最快的信号。
		m.GaugeNamed("scene_entity_id_high_water", lbl, "highest entity id ever allocated in scene", func() float64 {
			return float64(s.ids.stat().HighWater)
		})
		m.GaugeNamed("scene_entity_id_live", lbl, "entity ids currently in use", func() float64 {
			return float64(s.ids.stat().Live)
		})
		s.dropped = m.CounterNamed("scene_mailbox_dropped_total", lbl, "control messages dropped because mailbox is full")
		s.rejectedMoves = m.CounterNamed("scene_move_rejected_total", lbl, "moves clamped due to speed limit")
		s.blockedMoves = m.CounterNamed("scene_move_blocked_total", lbl, "moves clamped because the scene boundary has no reachable target")
		s.broadcasts = m.CounterNamed("scene_broadcast_total", lbl, "entity state packets broadcast")
		s.moveIntents = m.CounterNamed("scene_move_intent_total", lbl, "move intents written by clients")
		s.moveApplied = m.CounterNamed("scene_move_applied_total", lbl, "move intents applied to entities")
		s.attrSyncs = m.CounterNamed("scene_attr_sync_total", lbl, "attribute packets sent")
		s.sysPanics = m.CounterNamed("scene_system_panic_total", lbl, "recovered panics inside systems")
		s.handoversOut = m.CounterNamed("scene_handover_out_total", lbl, "entities handed over to another scene")
		s.handoversIn = m.CounterNamed("scene_handover_in_total", lbl, "entities received from another scene")
		s.handoverFails = m.CounterNamed("scene_handover_failed_total", lbl, "handovers rejected, timed out or dropped")
	} else {
		s.dropped, s.rejectedMoves, s.broadcasts = &atomic.Int64{}, &atomic.Int64{}, &atomic.Int64{}
		s.blockedMoves = &atomic.Int64{}
		s.moveIntents, s.moveApplied = &atomic.Int64{}, &atomic.Int64{}
		s.attrSyncs, s.sysPanics = &atomic.Int64{}, &atomic.Int64{}
		s.handoversOut, s.handoversIn, s.handoverFails = &atomic.Int64{}, &atomic.Int64{}, &atomic.Int64{}
	}
	empty := []ecs.PoolStat{}
	s.compStats.Store(&empty)
	return s
}

// ID 返回场景 ID。
func (s *Scene) ID() uint32 { return s.id }

// Node 返回本场景所在节点。
func (s *Scene) Node() string { return s.node }

// Peer 返回本场景的全局地址。迁移与转发都用它寻址。
func (s *Scene) Peer() mesh.Peer { return mesh.Peer{Node: s.node, Scene: s.id} }

// peerIsLocal 判断一个地址是否指向本进程。
func (s *Scene) peerIsLocal(p mesh.Peer) bool { return p.Local(s.node) && p.Scene == s.id }

// sameProcess 判断地址是否需要走网络。
func (s *Scene) sameProcess(p mesh.Peer) bool { return p.Local(s.node) }

// Name 实现 core.Component。带场景 ID 是因为多场景部署时
// 日志里全是 "scene" 就没法判断是哪个场景起停失败。
func (s *Scene) Name() string { return "scene-" + strconv.FormatUint(uint64(s.id), 10) }

// Start 启动 tick 循环。
func (s *Scene) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	// 注册表从此只读：Stats() 由 HTTP 线程并发读，靠的就是这一点。
	s.started.Store(true)
	s.wg.Add(1)
	go s.loop()
	s.log.Info("scene started", "scene", s.id, "tick", s.tickDur.String(),
		"systems", len(s.systemStats()))
	return nil
}

// Stop 停止 tick 循环。M1 只打日志，M5 会在这里触发全量存档。
func (s *Scene) Stop(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.log.Info("scene stopped", "scene", s.id, "entities", s.entityCount.Load())
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scene) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.tickDur)
	defer ticker.Stop()

	// 只在 tick 触发时推进逻辑，消息在帧内批量消费。
	//
	// 不要写成 select { case <-ticker.C: ...; case m := <-mailbox: ... }。
	// 那种写法在 mailbox 持续非空时，select 会在两个 case 之间随机选择，
	// ticker 的帧会被丢弃，实测 500 人压测时累积出 250ms 的 tick 滞后。
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.doTick()
			// 场景被搬到别的节点了：拓扑已经改完，这里再跑就是
			// 第二个写者。退出循环而不调 Stop——Stop 会等 wg，
			// 而 wg 等的正是当前这个协程。
			if s.retired {
				return
			}
		}
	}
}

func (s *Scene) handle(m any) {
	switch v := m.(type) {
	case *msgEnter:
		s.onEnter(v)
	case *msgLeave:
		s.onLeave(v)
	case *msgHeartbeat:
		s.onHeartbeat(v)
	case *msgSetSpeed:
		if e, ok := s.entities[v.entityID]; ok {
			e.SetSpeed(v.speed)
		}
	case *msgAttack:
		if s.onAttack != nil {
			s.onAttack(s.sysCtx.Now, v.attacker, v.target, v.skill)
		}
	case *msgQuerySkills:
		if s.onQuerySkills != nil {
			s.onQuerySkills(v.entityID)
		}
	case *msgAdmit:
		s.onAdmit(v)
	case *msgAdmitAck:
		s.onAdmitAck(v)
	case *msgCommit:
		s.onCommit(v)
	case *msgBeginMove:
		s.beginEvacuate(v.to, v.token, v.ver, s.clock())
	case *msgMoveReady:
		s.onMoveReady(v)
	case *msgMoveActivated:
		s.onMoveActivated(v)
	case *msgRetire:
		s.retireNow(v.reason)
	case *msgSave:
		v.result <- s.snapshot()
	case *msgRestore:
		v.result <- s.restore(v.scene)
	}
}

// Post 非阻塞地投递一条指令。邮箱满时丢弃并计数——
// 对移动这类高频指令，丢一帧比阻塞调用方（Gate 的读协程）代价小得多。
func (s *Scene) post(m any) bool {
	select {
	case s.mailbox <- m:
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

// EnterAt 在指定坐标进入场景。多场景部署时由 World.Assign 先选好场景和落点。
//
// hasPos 为 true 时不再用 ID 散列出生点：落点由调用方（登录分流）决定，
// 这样同一个玩家每次上线都落在同一个区，不会因为"这次散列到另一个场景"
// 而出现在地图另一头。
func (s *Scene) EnterAt(player string, client Client, x, z float32) uint64 {
	id := s.ids.alloc(time.Now())
	if id == 0 {
		s.log.Error("entity id space exhausted", "scene", s.id,
			"highWater", s.ids.stat().HighWater, "player", player)
		return 0
	}
	msg := &msgEnter{id: id, player: player, client: client, hasPos: true, x: x, z: z}
	if !s.post(msg) {
		s.ids.release(id, time.Now())
		return 0
	}
	return id
}

// Enter 分配实体 ID 并投递进场景。
//
// 返回 0 表示 ID 空间耗尽（有实体没释放）。Gate 必须检查这个返回值，
// 否则会拿到一个永远进不了场景的会话。
func (s *Scene) Enter(player string, client Client) uint64 {
	id := s.ids.alloc(time.Now())
	if id == 0 {
		s.log.Error("entity id space exhausted", "scene", s.id,
			"highWater", s.ids.stat().HighWater, "player", player)
		return 0
	}
	if !s.post(&msgEnter{id: id, player: player, client: client}) {
		// 邮箱满 = 这条进场指令永远不会被处理，实体永远不会真的建出来。
		// 不还回去就是纯粹泄漏一个 ID，而且是每次登录洪峰漏一批。
		s.ids.release(id, time.Now())
		return 0
	}
	return id
}

// Leave 投递离开指令。
func (s *Scene) Leave(entityID uint64) {
	s.post(&msgLeave{entityID: entityID})
}

// Move 记录一次移动意图。不经过 mailbox，见 intent.go 的说明。
func (s *Scene) Move(entityID uint64, x, y, z float32) {
	s.intents.set(entityID, x, y, z)
	s.moveIntents.Add(1)
}

// Heartbeat 投递心跳，用于刷新存活时间。
func (s *Scene) Heartbeat(entityID uint64) {
	s.post(&msgHeartbeat{entityID: entityID})
}

func (s *Scene) onEnter(m *msgEnter) {
	x, z := s.spawnPos(m.id)
	if m.hasPos {
		x, z = m.x, m.z
	}
	e := &Entity{
		id:            m.id,
		kind:          KindPlayer,
		player:        m.player,
		x:             x,
		y:             0,
		z:             z,
		speed:         DefaultSpeed,
		client:        m.client,
		lastHeartbeat: time.Now(),
		lastMoveAt:    time.Now(),
	}

	s.insert(e)

	// 先让玩法层挂组件，再广播进场。
	// 顺序反过来的话，周围玩家会先收到一条"没有属性的实体"，
	// 客户端得自己兜一个"等属性包"的中间态，白白增加客户端复杂度。
	s.fireEnter(e)

	self := &protocol.EntityEnter{EntityID: e.id, X: e.x, Y: e.y, Z: e.z}

	// 双向补齐视野：新玩家看到周围，周围（只限玩家）看到新玩家。
	s.grid.ForEachNear(e.cell, func(_ uint64, o *Entity) {
		if o.id == e.id {
			return
		}
		_ = e.client.Send(protocol.MsgS2CEntityEnter, (&protocol.EntityEnter{
			EntityID: o.id, X: o.x, Y: o.y, Z: o.z,
		}).Encode())
		if o.kind == KindPlayer {
			_ = o.client.Send(protocol.MsgS2CEntityEnter, self.Encode())
		}
	})

	resp := &protocol.EnterSceneResp{
		Code: protocol.CodeOK, EntityID: e.id, SceneID: s.id, X: e.x, Y: e.y, Z: e.z,
	}
	_ = e.client.Send(protocol.MsgS2CEnterScene, resp.Encode())
}

// Spawn 创建一个服务端驱动的实体（NPC / 怪物 / 刷新物）。
//
// 只能在 tick 协程内（即某个 System 里）或 Start 之前调用：
// 它直接改 entities 和 grid，没有加锁，跨 goroutine 调就是数据竞争。
//
// 返回的实体还没有组件，调用方紧接着用 ecs.Get 挂上即可；
// 如果希望"生成即完整"，封装一层玩法函数（见 game/creature.Spawn）。
//
// ID 空间耗尽时返回 nil。调用方必须检查——刷怪循环里忽略这个返回值，
// 会在 ID 泄漏时表现成"怪不刷了"，而不是一个显眼的报错。
func (s *Scene) Spawn(kind Kind, name string, x, z float32) *Entity {
	now := time.Now()
	if s.sysCtx.Now.IsZero() {
		// Start 之前没有帧上下文，用当前时间填一下，
		// 否则速度钳制会因为 elapsed 为负而算出天文数字。
		s.sysCtx.Now = now
	}
	now = s.sysCtx.Now

	id := s.ids.alloc(now)
	if id == 0 {
		s.log.Error("entity id space exhausted, spawn rejected",
			"scene", s.id, "kind", uint8(kind), "name", name)
		return nil
	}

	e := &Entity{
		id:            id,
		kind:          kind,
		player:        name,
		x:             x,
		z:             z,
		speed:         DefaultSpeed,
		client:        npcClient,
		lastHeartbeat: now,
		lastMoveAt:    now,
	}
	s.insert(e)
	s.fireEnter(e)

	// NPC 没有自己的连接，只需要让周围玩家看见它。
	body := (&protocol.EntityEnter{EntityID: e.id, X: e.x, Y: e.y, Z: e.z}).Encode()
	s.BroadcastNear(e, protocol.MsgS2CEntityEnter, body)
	return e
}

// insert 把实体登记进场景（实体表 + 空间索引）。
func (s *Scene) insert(e *Entity) {
	s.entities[e.id] = e
	e.cell = s.grid.Insert(e.id, aoi.Pos{X: e.x, Z: e.z}, e)
	s.entityCount.Add(1)
}

// removeEntity 是实体离场的唯一出口。
//
// 集中在一处的原因：离场要做四件事（回调、广播、摘索引、清组件），
// 任何一处漏掉都会留下"幽灵"——早期踢人路径就没广播 Leave，
// 表现是附近玩家眼里那个人一直站着不动，直到自己走开视野。
func (s *Scene) removeEntity(e *Entity) {
	// 迁移中的实体被下线（玩家在跨界那一瞬间断线）：把在途的迁移取消，
	// 否则目的场景收到 Commit 后会凭空造出一个没有会话的实体。
	s.dropTransfer(e.id)
	e.migrating = false

	s.detach(e)
	// 组件最后清：玩法层的离场回调可能还要读它们做结算。
	s.store.Destroy(e.id)
	// ID 最后还：还早了的话，同帧内的 Spawn 可能抢到这个 ID，
	// 而那时实体还在 entities 里，会出现"两个实例同一个 ID"。
	//
	// 跨节点迁进来的实体带着源节点的 ID，本节点无权回收（会让两个节点
	// 同时存在同一个 ID 的实体），只能通知 ID 的所属节点把它收回去。
	// 不通知也不影响正确性，但那个 ID 就永久泄漏了——M3.1 花力气做的
	// 冷却回收会在跨区玩家身上失效，稀疏数组又开始随累计登录上涨。
	if foreign := s.ids.release(e.id, s.clock()); foreign {
		s.notifyReleaseID(e.id)
	}
}

// notifyReleaseID 告诉 ID 的所属节点：这个 ID 可以回收了。
func (s *Scene) notifyReleaseID(id uint64) {
	owner := ""
	if s.world != nil {
		owner = s.world.IDOwner(id)
	}
	if owner == "" || owner == s.node || s.msh == nil {
		return
	}
	if err := s.msh.Send(owner, mesh.MsgReleaseID, mesh.EncodeReleaseID(&mesh.ReleaseID{ID: id})); err != nil {
		// 丢了就丢了：最坏情况是这个 ID 不再被复用，不会造成错误。
		s.log.Debug("release id notify failed", "id", id, "owner", owner, "err", err)
	}
}

// metricLabels 返回这个场景的指标标签片段。
func (s *Scene) metricLabels() string {
	return fmt.Sprintf(`{scene="%d"}`, s.id)
}

// SetDraining 标记场景进入排水状态。排水中的场景不再接收跨场景迁入——
// 它马上就要关了，收进来的每一个人都要在关服时再赶出去一次。
func (s *Scene) SetDraining(v bool) { s.drainingFlag.Store(v) }

func (s *Scene) draining() bool { return s.drainingFlag.Load() }

// Standby 表示本场景是重分区接管的中间状态：能收 Commit，不在路由表里。
func (s *Scene) Standby() bool { return s.standbyFlag.Load() }

// Moving 表示本场景正在整块搬迁。对账看到它会绕开：归属由搬迁协议说了算。
func (s *Scene) Moving() bool { return s.movingFlag.Load() }

// SetTransferCodec 注入玩法数据的迁移编解码器。
//
// 必须在 Start 之前调用：它被 tick 协程读取，运行期改动就是数据竞争。
// 与 AddTransferCodec 的区别：Set 只保留一个 codec（覆盖之前的），
// Add 追加。多个玩法包各自有数据要存时用 Add，单一玩法用 Set 即可。
func (s *Scene) SetTransferCodec(c TransferCodec) { s.codecs = []TransferCodec{c} }

// AddTransferCodec 追加一个玩法数据的迁移编解码器。
//
// 多个玩法包（creature、combat）各自注册自己的 codec，编码时按注册顺序
// 各自产出一段字节，用长度前缀拼成一个 blob；解码时按同样顺序各取一段。
// 新增一个玩法包只需 Add 一个 codec，不动已有包的格式——这是"多玩法层
// 共享一套迁移/存档机制"的关键。
func (s *Scene) AddTransferCodec(c TransferCodec) {
	if c == nil {
		return
	}
	s.codecs = append(s.codecs, c)
}

// encodeBlob 把所有 codec 的字节用长度前缀拼成一个 blob。
//
// 格式：u16 段数 + 每段 [u16 长度 + 字节]。某个 codec 返回 nil 时记长度 0，
// 保留槽位——这样解码顺序永远与编码顺序一致，不靠"有没有数据"来对齐。
func (s *Scene) encodeBlob(e *Entity) []byte {
	if len(s.codecs) == 0 {
		return nil
	}
	parts := make([][]byte, len(s.codecs))
	total := 2 + len(s.codecs)*2
	for i, c := range s.codecs {
		b := c.Encode(&s.sysCtx, e)
		parts[i] = b
		total += len(b)
	}
	out := make([]byte, 0, total)
	var hdr [2]byte
	putU16(hdr[:], uint16(len(s.codecs)))
	out = append(out, hdr[:]...)
	for _, b := range parts {
		putU16(hdr[:], uint16(len(b)))
		out = append(out, hdr[:]...)
		out = append(out, b...)
	}
	return out
}

// decodeBlob 按长度前缀拆出各段，按注册顺序喂给各 codec。
//
// 段数对不上（旧档少段 / 新档多段）时按 min 处理：少的 codec 拿不到数据
// 就走默认值，多的数据段忽略。这是格式演进的兼容策略。
func (s *Scene) decodeBlob(e *Entity, blob []byte) {
	if len(s.codecs) == 0 || len(blob) < 2 {
		return
	}
	n := int(u16(blob[:2]))
	if n > len(s.codecs) {
		n = len(s.codecs)
	}
	off := 2
	for i := 0; i < n; i++ {
		if off+2 > len(blob) {
			return
		}
		seg := int(u16(blob[off : off+2]))
		off += 2
		if off+seg > len(blob) {
			return
		}
		data := blob[off : off+seg]
		off += seg
		if seg > 0 {
			s.codecs[i].Decode(&s.sysCtx, e, data)
		}
	}
}

func putU16(dst []byte, v uint16) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
}

func u16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

// SetAttackHandler 注入攻击指令的处理器。
//
// 必须在 Start 之前调用：处理器在 tick 协程里被调用，运行期改它
// 就是数据竞争。attack 处理器负责伤害结算、Buff、死亡移除等全部
// 战斗逻辑，world 包只负责把指令从网络/客户端搬到 tick 协程。
func (s *Scene) SetAttackHandler(fn func(now time.Time, attacker, target uint64, skill uint8)) {
	s.onAttack = fn
}

// SetSkillQueryHandler 注入技能目录查询处理器。
func (s *Scene) SetSkillQueryHandler(fn func(entityID uint64)) {
	s.onQuerySkills = fn
}

// Attack 投递一次攻击指令。非阻塞：邮箱满时丢弃。
//
// 攻击是低频指令（每秒几次），但它要改实体状态，和 Move 不同，
// 必须走 mailbox 串行化，不能进意图缓冲。
func (s *Scene) Attack(attacker, target uint64, skill uint8) bool {
	return s.post(&msgAttack{attacker: attacker, target: target, skill: skill})
}

// QuerySkills 请求技能目录。走 mailbox 串行化（读实体战斗组件），
// 非阻塞：邮箱满时丢弃。实体在别的节点时由 Gate 转发语义。
func (s *Scene) QuerySkills(entityID uint64) bool {
	return s.post(&msgQuerySkills{entityID: entityID})
}

// Bounds 返回场景负责的世界矩形，零值表示不限范围。
func (s *Scene) Bounds() Rect { return s.bounds }

// SetSpeed 修改实体移动速度。
//
// 走 mailbox 而不是直接改字段：速度参与超速校验，只能由 tick 协程写。
// 玩法层改速度（加速 Buff、载具、GM 指令）都走这里，不要碰 Entity 的字段。
func (s *Scene) SetSpeed(entityID uint64, v float32) bool {
	return s.post(&msgSetSpeed{entityID: entityID, speed: v})
}

func (s *Scene) onLeave(m *msgLeave) {
	e, ok := s.entities[m.entityID]
	if !ok {
		// 实体不在这里，但可能正在往别处迁：把下线指令转给目的场景。
		//
		// 这是"断线刚好发生在迁移的一两个 tick 内"的补丁。此时 Gate 读到的
		// 还是旧场景（SetScene 还没被调用），Leave 就投到了已经摘除实体的
		// 源场景，而目的场景那边会凭空多出一个没有会话的实体。
		// 转一手能盖住"目的场景已经落地"的情况；还没落地的，靠心跳超时兜底
		// （落地时 lastHeartbeat 重置为当前帧，所以最多多活一个 HeartbeatTimeout）。
		//
		// 目的场景可能已经在本进程，也可能在另一个进程（M3.3）：
		// 后者走 Relay，接收端的 Deliver 会把它还原成同样的 msgLeave。
		if t, pending := s.transfers[m.entityID]; pending {
			s.send(t.dst, &msgLeave{entityID: m.entityID}, mesh.MsgRelay, func() []byte {
				return mesh.EncodeRelay(&mesh.Relay{
					To: t.dst, Entity: m.entityID, Kind: mesh.RelayLeave,
				})
			})
		}
		return
	}
	s.removeEntity(e)
}

func (s *Scene) onHeartbeat(m *msgHeartbeat) {
	if e, ok := s.entities[m.entityID]; ok {
		e.lastHeartbeat = time.Now()
	}
}

// applyMove 把一条意图落到实体上。
//
// now 由调用方传入（帧开始时刻），不在函数内部取——一帧处理上千条位移，
// 每条约一次 time.Now() 是没必要的系统调用开销，而且同一帧内的时间
// 本来就应该一致，否则同帧内的位移会因为执行顺序不同而算出不同的速度额度。
func (s *Scene) applyMove(id uint64, x, y, z float32, now time.Time) {
	e, ok := s.entities[id]
	if !ok {
		return
	}

	elapsed := now.Sub(e.lastMoveAt).Seconds()
	if elapsed < 0.01 {
		elapsed = 0.01
	}

	dx := float64(x - e.x)
	dy := float64(y - e.y)
	dz := float64(z - e.z)
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	// 服务端权威：客户端只能表达意图。允许 1.5 倍速度和 0.5 米容差，
	// 超出部分按比例钳制，而不是直接拒绝——拒绝会让玩家卡顿，
	// 钳制则表现为"跑不动"，同时计数暴露异常客户端。
	allowed := float64(e.speed)*elapsed*1.5 + 0.5
	if dist > allowed {
		s.rejectedMoves.Add(1)
		if dist > 0 {
			k := allowed / dist
			x = e.x + float32(dx*k)
			y = e.y + float32(dy*k)
			z = e.z + float32(dz*k)
		}
	}

	// 边界检查放在坐标钳制之后、落盘之前。
	// 顺序很重要：先按速度钳制，否则客户端可以一帧报到隔壁区，
	// 迁移就变成了免费的传送。
	//
	// 迁移在途时跳过这一段：实体还是归本场景管的，位置继续更新，
	// 落地时用最新位置。冻结输入曾经让边界处出现约 100ms 的空档，
	// 客户端多走几步之后落点对不上，表现为一批 moveRejected。
	// 唯一要守住的是"别再发起第二次迁移"——transfers 里已经有它了。
	if !e.migrating && s.bounds.Valid() && !s.bounds.Contains(x, z) {
		if !s.tryHandover(e, x, z, now) {
			// 没有可去的场景（空白区 / 只有自己 / 邻居不可达 / 对方邮箱满）：
			// 夹回边界内。这比"放行到界外"好——界外的坐标不属于任何场景，
			// 实体就没人管了。
			x, z = s.bounds.Clamp(x, z)
			// 单独计数："这里有墙"和"这个客户端超速"在 rejectedMoves 里
			// 长得一样，处置却完全不同（拓扑问题 vs 安全事件）。
			// 实测一个缺席的邻居让两个数同时涨了一千上下，只看总和
			// 会把排障引向作弊检测。
			s.blockedMoves.Add(1)
		}
		// 发起成功时也继续走完这一帧：实体在 Commit 之前仍归本场景管，
		// 位置照常更新，落地时用最新坐标。不这么做的话，跨界那一步的
		// 位移会被丢掉，玩家落地后会比客户端少走半米，下一包的位移
		// 距离刚好卡在速度上限附近，表现为边界处一串 moveRejected。
	}

	oldCell := e.cell
	e.x, e.y, e.z = x, y, z
	e.lastMoveAt = now
	s.moveApplied.Add(1)

	// Move 只在真正跨格时才动索引，同格移动是纯内存赋值。
	newCell, moved := s.grid.Move(e.id, e, oldCell, aoi.Pos{X: e.x, Z: e.z})
	e.cell = newCell

	// 包体只编码一次，广播给 N 个邻居。
	// 这是广播路径上唯一的分配，不要在循环里重复 Encode。
	body := (&protocol.EntityState{EntityID: e.id, X: e.x, Y: e.y, Z: e.z}).Encode()
	s.grid.ForEachNear(newCell, func(id uint64, o *Entity) {
		// 只发给玩家。NPC 的出口是空实现，给它们编码/调用是纯浪费：
		// 2000 只怪挤在一起时，一半以上的广播收件人是另一只怪。
		if id == e.id || o.kind != KindPlayer {
			return
		}
		if o.client.Send(protocol.MsgS2CEntityState, body) == nil {
			s.broadcasts.Add(1)
		}
	})

	if moved {
		s.refreshView(e, oldCell, newCell)
	}
}

// maxCmdsPerTick 限制单帧处理的控制指令数（进入/离开/心跳）。
//
// 移动不在此列——它走意图缓冲，每帧的位移次数天然以实体数为上界。
// 这条配额存在只是为了防"一批玩家同时上线"或"一次范围事件触发大量
// 指令"把某一帧撑爆，超出的留到下一帧，代价是最多一帧（50ms）的延迟。
//
// 按 1 万人、心跳 1Hz 估算，每帧约 500 条心跳，离 4096 还有 8 倍余量。
const maxCmdsPerTick = 4096

// doTick 处理一帧的全部逻辑。
//
// 顺序是有意安排的：
//
//  1. 控制指令 —— 先让 Enter/Leave 落地，否则同一帧内的位移会找不到实体
//  2. StageInput —— 结算位移意图，此时实体集合已经稳定
//  3. StageLogic —— 玩法逻辑（AI/战斗/Buff），它们的改动下一帧结算
//  4. 心跳超时 —— 放在玩法之后，避免刚进来的实体在同一帧被判死刑
//  5. StageReplicate —— 对外同步，只发消息不改状态
func (s *Scene) doTick() {
	start := time.Now()
	s.tickTotal.Add(1)

	// 帧内时间只取一次：既要给 System 用，也要给速度钳制用。
	// 同一帧里不同来源各取一次 time.Now()，会让"谁先跑"影响位移额度。
	if s.lastTickAt.IsZero() {
		s.sysCtx.Delta = s.tickDur
	} else {
		s.sysCtx.Delta = start.Sub(s.lastTickAt)
	}
	s.sysCtx.Now = start

	s.drainCmds()
	// 迁移超时必须在 Input 之前扫：超时的实体要尽快解冻，
	// 否则这一帧还会因为 migrating 丢掉玩家的位移。
	s.sweepTransfers(start)
	s.runSystems(StageCommand)
	s.runSystems(StageInput)
	s.runSystems(StageLogic)
	s.reapIdle(start)
	s.runSystems(StageReplicate)
	// 搬迁放在所有 System 之后：它是"把实体从世界里拿走"，
	// 放在前面的话，本帧的 AI / 广播会看到一个搬了一半的实体表。
	s.tickEvacuate(start)
	s.refreshStats(start)

	s.lastTickNanos.Store(time.Since(start).Nanoseconds())

	// tick 滞后 = 本次与上次 tick 的实际间隔 - 理想间隔。
	//
	// 为什么不用"实际时刻 - 理想时刻"：那样测出的是相位偏移，
	// 启动阶段卡一次就会永久偏移，之后明明很健康也一直显示大数字。
	// 相邻间隔的偏差才反映"当前这一帧有没有被拖住"。
	//
	// 另外，部分虚拟化环境的单调时钟粒度在毫秒级，单帧几微秒的 CPU 耗时
	// 会直接测成 0（本机实测 1000 次 time.Now() 都是 0）。
	// 滞后基于 50ms 量级，一定能测出来，生产环境应优先看它。
	if !s.lastTickAt.IsZero() {
		interval := start.Sub(s.lastTickAt)
		s.tickLagNanos.Store((interval - s.tickDur).Nanoseconds())
	}
	s.lastTickAt = start
}

// statsInterval 是组件规模快照的刷新间隔。
//
// 不每帧刷新有两个原因：一是 Stats() 要分配一个切片，20Hz 跑一天就是
// 170 万次无谓分配；二是没人需要秒级以内的组件数变化。
const statsInterval = time.Second

// refreshStats 刷新给 /admin/state 用的组件规模快照。
//
// 快照是原子指针，HTTP 线程只读指针指向的不可变切片，
// 因此不需要和 tick 争任何锁，也不会读到写了一半的数据。
func (s *Scene) refreshStats(now time.Time) {
	if !s.lastStats.IsZero() && now.Sub(s.lastStats) < statsInterval {
		return
	}
	s.lastStats = now
	st := s.store.Stats()
	s.compStats.Store(&st)
}

// drainCmds 批量消费控制指令，单帧上限 maxCmdsPerTick。
func (s *Scene) drainCmds() {
	for i := 0; i < maxCmdsPerTick; i++ {
		select {
		case m := <-s.mailbox:
			s.handle(m)
		default:
			return
		}
	}
}

// applyIntents 把本帧收集到的移动意图落到实体上。
//
// 同一实体在同一帧内只处理一次：drainTo 已经用 map 覆盖语义把多次写入合并成一条。
func (s *Scene) applyIntents(now time.Time) {
	s.moveScratch = s.intents.drainTo(s.moveScratch[:0])
	for _, mv := range s.moveScratch {
		s.applyMove(mv.id, mv.x, mv.y, mv.z, now)
	}
}

// reapIdle 踢掉心跳超时的玩家。
//
// 只处理玩家：NPC 是服务端自己刷出来的，没有心跳可发，
// 按这个规则判超时的话，所有怪都会在进场 30 秒后被清空。
func (s *Scene) reapIdle(now time.Time) {
	for _, e := range s.entities {
		if e.kind != KindPlayer || e.migrating {
			// 迁移中的实体不算挂机：它可能正在等对方场景回复，
			// 这几帧里的心跳还没被重新路由过来。
			continue
		}
		if now.Sub(e.lastHeartbeat) > HeartbeatTimeout {
			s.log.Info("kick idle entity", "entity", e.id, "player", e.player,
				"idle", now.Sub(e.lastHeartbeat).String())
			_ = e.client.Kick(protocol.CodeKickHeartbeatTimeout, "heartbeat timeout")
			s.removeEntity(e)
		}
	}
}

// refreshView 在实体跨格时补齐视野差集：谁新进入视野、谁离开了视野。
//
// 这里不构造"旧视野集合/新视野集合"两个临时 map。
// 跨格时新旧 3×3 区域重合 90% 以上，遍历一遍新视野、用 InView 判断
// 是否原本就可见，就能得出差集，全程零分配。
//
// 代价是每次遍历都会看一些被过滤掉的实体（约 9 倍于真正新增的），
// 但跨格本身不频繁（6m/s、32m 格，约 5 秒一次），这笔开销远比每次
// 分配两个 map 便宜。
func (s *Scene) refreshView(e *Entity, oldCell, newCell aoi.Cell) {
	isPlayer := e.kind == KindPlayer
	selfBody := (&protocol.EntityEnter{EntityID: e.id, X: e.x, Y: e.y, Z: e.z}).Encode()

	// 新看见的：在新视野内，但不在旧视野内。
	s.grid.ForEachNear(newCell, func(id uint64, o *Entity) {
		if id == e.id || s.grid.InView(oldCell, o.cell) {
			return
		}
		// NPC 不需要"看见"别人，别为它编码；只有玩家才要收看到别人的包。
		if isPlayer {
			_ = e.client.Send(protocol.MsgS2CEntityEnter, (&protocol.EntityEnter{
				EntityID: o.id, X: o.x, Y: o.y, Z: o.z,
			}).Encode())
		}
		if o.kind == KindPlayer {
			_ = o.client.Send(protocol.MsgS2CEntityEnter, selfBody)
		}
	})

	// 看不见了：在旧视野内，但不在新视野内。必须双向通知，
	// 否则双方各自保留一个"幽灵实体"，客户端表现为人走了影子还在。
	myLeave := (&protocol.EntityLeave{EntityID: e.id}).Encode()
	s.grid.ForEachNear(oldCell, func(id uint64, o *Entity) {
		if id == e.id || s.grid.InView(newCell, o.cell) {
			return
		}
		if o.kind == KindPlayer {
			_ = o.client.Send(protocol.MsgS2CEntityLeave, myLeave)
		}
		if isPlayer {
			_ = e.client.Send(protocol.MsgS2CEntityLeave, (&protocol.EntityLeave{EntityID: id}).Encode())
		}
	})
}

// spawnPos 用实体 ID 散列出一个出生点，避免所有玩家叠在原点。
//
// 有边界时映射到本场景的矩形里。不能"先散列再 Clamp"——
// 散列结果是 [-500,500]，一 Clamp 所有负数都会贴到 MinX 上，
// 玩家会沿着西边界排成一列。
func (s *Scene) spawnPos(id uint64) (float32, float32) {
	const extent = 1000.0
	h := id * 2654435761
	fx := float64(h%uint64(extent)) / extent
	fz := float64((h/7)%uint64(extent)) / extent

	if s.bounds.Valid() {
		w := float64(s.bounds.MaxX - s.bounds.MinX)
		d := float64(s.bounds.MaxZ - s.bounds.MinZ)
		return s.bounds.MinX + float32(fx*w), s.bounds.MinZ + float32(fz*d)
	}
	return float32(fx*extent) - extent/2, float32(fz*extent) - extent/2
}

// Stats 返回场景状态快照，供 /admin/state 使用。
type Stats struct {
	SceneID     uint32 `json:"sceneId"`
	Entities    int64  `json:"entities"`
	CellCount   int    `json:"cells"`
	TickTotal   int64  `json:"tickTotal"`
	LastTickNs  int64  `json:"lastTickNs"`
	TickLagNs   int64  `json:"tickLagNs"`
	Dropped     int64  `json:"mailboxDropped"`
	MoveRejects int64  `json:"moveRejected"`
	// MoveBlocked 是"走到边界、对面没有可达场景"被夹住的次数。
	//
	// 它和 moveRejected 分开计，但**不能只看一个**：玩家顶着墙走时，
	// 客户端仍在按"我在走路"上报坐标，服务端先按速度钳制（rejected +1），
	// 再发现越界并夹回（blocked +1）。所以贴墙走会让两个数一起涨。
	// 判读方法是看比例——
	//   blocked 高、rejected 也高 → 有一堵墙（邻居没起 / 拓扑缺口）
	//   blocked 约等于 0、rejected 高 → 真的是客户端超速
	MoveBlocked int64 `json:"moveBlocked"`
	Broadcasts  int64 `json:"broadcasts"`
	// 移动意图数与实际位移数。两者之差就是被同帧合并掉的次数，
	// 用于判断客户端是否发得比 tick 快（合并率高说明可以降低发包频率）。
	MoveIntents int64 `json:"moveIntents"`
	MoveApplied int64 `json:"moveApplied"`
	// 属性同步包数与系统 panic 次数。
	AttrSyncs int64 `json:"attrSyncs"`
	SysPanics int64 `json:"systemPanics"`
	// Components 是各组件池的规模，Systems 是各 System 的上一帧耗时。
	// 这两项让"某个玩法把帧吃满了"能一眼看出来是谁干的。
	Components []ecs.PoolStat `json:"components,omitempty"`
	Systems    []SystemStat   `json:"systems,omitempty"`
	// IDs 是 ID 空间的快照。HighWater 直接决定组件池稀疏数组的长度，
	// 持续单向上涨就是有实体没释放。
	IDs IDStat `json:"ids"`
	// 拓扑与迁移。Bounds 零值表示不限范围（单场景部署）。
	Bounds Rect `json:"bounds"`
	// HandoversOut/In 是成功迁出/迁入的实体数，Fails 是被拒、超时或投递失败。
	// 稳态下 Out 和 In 应该接近相等；只涨 Fails 说明拓扑或容量配错了。
	HandoversOut  int64 `json:"handoversOut"`
	HandoversIn   int64 `json:"handoversIn"`
	HandoverFails int64 `json:"handoverFails"`
	// Migrating 是当前在途的迁移数，正常情况下是个位数。
	Migrating int  `json:"migrating"`
	Draining  bool `json:"draining,omitempty"`
	// Standby 表示这个场景还没接进路由（重分区接管中）。
	Standby bool `json:"standby,omitempty"`
	// Move 是正在进行的整场景搬迁；nil 表示这块地没在搬。
	Move *EvacInfo `json:"move,omitempty"`
}

// clock 返回"当前帧的时间"。tick 协程内用帧开始时刻，
// 保证同一帧内所有时间计算一致；Start 之前或非 tick 协程回落到 time.Now。
func (s *Scene) clock() time.Time {
	if t := s.sysCtx.Now; !t.IsZero() {
		return t
	}
	return time.Now()
}

// Stats 读取场景状态。注意这里是跨 goroutine 读（admin HTTP 接口），
// 只能读原子值、只读结构和索引自己维护的计数，不能遍历 entities/grid 内部结构。
func (s *Scene) Stats() Stats {
	g := s.grid.Stats()
	var comps []ecs.PoolStat
	if p := s.compStats.Load(); p != nil {
		comps = *p
	}
	return Stats{
		SceneID:       s.id,
		Entities:      s.entityCount.Load(),
		CellCount:     g.Cells,
		TickTotal:     s.tickTotal.Load(),
		LastTickNs:    s.lastTickNanos.Load(),
		TickLagNs:     s.tickLagNanos.Load(),
		Dropped:       s.dropped.Load(),
		MoveRejects:   s.rejectedMoves.Load(),
		MoveBlocked:   s.blockedMoves.Load(),
		Broadcasts:    s.broadcasts.Load(),
		MoveIntents:   s.moveIntents.Load(),
		MoveApplied:   s.moveApplied.Load(),
		AttrSyncs:     s.attrSyncs.Load(),
		SysPanics:     s.sysPanics.Load(),
		Components:    comps,
		Systems:       s.systemStats(),
		IDs:           s.ids.stat(),
		Bounds:        s.bounds,
		HandoversOut:  s.handoversOut.Load(),
		HandoversIn:   s.handoversIn.Load(),
		HandoverFails: s.handoverFails.Load(),
		// 只在 tick 协程里改的 map，跨 goroutine 读长度是有风险的；
		// 这里用原子计数替代。len(map) 本身不是原子的，
		// 所以迁移数单独维护一个计数器。
		Migrating: int(s.migratingCount.Load()),
		Draining:  s.drainingFlag.Load(),
		Standby:   s.standbyFlag.Load(),
		Move:      s.evacStat.Load(),
	}
}
