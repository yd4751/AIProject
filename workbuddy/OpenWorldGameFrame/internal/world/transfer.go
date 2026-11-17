// 跨场景迁移（Handover）。
//
// 玩家走到场景边界外时，实体要从一个 Scene 搬到另一个 Scene。
// 两个 Scene 各有一个 goroutine，所以这不是"改个指针"，是一次分布式事务：
// 要么实体在 A、要么在 B，绝不能两边都有（重复广播）或者两边都没有（丢人）。
//
// 三阶段：
//
//	Prepare  A 问 B：这个人能过来吗？（B 校验容量 / 是否在排水 / 能否找到会话）
//	Ready    B 说能 → A 摘除实体，给旧视野发 Leave
//	Commit   A 把实体数据发给 B，B 落地并给新视野发 Enter
//
// 为什么必须先问再摘：如果 A 先摘了 B 才说"满了"，A 就得把自己刚删掉的人
// 再塞回去——位置、组件、视野全要重建，而且这期间玩家是"消失"的。
// 先问一句的成本是一个消息，换掉一整类回滚。
//
// M3.3 之后 B 可能在另一个进程里。差别只落在两处：
//
//	投递    本地进 mailbox（零拷贝），远端编码后走 mesh
//	客户端  实体在 B、玩家的 TCP 在 A，落地时把 client 换成"写回 A 的代理"
//
// 三阶段的状态机本身不变——这正是当初把消息设计成"带 token 的幂等指令"
// 而不是"两场景直接调用"的回报。
package world

import (
	"time"

	"openworld/internal/aoi"
	"openworld/internal/mesh"
	"openworld/internal/protocol"
)

// handoverTimeout 是等对方回 Prepare 结果的上限。
//
// 3 秒的依据：正常情况对方在一个 tick（50ms）内一定回复。
// 超过 3 秒只能是对方卡死或者消息丢了，这时候必须把人留在原地——
// 让玩家卡在边界上，比让他在两个场景之间蒸发要好。
const handoverTimeout = 3 * time.Second

// TransferCodec 把一个实体的玩法数据打包成可跨场景传输的字节。
//
// 为什么不让每个组件自己注册编解码：那样框架就得管注册顺序、版本号、
// 缺组件时的默认值，很快就变成一个自制的序列化框架。玩法层本来就知道
// 自己要带什么，让它写一组 Encode/Decode 最省事，跨版本兼容也由它自己决定。
//
// 实现方约定：Encode 只在 tick 协程内被调用，可以安全读组件；
// Decode 拿到的 Entity 已经挂好了进场钩子产生的默认组件，直接覆盖即可。
//
// 跨进程时 blob 会进网络，编码方要自己控制大小——它没有压缩，
// 也没有版本协商，改格式等于要求全网同时升级。
type TransferCodec interface {
	Encode(ctx *Ctx, e *Entity) []byte
	Decode(ctx *Ctx, e *Entity, data []byte)
}

// transferPayload 是随迁移一起搬走的实体状态。
//
// 只带"重建一个实体必需的东西"，不带任何指向源场景的引用
// （连 *Entity 都不带）：源场景那边的实体在迁移完成后就销毁了，
// 带过去一个悬空指针，等于把 use-after-free 藏进跨场景调用里。
type transferPayload struct {
	id            uint64
	kind          Kind
	player        string
	client        Client
	ref           mesh.ClientRef
	remote        bool // true 表示 client 要用 ref 现场解析，而不是直接带走
	x, y, z       float32
	speed         float32
	lastHeartbeat time.Time
	blob          []byte
}

type msgAdmit struct {
	token  uint64
	from   mesh.Peer
	size   int32
	client mesh.ClientRef
}

type msgAdmitAck struct {
	token  uint64
	ok     bool
	reason string
}

type msgCommit struct {
	token   uint64
	payload transferPayload
}

// pendingTransfer 是源场景侧的迁移状态机。
type pendingTransfer struct {
	e        *Entity
	dst      mesh.Peer
	token    uint64
	tx, tz   float32 // 目标坐标，仅用于日志
	deadline time.Time
}

// send 把一条迁移消息投给目标场景：本地直接进 mailbox，远端编码后走 mesh。
//
// encode 只在真的要走网络时才调用：进程内迁移不该为"可能跨进程"付一次
// 序列化的钱——热路径上每次边界穿越都 malloc 一块 body，很快就变成 GC 压力。
func (s *Scene) send(p mesh.Peer, local any, msgID uint16, encode func() []byte) bool {
	if s.sameProcess(p) {
		dst, ok := s.world.SceneByPeer(p)
		if !ok {
			return false
		}
		return dst.post(local)
	}
	if s.msh == nil {
		s.log.Error("cross-node send without mesh", "scene", s.id, "dst", p.String())
		return false
	}
	if err := s.msh.Send(p.Node, msgID, encode()); err != nil {
		s.log.Warn("cross-node send failed", "scene", s.id, "dst", p.String(), "err", err)
		return false
	}
	return true
}

// tryHandover 尝试把实体迁到 (x, z) 所属的场景。
//
// 返回 true 表示迁移已发起，调用方不要再把这次位移落到本地实体上。
func (s *Scene) tryHandover(e *Entity, x, z float32, now time.Time) bool {
	if s.world == nil || e.kind != KindPlayer {
		return false
	}
	dst, ok := s.world.PeerAt(x, z)
	if !ok || s.peerIsLocal(dst) {
		return false
	}
	// 路由是按 Chunk 算的，Chunk 是场景边界的子集，所以正常情况下
	// 目标场景一定包含这个点。不包含只可能是拓扑没按 Chunk 对齐，
	// 这时候宁可把人夹在边界上，也不要发起一次注定反复的迁移。
	bounds, hasBounds := s.world.BoundsOf(dst)
	if hasBounds && !bounds.Contains(x, z) {
		s.log.Warn("handover target does not contain position, clamped",
			"scene", s.id, "dst", dst.String(), "x", x, "z", z)
		return false
	}
	if _, busy := s.transfers[e.id]; busy {
		return false
	}

	// 跨进程迁移必须先确认"这个人能被对方写回"：落地节点要靠 ClientRef
	// 找到玩家的连接，而 ClientRef 只有实现 Locator 的 Client 才给得出来。
	// 在这里拒绝，比让他落地成"收不到任何包"的实体要好。
	cross := !s.sameProcess(dst)
	var ref mesh.ClientRef
	if cross {
		if s.msh == nil {
			s.log.Error("cross-node handover without mesh", "scene", s.id, "dst", dst.String())
			s.handoverFails.Add(1)
			return false
		}
		loc, ok := e.client.(Locator)
		if !ok {
			s.log.Error("cross-node handover: client has no address",
				"scene", s.id, "entity", e.id, "dst", dst.String())
			s.handoverFails.Add(1)
			return false
		}
		ref = loc.Locate()
	}

	s.transferSeq++
	tok := s.transferSeq
	admit := &msgAdmit{token: tok, from: s.Peer(), size: int32(s.entityCount.Load()), client: ref}
	okSend := s.send(dst, admit, mesh.MsgAdmit, func() []byte {
		return mesh.EncodeAdmit(&mesh.Admit{Token: tok, From: admit.from, To: dst, Size: admit.size, Client: ref})
	})
	if !okSend {
		// 对方邮箱满 / 链路断开。极罕见，且下一帧玩家继续往界外走会重试，
		// 所以这里不排队、不阻塞，直接放弃这一次。
		s.handoverFails.Add(1)
		return false
	}

	s.addTransfer(&pendingTransfer{
		e: e, dst: dst, token: tok, tx: x, tz: z,
		deadline: now.Add(handoverTimeout),
	})
	e.migrating = true
	return true
}

// onAdmit 是目标场景处理 Prepare。
func (s *Scene) onAdmit(m *msgAdmit) {
	ok, reason := true, ""
	// 排水中的场景不再接收任何实体：它马上就要关了，
	// 收进来的每一个都要在关服时再赶出去。
	if s.draining() {
		ok, reason = false, "draining"
	} else if s.maxEntities > 0 && int(s.entityCount.Load()) >= s.maxEntities {
		ok, reason = false, "full"
	} else if !m.from.Local(s.node) && s.resolve == nil {
		// 跨节点进来的人要靠 resolver 找到写消息的出口。没有它，
		// 实体落地即"失联"——不如现在拒绝，让源场景把人留在原地。
		ok, reason = false, "no client resolver"
	}
	if !ok {
		s.handoverFails.Add(1)
	}

	ack := &msgAdmitAck{token: m.token, ok: ok, reason: reason}
	sent := s.send(m.from, ack, mesh.MsgAdmitAck, func() []byte {
		return mesh.EncodeAdmitAck(&mesh.AdmitAck{Token: m.token, To: m.from, OK: ok, Reason: reason})
	})
	if !sent {
		s.log.Error("handover ack dropped", "scene", s.id, "src", m.from.String(), "token", m.token)
	}
	// 这里不记 handoversIn：Prepare 只是"答应了"，还没落地。
	// 计数放在 onCommit，否则一次迁移会让迁入数加两次。
}

// onAdmitAck 是源场景处理 Prepare 的结果。
func (s *Scene) onAdmitAck(m *msgAdmitAck) {
	t, ok := s.transfersByToken(m.token)
	if !ok {
		// 超时之后才到的迟到回复。实体已经回滚了，忽略即可。
		return
	}
	s.dropTransfer(t.e.id)

	if !m.ok {
		s.abortTransfer(t, "rejected: "+m.reason)
		return
	}

	// Commit：先把玩法数据编码出来，再销毁组件。
	// 顺序不能反——离场钩子和 Encode 都可能还要读组件。
	e := t.e
	blob := s.encodeBlob(e)
	// 位置用"提交这一刻"的，而不是发起迁移时记下的 t.tx/t.tz：
	// Prepare 期间实体仍在源场景里正常移动，用旧坐标会把它拉回到
	// 100ms 之前的位置，客户端看到一次回跳。
	payload := transferPayload{
		id: e.id, kind: e.kind, player: e.player, client: e.client,
		x: e.x, y: e.y, z: e.z, speed: e.speed,
		lastHeartbeat: e.lastHeartbeat, blob: blob,
	}
	cross := !s.sameProcess(t.dst)
	if cross {
		// 跨进程时只带地址，不带对象：接口值过不了网络，源进程的
		// Session 指针在目的进程里也没有意义。
		loc, ok := e.client.(Locator)
		if !ok {
			s.log.Error("handover commit: client has no address", "entity", e.id)
			s.handoverFails.Add(1)
			t.e.migrating = false
			s.placeInBounds(t.e)
			return
		}
		payload.remote = true
		payload.ref = loc.Locate()
		payload.client = nil
	}
	e.migrating = false

	// 告诉这个玩家：你原来能看见的人都不在了。
	// 不做这一步，客户端会保留一屏"幽灵"，直到它自己走出视野。
	leaveBody := func(id uint64) []byte {
		return (&protocol.EntityLeave{EntityID: id}).Encode()
	}
	s.grid.ForEachNear(e.cell, func(id uint64, o *Entity) {
		if id == e.id || o.kind != KindPlayer {
			return
		}
		_ = e.client.Send(protocol.MsgS2CEntityLeave, leaveBody(id))
	})

	s.detach(e)
	s.store.Destroy(e.id)

	sent := s.send(t.dst, &msgCommit{token: t.token, payload: payload}, mesh.MsgCommit, func() []byte {
		return mesh.EncodeCommit(&mesh.Commit{Token: t.token, To: t.dst, P: mesh.Payload{
			ID: payload.id, Kind: uint8(payload.kind), Player: payload.player,
			Client: payload.ref, X: payload.x, Y: payload.y, Z: payload.z,
			Speed: payload.speed, Blob: payload.blob,
		}})
	})
	if !sent {
		// 走到这里说明对方的邮箱在 Prepare 之后被灌满了，或者链路刚断。
		// 实体已经从源场景摘掉，但还没到目的场景——只能记为一次失败迁移并
		// 立刻请求重新落地：把实体按原样重建回源场景，玩家看到的是一次
		// 短暂的消失，而不是永久丢人。
		s.log.Error("handover commit dropped",
			"scene", s.id, "dst", t.dst.String(), "entity", e.id, "cross", cross)
		s.handoverFails.Add(1)
		s.rebuild(e, payload)
		return
	}
	s.handoversOut.Add(1)
}

// onCommit 是目标场景落地实体。
func (s *Scene) onCommit(m *msgCommit) {
	s.land(&m.payload)
}

// land 是落地的唯一实现，本地迁移和跨进程迁移共用。
//
// 两条路径的区别只在 client 从哪来：本地直接带着对象，跨进程用 ClientRef
// 现场解析。其余（查重、进场钩子、解码、视野、切路由）完全一致——
// 分开写两遍会出现"本地加了新钩子、跨进程忘了加"这种只在生产才暴露的偏差。
func (s *Scene) land(p *transferPayload) {
	if _, dup := s.entities[p.id]; dup {
		// ID 是全局唯一的（分配器在 World 上），正常绝不会撞。
		// 真撞了说明有人绕过分配器手写了 ID，直接拒绝比覆盖安全。
		s.log.Error("handover commit: entity id already exists", "scene", s.id, "entity", p.id)
		s.handoverFails.Add(1)
		return
	}

	client := p.client
	if p.remote {
		client = s.resolveRef(p.ref)
		if client == nil {
			// 准备阶段已经校验过 resolver 存在，走到这里说明会话已经断了
			// （玩家在迁移的两帧之间掉线）。不落地是正确选择：落一个没有
			// 出口的实体会一直占着 AOI 格子，直到心跳超时才被清掉。
			s.log.Warn("handover commit: client gone, entity dropped",
				"scene", s.id, "entity", p.id, "client", p.ref.String())
			s.handoverFails.Add(1)
			return
		}
	}
	if client == nil {
		// 整场景搬迁（M3.5）会把怪一起带走，而 NPC 没有连接——
		// 接口值过不了网络，落地侧补一个空实现，否则后面每次
		// e.client.Send 都会空指针。
		client = npcClient
	}

	e := &Entity{
		id: p.id, kind: p.kind, player: p.player, client: client,
		x: p.x, y: p.y, z: p.z, speed: p.speed,
		// 心跳时间重置为本帧：否则跨场景后第一次 reapIdle 就会把这个
		// 刚落地、还没来得及发心跳的玩家判成挂机。
		lastHeartbeat: s.clock(),
		lastMoveAt:    s.clock(),
	}
	s.insert(e)

	// 进场钩子先跑（它负责挂默认组件），codec 再覆盖成迁移过来的值。
	s.fireEnter(e)
	if len(p.blob) > 0 {
		s.decodeBlob(e, p.blob)
	}

	// 新视野：把周围的人推给玩家，也把自己推给周围的人。
	self := (&protocol.EntityEnter{EntityID: e.id, X: e.x, Y: e.y, Z: e.z}).Encode()
	s.grid.ForEachNear(e.cell, func(id uint64, o *Entity) {
		if id == e.id || o.kind != KindPlayer {
			return
		}
		_ = e.client.Send(protocol.MsgS2CEntityEnter, (&protocol.EntityEnter{
			EntityID: o.id, X: o.x, Y: o.y, Z: o.z,
		}).Encode())
		_ = o.client.Send(protocol.MsgS2CEntityEnter, self)
	})

	// 复用 EnterSceneResp 告知客户端场景变了。
	// 不新加一条消息：客户端对 EnterSceneResp 的处理本来就是
	// "清空本地实体表、按 resp 里的坐标重建"，正是迁移后要的效果。
	_ = e.client.Send(protocol.MsgS2CEnterScene, (&protocol.EnterSceneResp{
		Code: protocol.CodeOK, EntityID: e.id, SceneID: s.id, X: e.x, Y: e.y, Z: e.z,
	}).Encode())

	// 切换路由。本地：直接改会话的场景指针；跨进程：代理发 Rebind 回接入节点。
	// 这是"最后一步"，因为早切的话，客户端在实体还没落地时发来的 Move
	// 会打到一个不存在的实体上；晚切一帧只是丢一两个包的位移。
	if sw, ok := e.client.(SceneSwitcher); ok {
		sw.SetScene(s)
	}
	if r, ok := e.client.(Router); ok {
		r.RouteTo(s.Peer(), e.id, e.x, e.y, e.z)
	}
	s.handoversIn.Add(1)
}

// resolveRef 把客户端地址还原成可写消息的 Client。
func (s *Scene) resolveRef(ref mesh.ClientRef) Client {
	if s.resolve != nil {
		return s.resolve(ref)
	}
	if s.world != nil {
		return s.world.ResolveClient(ref)
	}
	return nil
}

// abortTransfer 回滚一次迁移：实体留在源场景，位置夹回边界内。
func (s *Scene) abortTransfer(t *pendingTransfer, reason string) {
	s.handoverFails.Add(1)
	e := t.e
	e.migrating = false
	s.placeInBounds(e)
	s.log.Warn("handover aborted", "scene", s.id, "dst", t.dst.String(),
		"entity", e.id, "reason", reason)
}

// rebuild 在 commit 消息投递失败时，把实体按原样重建回本场景。
//
// 这是唯一一条"两边都没有"的路径，所以单独处理：组件数据还在 payload 里，
// 重建出来的实体和迁移前一模一样，玩家只是被瞬移回了原位置。
func (s *Scene) rebuild(e *Entity, p transferPayload) {
	e.migrating = false
	s.placeInBounds(e)
	if len(p.blob) > 0 {
		s.store.Destroy(e.id)
		s.fireEnter(e)
		s.decodeBlob(e, p.blob)
	}
}

// placeInBounds 把实体夹回场景边界内并更新空间索引。
func (s *Scene) placeInBounds(e *Entity) {
	if !s.bounds.Valid() {
		return
	}
	x, z := s.bounds.Clamp(e.x, e.z)
	if x == e.x && z == e.z {
		return
	}
	oldCell := e.cell
	e.x, e.z = x, z
	newCell, moved := s.grid.Move(e.id, e, oldCell, aoi.Pos{X: x, Z: z})
	e.cell = newCell
	if moved {
		s.refreshView(e, oldCell, newCell)
	}
}

// sweepTransfers 清理超时的迁移。
func (s *Scene) sweepTransfers(now time.Time) {
	if len(s.transfers) == 0 {
		return
	}
	for id, t := range s.transfers {
		if now.Before(t.deadline) {
			continue
		}
		s.dropTransfer(id)
		s.abortTransfer(t, "timeout")
	}
}

// addTransfer / dropTransfer 是 transfers 的唯一写入口。
//
// 除了维护 map，还要维护 migratingCount：/admin/state 是 HTTP 协程读的，
// 直接读 len(map) 就是数据竞争（哪怕它"通常"能读对）。
func (s *Scene) addTransfer(t *pendingTransfer) {
	s.transfers[t.e.id] = t
	s.migratingCount.Add(1)
}

func (s *Scene) dropTransfer(id uint64) {
	if _, ok := s.transfers[id]; ok {
		delete(s.transfers, id)
		s.migratingCount.Add(-1)
	}
}

func (s *Scene) transfersByToken(token uint64) (*pendingTransfer, bool) {
	for _, t := range s.transfers {
		if t.token == token {
			return t, true
		}
	}
	return nil, false
}

// detach 把实体从场景摘除，但不回收 ID、不销毁组件。
//
// 和 removeEntity 的区别只有这一点，但意义重大：迁移出去的实体在目的
// 场景用的是同一个 ID，源场景一旦回收，这个 ID 就可能被发给别人，
// 而那个"别人"和刚迁移走的玩家会在客户端眼里撞号。
func (s *Scene) detach(e *Entity) {
	s.fireLeave(e)

	body := (&protocol.EntityLeave{EntityID: e.id}).Encode()
	s.BroadcastNear(e, protocol.MsgS2CEntityLeave, body)

	s.grid.Remove(e.id, e.cell)
	delete(s.entities, e.id)
	s.entityCount.Add(-1)
}
