package world

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/mesh"
	"openworld/internal/observability"
)

// World 是场景容器与世界拓扑的所有者。
//
// 它持有四样东西：
//
//	scenes  本进程承载的场景集合
//	topo    Chunk → 场景地址 的归属关系（决定玩家该进哪个场景、跨界该找谁）
//	ids     全局实体 ID 分配器（决定实体 ID 在所有场景间唯一）
//	msh     节点间链路（跨进程迁移与远端会话转发走它）
//
// 为什么 ID 分配器在 World 而不是 Scene：跨场景迁移时实体必须保留原 ID
// （客户端靠 ID 认自己的角色），所以 ID 空间必须全局唯一。如果每个场景
// 各有一份分配器，迁移过来的实体在目的场景就是一个"外来 ID"——它离开时
// 该由谁回收？引入"归还给源场景"的消息协议，就多了一条会丢、会漏的路径。
// 上移一层，这个问题直接不存在。
//
// 跨进程后 ID 空间由**步长分区**切成互不相交的同余类（M3.3）：
// id = seq × stride + offset，stride 是槽位上限、offset 是本节点的槽位。
// 步长固定不随节点增减变化，所以扩容不会让已经发出去的 ID 改变含义（M3.4）。
type World struct {
	mu      sync.RWMutex
	scenes  map[uint32]*Scene
	topo    *Topology
	ids     *idAlloc
	def     *Scene
	log     *slog.Logger
	node    string
	msh     *mesh.Mesh
	resolve ClientResolver
	// idSlots 是按槽位排列的节点名表（下标 = offset，空槽为 ""），
	// 用来把实体 ID 反查回所属节点。
	idSlots []string

	// factory 造运行期新建的场景（重分区接管用），由 cmd/server 注入。
	factory SceneFactory
	// dyn 是运行期创建的场景，退出时要单独停——它们不在 core.App 的组件表里。
	dyn map[uint32]*Scene
	// pending 是"等某个 token 的答复"的协程表，只有接管路径用得上。
	pending map[uint64]chan *mesh.SceneCtl
	// moveSeq 是重分区的令牌发号器，跨节点唯一即可（用于丢弃迟到答复）。
	moveSeq atomic.Uint64

	// 拓扑对账的计数与状态（M3.6）。
	//
	// pushAt 记"上次给某个节点回推摘要的时间"，用于限流；
	// digestSent 记"每个场景上次周期广播出去的版本号"，增量对账用它
	// 判断哪些地变化了（M6.5 增量摘要）；其余是 atomics，读的时候不需要锁。
	topoRound     atomic.Uint64
	topoSent      atomic.Int64
	topoRecv      atomic.Int64
	topoApplied   atomic.Int64
	topoPushed    atomic.Int64
	topoStale     atomic.Int64
	topoConflicts atomic.Int64
	topoRetired   atomic.Int64
	pushAt        map[string]time.Time
	digestSent    map[uint32]uint64
}

// ClientResolver 把 (节点, 会话) 还原成能写消息的 Client。
//
// 本地会话返回 Session 本身；远端会话返回一个代理，Send 走节点间链路。
// 由 Gate 注入：只有它知道"本进程有哪些连接"。
type ClientResolver func(ref mesh.ClientRef) Client

// WorldOption 是 World 的构造选项。
type WorldOption func(*World)

// WithNodeID 设置本节点标识。必须与拓扑配置里的节点名一致，
// 否则本进程会把自己的场景当成"别人的"。
func WithNodeID(id string) WorldOption {
	return func(w *World) {
		w.node = id
		w.topo.node = id
	}
}

// WithMesh 挂上节点间链路。不设表示单进程部署。
func WithMesh(m *mesh.Mesh) WorldOption {
	return func(w *World) { w.msh = m }
}

// WithChunkSize 覆盖拓扑的 Chunk 边长。
//
// 必须在建场景之前设置：Chunk 决定"边界对齐"的判定，中途改会让
// 已经登记进 owner 表的映射和新的对齐规则对不上。
func WithChunkSize(size float32) WorldOption {
	return func(w *World) {
		if size > 0 {
			w.topo.chunkSize = size
		}
	}
}

// WithIDPartition 设置 ID 空间的节点分区：id = seq × stride + offset。
//
// 多进程部署必须设置，否则两个节点都会发出 1、2、3，玩家跨节点之后
// 客户端会看到两个不同的人拿同一个 ID。单进程不设即可（1/0），
// 分配结果与之前完全一致。
func WithIDPartition(stride, offset uint64) WorldOption {
	return func(w *World) { w.ids = newIDAllocSized(stride, offset) }
}

// WithNodeSlots 设置按槽位排列的节点名表（下标 = ID 分区 offset，空槽为 ""）。
//
// 表长必须等于 ID 步长：跨节点死掉的实体要通知 ID 的所有者回收，
// 而实体身上只有 ID，没有"谁发的"。反查规则是 id % stride → 槽位 → 节点名。
func WithNodeSlots(slots []string) WorldOption {
	return func(w *World) { w.idSlots = append([]string(nil), slots...) }
}

// WithClientResolver 设置会话还原函数。跨进程迁移必须有它，
// 否则落地节点拿到 (节点, 会话) 也找不到写消息的出口。
func WithClientResolver(fn ClientResolver) WorldOption {
	return func(w *World) { w.resolve = fn }
}

// NewWorld 创建空的场景容器。
func NewWorld(log *slog.Logger, opts ...WorldOption) *World {
	w := &World{
		scenes:     make(map[uint32]*Scene),
		topo:       NewTopology(DefaultChunkSize, ""),
		ids:        newIDAlloc(),
		log:        log,
		dyn:        make(map[uint32]*Scene),
		pending:    make(map[uint64]chan *mesh.SceneCtl),
		digestSent: make(map[uint32]uint64),
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// NewScene 创建场景并注册到世界，返回的场景与其它场景共享 ID 空间。
//
// 通过 World 创建的场景才能在场景间迁移（它有 world 指针）；
// 直接调 world.NewScene 得到的是独立场景，适合单测。
func (w *World) NewScene(id uint32, tickDur time.Duration, m *observability.Metrics,
	log *slog.Logger, opts ...Option) *Scene {
	s := w.buildScene(id, tickDur, m, log, opts...)
	w.Add(s)
	return s
}

// NewSceneDetached 造场景但**不**登记进 world 的拓扑。
//
// 给重分区用：接管进来的场景要先 Start（这样它才能处理涌进来的 Commit），
// 但还不能进路由表——它得先在"能收"和"开始接客"之间待一会儿。
func (w *World) NewSceneDetached(id uint32, tickDur time.Duration, m *observability.Metrics,
	log *slog.Logger, opts ...Option) *Scene {
	return w.buildScene(id, tickDur, m, log, opts...)
}

// buildScene 造场景但**不**登记进 world。
//
// 拆出来是给重分区用的：接管进来的场景要先 Start（这样它才能处理
// 涌进来的 Commit），但还不能进路由表——它得先在"能收"和"开始接客"
// 之间待一会儿。NewScene 是 buildScene + Add 的简写。
func (w *World) buildScene(id uint32, tickDur time.Duration, m *observability.Metrics,
	log *slog.Logger, opts ...Option) *Scene {
	opts = append(opts,
		withIDAllocator(w.ids),
		withOwner(w),
		withNodeID(w.node),
		withMesh(w.msh),
		withClientResolver(w.resolve),
	)
	return NewScene(id, tickDur, m, log, opts...)
}

// Add 注册场景。第一个注册的成为默认场景。
//
// 带边界的场景会同时登记进拓扑，并校验"不重叠、按 Chunk 对齐"。
// 边界冲突时 Add 会打日志并拒绝登记拓扑（场景仍然可用，只是不参与路由）——
// 这比 panic 温和：拓扑配错应该表现为"某块地没人管"，而不是整个进程起不来。
func (w *World) Add(s *Scene) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, dup := w.scenes[s.ID()]; dup {
		return
	}
	w.scenes[s.ID()] = s
	if w.def == nil {
		w.def = s
	}
	if s.bounds.Valid() {
		if err := w.topo.Add(s, s.bounds); err != nil {
			// 拓扑配错不应该让进程起不来：场景照常可用，只是不参与路由，
			// 落在它范围内的坐标会走默认场景或者被夹在边界上。
			if w.log != nil {
				w.log.Error("register scene bounds failed", "scene", s.ID(), "err", err)
			}
		}
	}
}

// Default 返回默认场景。没有配置拓扑时的兜底入口。
func (w *World) Default() *Scene {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.def
}

// Scene 按 ID 查找场景。
func (w *World) Scene(id uint32) (*Scene, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.scenes[id]
	return s, ok
}

// Route 返回坐标所属的**本地**场景。
//
// 拓扑里可能记着一块属于别的节点的地，那不是本地场景，返回 false。
// 需要"这块地归谁"（不管本地还是远端）时用 PeerAt。
//
// 没有配置拓扑（所有场景都没有边界）时回落到默认场景，
// 这样单场景部署不需要关心拓扑。
func (w *World) Route(x, z float32) (*Scene, bool) {
	p, ok := w.PeerAt(x, z)
	if !ok {
		return nil, false
	}
	return w.localScene(p)
}

// PeerAt 返回坐标归属的场景地址，可能是别的节点。
func (w *World) PeerAt(x, z float32) (mesh.Peer, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p, ok := w.topo.Route(x, z); ok {
		return p, true
	}
	if w.def != nil && !w.topo.hasBounds() {
		return mesh.Peer{Node: w.node, Scene: w.def.id}, true
	}
	return mesh.Peer{}, false
}

// localScene 把地址解析成本地场景对象，不是本地场景则 false。
func (w *World) localScene(p mesh.Peer) (*Scene, bool) {
	if !p.Local(w.node) {
		return nil, false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.scenes[p.Scene]
	return s, ok
}

// SceneByPeer 把地址解析成本地场景，语义与 localScene 相同，供迁移使用。
func (w *World) SceneByPeer(p mesh.Peer) (*Scene, bool) { return w.localScene(p) }

// PeerOf 返回某个场景的全局地址。
func (w *World) PeerOf(sceneID uint32) (mesh.Peer, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p, ok := w.topo.PeerOf(sceneID); ok {
		return p, true
	}
	if _, ok := w.scenes[sceneID]; ok {
		return mesh.Peer{Node: w.node, Scene: sceneID}, true
	}
	return mesh.Peer{}, false
}

// DeclareRemote 登记一块属于其它节点的地。
//
// 只影响路由，不在本地建场景：本进程对这块地上的实体一无所知，
// 唯一需要知道的是"走到这里该把 Prepare 发给谁"。
func (w *World) DeclareRemote(p mesh.Peer, rect Rect) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p.Local(w.node) {
		return fmt.Errorf("world: %s 属于本节点，请用 WithBounds 建场景而不是 DeclareRemote", p)
	}
	return w.topo.AddRemote(p, rect)
}

// Node 返回本节点标识。
func (w *World) Node() string { return w.node }

// Mesh 返回节点间链路，单进程部署时返回 nil。
func (w *World) Mesh() *mesh.Mesh { return w.msh }

// ResolveClient 把 (节点, 会话) 还原成可写消息的 Client。
//
// 没配 resolver 时返回 nil，调用方必须处理——落地一个"没有出口"的实体，
// 等于让玩家在一个收不到任何包的世界里游荡。
func (w *World) ResolveClient(ref mesh.ClientRef) Client {
	if w.resolve == nil {
		return nil
	}
	return w.resolve(ref)
}

// BoundsOf 返回某个场景的边界，本地远端都算。
func (w *World) BoundsOf(p mesh.Peer) (Rect, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.topo.Rect(p.Scene)
}

// IDOwner 返回一个实体 ID 的所属节点。
//
// 单节点部署返回自己（不需要通知任何人）。槽位为空或者越界时返回空串，
// 调用方据此跳过通知——宁可漏一个 ID，也不要把"回收指令"发给错误的节点
// （那会让对方把一个还在用的 ID 发出去）。
func (w *World) IDOwner(id uint64) string {
	if len(w.idSlots) <= 1 {
		return w.node
	}
	idx := int(id % uint64(len(w.idSlots)))
	if idx < 0 || idx >= len(w.idSlots) {
		return ""
	}
	return w.idSlots[idx]
}

// NodeMembers 返回配置声明的全部节点（取自槽位表，空槽忽略）。
//
// 它是"期望的世界"，不包括服务发现临时发现的陌生节点——
// 那些节点没有场景归属，出现在成员表里只会让人困惑。
func (w *World) NodeMembers() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]string, 0, len(w.idSlots))
	for _, n := range w.idSlots {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// SetNodeAlive 标记一个节点当前是否可达。
//
// 不可达的节点，它承载的地块会从路由表里"消失"：玩家走到那块边界会被
// 夹住，而不是发起一次注定超时的迁移。这比"发出去等 3 秒超时再回滚"好——
// 后者每帧都在重试，日志和链路都被无意义的重试刷满。
//
// 注意这里只影响**别人**的地：本节点永远可达（自己不会把自己判死）。
func (w *World) SetNodeAlive(name string, alive bool) {
	if name == "" || name == w.node {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.topo.setAlive(name, alive)
}

// NodeAlive 返回节点是否可达。未表态的节点默认可达（单进程/静态配置场景）。
func (w *World) NodeAlive(name string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.topo.nodeAlive(name)
}

// AliveNodes 返回当前的节点可达视图。
func (w *World) AliveNodes() map[string]bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[string]bool, len(w.idSlots)+1)
	for _, n := range w.idSlots {
		if n != "" {
			out[n] = w.topo.nodeAlive(n)
		}
	}
	return out
}

// Assign 为一个新登录的玩家挑选入场场景和落点。
//
// 多场景部署时必须分流，否则所有人都会堆在默认场景里——拓扑只对
// "走过去的玩家"生效，登录洪峰照样把首个场景打满。
//
// 只在**本节点**的场景里散列（M3.3）：玩家的 TCP 连接接在本节点的 Gate 上，
// 如果把他分配到别的节点的场景，入场就得先跨节点建实体——那是一条
// 额外的 RPC 路径，而且玩家一上线就跨进程，没有任何收益。登录落本地、
// 只有"走过去"才跨节点，是把跨进程的复杂度限制在一个地方。
//
// 分流依据是玩家 ID 的散列，而不是负载均衡：同一个玩家每次上线落在同一块地，
// 位置才稳定（正式的落点应该来自存档，那时仍然用这里的结果做兜底）。
// 按当前负载挑最空的场景看起来更"聪明"，但会让玩家每次上线都换地方。
func (w *World) Assign(player string) (*Scene, float32, float32) {
	w.mu.RLock()
	scenes := make([]*Scene, 0, len(w.scenes))
	for _, s := range w.scenes {
		scenes = append(scenes, s)
	}
	w.mu.RUnlock()
	if len(scenes) == 0 {
		return nil, 0, 0
	}
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].ID() < scenes[j].ID() })

	h := hashString(player)
	s := scenes[int(h%uint64(len(scenes)))]
	x, z := s.spawnPos(h)
	return s, x, z
}

// Chunks 返回 Chunk 归属快照，供控制台画拓扑。
func (w *World) Chunks() []ChunkInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.topo.Chunks()
}

// ChunkSize 返回拓扑的 Chunk 边长。
func (w *World) ChunkSize() float32 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.topo.ChunkSize()
}

// Stats 返回所有场景的状态快照。
func (w *World) Stats() []Stats {
	w.mu.RLock()
	scenes := make([]*Scene, 0, len(w.scenes))
	for _, s := range w.scenes {
		scenes = append(scenes, s)
	}
	w.mu.RUnlock()

	out := make([]Stats, 0, len(scenes))
	for _, s := range scenes {
		out = append(out, s.Stats())
	}
	return out
}

// Count 返回场景数量。
func (w *World) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.scenes)
}

// IDStat 返回全局 ID 空间的快照。
func (w *World) IDStat() IDStat { return w.ids.stat() }

// Partition 把世界切成 rows×cols 个等大场景，返回创建好的场景。
//
// 这是给"一个进程跑多场景"准备的便捷入口：开放世界正式部署时，
// 场景归属应该来自配置中心（哪个 World 负责哪些 Chunk），
// 而不是启动参数里的行列数。
func (w *World) Partition(rows, cols int, size float32, tickDur time.Duration,
	m *observability.Metrics, log *slog.Logger, baseID uint32, opts ...Option) []*Scene {
	if rows <= 0 || cols <= 0 || size <= 0 {
		return nil
	}
	out := make([]*Scene, 0, rows*cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			id := baseID + uint32(r*cols+c)
			rect := Rect{
				MinX: float32(c) * size, MinZ: float32(r) * size,
				MaxX: float32(c+1) * size, MaxZ: float32(r+1) * size,
			}
			allOpts := append([]Option{WithBounds(rect)}, opts...)
			out = append(out, w.NewScene(id, tickDur, m, log, allOpts...))
		}
	}
	return out
}

// hashString 是 FNV-1a。用它而不是 maphash：需要跨进程稳定，
// 否则同一个玩家重启后会分到另一个场景。
func hashString(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// String 便于日志输出。
func (w *World) String() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return fmt.Sprintf("world{scenes=%d chunk=%.0f}", len(w.scenes), w.topo.ChunkSize())
}
