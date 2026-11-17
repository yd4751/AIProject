// 世界拓扑配置：哪个场景在哪个节点、边界在哪、节点用哪个 ID 槽位。
//
// 这份配置是**每节点一份、内容一致**的全局视图：本节点只创建属于自己的
// 场景，别人的场景只登记边界。这样玩家走到边界时，两边都知道对面是谁。
//
// 为什么不做成"每个节点只知道自己"：迁移需要目的节点的地址，而地址来自
// 拓扑。如果 A 只知道自己的地，玩家走到边界就过不去了。
//
// M3.4 起职责拆成两半：
//
//	配置（本文件）   期望的世界：谁承载哪块地、每个节点的 ID 槽位
//	服务发现         实际的世界：谁现在活着、它的链路地址是什么
//
// 之所以不把场景归属也交给注册中心：场景搬家要迁移玩家，是个需要
// 计划、观察、回滚的运维动作，不能由一次心跳抖动触发。声明式的拓扑
// 加上动态的成员，才是能安全热更的组合。
package world

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"openworld/internal/mesh"
	"openworld/internal/observability"
)

// SceneSpec 是一个场景的静态定义。
type SceneSpec struct {
	ID   uint32 `json:"id"`
	Node string `json:"node"`
	// Rect 是 [MinX, MinZ, MaxX, MaxZ]，半开区间，必须按 Chunk 对齐。
	Rect [4]float32 `json:"rect"`
	// Capacity 是迁入软上限，0 表示不限。
	Capacity int `json:"capacity,omitempty"`
	// Incoming 是有多少实体正在搬进这个场景，只在重分区时非零。
	//
	// 它不是"配置"，而是运行期的上下文：场景工厂据此决定要不要预先填充
	// 玩法内容。一个正在搬迁的场景会把自己的怪一起带过来，工厂再刷一批
	// 就是双份——实测里一块 169 个实体的地搬过去变成了 269 个。
	// 而接管一块死掉节点的地时地上已经没人了，那边必须重新填充。
	Incoming int `json:"-"`
}

// Config 是整份世界拓扑。
type Config struct {
	ChunkSize float32 `json:"chunkSize"`
	// Nodes 声明世界里的全部节点：节点名 → 节点间链路地址。
	//
	// 有服务发现时地址以注册中心为准，这里的地址是"发现还没起来时的兜底"。
	Nodes map[string]string `json:"nodes"`
	// MaxNodes 是 ID 槽位上限，同时也是 ID 分配器的固定步长。
	//
	// 为什么要预先定一个上限、而不是用"当前节点数"：步长一旦变化，
	// 已经发出去的 ID 在新步长下的同余类会变。举个真实的例子——
	// 步长 2 时 n1 发过 201、n2 发过 203；步长改成 3 之后 n1 的下一个 ID
	// 就是 204、207……看起来没撞，但 n2 在步长 2 下已经用掉的 207
	// 会和 n1 新发的 207 撞在一起，客户端把两个人认成一个。
	// 固定步长就没有这个问题：加节点只占一个新槽位，其余节点的步长不变。
	//
	// 代价是 ID 空间稀疏 stride 倍（槽位 8、1 万实体 → 高水位 8 万，
	// 每组件池 320KB），这个代价换掉一整类"扩容引发的 ID 撞号"，值得。
	MaxNodes int `json:"maxNodes,omitempty"`
	// Slots 固定"节点 → 槽位"，槽位就是 ID 分区里的 offset。
	//
	// 留空则按节点名字典序自动分配（M3.3 的行为）。显式写出来之后，
	// 新增节点会拿到一个**没人用过的槽位**，已有节点的 offset 不变——
	// 这是"加节点不用重启"在 ID 层面的前提。
	Slots  map[string]int `json:"slots,omitempty"`
	Scenes []SceneSpec    `json:"scenes"`
}

// LoadConfig 从文件读取拓扑配置。
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("world: 解析 %s 失败: %w", path, err)
	}
	if c.ChunkSize <= 0 {
		c.ChunkSize = DefaultChunkSize
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate 检查配置自洽：场景 ID 唯一、节点已声明、矩形有效、槽位无冲突。
//
// 边界对齐和重叠交给 Topology 校验——那是唯一权威，在这里重复一遍
// 只会出现"两处规则不一致"的经典问题。
func (c *Config) Validate() error {
	seen := make(map[uint32]bool, len(c.Scenes))
	for _, s := range c.Scenes {
		if s.ID == 0 {
			return fmt.Errorf("world: 场景 ID 不能为 0")
		}
		if seen[s.ID] {
			return fmt.Errorf("world: 场景 ID %d 重复", s.ID)
		}
		seen[s.ID] = true
		if s.Node != "" {
			if _, ok := c.Nodes[s.Node]; !ok {
				return fmt.Errorf("world: 场景 %d 属于未声明的节点 %q", s.ID, s.Node)
			}
		}
		r := Rect{MinX: s.Rect[0], MinZ: s.Rect[1], MaxX: s.Rect[2], MaxZ: s.Rect[3]}
		if !r.Valid() {
			return fmt.Errorf("world: 场景 %d 的边界无效: %s", s.ID, r)
		}
	}
	return c.normalizeSlots()
}

// normalizeSlots 把槽位表补全并校验，结果写回 c。
//
// 未显式配置时退化成"按节点名字典序"，与 M3.3 完全一致——老配置不改也能跑。
func (c *Config) normalizeSlots() error {
	names := make([]string, 0, len(c.Nodes))
	for n := range c.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)

	if c.MaxNodes == 0 {
		c.MaxNodes = len(names)
	}
	if c.MaxNodes < 1 {
		c.MaxNodes = 1
	}
	if len(c.Nodes) > c.MaxNodes {
		return fmt.Errorf("world: 声明了 %d 个节点，超过 maxNodes=%d（改上限会让 ID 步长变化，必须全网重启）",
			len(c.Nodes), c.MaxNodes)
	}

	if len(c.Slots) == 0 {
		c.Slots = make(map[string]int, len(names))
		for i, n := range names {
			c.Slots[n] = i
		}
		return nil
	}

	used := make(map[int]string, len(c.Slots))
	for name, slot := range c.Slots {
		if _, ok := c.Nodes[name]; !ok {
			return fmt.Errorf("world: 槽位表里有未声明的节点 %q", name)
		}
		if slot < 0 || slot >= c.MaxNodes {
			return fmt.Errorf("world: 节点 %q 的槽位 %d 超出 [0,%d)", name, slot, c.MaxNodes)
		}
		if other, dup := used[slot]; dup {
			return fmt.Errorf("world: 节点 %q 与 %q 抢同一个槽位 %d", name, other, slot)
		}
		used[slot] = name
	}
	// 没写槽位的节点：取第一个空槽。这样"只给新节点补一行"也能工作。
	for _, n := range names {
		if _, ok := c.Slots[n]; ok {
			continue
		}
		placed := false
		for i := 0; i < c.MaxNodes; i++ {
			if _, taken := used[i]; !taken {
				c.Slots[n] = i
				used[i] = n
				placed = true
				break
			}
		}
		if !placed {
			return fmt.Errorf("world: 没有空槽位可以分配给节点 %q", n)
		}
	}
	return nil
}

// Local 返回本节点承载的场景，按 ID 排序。
//
// 排序是为了让"新建场景的顺序"稳定：默认场景是第一个建出来的，
// 它承担登录兜底，顺序不稳定会让重启后的兜底场景变来变去。
func (c *Config) Local(node string) []SceneSpec {
	out := make([]SceneSpec, 0, len(c.Scenes))
	for _, s := range c.Scenes {
		if s.Node == node {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Remote 返回属于其它节点的场景，按 ID 排序。
func (c *Config) Remote(node string) []SceneSpec {
	out := make([]SceneSpec, 0, len(c.Scenes))
	for _, s := range c.Scenes {
		if s.Node != node {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Peers 返回除自己以外的节点地址表。
func (c *Config) Peers(node string) map[string]string {
	out := make(map[string]string, len(c.Nodes))
	for n, addr := range c.Nodes {
		if n != node {
			out[n] = addr
		}
	}
	return out
}

// NodeNames 返回排序后的节点名表。
//
// 只用于日志和展示。ID 分区**不要**用它的下标当 offset——
// 加一个节点就会让排在后边的节点 offset 全部平移，已经发出去的 ID
// 会指向错误的所有者。分区一律走 SlotTable。
func (c *Config) NodeNames() []string {
	names := make([]string, 0, len(c.Nodes))
	for n := range c.Nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Partition 返回 ID 空间的节点分区参数 (stride, offset)。
//
//	stride 恒等于 MaxNodes（不随节点增减变化）
//	offset 是本节点的固定槽位
//
// 这两个值一起保证了"加节点不断服"：新节点拿到一个空槽位，
// 已有节点的 (stride, offset) 一个字节都不变，历史 ID 的含义也就不变。
func (c *Config) Partition(node string) (stride, offset uint64) {
	stride = uint64(c.MaxNodes)
	if stride == 0 {
		stride = 1
	}
	if slot, ok := c.Slots[node]; ok {
		return stride, uint64(slot)
	}
	// 节点不在表里（配错）：退化成单分区，宁可撞号也要能起来，
	// 但这一步会在启动日志里以分区参数的形式暴露出来。
	return 1, 0
}

// SlotTable 返回按槽位排列的节点名表，空槽留空串。
//
// 它同时是 ID 的"反查表"（id % stride → 槽位 → 节点）和"世界成员表"
// （非空槽位就是配置声明过的全部节点）。
func (c *Config) SlotTable() []string {
	n := c.MaxNodes
	if n < 1 {
		n = 1
	}
	out := make([]string, n)
	for name, slot := range c.Slots {
		if slot >= 0 && slot < n {
			out[slot] = name
		}
	}
	return out
}

// RectOf 返回场景定义的矩形。
func (s SceneSpec) RectOf() Rect {
	return Rect{MinX: s.Rect[0], MinZ: s.Rect[1], MaxX: s.Rect[2], MaxZ: s.Rect[3]}
}

// PeerOf 返回场景定义对应的全局地址。
func (s SceneSpec) PeerOf() mesh.Peer { return mesh.Peer{Node: s.Node, Scene: s.ID} }

// Apply 按配置建场景：属于自己的建出来，别人的只登记边界。
//
// 返回本地场景列表，顺序与 Config.Local 一致。
func (c *Config) Apply(w *World, node string, tickDur time.Duration,
	m *observability.Metrics, log *slog.Logger, opts ...Option) []*Scene {
	out := make([]*Scene, 0, len(c.Scenes))
	for _, spec := range c.Local(node) {
		all := append([]Option{WithBounds(spec.RectOf())}, opts...)
		if spec.Capacity > 0 {
			all = append(all, WithCapacity(spec.Capacity))
		}
		out = append(out, w.NewScene(spec.ID, tickDur, m, log, all...))
	}
	for _, spec := range c.Remote(node) {
		if err := w.DeclareRemote(spec.PeerOf(), spec.RectOf()); err != nil {
			log.Error("declare remote scene failed", "scene", spec.ID, "node", spec.Node, "err", err)
		}
	}
	return out
}
