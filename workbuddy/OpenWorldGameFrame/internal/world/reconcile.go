// 拓扑对账：让"这块地归谁"在所有节点上最终一致。
//
// M3.5 的重分区靠一条广播告知全网。广播会丢：节点链路的队列满时丢当前帧、
// 链路正好断、对端那一刻还没起来。漏收的节点没有任何办法知道自己漏了——
// 它手里的拓扑和别人的长得一样"合理"，只是旧了。后果不是报错，是玩家
// 走到边界时 Admit 投给一个已经没有这个场景的节点，3 秒后超时被夹住，
// 而两边日志都看不出问题在哪。
//
// 解决思路不是"让广播一定送达"（做不到），而是给拓扑加一个可比的东西：
// 每块地一个版本号，归属每变一次加一。然后定期互换整份视图，谁新谁赢。
//
//	广播   快：毫秒级生效，绝大多数情况下一次就够
//	对账   准：漏了也能在两个周期内补回来，不需要人介入
//
// 版本号只在**归属变化**时加一，不是每次广播加一。这一点很重要：
// 如果每次广播都加一，两个节点各自广播就会互相"证明"自己更新，
// 版本号变成噪音，谁也不服谁。它回答的是"这块地换过几次主人"。
package world

import (
	"context"
	"time"

	"openworld/internal/mesh"
)

const (
	// reconcileInterval 是对账周期。
	//
	// 2 秒是"玩家走不到那么远"的量级：漏收的广播最坏情况下要等这么久
	// 才被补上，而这期间受影响的只有"正好从那个方向走到边界"的玩家，
	// 他会被夹住而不是掉线。再短的话，摘要帧会占掉本可以给实体迁移的带宽。
	reconcileInterval = 2 * time.Second

	// pushInterval 是"我比你新"时回推摘要的最小间隔。
	//
	// 没有它会打出乒乓：A 推给 B，B 发现自己确实旧了、改成新的，
	// 但 B 手里还有别的新东西要告诉 A，于是立刻回推……两边每收到一条
	// 就回一条。限流之后最坏也只是每个周期一轮。
	pushInterval = time.Second
)

// verdict 是一条对端声明与本地视图比对之后的结论。
type verdict uint8

const (
	// verdictSame 两边一致，什么也不用做。
	verdictSame verdict = iota
	// verdictSkip 这条不归对账管（正在搬迁/接管），或者缺边界登记不了。
	verdictSkip
	// verdictApplied 采用了对方的声明。
	verdictApplied
	// verdictAhead 本地更新，应该把摘要推回去。
	verdictAhead
	// verdictConflict 版本相同但归属不同：一次广播丢在了路上，需要裁决。
	verdictConflict
)

// TopoStat 是拓扑对账的统计快照，供 /admin/state 展示。
type TopoStat struct {
	// Round 是本节点发出的摘要轮次。
	Round uint64 `json:"round"`
	Sent  int64  `json:"sent"`
	Recv  int64  `json:"recv"`
	// Applied 是被对端纠正的次数，Steady 状态应该是 0。
	Applied int64 `json:"applied"`
	// Pushed 是"我比对方新、把摘要推回去"的次数。
	Pushed int64 `json:"pushed"`
	// Stale 是收到过期声明的次数，正常存在（周期性重发会造成一些）。
	Stale int64 `json:"stale"`
	// Conflicts 是同版本不同归属的次数。非 0 说明丢失过广播，
	// 但已经自动裁决——它的价值是"知道自己曾经不一致过"。
	Conflicts int64 `json:"conflicts"`
	// Retired 是因为归属变了而停掉的本地场景数（重启回来的节点会看到）。
	Retired int64 `json:"retired"`
	// Versions 是每块地的归属版本号。
	Versions map[uint32]uint64 `json:"versions"`
}

// Reconcile 是拓扑对账的主循环，直到 ctx 结束。
//
// 单进程部署（没有链路）直接返回：没有别人可以比。
func (w *World) Reconcile(ctx context.Context) {
	if w.msh == nil {
		return
	}
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()

	// 启动时先发一轮：本节点可能是"回来"的那一个，手里是配置里的
	// 初始版本，而世界在这期间被搬过。等一个周期不算久，但玩家
	// 可能正好在这两秒里走到边界上。
	w.SendTopoDigest(nil)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.SendTopoDigest(nil)
		}
	}
}

// SendTopoDigest 把本方的拓扑视图发给 nodes。
//
// nodes 为空表示周期广播（发给所有活节点），走**增量**：只发"自上次
// 周期广播后版本号变过的场景"，稳态下消息量趋近 0。
// nodes 非空表示定向推送（节点回归、回推旧节点），走**全量**：目标
// 节点可能漏收了很多，必须给整份视图。
//
// 返回成功发出的份数。发送失败不重试：下一个周期还会发，
// 而对账的价值恰恰在于"它总会再来一次"。
func (w *World) SendTopoDigest(nodes []string) int {
	if w.msh == nil {
		return 0
	}
	broadcast := len(nodes) == 0
	var entries []mesh.TopoEntry
	if broadcast {
		entries = w.incrementalDigest()
	} else {
		entries = w.topoSnapshot()
	}

	body := mesh.EncodeTopoDigest(&mesh.TopoDigest{
		From:    w.node,
		Round:   w.topoRound.Add(1),
		Entries: entries,
	})
	if broadcast {
		for n := range w.msh.Peers() {
			// 判死的节点跳过：拨号要等 dialTimeout，而周期性对账
			// 每两秒一轮，为它空等一秒不划算。它回来时 discovery 会
			// 定向发一次（见 discoveryComponent.apply 的 back 分支）。
			if n != w.node && w.NodeAlive(n) {
				nodes = append(nodes, n)
			}
		}
	}
	sent := 0
	for _, n := range nodes {
		if n == w.node || n == "" {
			continue
		}
		if err := w.msh.Send(n, mesh.MsgTopoDigest, body); err != nil {
			w.log.Debug("topology: digest send failed", "node", n, "err", err)
			continue
		}
		sent++
	}
	if sent > 0 {
		w.topoSent.Add(int64(sent))
	}
	return sent
}

// incrementalDigest 返回自上次周期广播后变化过的 Entry，并推进水位。
//
// 增量对账的关键：只发"版本号变过或从未发过"的场景。对端对没提到的
// 场景保持现状（本来就没变），而对账规则"谁版本高谁赢"保证最终一致。
// 稳态下（没有重分区）返回空切片，一次广播只带一个空 body——O(1)。
func (w *World) incrementalDigest() []mesh.TopoEntry {
	w.mu.Lock()
	defer w.mu.Unlock()

	snap := w.topo.Snapshot()
	// 先算变化集，再推进水位。用快照遍历，避免边遍历边改 digestSent。
	out := make([]mesh.TopoEntry, 0, 4)
	for _, e := range snap {
		last, seen := w.digestSent[e.Scene]
		// 从未发过（对端可能根本不知道这块地）或版本变了，才发。
		if !seen || e.Ver != last {
			out = append(out, e)
		}
	}
	// 推进水位。注意：只推进"这次发出去了"的，但这里无法知道最终
	// 有没有成功送达——所以水位是"尝试发送"的记录，不是"已确认"。
	// 这正是"最终一致"的代价：最坏情况下丢一条增量，下一轮因为
	// 水位已推进而不再发，直到下一次变化。但这可接受，因为对账的
	// 兜底是"版本号谁高谁赢"——对端就算漏了一条，它的版本也只会
	// 暂时落后，而定向推送（全量）会在节点回归时补上。
	for _, e := range out {
		w.digestSent[e.Scene] = e.Ver
	}
	return out
}

func (w *World) topoSnapshot() []mesh.TopoEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.topo.Snapshot()
}

// TopoStat 返回对账统计。
func (w *World) TopoStat() TopoStat {
	w.mu.RLock()
	// 按 peers 遍历而不是按 vers：从未搬过的地没有版本记录（默认 0），
	// 但运营看拓扑时想看的是"每块地现在第几版"，而不是"哪些搬过"。
	vers := make(map[uint32]uint64, len(w.topo.peers))
	for id := range w.topo.peers {
		vers[id] = w.topo.vers[id]
	}
	w.mu.RUnlock()
	return TopoStat{
		Round:     w.topoRound.Load(),
		Sent:      w.topoSent.Load(),
		Recv:      w.topoRecv.Load(),
		Applied:   w.topoApplied.Load(),
		Pushed:    w.topoPushed.Load(),
		Stale:     w.topoStale.Load(),
		Conflicts: w.topoConflicts.Load(),
		Retired:   w.topoRetired.Load(),
		Versions:  vers,
	}
}

// onDigest 处理对端发来的整份拓扑视图。
func (w *World) onDigest(m *mesh.TopoDigest) {
	w.topoRecv.Add(1)
	if m.From == w.node {
		return
	}

	ahead := false
	for _, e := range m.Entries {
		v, retired := w.reconcileEntry(e)
		switch v {
		case verdictApplied:
			w.topoApplied.Add(1)
			w.log.Warn("topology: adopted peer view",
				"scene", e.Scene, "owner", e.Owner, "ver", e.Ver, "from", m.From)
		case verdictAhead:
			w.topoStale.Add(1)
			ahead = true
		case verdictConflict:
			w.topoConflicts.Add(1)
			w.log.Error("topology: ownership conflict, resolved by version tiebreak",
				"scene", e.Scene, "peerOwner", e.Owner, "peerVer", e.Ver, "from", m.From)
		}
		if retired != nil {
			w.retireScene(retired)
		}
	}

	if ahead && w.acquirePush(m.From) {
		w.topoPushed.Add(1)
		// 只推给它，不推全网：它显然是旧的那个，别人未必。
		w.SendTopoDigest([]string{m.From})
	}
}

// applyRemoteMove 处理别的节点广播的归属变更。
//
// 和 onDigest 的区别只是入口：广播是"有人刚改完，快跟上"，
// 摘要是"我们对一下账"。两者的比较规则必须是同一个，
// 否则会出现"广播接受、对账拒绝"这种两边都觉得自己对的僵局。
func (w *World) applyRemoteMove(m *mesh.SceneCtl) {
	w.mu.Lock()
	if s, ok := w.scenes[m.Scene]; ok && (s.Standby() || s.Moving()) {
		// 这块地正在本节点搬迁或接管中。归属由搬迁协议决定，
		// 广播这时候插手会把"目标已接、源还没摘"的中间态坐实。
		w.mu.Unlock()
		return
	}
	localVer := w.topo.Version(m.Scene)
	if m.Ver <= localVer {
		w.mu.Unlock()
		w.topoStale.Add(1)
		w.log.Debug("repartition: stale move ignored",
			"scene", m.Scene, "to", m.To, "ver", m.Ver, "local", localVer)
		return
	}
	n := w.topo.relocate(m.Scene, m.To, m.Ver)
	var retired *Scene
	if m.To != w.node {
		retired = w.detachLocalLocked(m.Scene)
	}
	w.mu.Unlock()

	if retired != nil {
		w.retireScene(retired)
	}
	if n == 0 {
		w.log.Warn("repartition: move for unregistered scene", "scene", m.Scene, "to", m.To)
		return
	}
	w.log.Info("repartition: route updated", "scene", m.Scene, "to", m.To, "ver", m.Ver, "chunks", n)
}

// reconcileEntry 把一条对端声明和本地视图比对，必要时修正本地。
//
// 返回结论和"被摘下来的本地场景"（如果有）。后者必须由调用方在**锁外**
// 停止：Stop 要等 tick 协程退出，而 tick 协程随时可能在等 w.mu。
func (w *World) reconcileEntry(e mesh.TopoEntry) (verdict, *Scene) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if s, ok := w.scenes[e.Scene]; ok && (s.Standby() || s.Moving()) {
		return verdictSkip, nil
	}

	cur, known := w.topo.PeerOf(e.Scene)
	localVer := w.topo.Version(e.Scene)

	if !known {
		// 本节点对这块地一无所知：配置漂移，或者它是一块在别处
		// 重分区接管来的地。补登记（含边界），但仍然不建场景——
		// 地是别人的，本进程只需要知道"走过去该找谁"。
		r := Rect{MinX: e.Rect[0], MinZ: e.Rect[1], MaxX: e.Rect[2], MaxZ: e.Rect[3]}
		if !r.Valid() || e.Owner == "" {
			return verdictSkip, nil
		}
		w.topo.bind(mesh.Peer{Node: e.Owner, Scene: e.Scene}, r)
		w.topo.setVersion(e.Scene, e.Ver)
		if e.Owner == w.node {
			// 对端说这块地归我，但我手上没有它。只登记边界，
			// 玩家会被夹在边界上而不是走进一个没人管的坐标。
			return verdictConflict, nil
		}
		return verdictApplied, nil
	}

	switch {
	case e.Ver > localVer:
		n := w.topo.relocate(e.Scene, e.Owner, e.Ver)
		if n == 0 {
			return verdictSkip, nil
		}
		var retired *Scene
		if e.Owner != w.node {
			retired = w.detachLocalLocked(e.Scene)
		}
		return verdictApplied, retired

	case e.Ver < localVer:
		return verdictAhead, nil
	}

	if cur.Node == e.Owner {
		return verdictSame, nil
	}

	// 同版本、不同归属：一次 Move 丢在了路上，或者两边各改各的。
	// 用节点名做确定性裁决——两边都选字典序小的那个，并各自把版本加一。
	// 于是双方**独立**算出同一个结果，不需要再协商一轮，也不会乒乓：
	// 下一轮两条摘要会是 (win, ver+1)，相等，直接 verdictSame。
	win := cur.Node
	if e.Owner < win {
		win = e.Owner
	}
	w.topo.relocate(e.Scene, win, localVer+1)
	var retired *Scene
	if win != w.node {
		retired = w.detachLocalLocked(e.Scene)
	}
	return verdictConflict, retired
}

// msgRetire 让场景在 tick 协程里清空实体并退休。
//
// 为什么不在 retireScene 里直接 Stop：entities 是**无锁** map，只有 tick
// 协程能碰。更麻烦的是 ID——直接停等于把这批实体连同它们的 ID 一起丢掉，
// 而 ID 是全局资源：实测一个重启节点退休 100 只怪，它的 `live` 永远停在
// 100，M3.1 的冷却回收在这条路径上白做。所以清理必须回到单写者线程里做。
type msgRetire struct{ reason string }

// retireNow 清空场景里的所有实体并标记退休。只能在 tick 协程里调用。
func (s *Scene) retireNow(reason string) {
	n := len(s.entities)
	for _, e := range s.entities {
		// removeEntity 会把 ID 还回分配器（外来 ID 则通知所有者），
		// 这条路径和玩家正常下线走的是同一段代码。
		s.removeEntity(e)
	}
	s.retired = true
	s.log.Warn("scene purged", "scene", s.id, "entities", n, "reason", reason)
}

// retireScene 停掉一个已经不是主人的本地场景。
//
// 只有一种情况会走到这里：对账发现归属变了，而本进程还留着一个
// 从配置里建出来的场景（典型场景是节点崩溃后重启——它按配置重建了
// 自己原来的地，而那块地在这期间已经被别人接管）。
//
// 丢掉地上的实体是**正确**的：那些实体是本进程刚造出来的副本，
// 真正活着的那份在对面。留着才是灾难——同一块地两个写者。
func (w *World) retireScene(s *Scene) {
	n := s.entityCount.Load()
	w.log.Error("topology: local scene retired, ownership moved elsewhere",
		"scene", s.ID(), "entities", n)
	s.SetDraining(true)
	w.topoRetired.Add(1)
	if s.post(&msgRetire{reason: "topology: ownership moved"}) {
		// tick 协程清空实体、回收 ID，然后自己退出。
		return
	}
	// 邮箱满：退化成硬停。这条路会漏掉实体 ID，所以要留下痕迹。
	w.log.Error("topology: retire mailbox full, stopping scene the hard way", "scene", s.ID())
	go func() { _ = s.Stop(context.Background()) }()
}

// acquirePush 判断能不能在现在给 node 回推摘要。
func (w *World) acquirePush(node string) bool {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pushAt == nil {
		w.pushAt = make(map[string]time.Time, 4)
	}
	if t, ok := w.pushAt[node]; ok && now.Sub(t) < pushInterval {
		return false
	}
	w.pushAt[node] = now
	return true
}
