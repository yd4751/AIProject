package world

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/mesh"
)

// Scheduler 是自动重分区的决策器。
//
// M3.5 把"手动 POST /admin/repartition?scene=&to=" 跑通了：整场景排空实体、
// 改路由、广播版本号。但它要人去判断"谁该搬、搬到哪"——没人盯着的时候，
// 一个节点挂掉被接管之后，剩下的地就一直歪着：n1 拿了两块、n2 一块，
// 直到下一次人为介入。M3.7 让每个节点自己看着拓扑，**只动自己拥有的地**，
// 把过载的那块搬到最空的活节点上。
//
// 为什么是"每节点只搬自己的地"而不是选一个 leader 来调度：
//
//	单写者   一个场景只有一个主人，只有主人能发起排空（MoveScene 的
//	         本地分支）。第三方只能接管死节点，不能搬活节点。
//	无协调   不需要选主、不需要分布式锁。两个节点各自决策各自的场景，
//	         不会撞到同一块地——因为它们的地不重叠。
//	收敛性   决策只在一个方向上"减少最大持有数"，而且加了"搬过去之后
//	         目标不能变成新的最大"的硬条件，所以不会左右横跳。
//
// 不做的事：
//
//	不预测   不根据历史趋势预搬，只看当前快照。预测错了比不搬更糟——
//	         把一块地搬过去、玩家又涌到原来的地，两头空。
//	不拆场景 一个场景就是一个 Chunk（512m），是最小的搬迁单位。
//	         把一个场景劈成两半搬是 M4 之后的事，需要子块路由。
//	不强行均 只要"最大持有 - 平均持有 > slack"才动；能接受 1 个场景的
//	         不均衡（5 个场景 3 个节点，2/2/1 就停，不追求 1.67/1.67/1.67）。
type Scheduler struct {
	w   *World
	log *slog.Logger

	eval     time.Duration // 评估周期
	cooldown time.Duration // 同一场景两次搬迁的最小间隔
	slack    int           // 允许超出"公平份额"几个场景才触发

	// lastMove 记录每个场景最近一次发起搬迁的时间，用于冷却。
	// 调度器是单 goroutine（loop 串行 evaluate），但冷却表还是要锁——
	// Stat() 会从 /admin/state 的 HTTP 协程读。
	mu       sync.Mutex
	lastMove map[uint32]time.Time
	inflight uint32 // 正在搬的场景 id；0 表示空闲

	ctx    context.Context
	cancel context.CancelFunc

	evaluations atomic.Int64
	attempted   atomic.Int64
	succeeded   atomic.Int64
	failed      atomic.Int64
	rejected    atomic.Int64

	snap atomic.Pointer[schedulerSnap]
}

// schedulerSnap 是 Stat() 读的快照，避免锁。
type schedulerSnap struct {
	decision string
	moveAt   time.Time
	scene    uint32
	from     string
	to       string
	nextEval time.Time
	inflight uint32
}

// SchedulerOption 配置调度器。
type SchedulerOption func(*Scheduler)

// WithEvalInterval 设置评估周期。越小反应越快、空转越多。
// 默认 5 秒：搬迁本身 ~200ms，5 秒足够一轮收敛完再评估下一轮。
func WithEvalInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		if d > 0 {
			s.eval = d
		}
	}
}

// WithCooldown 设置同一场景的搬迁冷却。
// 必须大于一次搬迁的耗时（prepare+drain+switch≈300ms）加一个安全裕度，
// 否则上一轮还没落地、下一轮又对同一场景发起 MoveScene，会被 MoveScene
// 自己的 Moving() guard 拒掉——统计上看着像在反复失败。
func WithCooldown(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		if d > 0 {
			s.cooldown = d
		}
	}
}

// WithSlack 设置允许超出公平份额的场景数。
// 0 表示严格按 ceil(total/alive) 来；1 表示多持一个也不动。
// 默认 0：让 5/3 这种情况收敛到 2/2/1 而不是停在 3/1/1。
func WithSlack(n int) SchedulerOption {
	return func(s *Scheduler) {
		if n >= 0 {
			s.slack = n
		}
	}
}

// WithLogger 设置日志。
func WithLogger(l *slog.Logger) SchedulerOption {
	return func(s *Scheduler) {
		if l != nil {
			s.log = l
		}
	}
}

// NewScheduler 创建调度器。不持有 World 之外的任何资源，构造即可用。
func (w *World) NewScheduler(opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
		w:        w,
		log:      slog.Default(),
		eval:     5 * time.Second,
		cooldown: 15 * time.Second,
		slack:    0,
		lastMove: make(map[uint32]time.Time),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Name 是组件名，进 core.App 的组件表。
func (s *Scheduler) Name() string { return "auto-rebalance" }

// Start 启动评估循环。首轮延迟一个 eval 周期——给场景 tick、服务发现、
// 拓扑摘要留出收敛时间：第一秒里 alive 视图还没稳定，贸然评估会基于
// "我以为只有自己活着"把地搬给一个其实在线的节点。
func (s *Scheduler) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.ctx, s.cancel = ctx, cancel
	go s.loop()
	return nil
}

// Stop 取消循环。不等待 in-flight 搬迁：搬迁挂在场景 tick 上，
// 进程退出时场景 tick 自然停；调度器自己的 goroutine 在 ctx.Done 后即返回。
func (s *Scheduler) Stop(context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (s *Scheduler) loop() {
	// 首轮延迟：见 Start 注释。
	timer := time.NewTimer(s.eval)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			s.evaluate()
			timer.Reset(s.eval)
		}
	}
}

// SchedulerStat 是 /admin/state 里看到的调度器状态。
type SchedulerStat struct {
	Enabled       bool   `json:"enabled"`
	Evaluations   int64  `json:"evaluations"`
	Attempted     int64  `json:"attempted"`
	Succeeded     int64  `json:"succeeded"`
	Failed        int64  `json:"failed"`
	Rejected      int64  `json:"rejected"`
	InflightScene uint32 `json:"inflightScene,omitempty"`
	LastDecision  string `json:"lastDecision,omitempty"`
	LastMoveAt    string `json:"lastMoveAt,omitempty"`
	LastScene     uint32 `json:"lastScene,omitempty"`
	LastFrom      string `json:"lastFrom,omitempty"`
	LastTo        string `json:"lastTo,omitempty"`
	NextEvalAt    string `json:"nextEvalAt,omitempty"`
}

// Stat 返回调度器状态快照。
func (s *Scheduler) Stat() SchedulerStat {
	st := SchedulerStat{
		Enabled:     true,
		Evaluations: s.evaluations.Load(),
		Attempted:   s.attempted.Load(),
		Succeeded:   s.succeeded.Load(),
		Failed:      s.failed.Load(),
		Rejected:    s.rejected.Load(),
	}
	if p := s.snap.Load(); p != nil {
		st.InflightScene = p.inflight
		st.LastDecision = p.decision
		if !p.moveAt.IsZero() {
			st.LastMoveAt = p.moveAt.Format(time.RFC3339Nano)
		}
		st.LastScene = p.scene
		st.LastFrom = p.from
		st.LastTo = p.to
		if !p.nextEval.IsZero() {
			st.NextEvalAt = p.nextEval.Format(time.RFC3339Nano)
		}
	}
	return st
}

// record 记一条决策快照。nextEval 用于让控制台知道下一次评估时间。
func (s *Scheduler) record(decision string, scene uint32, from, to string, moveAt time.Time) {
	snap := &schedulerSnap{
		decision: decision,
		scene:    scene,
		from:     from,
		to:       to,
		moveAt:   moveAt,
		nextEval: time.Now().Add(s.eval),
	}
	s.mu.Lock()
	snap.inflight = s.inflight
	s.mu.Unlock()
	s.snap.Store(snap)
}

// evaluate 是单次决策。整个方法是纯函数式的：读拓扑快照 → 算该不该搬 →
// 调一次 MoveScene。没有任何跨调用的中间状态（除了冷却表）。
func (s *Scheduler) evaluate() {
	s.evaluations.Add(1)

	// 先清掉已经落地的 in-flight 标记：MoveScene 返回 nil 后搬迁在 tick 里
	// 跑 ~300ms，到下一次评估（默认 5s）时一定已经结束。这里只把"上次搬的、
	// 现在 Moving() 已转 false"的那个标记清掉，不影响正在搬的。
	s.clearFinishedInflight()

	me := s.w.Node()
	if me == "" {
		s.record("no node id, scheduler idle", 0, "", "", time.Time{})
		return
	}
	alive := s.w.AliveNodes()
	if !alive[me] {
		// 自己不可达时不决策：可能是 discovery 短暂抖动，下一轮再看。
		s.record("self not alive, skip", 0, "", "", time.Time{})
		return
	}

	chunks := s.w.Chunks()
	// 聚合：场景→主人，节点→持有场景数（只数活节点持有的）。
	sceneOwner := make(map[uint32]string, len(chunks))
	sceneRect := make(map[uint32]Rect, len(chunks))
	nodeScenes := make(map[string]int, len(alive))
	aliveCount := 0
	for _, a := range alive {
		if a {
			aliveCount++
		}
	}
	for _, c := range chunks {
		if _, dup := sceneOwner[c.SceneID]; dup {
			continue
		}
		sceneOwner[c.SceneID] = c.Node
		// 边界：本地场景直接从 Scene.Bounds() 拿（最快），
		// 远程场景走 BoundsOf（拓扑表里登记过的矩形）。
		if sc, ok := s.w.Scene(c.SceneID); ok {
			sceneRect[c.SceneID] = sc.Bounds()
		} else if r, ok := s.w.BoundsOf(mesh.Peer{Scene: c.SceneID}); ok {
			sceneRect[c.SceneID] = r
		}
		// 死节点持有的场景不计入"公平份额"——那块地正处在被接管的中途，
		// 数它会让分母虚高，把活节点都判成"低于平均、不用动"。
		if alive[c.Node] {
			nodeScenes[c.Node]++
		}
	}
	totalScenes := len(sceneOwner)
	if aliveCount <= 1 || totalScenes == 0 {
		s.record(fmt.Sprintf("idle: %d scenes, %d alive nodes", totalScenes, aliveCount), 0, "", "", time.Time{})
		return
	}

	// 公平份额：totalScenes 个场景分给 aliveCount 个活节点。
	//   fair    = totalScenes / aliveCount          （向下取整）
	//   ceil    = fair + (余数>0 ? 1 : 0)           （向上取整，少数节点多持一个）
	// 允许多持 slack 个才触发，避免在 2/2/1 这种已经最优的分布上来回微调。
	fair := totalScenes / aliveCount
	allowance := fair
	if totalScenes%aliveCount != 0 {
		allowance = fair + 1
	}
	myScenes := nodeScenes[me]
	if myScenes <= allowance+s.slack {
		s.record(fmt.Sprintf("balanced: own %d/%d scenes, fair=%d, allowance=%d, slack=%d",
			myScenes, totalScenes, fair, allowance, s.slack), 0, "", "", time.Time{})
		return
	}

	// 选目标：活节点里持有场景最少的（不含自己）。
	// 同样少时取名字字典序小的——确定性，避免两个候选之间随机选。
	target := ""
	minHold := -1
	for _, n := range s.w.NodeMembers() {
		if n == me || !alive[n] {
			continue
		}
		hold := nodeScenes[n]
		if minHold < 0 || hold < minHold || (hold == minHold && n < target) {
			minHold = hold
			target = n
		}
	}
	if target == "" {
		s.record("overloaded but no candidate target node", 0, me, "", time.Time{})
		s.rejected.Add(1)
		return
	}
	// 硬条件：搬过去之后目标不能变成新的最大。
	//   target+1 < myScenes  才允许搬
	// 等号都不行——等号意味着把最大从我这儿挪到目标那儿，总数没改善，
	// 白搬一趟还制造一次版本号变更和广播。
	if minHold+1 >= myScenes {
		s.record(fmt.Sprintf("rejected: target %s has %d, move wouldn't reduce max (%d)",
			target, minHold, myScenes), 0, me, target, time.Time{})
		s.rejected.Add(1)
		return
	}

	// 选要搬的场景：候选 = 我持有的、没在搬/没在接管的、过了冷却的。
	type cand struct {
		id   uint32
		ents int64
		rect Rect
	}
	cands := make([]cand, 0, myScenes)
	now := time.Now()
	for id, owner := range sceneOwner {
		if owner != me {
			continue
		}
		sc, ok := s.w.Scene(id)
		if !ok {
			continue
		}
		if sc.Moving() || sc.Standby() {
			continue
		}
		s.mu.Lock()
		if t, has := s.lastMove[id]; has && now.Sub(t) < s.cooldown {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		cands = append(cands, cand{id: id, ents: sc.entityCount.Load(), rect: sc.Bounds()})
	}
	if len(cands) == 0 {
		s.record("overloaded but no movable scene (cooldown/moving/standby)", 0, me, target, time.Time{})
		s.rejected.Add(1)
		return
	}

	// 排序：优先搬到"和目标地块相邻"的场景——这样搬完之后路由表还是
	// 连续的，玩家穿越边界时少一次跨节点跳。相邻相同时按实体数降序
	// （搬走最重的那个最能降压），再按 id 升序（确定性）。
	targetRects := make([]Rect, 0, minHold)
	for id, owner := range sceneOwner {
		if owner == target {
			if r, ok := sceneRect[id]; ok {
				targetRects = append(targetRects, r)
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		ei, ej := adjScore(cands[i].rect, targetRects), adjScore(cands[j].rect, targetRects)
		if ei != ej {
			return ei > ej
		}
		if cands[i].ents != cands[j].ents {
			return cands[i].ents > cands[j].ents
		}
		return cands[i].id < cands[j].id
	})
	pick := cands[0]

	// 占 in-flight 槽：同一节点同时只搬一个场景。
	// 不同场景的搬迁理论上互不依赖（各自 tick 单写者），但一次只搬一个
	// 能让广播/摘要的版本号变更串行化，日志也好看。
	s.mu.Lock()
	if s.inflight != 0 {
		s.mu.Unlock()
		s.record(fmt.Sprintf("inflight scene %d, defer", s.inflight), s.inflight, me, "", time.Time{})
		s.rejected.Add(1)
		return
	}
	s.inflight = pick.id
	s.lastMove[pick.id] = now
	s.mu.Unlock()

	s.attempted.Add(1)
	err := s.w.MoveScene(pick.id, target)
	moveAt := time.Now()
	if err != nil {
		s.failed.Add(1)
		// 失败要回滚 in-flight 和冷却：不然这一轮的失败会阻塞下一轮整整一个冷却周期。
		s.mu.Lock()
		s.inflight = 0
		delete(s.lastMove, pick.id)
		s.mu.Unlock()
		s.record(fmt.Sprintf("move scene %d -> %s failed: %v", pick.id, target, err),
			pick.id, me, target, moveAt)
		s.log.Warn("auto-rebalance move failed", "scene", pick.id,
			"to", target, "ents", pick.ents, "err", err)
		return
	}
	s.succeeded.Add(1)
	s.record(fmt.Sprintf("move scene %d (%d ents) -> %s; own %d->%d, target %d->%d",
		pick.id, pick.ents, target, myScenes, myScenes-1, minHold, minHold+1),
		pick.id, me, target, moveAt)
	s.log.Info("auto-rebalance move", "scene", pick.id, "to", target,
		"ents", pick.ents, "from", myScenes, "fair", allowance)
}

// clearFinishedInflight 把已经落地的 in-flight 标记清掉。
//
// 判定"落地"用两条路：
//
//  1. 这块地已经不属于我了（PeerOf 返回别人）—— 成功搬走。
//  2. 还在我手里，但离发起已超过一个冷却周期 —— 多半是 prepare 失败
//     被 abortEvacuate 回滚了（搬迁只有"搬人之前"能回滚，回滚后场景
//     原地不动，Moving() 转回 false）。
//
// 不用 Moving() 当判据：MoveScene 返回 nil 之后、tick 协程处理 msgBeginMove
// 之前的那个窗口里 Moving() 还是 false，拿它判会把"刚发起的搬迁"误判成
// "已完成"，下一次评估就会再发起一个并行的搬迁。
func (s *Scheduler) clearFinishedInflight() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight == 0 {
		return
	}
	id := s.inflight
	if p, ok := s.w.PeerOf(id); ok && p.Node != s.w.Node() {
		s.inflight = 0
		return
	}
	if t, has := s.lastMove[id]; has && time.Since(t) > s.cooldown {
		s.inflight = 0
	}
}

// adjScore 给一个候选场景相对目标地块集的相邻程度打分。
//
//	2 = 有一条完整边相接（共享一条 X 或 Z 边且区间重叠）
//	1 = 角相接（只在一点相交）
//	0 = 不相邻
//
// 半开区间矩形：MaxX==MinX 视为相接（左闭右开 [MinX,MaxX)）。
func adjScore(r Rect, targets []Rect) int {
	best := 0
	for _, t := range targets {
		// X 方向相接：r 的右边贴 t 的左边，或反之。
		if (r.MaxX == t.MinX || r.MinX == t.MaxX) && overlap(r.MinZ, r.MaxZ, t.MinZ, t.MaxZ) {
			if 2 > best {
				best = 2
			}
			continue
		}
		// Z 方向相接。
		if (r.MaxZ == t.MinZ || r.MinZ == t.MaxZ) && overlap(r.MinX, r.MaxX, t.MinX, t.MaxX) {
			if 2 > best {
				best = 2
			}
			continue
		}
		// 角相接。
		if (r.MaxX == t.MinX || r.MinX == t.MaxX) && (r.MaxZ == t.MinZ || r.MinZ == t.MaxZ) {
			if 1 > best {
				best = 1
			}
		}
	}
	return best
}

// overlap 判断两个半开区间 [a0,a1) 和 [b0,b1) 是否有交集。
func overlap(a0, a1, b0, b1 float32) bool {
	return a0 < b1 && b0 < a1
}
