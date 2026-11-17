// 节点间消息的接收端：把字节翻译成场景指令。
//
// 这一层刻意很薄——解码、找到本地场景、投进它的 mailbox，然后返回。
// 任何"顺手处理一下"的逻辑都不要加在这里：它跑在链路的读协程上，
// 一旦卡住，整条节点链路的所有消息都堵住。
package world

import (
	"time"

	"openworld/internal/mesh"
)

// Deliver 实现 mesh.Handler。
func (w *World) Deliver(msgID uint16, body []byte) {
	switch msgID {
	case mesh.MsgAdmit:
		m, ok := mesh.DecodeAdmit(body)
		if !ok {
			w.log.Warn("peer: bad admit frame")
			return
		}
		s, ok := w.SceneByPeer(m.To)
		if !ok {
			// 目标场景不在本节点：说明两边的拓扑配置不一致。
			// 不要回 Ack——回了对方会以为"这里能收"，然后 Commit 又落空。
			w.log.Error("peer: admit for unknown scene", "dst", m.To.String())
			return
		}
		s.post(&msgAdmit{token: m.Token, from: m.From, size: m.Size, client: m.Client})

	case mesh.MsgAdmitAck:
		m, ok := mesh.DecodeAdmitAck(body)
		if !ok {
			w.log.Warn("peer: bad admit ack frame")
			return
		}
		s, ok := w.SceneByPeer(m.To)
		if !ok {
			w.log.Error("peer: admit ack for unknown scene", "dst", m.To.String())
			return
		}
		s.post(&msgAdmitAck{token: m.Token, ok: m.OK, reason: m.Reason})

	case mesh.MsgCommit:
		m, ok := mesh.DecodeCommit(body)
		if !ok {
			w.log.Warn("peer: bad commit frame")
			return
		}
		s, ok := w.SceneByPeer(m.To)
		if !ok {
			w.log.Error("peer: commit for unknown scene", "dst", m.To.String())
			return
		}
		s.post(&msgCommit{token: m.Token, payload: transferPayload{
			id:     m.P.ID,
			kind:   Kind(m.P.Kind),
			player: m.P.Player,
			remote: !m.P.NoClient,
			ref:    m.P.Client,
			x:      m.P.X,
			y:      m.P.Y,
			z:      m.P.Z,
			speed:  m.P.Speed,
			blob:   m.P.Blob,
		}})

	case mesh.MsgRelay:
		m, ok := mesh.DecodeRelay(body)
		if !ok {
			w.log.Warn("peer: bad relay frame")
			return
		}
		s, ok := w.SceneByPeer(m.To)
		if !ok {
			return
		}
		switch m.Kind {
		case mesh.RelayMove:
			// 位移走意图缓冲，不进 mailbox：和本地客户端的路径保持一致，
			// 否则跨节点来的玩家会发现自己比同屏的人晚一帧。
			s.Move(m.Entity, m.X, m.Y, m.Z)
		case mesh.RelayHeartbeat:
			s.post(&msgHeartbeat{entityID: m.Entity})
		case mesh.RelayLeave:
			s.post(&msgLeave{entityID: m.Entity})
		case mesh.RelayAttack:
			s.Attack(m.Entity, m.Target, m.Skill)
		case mesh.RelaySkills:
			s.QuerySkills(m.Entity)
		}

	case mesh.MsgReleaseID:
		m, ok := mesh.DecodeReleaseID(body)
		if !ok {
			w.log.Warn("peer: bad release id frame")
			return
		}
		// 对方替我们收拾了一具尸体，把这个 ID 收回冷却队列。
		// 如果它不属于本分区（配置不一致导致的误通知），release 会自己忽略。
		if foreign := w.ids.release(m.ID, time.Now()); foreign {
			w.log.Warn("peer: release id for a foreign id ignored", "id", m.ID)
		}

	case mesh.MsgSceneCtl:
		m, ok := mesh.DecodeSceneCtl(body)
		if !ok {
			w.log.Warn("peer: bad scene ctl frame")
			return
		}
		w.onSceneCtl(m)

	case mesh.MsgTopoDigest:
		m, ok := mesh.DecodeTopoDigest(body)
		if !ok {
			w.log.Warn("peer: bad topo digest frame")
			return
		}
		w.onDigest(m)

	default:
		w.log.Debug("peer: unhandled message", "msgID", msgID)
	}
}
