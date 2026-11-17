// 实体 ID 分配与回收。
//
// 为什么需要回收：组件池的稀疏数组是 []int32，直接用实体 ID 当下标，
// 长度等于「历史最大 ID」。ID 只增不减的话，一个场景跑一天、玩家进出
// 几十万次之后，每个组件池都会摊着一个几 MB、全零的稀疏数组，
// 而场景里同时只有几千人。这部分内存不是逻辑需要，纯粹是 ID 空间漏了。
//
// 为什么不能立刻复用：客户端在途的包里带的是旧 ID（Move / Heartbeat /
// Leave）。ID 一被复用，这些包就打到了新实体身上——表现是新怪刷出来
// 就"自己走了一步"，或者新玩家进场时血量是上一个人的。
// 所以释放的 ID 先进冷却队列，过了 coolDown 才回到可用列表。
package world

import (
	"sync"
	"time"
)

const (
	// idCoolDown 是 ID 从释放到可再次分配之间的间隔。
	//
	// 取 2 秒的理由：在途包的寿命由网络 RTT 决定，公网 200~400ms，
	// 加上服务端邮箱排队、客户端重发，1 秒已经非常保守。2 秒是留了一倍余量。
	// 代价是峰值之后 ID 空间最多多占「2 秒内的实体周转量」——
	// 2000 只怪每 10 秒刷一批的话，多占 400 个 ID，可以忽略。
	idCoolDown = 2 * time.Second

	// defaultIDLimit 是单个场景允许分配到的最大 ID，即稀疏数组的长度上限。
	//
	// 1<<20 乘以 4 字节 = 4MB/组件池。十来个组件池就是 40MB，
	// 这是能接受的兜底。真实场景远到不了：AOI 调优后单场景约 7000 人，
	// 加上 2 秒冷却窗口里的周转量，ID 高水位稳定在万级。
	// 碰到这个上限说明有泄漏（该释放的没释放），此时拒绝新实体比默默涨内存好——
	// 前者在监控上是一个刺眼的计数器，后者是几小时后的一次 OOM。
	defaultIDLimit = 1 << 20
)

// coolingID 是一个已释放、还在冷却期的 ID。
type coolingID struct {
	id      uint64
	release time.Time
}

// idAlloc 是场景内的实体 ID 分配器。
//
// 为什么要加锁：分配发生在两个 goroutine 上——玩家进场走 Gate 的读协程
// （Enter 需要同步拿到 ID 才能填进会话），NPC 生成在 tick 协程（Spawn）。
// 把分配统一搬进 tick 协程可以让它无锁，但进场就要等一帧（50ms）才能拿到 ID，
// 而 Gate 的读协程会被这次等待阻塞住。分配频率是"实体周转率"级别
// （每秒几十到几千次），一把 mutex 的成本是几十纳秒，不值得为此改变调用时序。
//
// 跨进程（M3.3）后每个节点各有一份分配器，光靠"各自从 1 开始数"必然撞号。
// 解决办法是**步长分区**：id = seq × stride + offset，stride = 节点数，
// offset = 本节点在排序后的节点表里的下标。不同节点的 ID 落在不同的
// 同余类上，物理上不可能相等；单进程时 stride=1、offset=0，
// 分配结果与 M3.2 完全一致。
//
// 为什么不用"高 16 位放节点号"：组件池用实体 ID 直接下标寻址，
// 高位编号会让每个池的稀疏数组至少长到 2^16 × 节点数——
// 一个 3 节点的世界，光是空数组就要几十 MB。步长分区只把水位放大
// stride 倍（3 节点、2000 实体约 6000 个槽），代价可以忽略。
type idAlloc struct {
	mu sync.Mutex

	// seq 是已分配过的序号（从 1 开始）。实际 ID 由 seq 和节点分区算出。
	seq uint64
	// stride / offset 是节点分区参数。单进程为 1/0。
	stride uint64
	offset uint64

	// free 是冷却完毕、可以立刻复用的 ID。用栈（后进先出）：
	// 刚回收的 ID 最近被用过，对应的缓存行还是热的。
	free []uint64
	// cool 是还在冷却期的 ID，按释放时间升序，所以复活只需要扫前缀。
	// coolHead 是"已经复活、但还留在数组里"的前缀长度。
	//
	// 为什么不直接 cool = cool[head:]：切片前移不回收容量，
	// cap 会一路缩到 0，之后每次 append 都要重新分配——
	// 实测这条路径会变成 295ns/op + 32B/op，比新分配还慢。
	// 用一个游标 + 定期原地压缩，才是真正的 O(1) 摊还。
	cool     []coolingID
	coolHead int

	limit    uint64
	coolDown time.Duration

	// 高水位 = 历史最大 ID。稀疏数组的长度就是这个数。
	highWater uint64
	fresh     int64
	reused    int64
	exhausted int64
	foreign   int64
}

func newIDAlloc() *idAlloc { return newIDAllocSized(1, 0) }

// newIDAllocSized 创建带节点分区的分配器。
func newIDAllocSized(stride, offset uint64) *idAlloc {
	if stride == 0 {
		stride = 1
	}
	if offset >= stride {
		offset = 0
	}
	return &idAlloc{
		stride:   stride,
		offset:   offset,
		limit:    defaultIDLimit,
		coolDown: idCoolDown,
	}
}

// alloc 分配一个实体 ID。ID 空间耗尽时返回 0，调用方必须检查。
//
// 先看 free 再看 seq：只要回收队列里有货就绝不推高水位，
// 这是整个机制生效的关键——否则高水位仍会随累计实体数增长，
// 回收就白做了。
func (a *idAlloc) alloc(now time.Time) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.promote(now)

	if n := len(a.free); n > 0 {
		id := a.free[n-1]
		a.free = a.free[:n-1]
		a.reused++
		return id
	}

	// 先算再自增：越界时不能把 seq 推上去，否则之后每次分配都白白撞一次上限。
	// 上限是"最大可用 ID"（含），与 M3.2 的语义保持一致。
	id := (a.seq+1)*a.stride + a.offset
	if id > a.limit {
		a.exhausted++
		return 0
	}
	a.seq++
	if id > a.highWater {
		a.highWater = id
	}
	a.fresh++
	return id
}

// release 把一个不再使用的 ID 放进冷却队列，返回它是否是"外来 ID"。
//
// id == 0 直接忽略：0 是"没有实体"，不该被回收，否则会被当成可分配 ID 发出去。
//
// 不属于本节点分区的 ID（迁移进来的实体在这里死掉）也忽略并计数：
// 塞进自己的 free 列表会让本节点发出一个别的节点的 ID，两边同时存在
// 两个同 ID 实体——那是比"漏一个 ID"严重得多的错误。返回 true 让调用方
// 去通知真正的所有者回收。
func (a *idAlloc) release(id uint64, now time.Time) bool {
	if id == 0 {
		return false
	}
	a.mu.Lock()
	if a.stride > 1 && id%a.stride != a.offset {
		a.foreign++
		a.mu.Unlock()
		return true
	}
	a.cool = append(a.cool, coolingID{id: id, release: now})
	a.mu.Unlock()
	return false
}

// promote 把冷却期满的 ID 移到可用列表。
//
// cool 是按释放时间升序追加的，所以扫到第一个还没到期的就可以停。
// 要求 now 单调递增（time.Now 带单调时钟读数，跨 goroutine 也成立）。
func (a *idAlloc) promote(now time.Time) {
	head := a.coolHead
	for head < len(a.cool) && now.Sub(a.cool[head].release) >= a.coolDown {
		a.free = append(a.free, a.cool[head].id)
		head++
	}
	a.coolHead = head

	// 全部复活：直接归零长度，容量留着复用。这是最常见的分支——
	// 只要有一小段时间没有实体死亡，冷却队列就会被清空。
	if head == len(a.cool) {
		a.cool = a.cool[:0]
		a.coolHead = 0
		return
	}
	// 部分复活：前 head 项是死空间。等死空间占到一半再整体前移，
	// 每次前移的搬运量由后续 append 摊还，长期是 O(1)。
	// 不做这一步的话，cool 会随累计释放次数一直涨（游标只进不退）。
	if head >= 256 && head*2 >= len(a.cool) {
		n := copy(a.cool, a.cool[head:])
		a.cool = a.cool[:n]
		a.coolHead = 0
	}
}

// IDStat 是 ID 空间的快照，供 /admin/state 使用。
//
// HighWater 是最值得盯的一个数：它等于组件池稀疏数组的长度，
// 正常情况下应该稳定在「同时在线的实体数 + 2 秒周转量」附近，
// 持续单向上涨就是有实体没释放。
type IDStat struct {
	HighWater uint64 `json:"highWater"`
	Live      int    `json:"live"`
	Free      int    `json:"free"`
	Cooling   int    `json:"cooling"`
	Fresh     int64  `json:"allocated"`
	Reused    int64  `json:"reused"`
	Exhausted int64  `json:"exhausted"`
	// Foreign 是"别的节点分配、在本节点死掉"的实体数。
	//
	// 它们的 ID 不会被本节点回收（回收会造成两节点同 ID），所以只记数。
	// 这个数持续增长说明本节点在替别的节点收拾尸体——正常，但值得知道。
	Foreign int64 `json:"foreign"`
	// Stride / Offset 是节点分区参数，方便对着一张 ID 反查它是谁发的。
	Stride uint64 `json:"stride"`
	Offset uint64 `json:"offset"`
}

func (a *idAlloc) stat() IDStat {
	a.mu.Lock()
	defer a.mu.Unlock()
	// cool 里前 coolHead 项已经复活过（还在数组里只是没压缩），不算待复用。
	cooling := len(a.cool) - a.coolHead
	live := int(a.seq) - len(a.free) - cooling
	if live < 0 {
		live = 0
	}
	return IDStat{
		HighWater: a.highWater,
		Live:      live,
		Free:      len(a.free),
		Cooling:   cooling,
		Fresh:     a.fresh,
		Reused:    a.reused,
		Exhausted: a.exhausted,
		Foreign:   a.foreign,
		Stride:    a.stride,
		Offset:    a.offset,
	}
}
