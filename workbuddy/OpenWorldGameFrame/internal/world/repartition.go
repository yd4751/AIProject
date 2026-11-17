// 世界重分区：把一块地从一个节点搬到另一个节点，玩家跟着走。
//
// M3.4 之后"谁活着"可以热更，但"谁管哪块地"还是写死在配置里。
// 那个缺口有两个具体后果：
//
//	扩容   新节点起来了，但它没有地——玩家进不去，等于白加机器
//	掉线   节点崩了，它的地变成一堵墙——玩家走到边界就被夹住，永远过不去
//
// 这个文件解决的就是这两件事。它刻意分成两条路径，因为它们的**代价不同**：
//
//	搬迁 MoveScene   主人还活着，地上有人。要把每个实体迁过去，代价与人数成正比
//	接管 Reclaim     主人已经死了，地上的实体随进程一起消失。只改拓扑，零迁移
//
// 两条路径共用同一套两阶段提交（Prepare → Activate → Move），区别只在于
// 中间要不要排空实体。接管是搬迁的退化情形——把"要搬 0 个人"想清楚之后，
// 两者就是同一段代码。
//
// 为什么不能让两边各自改自己的拓扑就算完：
//
//	n1 认为 scene3 是自己的，n2 也认为是自己的
//	→ 玩家从 n1 走进去，n1 收下；从 n2 走进去，n2 也收下
//	→ 同一个场景有两个写者，"单写者"这个前提当场失效，实体表会分叉
//
// 所以拓扑切换必须是**一个有先后的动作**，而不是各改各的。顺序是：
// 目标先接（别人还不知道），然后广播，最后源摘。中间那段窗口里
// 源已经没有实体了，收到迁入会拒（draining），所以不会分叉。
package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"openworld/internal/mesh"
)

const (
	// evacBatch 是每帧最多搬多少个实体。
	//
	// 64 × 20Hz = 1280 个/秒。这个上限不是为了省 CPU，而是为了让节点链路
	// 的发送队列（4096）有喘息空间：一帧把 5000 个 Commit 全塞进去，
	// 队列满之后 TrySend 会丢掉一部分，落得个"搬了一半、另一半还在原地"。
	// 限速之后拥塞点会自然暴露成"搬得慢"，而不是"搬丢了"。
	evacBatch = 64
	// prepareTimeout 是等目标建好 standby 的上限。
	prepareTimeout = 5 * time.Second
	// activateTimeout 是等目标把场景接进路由的上限。
	//
	// 比 Prepare 长：目标在这期间要做一次拓扑写入。超时**不中止**，
	// 只是重发——此时实体已经全在对面了，中止等于把所有人弄丢。
	activateTimeout = 10 * time.Second
	// prepareRetries 是 Prepare 阶段的最大重试次数。
	//
	// 只有这个阶段可以干净回滚（一个人都还没搬），所以只有它有次数上限。
	// 超过之后撤销：源场景恢复接客，目标是空的，两边都没有损失。
	prepareRetries = 3
)

// ErrNotLocalScene 表示要搬的场景不在本节点。调用方应该改用接管路径。
var ErrNotLocalScene = errors.New("world: 场景不在本节点")

// evacPhase 是搬迁的阶段。
type evacPhase uint8

const (
	// evacPreparing 等目标建 standby。此时一个实体都没动，可以回滚。
	evacPreparing evacPhase = iota + 1
	// evacDraining 正在往目标搬实体。从这里开始只能往前走。
	evacDraining
	// evacActivating 等目标把场景接进路由。
	evacActivating
)

func (p evacPhase) String() string {
	switch p {
	case evacPreparing:
		return "preparing"
	case evacDraining:
		return "draining"
	case evacActivating:
		return "activating"
	}
	return "unknown"
}

// EvacInfo 是一次搬迁的进度快照。
//
// 单独一个不可变结构而不是直接暴露 evacuation：后者在 tick 协程里被反复
// 改写，而 /admin/state 是 HTTP 协程读的。用一个原子指针指向不可变快照，
// 读写双方都不需要锁，也不会看到"moved 涨了但 phase 还没变"的中间态。
type EvacInfo struct {
	To    string `json:"to"`
	Phase string `json:"phase"`
	Moved int    `json:"moved"`
	Total int    `json:"total"`
}

// evacuation 是源场景侧的搬迁状态机，只在 tick 协程里读写。
type evacuation struct {
	token uint64
	to    string
	// ver 是这次搬迁之后的归属版本号，由 World.MoveScene 定下。
	// 全程不变：Prepare / Activate / Move 广播带的都是它。
	ver      uint64
	phase    evacPhase
	deadline time.Time
	attempts int
	total    int
	moved    int
}

// msgBeginMove 由 World.MoveScene 投递，让搬迁在 tick 协程里发生。
//
// 不在 HTTP 协程里直接改 evac：那个字段被 tick 每一帧读写，
// 外面碰它就是数据竞争，而且搬迁要遍历 entities（无锁的 map）。
type msgBeginMove struct {
	to    string
	token uint64
	ver   uint64
}

// msgMoveReady 是目标对 Prepare 的答复。
type msgMoveReady struct {
	token  uint64
	ok     bool
	reason string
}

// msgMoveActivated 表示目标已经把场景接进路由。
type msgMoveActivated struct {
	token uint64
	ok    bool
}

// --- 源场景侧 ---

// beginEvacuate 开始一次搬迁。只能在 tick 协程里调用。
func (s *Scene) beginEvacuate(to string, token, ver uint64, now time.Time) {
	switch {
	case s.evac != nil:
		s.log.Warn("scene move ignored: already moving", "scene", s.id, "to", s.evac.to)
		return
	case s.world == nil:
		s.log.Error("scene move ignored: scene has no world", "scene", s.id)
		return
	case to == "" || to == s.node:
		s.log.Warn("scene move ignored: bad target", "scene", s.id, "to", to)
		return
	case s.msh == nil:
		s.log.Error("scene move ignored: no mesh", "scene", s.id, "to", to)
		return
	}
	if _, ok := s.world.PeerOf(s.id); !ok {
		s.log.Error("scene move ignored: scene not in topology", "scene", s.id)
		return
	}

	s.evac = &evacuation{
		token:    token,
		to:       to,
		ver:      ver,
		phase:    evacPreparing,
		deadline: now.Add(prepareTimeout),
		total:    int(s.entityCount.Load()),
	}
	// 单独一个原子标志，供对账协程判断"这块地正在被搬，别插手"。
	// 直接读 s.evac 是数据竞争：那个字段由本协程独占。
	s.movingFlag.Store(true)
	// 从这里开始拒收迁入。否则一边往外套人、一边接新人，
	// 搬迁永远结束不了——而且新来的人刚落地就被搬走，白跑两趟。
	s.SetDraining(true)
	s.publishEvac()

	s.log.Info("scene move: prepare", "scene", s.id, "to", to, "ver", ver,
		"entities", s.evac.total, "bounds", s.bounds.String())
	s.sendCtl(mesh.SceneOpPrepare, true, "")
}

// tickEvacuate 推进搬迁状态机。每帧调用，没有搬迁时是空操作。
func (s *Scene) tickEvacuate(now time.Time) {
	if s.evac == nil {
		return
	}
	switch s.evac.phase {
	case evacPreparing:
		if now.Before(s.evac.deadline) {
			return
		}
		s.evac.attempts++
		if s.evac.attempts > prepareRetries {
			s.abortEvacuate("prepare timeout")
			return
		}
		s.log.Warn("scene move: prepare retry", "scene", s.id, "to", s.evac.to,
			"attempt", s.evac.attempts)
		s.evac.deadline = now.Add(prepareTimeout)
		s.sendCtl(mesh.SceneOpPrepare, true, "")

	case evacDraining:
		s.evacuateBatch(now)

	case evacActivating:
		if now.Before(s.evac.deadline) {
			return
		}
		// 只重发，不中止：实体已经在对面了，这时候撤销等于丢掉所有人。
		s.log.Warn("scene move: activate retry", "scene", s.id, "to", s.evac.to)
		s.evac.deadline = now.Add(activateTimeout)
		s.sendCtl(mesh.SceneOpActivate, true, "")
	}
}

// evacuateBatch 把一批实体搬到目标场景。
//
// 顺序是"先发后摘"，不是"先摘后发"：发送可能失败（队列满、链路断），
// 那时实体还完整地留在本场景，下一帧重试即可；反过来就成了——发不出去，
// 人已经从源场景删了，两边都没有。
func (s *Scene) evacuateBatch(now time.Time) {
	dst := mesh.Peer{Node: s.evac.to, Scene: s.id}
	cross := !dst.Local(s.node)
	moved := 0

	for _, e := range s.entities {
		if moved >= evacBatch {
			break
		}
		if e.migrating {
			continue // 正在走常规的边界迁移，下一帧再说
		}
		payload, ok := s.buildEvacPayload(e, cross)
		if !ok {
			continue
		}
		p := payload
		sent := s.send(dst, &msgCommit{token: s.evac.token, payload: p}, mesh.MsgCommit, func() []byte {
			return mesh.EncodeCommit(&mesh.Commit{Token: s.evac.token, To: dst, P: mesh.Payload{
				ID: p.id, Kind: uint8(p.kind), Player: p.player, Client: p.ref,
				X: p.x, Y: p.y, Z: p.z, Speed: p.speed,
				// 只有非玩家实体才算"没有连接"。玩家的 client 字段在跨进程时
				// 被清空过（换成 ref），用它判空会把这个玩家当成 NPC 落地。
				NoClient: p.kind != KindPlayer,
				Blob:     p.blob,
			}})
		})
		if !sent {
			// 链路或邮箱一时消化不了。停这一帧，下一帧继续——
			// 实体没摘、ID 没还，玩家在下一帧之前还是原样活着。
			s.log.Warn("scene move: commit deferred", "scene", s.id, "dst", dst.String())
			break
		}
		s.detach(e)
		s.store.Destroy(e.id)
		s.handoversOut.Add(1)
		moved++
	}

	if moved > 0 {
		s.evac.moved += moved
		s.publishEvac()
	}

	if s.entityCount.Load() > 0 {
		return
	}
	// 清空之后不能立刻认为"到对面了"：最后一个 Commit 还在路上。
	// 目标收到最后一个实体才算真正接管，所以这里等它的 Activated。
	s.evac.phase = evacActivating
	s.evac.deadline = now.Add(activateTimeout)
	s.publishEvac()
	s.log.Info("scene move: drained, activating", "scene", s.id, "to", s.evac.to,
		"moved", s.evac.moved)
	s.sendCtl(mesh.SceneOpActivate, true, "")
}

// buildEvacPayload 打包一个待搬迁的实体。
//
// 和边界迁移的区别只有一处：这里要搬的不只是玩家，还有怪。地的归属变了，
// 地里的东西当然跟着走——把怪留下，玩家过一条街会发现对面是一座空城。
func (s *Scene) buildEvacPayload(e *Entity, cross bool) (transferPayload, bool) {
	var blob []byte
	blob = s.encodeBlob(e)
	p := transferPayload{
		id: e.id, kind: e.kind, player: e.player, client: e.client,
		x: e.x, y: e.y, z: e.z, speed: e.speed,
		lastHeartbeat: e.lastHeartbeat, blob: blob,
	}
	if !cross {
		return p, true
	}

	if loc, ok := e.client.(Locator); ok {
		p.remote = true
		p.ref = loc.Locate()
		p.client = nil
		return p, true
	}
	if e.kind == KindPlayer {
		// 玩家没有可拨号的会话地址，搬过去就是个收不到包的幽灵。
		// 这一步失败不中止整次搬迁：剩下的继续搬，他留在原地等人救。
		s.log.Error("scene move: player has no client address, left behind",
			"scene", s.id, "entity", e.id, "player", e.player)
		s.handoverFails.Add(1)
		return p, false
	}
	// NPC 没有连接：落地侧补一个空实现即可。接口值不跨进程，所以这里清空。
	p.client = nil
	return p, true
}

// onMoveReady 处理目标对 Prepare 的答复。
func (s *Scene) onMoveReady(m *msgMoveReady) {
	if s.evac == nil || s.evac.token != m.token || s.evac.phase != evacPreparing {
		// 上一轮搬迁的迟到答复（token 已经对不上了）。
		return
	}
	if !m.ok {
		s.abortEvacuate("rejected: " + m.reason)
		return
	}
	s.evac.phase = evacDraining
	s.publishEvac()
	s.log.Info("scene move: draining", "scene", s.id, "to", s.evac.to, "entities", s.evac.total)
}

// onMoveActivated 处理"目标已接管"的通知：切换本节点的拓扑并退休。
func (s *Scene) onMoveActivated(m *msgMoveActivated) {
	if s.evac == nil || s.evac.token != m.token {
		return
	}
	if s.evac.phase != evacActivating {
		// 目标提前接了？只可能是消息乱序，忽略即可——Activate 会重发。
		return
	}
	if !m.ok {
		// 实体已经全在对面了，中止等于丢人。只能继续重发 Activate，
		// 把目标那边的错误留在日志里等人处理。
		s.log.Error("scene move: target refused activation, will retry",
			"scene", s.id, "to", s.evac.to)
		s.evac.deadline = time.Time{} // 下一帧立刻重发
		return
	}
	to, moved := s.evac.to, s.evac.moved
	ver := s.evac.ver

	s.log.Info("scene move: activated", "scene", s.id, "to", to, "ver", ver,
		"moved", moved, "left", s.entityCount.Load())
	s.world.applyMove(s.id, to, ver)

	s.evac = nil
	s.movingFlag.Store(false)
	s.evacStat.Store((*EvacInfo)(nil))
	// 场景退休：loop 在下一帧看到它就退出。
	// 不在这里调 Stop——Stop 会等 wg，而 wg 等的就是当前这个协程。
	s.retired = true
	s.log.Info("scene retired", "scene", s.id, "to", to)
}

// abortEvacuate 撤销一次搬迁。只允许在 evacPreparing 阶段调用。
func (s *Scene) abortEvacuate(reason string) {
	if s.evac == nil {
		return
	}
	to := s.evac.to
	s.evac = nil
	s.movingFlag.Store(false)
	s.evacStat.Store((*EvacInfo)(nil))
	s.SetDraining(false)

	s.log.Warn("scene move aborted", "scene", s.id, "to", to, "reason", reason)
	if s.msh != nil {
		m := &mesh.SceneCtl{
			Op: mesh.SceneOpAbort, Scene: s.id, From: s.node, To: to,
			Reason: reason,
		}
		if err := s.msh.Send(to, mesh.MsgSceneCtl, mesh.EncodeSceneCtl(m)); err != nil {
			s.log.Warn("scene move: abort notify failed", "to", to, "err", err)
		}
	}
}

// sendCtl 把一条场景控制消息发给搬迁目标。
func (s *Scene) sendCtl(op mesh.SceneOp, ok bool, reason string) bool {
	if s.msh == nil || s.evac == nil {
		return false
	}
	m := &mesh.SceneCtl{
		Op: op, Scene: s.id, From: s.node, To: s.evac.to,
		Rect:     [4]float32{s.bounds.MinX, s.bounds.MinZ, s.bounds.MaxX, s.bounds.MaxZ},
		Entities: int32(s.evac.total),
		Token:    s.evac.token,
		Ver:      s.evac.ver,
		OK:       ok,
		Reason:   reason,
	}
	if err := s.msh.Send(s.evac.to, mesh.MsgSceneCtl, mesh.EncodeSceneCtl(m)); err != nil {
		s.log.Warn("scene move: send ctl failed", "op", op, "to", s.evac.to, "err", err)
		return false
	}
	return true
}

// publishEvac 把 tick 协程里的状态刷成一份不可变快照，供 HTTP 读。
func (s *Scene) publishEvac() {
	if s.evac == nil {
		s.evacStat.Store((*EvacInfo)(nil))
		return
	}
	info := EvacInfo{
		To: s.evac.to, Phase: s.evac.phase.String(),
		Moved: s.evac.moved, Total: s.evac.total,
	}
	s.evacStat.Store(&info)
}

// --- World 侧 ---

// SceneFactory 按场景定义造一个可运行（但还没 Start）的场景。
//
// 由 cmd/server 注入：World 不知道 tick 间隔、指标注册表和玩法包，
// 而这些在一个**运行期新建**的场景上必须和启动时建的完全一致。
// 让 World 自己猜这些参数，会出现"搬过去的场景没有 AI 系统"这种
// 只在重分区之后才暴露的偏差。
type SceneFactory func(spec SceneSpec) (*Scene, error)

// WithSceneFactory 设置场景工厂。不设表示本进程不支持接管场景。
func WithSceneFactory(fn SceneFactory) WorldOption {
	return func(w *World) { w.factory = fn }
}

// MoveScene 把一块地搬到 to 节点。
//
// 两种情形：
//
//	主人是我   搬迁：排空实体 → 目标接管 → 广播
//	主人已死   接管：直接让目标建场景接管，零迁移（地上已经没人了）
//
// 主人还活着且不是我：拒绝。第三方替一个活着的节点搬地，等于在它背后
// 抽走场景——它那边的实体还在往这块地上迁，两边会同时认为自己是主人。
func (w *World) MoveScene(sceneID uint32, to string) error {
	if to == "" {
		return errors.New("world: 重分区必须指定目标节点")
	}
	p, ver, ok := w.peerAndVersion(sceneID)
	if !ok {
		return fmt.Errorf("world: 场景 %d 不在拓扑里", sceneID)
	}
	token := w.moveSeq.Add(1)

	if p.Local(w.node) {
		if to == w.node {
			return fmt.Errorf("world: 场景 %d 已经在本节点", sceneID)
		}
		s, ok := w.Scene(sceneID)
		if !ok {
			return fmt.Errorf("world: 场景 %d 登记为本地但对象不存在", sceneID)
		}
		if s.Standby() {
			return fmt.Errorf("world: 场景 %d 还在接管中", sceneID)
		}
		if s.Moving() {
			return fmt.Errorf("world: 场景 %d 正在搬迁中", sceneID)
		}
		if !s.post(&msgBeginMove{to: to, token: token, ver: ver}) {
			return fmt.Errorf("world: 场景 %d 邮箱满，搬迁指令被丢弃", sceneID)
		}
		return nil
	}

	if w.NodeAlive(p.Node) {
		return fmt.Errorf("world: 场景 %d 仍在节点 %s 上；活着的主人自己搬，第三方只能接管死节点",
			sceneID, p.Node)
	}
	rect, ok := w.BoundsOf(p)
	if !ok {
		return fmt.Errorf("world: 场景 %d 的边界未登记", sceneID)
	}
	if to == w.node {
		// 自己接管：不需要 Prepare/Activate 两次往返，直接建场景 + 改路由。
		// 但广播还是要发——别的节点还认为这块地是死掉那个节点的。
		go w.selfReclaim(sceneID, rect, p.Node, ver)
		return nil
	}
	// 接管没有需要搬的东西（实体随原进程一起没了），同步流程只剩
	// 两次握手，但它是阻塞的，不能占着 HTTP 协程。
	go w.reclaim(sceneID, to, rect, token, ver)
	return nil
}

// peerAndVersion 一次性返回场景地址和它的下一个归属版本号。
//
// 两件事必须一起做：分开取的话，两次调用之间可能夹进一次别人的重分区，
// 于是我们拿着旧地址配新版本号广播出去——那比不广播更糟，
// 因为它带着"我更新"的证据，对端会照单全收。
func (w *World) peerAndVersion(sceneID uint32) (mesh.Peer, uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.topo.PeerOf(sceneID)
	if !ok {
		if _, local := w.scenes[sceneID]; local {
			p = mesh.Peer{Node: w.node, Scene: sceneID}
			ok = true
		}
	}
	if !ok {
		return mesh.Peer{}, 0, false
	}
	return p, w.topo.nextVersion(sceneID), true
}

// selfReclaim 由本节点自己接管一块无主之地。
func (w *World) selfReclaim(sceneID uint32, rect Rect, from string, ver uint64) {
	s, err := w.prepareStandby(SceneSpec{
		ID: sceneID, Node: w.node,
		Rect: [4]float32{rect.MinX, rect.MinZ, rect.MaxX, rect.MaxZ},
	}, 0)
	if err != nil {
		w.log.Error("reclaim: prepare failed", "scene", sceneID, "err", err)
		return
	}
	w.mu.Lock()
	w.topo.adopt(s, rect, ver)
	w.mu.Unlock()
	s.standbyFlag.Store(false)

	w.broadcastMove(sceneID, w.node, ver)
	w.log.Info("reclaim: adopted", "scene", sceneID, "from", from, "ver", ver,
		"entities", s.entityCount.Load())
}

// reclaim 接管一块无主之地：让目标节点建场景，然后广播归属变更。
func (w *World) reclaim(sceneID uint32, to string, rect Rect, token, ver uint64) {
	log := w.log.With("scene", sceneID, "to", to, "token", token, "ver", ver)
	ch := w.registerPending(token)
	defer w.dropPending(token)

	prepare := &mesh.SceneCtl{
		Op: mesh.SceneOpPrepare, Scene: sceneID, From: w.node, To: to,
		Rect:  [4]float32{rect.MinX, rect.MinZ, rect.MaxX, rect.MaxZ},
		Token: token,
		Ver:   ver,
	}
	var ready *mesh.SceneCtl
	for attempt := 0; attempt < prepareRetries; attempt++ {
		if err := w.sendCtl(to, prepare); err != nil {
			log.Error("reclaim: prepare failed", "err", err)
			return
		}
		m, ok := waitCtl(ch, prepareTimeout)
		if ok {
			ready = m
			break
		}
		log.Warn("reclaim: prepare timeout, retrying", "attempt", attempt+1)
	}
	if ready == nil {
		log.Error("reclaim: prepare gave up")
		return
	}
	if !ready.OK {
		log.Error("reclaim: rejected", "reason", ready.Reason)
		return
	}

	activate := *prepare
	activate.Op = mesh.SceneOpActivate
	if err := w.sendCtl(to, &activate); err != nil {
		log.Error("reclaim: activate failed", "err", err)
		return
	}
	// Activate 不设总超时：目标只要还活着，迟早会把它接进去；
	// 迟迟不回说明链路有问题，那时候报警比回滚有用——回滚会把
	// 一块已经建好的场景丢掉，而它已经没有实体，重来一次代价很低，
	// 但两个节点对"这块地归谁"的认知会不一致。
	for attempt := 0; attempt < prepareRetries; attempt++ {
		act, ok := waitCtl(ch, activateTimeout)
		if ok {
			if !act.OK {
				log.Error("reclaim: activate refused", "reason", act.Reason)
				return
			}
			w.applyMove(sceneID, to, ver)
			log.Info("reclaim: done")
			return
		}
		log.Warn("reclaim: activate timeout, retrying", "attempt", attempt+1)
		if err := w.sendCtl(to, &activate); err != nil {
			log.Error("reclaim: activate retry failed", "err", err)
			return
		}
	}
	log.Error("reclaim: gave up", "attempts", prepareRetries)
}

// applyMove 切换本节点的拓扑并广播给其它节点。
//
// 顺序：先广播，再改自己。反过来的话，广播途中收到别人的询问会得到
// "我已经不是主人了"，而对方还没收到通知——两边都指向空。
// 先广播的窗口里，本节点短暂地"多管一块已经没有人的地"，
// 最坏结果是把一个迁入请求拒掉（draining），比重试超时好。
func (w *World) applyMove(sceneID uint32, to string, ver uint64) {
	w.broadcastMove(sceneID, to, ver)

	w.mu.Lock()
	n := w.topo.relocate(sceneID, to, ver)
	if to != w.node {
		w.detachLocalLocked(sceneID)
	}
	w.mu.Unlock()

	if n == 0 {
		w.log.Error("repartition: scene not registered in topology", "scene", sceneID, "to", to)
		return
	}
	w.log.Info("repartition applied", "scene", sceneID, "to", to, "ver", ver, "chunks", n)
}

// detachLocalLocked 把一块地的方法从本地场景表里摘掉，返回被摘掉的对象。
//
// 只摘不停止：调用方可能就在这个场景的 tick 协程里（搬迁完成时，
// onMoveActivated 由场景自己处理），在那里调 Stop 会等自己退出。
// 搬迁路径靠 s.retired 让 loop 自己收尾，对账路径在协程里 Stop。
// 调用方必须持有 w.mu。
func (w *World) detachLocalLocked(sceneID uint32) *Scene {
	s, ok := w.scenes[sceneID]
	if !ok {
		return nil
	}
	delete(w.scenes, sceneID)
	if old, ok := w.dyn[sceneID]; ok && old == s {
		delete(w.dyn, sceneID)
	}
	if w.def != nil && w.def.ID() == sceneID {
		w.def = w.pickDefaultLocked()
	}
	return s
}

// broadcastMove 把归属变更通知所有已知节点。
//
// 这是一次**尽力而为**的广播：Mesh 的 Send 不保证送达，漏收的节点会保留
// 旧路由，表现为玩家走到那块边界时 Admit 投给了一个没有这个场景的节点，
// 3 秒后超时被夹住。
//
// M3.6 之后它不再是唯一的一致性来源：每条广播带版本号，并且每两秒和
// 所有节点互换一次整份拓扑摘要。广播负责"快"（毫秒级生效），
// 对账负责"最终一定对"（漏了也能在两秒内补回来）。
func (w *World) broadcastMove(sceneID uint32, to string, ver uint64) {
	if w.msh == nil {
		return
	}
	m := &mesh.SceneCtl{Op: mesh.SceneOpMove, Scene: sceneID, From: w.node, To: to, Ver: ver}
	body := mesh.EncodeSceneCtl(m)
	for node := range w.msh.Peers() {
		if node == w.node || node == to {
			continue // 目标自己已经是新主人，不需要这条
		}
		if !w.NodeAlive(node) {
			// 已经判死的节点不发：它收不到，而这一发要拨号，失败路径
			// 实测会卡满 1 秒（dialTimeout）。串行广播里，一个死节点
			// 会把排在它后面的活节点一起延迟——现场就这么发生过：
			// 广播比对账晚了 0.5 秒才发出去。它回来时 discovery 会
			// 触发一次定向摘要，那时候再补。
			continue
		}
		if err := w.msh.Send(node, mesh.MsgSceneCtl, body); err != nil {
			if w.log != nil {
				w.log.Warn("repartition: broadcast failed, topology digest will repair it",
					"scene", sceneID, "to", to, "node", node, "err", err)
			}
		}
	}
}

// sendCtl 给某个节点发一条场景控制消息。
func (w *World) sendCtl(node string, m *mesh.SceneCtl) error {
	if w.msh == nil {
		return errors.New("world: 没有节点间链路")
	}
	return w.msh.Send(node, mesh.MsgSceneCtl, mesh.EncodeSceneCtl(m))
}

// onSceneCtl 分发场景控制消息。
func (w *World) onSceneCtl(m *mesh.SceneCtl) {
	switch m.Op {
	case mesh.SceneOpPrepare:
		w.handlePrepare(m)

	case mesh.SceneOpReady:
		w.deliverCtl(m)

	case mesh.SceneOpActivate:
		w.handleActivate(m)

	case mesh.SceneOpActivated:
		if !w.deliverCtl(m) {
			// 没有等待者，也没有这个场景：可能是源节点已经把场景退休了。
			w.log.Debug("repartition: activated for unknown scene", "scene", m.Scene)
		}

	case mesh.SceneOpMove:
		// 广播的接收方：只改路由，不建场景（地是别人的）。
		w.applyRemoteMove(m)

	case mesh.SceneOpAbort:
		w.handleAbort(m)
	}
}

// deliverCtl 把 Ready/Activated 交给等待者：先看有没有协程在等（接管路径），
// 再落到场景邮箱（搬迁路径）。两条路径靠 token 天然分开——一次重分区
// 只有一个 token，也只有一种等待者。
func (w *World) deliverCtl(m *mesh.SceneCtl) bool {
	w.mu.RLock()
	ch, waiting := w.pending[m.Token]
	w.mu.RUnlock()
	if waiting {
		select {
		case ch <- m:
		default:
			w.log.Warn("repartition: pending channel full", "token", m.Token)
		}
		return true
	}

	if m.To != w.node {
		return false
	}
	s, ok := w.Scene(m.Scene)
	if !ok {
		return false
	}
	if m.Op == mesh.SceneOpReady {
		s.post(&msgMoveReady{token: m.Token, ok: m.OK, reason: m.Reason})
	} else {
		s.post(&msgMoveActivated{token: m.Token, ok: m.OK})
	}
	return true
}

// handlePrepare 在目标节点建一个 standby 场景。
//
// standby 已经能收 Commit（它进了 w.scenes，SceneByPeer 找得到），
// 但还不在路由表里——玩家不会自己走进去，只有正在搬迁的实体会被投进来。
// 这个差别就是"接得住"和"开始接客"的分界，也是两阶段提交的全部内容。
func (w *World) handlePrepare(m *mesh.SceneCtl) {
	if m.To != w.node {
		return
	}
	s, err := w.prepareStandby(SceneSpec{
		ID: m.Scene, Node: w.node, Rect: m.Rect, Capacity: int(m.Capacity),
		Incoming: int(m.Entities),
	}, int(m.Entities))

	reply := &mesh.SceneCtl{
		Op: mesh.SceneOpReady, Scene: m.Scene, From: w.node, To: m.From,
		Token: m.Token, Ver: m.Ver, OK: err == nil,
	}
	if err != nil {
		reply.Reason = err.Error()
		w.log.Warn("repartition: prepare refused", "scene", m.Scene, "from", m.From, "err", err)
	} else {
		w.log.Info("repartition: standby ready", "scene", m.Scene, "from", m.From,
			"entities", m.Entities, "standby", s != nil)
	}
	if err := w.sendCtl(m.From, reply); err != nil {
		w.log.Error("repartition: ready reply failed", "scene", m.Scene, "err", err)
	}
}

// prepareStandby 造一个已经开跑、但不在路由表里的场景。
//
// 必须 Start：Commit 落在 mailbox 里，没有 tick 循环就永远不会被处理，
// 8192 条之后开始丢——而丢弃的那部分正好是"搬了一半"。
func (w *World) prepareStandby(spec SceneSpec, incoming int) (*Scene, error) {
	if w.factory == nil {
		return nil, errors.New("本进程没有配置场景工厂，无法接管场景")
	}
	if s, ok := w.Scene(spec.ID); ok {
		// 已经有了（重发 / 上一轮没撤干净）。已经接进路由的直接报错：
		// 那说明有人想接管一块我正管着的地。
		if !s.Standby() {
			return nil, fmt.Errorf("场景 %d 已由本节点承载", spec.ID)
		}
		return s, nil
	}

	s, err := w.factory(spec)
	if err != nil {
		return nil, err
	}
	if cap := spec.Capacity; cap > 0 && incoming > cap {
		return nil, fmt.Errorf("容量不足：要接 %d 个实体，上限 %d", incoming, cap)
	}
	s.standbyFlag.Store(true)

	if err := s.Start(context.Background()); err != nil {
		return nil, err
	}

	w.mu.Lock()
	if old, ok := w.scenes[spec.ID]; ok {
		w.mu.Unlock()
		return old, nil
	}
	w.scenes[spec.ID] = s
	w.dyn[spec.ID] = s
	w.mu.Unlock()
	return s, nil
}

// handleActivate 把 standby 接进路由。
func (w *World) handleActivate(m *mesh.SceneCtl) {
	if m.To != w.node {
		return
	}
	reply := &mesh.SceneCtl{
		Op: mesh.SceneOpActivated, Scene: m.Scene, From: w.node, To: m.From,
		Token: m.Token, Ver: m.Ver, OK: true,
	}

	w.mu.Lock()
	s, ok := w.scenes[m.Scene]
	if !ok {
		w.mu.Unlock()
		reply.Reason = "no standby scene"
		reply.OK = false
		w.log.Error("repartition: activate for unknown scene", "scene", m.Scene, "from", m.From)
		_ = w.sendCtl(m.From, reply)
		return
	}
	rect := s.bounds
	w.topo.adopt(s, rect, m.Ver)
	w.mu.Unlock()

	s.standbyFlag.Store(false)
	w.log.Info("repartition: scene adopted", "scene", m.Scene, "from", m.From,
		"ver", m.Ver, "entities", s.entityCount.Load(), "bounds", rect.String())

	if err := w.sendCtl(m.From, reply); err != nil {
		w.log.Error("repartition: activated reply failed", "scene", m.Scene, "err", err)
	}
}

// handleAbort 销毁一个还没接过客的 standby。
func (w *World) handleAbort(m *mesh.SceneCtl) {
	if m.To != w.node {
		return
	}
	w.mu.Lock()
	s, ok := w.scenes[m.Scene]
	if !ok || !s.Standby() {
		w.mu.Unlock()
		return
	}
	delete(w.scenes, m.Scene)
	delete(w.dyn, m.Scene)
	w.mu.Unlock()

	if n := s.entityCount.Load(); n > 0 {
		// 已经有人搬进来了，不能再撤。留在原地当 standby：
		// 它收得到 Commit，只是不在路由里，等源节点重发 Activate 即可。
		w.log.Error("repartition: abort refused, standby already holds entities",
			"scene", m.Scene, "entities", n)
		w.mu.Lock()
		w.scenes[m.Scene] = s
		w.dyn[m.Scene] = s
		w.mu.Unlock()
		return
	}
	s.SetDraining(true)
	_ = s.Stop(context.Background())
	w.log.Info("repartition: standby destroyed", "scene", m.Scene, "reason", m.Reason)
}

// pickDefaultLocked 在默认场景被搬走之后选一个新的兜底场景。
// 调用方必须持有 w.mu。
func (w *World) pickDefaultLocked() *Scene {
	var best *Scene
	for _, s := range w.scenes {
		if best == nil || s.ID() < best.ID() {
			best = s
		}
	}
	return best
}

// StopDynamic 停止运行期创建的场景（接管进来的那些）。
//
// 它们在启动时还不存在，所以不在 core.App 的组件表里——
// 没有这一步，进程退出时这些场景的 tick 协程会被一起带走，
// 表现为关服日志里少了几个场景的停止记录。
func (w *World) StopDynamic(ctx context.Context) {
	w.mu.Lock()
	dyn := make([]*Scene, 0, len(w.dyn))
	for _, s := range w.dyn {
		dyn = append(dyn, s)
	}
	w.dyn = make(map[uint32]*Scene)
	w.mu.Unlock()

	for _, s := range dyn {
		s.SetDraining(true)
		_ = s.Stop(ctx)
	}
}

// registerPending / dropPending / waitCtl 是接管路径的等待机制。
//
// 搬迁路径不需要它们：它有场景的 tick 循环当"等待者"。
// 接管没有本地场景，只能起一个协程等消息，于是需要一个 token → channel 的表。
func (w *World) registerPending(token uint64) chan *mesh.SceneCtl {
	ch := make(chan *mesh.SceneCtl, 4)
	w.mu.Lock()
	w.pending[token] = ch
	w.mu.Unlock()
	return ch
}

func (w *World) dropPending(token uint64) {
	w.mu.Lock()
	delete(w.pending, token)
	w.mu.Unlock()
}

func waitCtl(ch <-chan *mesh.SceneCtl, d time.Duration) (*mesh.SceneCtl, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case m := <-ch:
		return m, true
	case <-t.C:
		return nil, false
	}
}
