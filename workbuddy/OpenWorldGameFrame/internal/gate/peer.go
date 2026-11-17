// 跨进程的会话出口与会话路由。
//
// 玩家连在节点 A 的 Gate 上，实体可以跑到节点 B 的场景里。于是有两件事
// 必须由这条链路兜住：
//
//	remoteClient  实体在 B、连接在 A：B 发的每一个下行包都要回到 A 再写 socket
//	Rebind        实体换了节点，A 那边的会话要改转发方向
//
// 为什么不让连接跟着实体走：TCP 连接不能跨进程搬。要么把玩家踢下线重连
// （体验上是"过个图掉一次线"），要么就在这里做一层代理。所有大型 MMO 的
// gate/game 分离都是后者。
package gate

import (
	"openworld/internal/mesh"
	"openworld/internal/protocol"
	"openworld/internal/world"
)

// remoteClient 是"实体在别的节点"时的 world.Client 实现。
//
// 它只记一个地址，不缓存连接：连接属于 Mesh，Mesh 自己负责重拨。
// 每次 Send 都问一遍 Mesh，换来的是"链路断了自动重连"不用在这里再写一遍。
type remoteClient struct {
	ref mesh.ClientRef
	msh *mesh.Mesh
}

// Send 实现 world.Client：把下行包转回接入节点。
//
// 走 mesh 的 SendClientSend 批量合并路径：每条下行广播不用单独组帧，
// 攒进同一节点的聚合缓冲，由 flushLoop 合并成一条 MsgClientBatch。
func (c *remoteClient) Send(msgID uint16, body []byte) error {
	return c.msh.SendClientSend(c.ref, msgID, body)
}

// Kick 实现 world.Client。
func (c *remoteClient) Kick(code uint8, reason string) error {
	body := (&protocol.Kick{Code: code, Reason: reason}).Encode()
	return c.Send(protocol.MsgS2CKick, body)
}

// Locate 实现 world.Locator：不管实体搬了几次家，连接始终在最初那个节点。
func (c *remoteClient) Locate() mesh.ClientRef { return c.ref }

// RouteTo 实现 world.Router：实体又一次搬家了，通知接入节点改转发方向。
func (c *remoteClient) RouteTo(p mesh.Peer, entity uint64, x, y, z float32) {
	if p.Local(c.ref.Node) {
		// 实体回到了连接所在节点：这条代理可以退场了。
		// 接入节点收到 Rebind 后会用自己的 Session 接管，
		// 之后 B 侧不会再通过这个代理发消息（实体已经不在 B 了）。
	}
	_ = c.msh.Send(c.ref.Node, mesh.MsgRebind, mesh.EncodeRebind(&mesh.Rebind{
		Client: c.ref, To: p, Entity: entity, X: x, Y: y, Z: z,
	}))
}

// ResolveClient 实现 world.ClientResolver：把 (节点, 会话) 还原成出口。
//
// 本地会话直接返回 Session；远端会话返回代理。返回 nil 表示这个玩家
// 已经不在了——调用方（onCommit）会因此拒绝落地，而不是落一个
// 永远收不到包、却一直占着 AOI 格子的实体。
func (g *Gate) ResolveClient(ref mesh.ClientRef) world.Client {
	if ref.Node == g.cfg.Node {
		v, ok := g.sessions.Load(ref.Session)
		if !ok {
			return nil
		}
		return v.(*Session)
	}
	if g.msh == nil {
		return nil
	}
	return &remoteClient{ref: ref, msh: g.msh}
}

// Deliver 实现 mesh.Handler 里属于网关的那一半。
func (g *Gate) Deliver(msgID uint16, body []byte) {
	switch msgID {
	case mesh.MsgClientSend:
		m, ok := mesh.DecodeClientSend(body)
		if !ok {
			g.log.Warn("peer: bad client send frame")
			return
		}
		g.deliverClientSend(m)

	case mesh.MsgClientBatch:
		b, ok := mesh.DecodeClientBatch(body)
		if !ok {
			g.log.Warn("peer: bad client batch frame")
			return
		}
		for i := range b.Items {
			g.deliverClientSend(&b.Items[i])
		}

	case mesh.MsgRebind:
		m, ok := mesh.DecodeRebind(body)
		if !ok {
			g.log.Warn("peer: bad rebind frame")
			return
		}
		v, ok := g.sessions.Load(m.Client.Session)
		if !ok {
			return
		}
		s := v.(*Session)
		s.setRoute(m.To, m.Entity)
		// 本地场景指针必须同时清掉：否则这个会话的 Move 会继续投给
		// 它离开的那个本地场景，用一个已经不属于它的实体 ID。
		if !m.To.Local(g.cfg.Node) {
			s.scene.Store(nil)
		}
		g.log.Info("session rebound", "player", s.playerID, "entity", m.Entity,
			"scene", m.To.String())
	}
}

// deliverClientSend 把一条下行消息写进对应玩家的连接。
func (g *Gate) deliverClientSend(m *mesh.ClientSend) {
	v, ok := g.sessions.Load(m.Client.Session)
	if !ok {
		// 玩家已经断线。丢包即可：实体那侧的清理靠 Leave 和心跳超时，
		// 这里再回一条"发送失败"只会让链路多一条没人处理的错误路径。
		return
	}
	if err := v.(*Session).conn.Send(m.MsgID, m.Body); err != nil {
		g.log.Debug("peer: client send failed", "session", m.Client.Session, "err", err)
	}
}

// sessionRoute 是一个会话当前的路由目标。
//
// 用不可变快照 + 原子指针，而不是给几百万次/秒的 Move 路径加锁：
// 读协程每收一个包都要读一次，写只在迁移落地时发生一次。
type sessionRoute struct {
	node   string
	scene  uint32
	entity uint64
}

// setRoute 更新会话的转发目标。
func (s *Session) setRoute(p mesh.Peer, entity uint64) {
	s.route.Store(&sessionRoute{node: p.Node, scene: p.Scene, entity: entity})
}

// routeOf 返回当前路由快照，没有实体时返回 nil。
func (s *Session) routeOf() *sessionRoute { return s.route.Load() }

// relay 把一条上行消息转给实体所在节点。
func (s *Session) relay(g *Gate, kind mesh.RelayKind, x, y, z float32) {
	rt := s.routeOf()
	if rt == nil || g.msh == nil {
		return
	}
	_ = g.msh.Send(rt.node, mesh.MsgRelay, mesh.EncodeRelay(&mesh.Relay{
		To:     mesh.Peer{Node: rt.node, Scene: rt.scene},
		Entity: rt.entity,
		Kind:   kind,
		X:      x, Y: y, Z: z,
	}))
}

// relayAttack 把攻击指令转给实体所在节点。
func (s *Session) relayAttack(g *Gate, entity, target uint64, skill uint8) {
	rt := s.routeOf()
	if rt == nil || g.msh == nil {
		return
	}
	_ = g.msh.Send(rt.node, mesh.MsgRelay, mesh.EncodeRelay(&mesh.Relay{
		To:     mesh.Peer{Node: rt.node, Scene: rt.scene},
		Entity: entity,
		Kind:   mesh.RelayAttack,
		Target: target,
		Skill:  skill,
	}))
}

// relayQuerySkills 把技能目录查询转给实体所在节点。
func (s *Session) relayQuerySkills(g *Gate, entity uint64) {
	rt := s.routeOf()
	if rt == nil || g.msh == nil {
		return
	}
	_ = g.msh.Send(rt.node, mesh.MsgRelay, mesh.EncodeRelay(&mesh.Relay{
		To:     mesh.Peer{Node: rt.node, Scene: rt.scene},
		Entity: entity,
		Kind:   mesh.RelaySkills,
	}))
}
