// 世界拓扑：坐标 → 场景的归属关系。
//
// 两级分区的第二级（第一级是 AOI 格）：
//
//	Chunk    512m × 512m，是「谁负责这块地」的单位，只能整块归属一个场景
//	AOI Cell 16m × 16m，是「谁能看见谁」的单位，纯进程内索引
//
// 两者解耦的原因：Chunk 决定跨进程/跨场景的边界，改动它的成本是一次数据迁移，
// 所以它要粗、要稳定；AOI 格决定广播范围，要随玩法密度调，所以它要细、要可调。
//
// M3.3 之后归属不再是本地 *Scene，而是 mesh.Peer（节点 + 场景号）：
// 一块地可能属于另一个进程，本进程只需要知道"它归谁管"，不需要有它的实体。
package world

import (
	"fmt"
	"math"
	"sort"

	"openworld/internal/mesh"
)

// DefaultChunkSize 是 Chunk 的默认边长。
//
// 512m 的取法：视野 48m 的话，一个 Chunk 覆盖约 10×10 个视野，
// 单 Chunk 人数 ≈ 视野人数 × 大量级，主城一个 Chunk 就是几千人，
// 正好是一个场景线程能扛的量级。再小会导致场景数量爆炸（每个场景一个 goroutine）。
const DefaultChunkSize = 512

// Rect 是世界坐标系里的一块矩形，半开区间 [MinX, MaxX) × [MinZ, MaxZ)。
//
// 半开区间是为了让相邻场景在边界上不重叠也不留缝：
// 场景 A 是 [0,512)，场景 B 是 [512,1024)，x=512 恰好属于 B。
type Rect struct {
	MinX, MinZ, MaxX, MaxZ float32
}

// Valid 判断矩形是否非空。零值矩形表示"没有边界"，即不限范围。
func (r Rect) Valid() bool { return r.MaxX > r.MinX && r.MaxZ > r.MinZ }

// Contains 判断点是否落在矩形内。
func (r Rect) Contains(x, z float32) bool {
	return x >= r.MinX && x < r.MaxX && z >= r.MinZ && z < r.MaxZ
}

func (r Rect) String() string {
	return fmt.Sprintf("[%.0f,%.0f)-[%.0f,%.0f)", r.MinX, r.MinZ, r.MaxX, r.MaxZ)
}

// Overlaps 判断两个矩形是否重叠。
func (r Rect) Overlaps(o Rect) bool {
	return r.MinX < o.MaxX && o.MinX < r.MaxX && r.MinZ < o.MaxZ && o.MinZ < r.MaxZ
}

// Clamp 把点夹进矩形内。留一点点内缩量，避免夹在边界上反复触发迁移：
// 半开区间意味着 x == MaxX 已经属于下一个场景了。
func (r Rect) Clamp(x, z float32) (float32, float32) {
	const inset = 0.01
	if x < r.MinX {
		x = r.MinX
	} else if x >= r.MaxX {
		x = r.MaxX - inset
	}
	if z < r.MinZ {
		z = r.MinZ
	} else if z >= r.MaxZ {
		z = r.MaxZ - inset
	}
	return x, z
}

// Chunk 是拓扑分配单位。
type Chunk struct {
	X, Z int32
}

func (c Chunk) String() string { return fmt.Sprintf("(%d,%d)", c.X, c.Z) }

// ChunkInfo 是单个 Chunk 的归属快照，供控制台的拓扑视图使用。
type ChunkInfo struct {
	Chunk   Chunk  `json:"chunk"`
	SceneID uint32 `json:"sceneId"`
	// Node 是这块地所属的节点。单进程部署时为空。
	Node string `json:"node,omitempty"`
	// Local 表示这块地是否由本进程承载。跨进程部署时，相邻的 Chunk
	// 会落在别的节点上，控制台据此区分"我的"和"别人的"。
	Local bool `json:"local"`
	// Alive 表示这块地的主人现在是否可达。false 的地块在控制台上应该
	// 画成灰色：它还在拓扑里（配置声明过），但走不过去（节点没起或挂了）。
	Alive bool `json:"alive"`
	// Entities 是该场景的实体总数，不是这个 Chunk 的。
	// 按 Chunk 拆分人数需要场景按 Chunk 统计，属于 M6.10 的热力图工作；
	// 这里先给出归属关系，已经足够回答"哪个区服要拆"。
	// 非本地 Chunk 恒为 0：本进程没有它的实体。
	Entities int64 `json:"entities"`
	Load     int64 `json:"load"` // 该场景实体数 / 容量，0 表示未设容量
}

// Topology 维护 Chunk 到场景的映射。
//
// 不是并发安全的：所有写操作经 World 的锁，读操作（Route）也由 World 加读锁调用。
type Topology struct {
	chunkSize float32
	node      string
	owner     map[Chunk]mesh.Peer
	rects     map[uint32]Rect
	peers     map[uint32]mesh.Peer
	scenes    map[uint32]*Scene // 只有本地场景，远端场景本进程没有对象
	// vers 是每块地的归属版本号：只在归属变化时加一。
	//
	// 它是 M3.6 对账的全部依据。没有它，两个节点对同一块地记着不同的
	// 主人时，谁也无法判断"谁更新"——只能靠时间戳或者永远相信对方，
	// 而后者在双向不一致时会来回改写。
	vers map[uint32]uint64
	// alive 记录被明确判死的节点。没有记录的节点默认可达——
	// 这样单进程和静态配置的部署完全不用关心它。
	alive map[string]bool
}

// NewTopology 创建空拓扑。node 是本节点标识，用于判断归属是否本地。
func NewTopology(chunkSize float32, node string) *Topology {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return &Topology{
		chunkSize: chunkSize,
		node:      node,
		owner:     make(map[Chunk]mesh.Peer, 16),
		rects:     make(map[uint32]Rect, 4),
		peers:     make(map[uint32]mesh.Peer, 4),
		scenes:    make(map[uint32]*Scene, 4),
		vers:      make(map[uint32]uint64, 4),
	}
}

// Add 把本地场景登记进拓扑。
//
// rect 必须按 Chunk 对齐。不对齐的话，一个 Chunk 会横跨两个场景，
// Route 只能二选一，选中的场景又不包含那个坐标——玩家会在边界上
// 被反复迁移，每帧一次 handover，看起来像原地抖动。与其容忍，
// 不如在注册时就报错。
func (t *Topology) Add(s *Scene, rect Rect) error {
	if !rect.Valid() {
		return fmt.Errorf("world: scene %d 的边界无效: %s", s.ID(), rect)
	}
	if err := t.register(mesh.Peer{Node: t.node, Scene: s.ID()}, rect); err != nil {
		return err
	}
	t.scenes[s.ID()] = s
	s.bounds = rect
	return nil
}

// AddRemote 登记一块属于其它节点的地。
//
// 为什么要登记别人的地：玩家走到边界时，本进程必须知道"对面是谁"，
// 才能把 Prepare 发过去。如果不登记，Route 会在边界外返回 false，
// 玩家被夹在边界上，表现为"走到边界就过不去"。
func (t *Topology) AddRemote(p mesh.Peer, rect Rect) error {
	if !rect.Valid() {
		return fmt.Errorf("world: remote %s 的边界无效: %s", p, rect)
	}
	return t.register(p, rect)
}

func (t *Topology) register(p mesh.Peer, rect Rect) error {
	if err := t.checkAligned(rect); err != nil {
		return fmt.Errorf("world: %s: %w", p, err)
	}
	for id, other := range t.rects {
		if rect.Overlaps(other) {
			return fmt.Errorf("world: %s 的边界 %s 与 %s 的 %s 重叠",
				p, rect, t.peers[id], other)
		}
	}
	t.bind(p, rect)
	return nil
}

// bind 把 rect 覆盖的 Chunk 指给 p，不做任何校验。
//
// 单独拆出来是给重分区用的：接管一块地时它已经在 owner 表里（写着原主人），
// 再走一遍 register 会被"边界重叠"挡住——而那是**同一块地**的重叠，
// 不是冲突。校验和写入因此必须分开。
func (t *Topology) bind(p mesh.Peer, rect Rect) {
	cs := t.chunkSize
	x0 := int32(math.Floor(float64(rect.MinX / cs)))
	z0 := int32(math.Floor(float64(rect.MinZ / cs)))
	x1 := int32(math.Floor(float64(rect.MaxX/cs))) - 1
	z1 := int32(math.Floor(float64(rect.MaxZ/cs))) - 1
	for cx := x0; cx <= x1; cx++ {
		for cz := z0; cz <= z1; cz++ {
			t.owner[Chunk{X: cx, Z: cz}] = p
		}
	}
	t.rects[p.Scene] = rect
	t.peers[p.Scene] = p
}

func (t *Topology) checkAligned(r Rect) error {
	cs := float64(t.chunkSize)
	for _, v := range []float32{r.MinX, r.MinZ, r.MaxX, r.MaxZ} {
		q := float64(v) / cs
		if math.Abs(q-math.Round(q)) > 1e-4 {
			return fmt.Errorf("边界 %s 未按 Chunk 尺寸 %.0fm 对齐", r, cs)
		}
	}
	return nil
}

// Route 返回坐标所属的场景地址（可能是别的节点）。
//
// 主人不可达的地块在这里就被挡掉：调用方拿到 false，会当成"这里没有场景"，
// 玩家被夹在当前场景边界上。如果放行，跨界那一步会发起一次注定超时的
// 迁移，3 秒后回滚，而玩家在这 3 秒里一直贴着边界重试——比直接过不去更糟。
func (t *Topology) Route(x, z float32) (mesh.Peer, bool) {
	if len(t.owner) == 0 {
		return mesh.Peer{}, false
	}
	c := Chunk{
		X: int32(math.Floor(float64(x / t.chunkSize))),
		Z: int32(math.Floor(float64(z / t.chunkSize))),
	}
	p, ok := t.owner[c]
	if !ok {
		return mesh.Peer{}, false
	}
	if !p.Local(t.node) && !t.nodeAlive(p.Node) {
		return mesh.Peer{}, false
	}
	return p, true
}

// setAlive / nodeAlive 维护节点可达性。未表态的节点默认可达。
func (t *Topology) setAlive(name string, alive bool) {
	if t.alive == nil {
		t.alive = make(map[string]bool, 4)
	}
	t.alive[name] = alive
}

func (t *Topology) nodeAlive(name string) bool {
	if name == "" || name == t.node {
		return true
	}
	v, ok := t.alive[name]
	return !ok || v
}

// PeerOf 返回某个场景的地址（本地或远端）。
func (t *Topology) PeerOf(sceneID uint32) (mesh.Peer, bool) {
	p, ok := t.peers[sceneID]
	return p, ok
}

// relocate 把某个场景的全部 Chunk 改归到 to 节点，边界不变。
//
// 只改归属，不动 rects：地还是那块地，只是换人管。返回被改写的 Chunk 数，
// 0 表示这个场景根本没登记过（配置不一致），调用方据此决定是否报警。
//
// 注意它**不会**给 to 建场景对象：那是接管方自己的事（adopt）。
// 把"改路由"和"建场景"分开，是因为这两件事在三个节点上发生的时间不同——
// 源节点只改路由，目标节点还要多一步把自己的 standby 挂进 scenes。
func (t *Topology) relocate(sceneID uint32, to string, ver uint64) int {
	n := 0
	for c, p := range t.owner {
		if p.Scene == sceneID {
			p.Node = to
			t.owner[c] = p
			n++
		}
	}
	if p, ok := t.peers[sceneID]; ok {
		p.Node = to
		t.peers[sceneID] = p
	}
	if ver > t.vers[sceneID] {
		t.vers[sceneID] = ver
	}
	if to != t.node {
		// 不再是本地场景：本进程没有它的 tick 循环和实体表。
		// rects 保留——Route 仍然需要知道这块地存在，只是走不到。
		delete(t.scenes, sceneID)
	}
	return n
}

// adopt 把一个场景对象接到本节点名下（重分区的接管方用）。
//
// 和 Add 的区别：Add 要求"这是一块新地"，会做边界重叠校验；
// adopt 处理的是"这块地原来归别人，现在归我"——它必然和原来的自己重叠。
// rect 以拓扑里已登记的那份为准：重分区不改边界，只改归属，
// 传进来的 rect 只在这个场景从未登记过时才用得上（接管一个配置里没有的场景）。
//
// 刻意**不**写 s.bounds：那个字段是场景在 Start 之前定的，
// 之后 tick 协程和 /admin/state 都在读，运行期改写就是数据竞争。
// 接管方的场景对象由工厂按同一份 rect 造出来，边界本来就是对的。
func (t *Topology) adopt(s *Scene, rect Rect, ver uint64) {
	if old, ok := t.rects[s.ID()]; ok {
		rect = old
	} else {
		t.bind(mesh.Peer{Node: t.node, Scene: s.ID()}, rect)
	}
	t.relocate(s.ID(), t.node, ver)
	t.scenes[s.ID()] = s
}

// Version 返回一块地的归属版本号。没登记过返回 0。
func (t *Topology) Version(id uint32) uint64 { return t.vers[id] }

// nextVersion 返回下一个版本号：当前版本 +1。
//
// 只在归属要变化时调用。它可以被安全地重复调用（两次调用得到同一个结果，
// 直到有人真的写下去），所以 Prepare 重发不会把版本号推高——
// 推高会让对端看到一个"什么都没变的变更"，白白多做一轮对账。
func (t *Topology) nextVersion(id uint32) uint64 { return t.vers[id] + 1 }

func (t *Topology) setVersion(id uint32, v uint64) {
	if v > t.vers[id] {
		t.vers[id] = v
	}
}

// Entry 返回一块地的归属声明。场景没登记过则 false。
func (t *Topology) Entry(id uint32) (mesh.TopoEntry, bool) {
	p, ok := t.peers[id]
	if !ok {
		return mesh.TopoEntry{}, false
	}
	r := t.rects[id]
	return mesh.TopoEntry{
		Scene: id, Owner: p.Node, Ver: t.vers[id],
		Rect: [4]float32{r.MinX, r.MinZ, r.MaxX, r.MaxZ},
	}, true
}

// Snapshot 返回整份拓扑视图，按场景号排序。
//
// 排序是为了让日志和抓包可读，也让"两份摘要是否相同"能一眼看出来。
func (t *Topology) Snapshot() []mesh.TopoEntry {
	out := make([]mesh.TopoEntry, 0, len(t.peers))
	for id := range t.peers {
		if e, ok := t.Entry(id); ok {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scene < out[j].Scene })
	return out
}

// Rect 返回场景边界。
func (t *Topology) Rect(id uint32) (Rect, bool) {
	r, ok := t.rects[id]
	return r, ok
}

// ChunkSize 返回 Chunk 边长。
func (t *Topology) ChunkSize() float32 { return t.chunkSize }

// hasBounds 表示是否已经有场景登记了边界。
// 没有边界 = 单场景部署，路由应该回落到默认场景。
func (t *Topology) hasBounds() bool { return len(t.rects) > 0 }

// Chunks 返回按坐标排序的 Chunk 归属列表。
func (t *Topology) Chunks() []ChunkInfo {
	out := make([]ChunkInfo, 0, len(t.owner))
	for c, p := range t.owner {
		local := p.Local(t.node)
		info := ChunkInfo{Chunk: c, SceneID: p.Scene, Node: p.Node, Local: local,
			Alive: local || t.nodeAlive(p.Node)}
		if s := t.scenes[p.Scene]; s != nil {
			info.Entities = s.entityCount.Load()
			if cap := s.maxEntities; cap > 0 {
				info.Load = info.Entities * 100 / int64(cap)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chunk.Z != out[j].Chunk.Z {
			return out[i].Chunk.Z < out[j].Chunk.Z
		}
		return out[i].Chunk.X < out[j].Chunk.X
	})
	return out
}
