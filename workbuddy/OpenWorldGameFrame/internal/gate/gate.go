// Package gate 实现网关：连接鉴权、会话保持、消息分发。
//
// Gate 不含游戏逻辑。它只做三件事：
// 校验令牌、把会话挂到场景、把上行消息翻译成场景指令。
package gate

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/mesh"
	"openworld/internal/net"
	"openworld/internal/observability"
	"openworld/internal/protocol"
	"openworld/internal/world"
	"openworld/pkg/authtoken"
)

// Config 是网关参数。
type Config struct {
	Addr     string
	Secret   string
	TokenTTL time.Duration
	// Node 是本节点标识。空表示单进程部署。
	//
	// 它决定"这个会话的实体是不是在本进程"：会话记的实体所在节点
	// 等于它就走本地场景，不等就经节点间链路转发。
	Node string
}

// Session 代表一个已登录的玩家连接，同时实现 world.Client。
type Session struct {
	id       uint64
	playerID string
	conn     *net.Conn
	// scene 用原子指针：玩家跨场景迁移时，场景会从另一个 goroutine
	// 调用 SetScene 改它，而读协程同时在用它路由 Move/Heartbeat。
	// 只有实体在本节点时才有值。
	scene atomic.Pointer[world.Scene]
	// route 是实体当前所在节点/场景。跨进程后实体可能在别的节点上，
	// 这时 scene 为空，上行消息按 route 转发。
	route atomic.Pointer[sessionRoute]
	// node 是本会话所在节点（这个连接接在谁身上）。
	// 迁移会改 route.node，但永远不会改它——TCP 连接不能跨进程搬。
	node    string
	loginAt time.Time
}

// Send 实现 world.Client。
func (s *Session) Send(msgID uint16, body []byte) error {
	return s.conn.Send(msgID, body)
}

// Kick 实现 world.Client。先投递 Kick 包，稍等再断开，
// 否则连接立刻关闭会让通知包留在发送队列里发不出去。
func (s *Session) Kick(code uint8, reason string) error {
	_ = s.conn.Send(protocol.MsgS2CKick, (&protocol.Kick{Code: code, Reason: reason}).Encode())
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.conn.Close()
	}()
	return nil
}

// SetScene 实现 world.SceneSwitcher。实体跨场景迁移落地后由目的场景调用，
// 之后这个会话的 Move / Heartbeat 就会投给新场景。
//
// 调用方是目的场景的 tick 协程，而读 Move 的是这条连接的读协程，
// 所以必须走原子指针——加锁也能对，但这里是"每包都要读一次"的路径。
//
// 只有实体落在**本节点**的场景时才会被调用；跨节点时走 RouteTo。
func (s *Session) SetScene(sc *world.Scene) {
	s.scene.Store(sc)
	if sc != nil {
		s.setRoute(mesh.Peer{Node: sc.Node(), Scene: sc.ID()}, s.entity())
	}
}

// RouteTo 实现 world.Router。
//
// 实体落在本节点：交给 SetScene 处理（它要存的是指针，不是地址）。
// 实体落在别的节点：记下节点和场景，上行消息改成转发，并清掉本地场景指针——
// 那个场景已经不再拥有这个实体，继续往里投 Move 就是往一个空 ID 上写。
func (s *Session) RouteTo(p mesh.Peer, entity uint64, x, y, z float32) {
	if p.Local(s.node) {
		s.setRoute(p, entity)
		return
	}
	s.scene.Store(nil)
	s.setRoute(p, entity)
}

// Locate 实现 world.Locator：让场景知道该把下行包发回哪条连接。
func (s *Session) Locate() mesh.ClientRef {
	return mesh.ClientRef{Node: s.node, Session: s.id}
}

// entity 返回当前实体 ID，没有实体时为 0。
func (s *Session) entity() uint64 {
	if rt := s.routeOf(); rt != nil {
		return rt.entity
	}
	return 0
}

// Gate 是网关服务组件。
type Gate struct {
	cfg     Config
	log     *slog.Logger
	world   *world.World
	srv     *net.Server
	metrics *observability.Metrics
	msh     *mesh.Mesh

	sessions sync.Map // connID -> *Session
	online   atomic.Int64

	// draining 为 true 时拒绝新的登录和进场景请求。
	// 这是关服四步里的第一步"摘流"——不摘流就排水，玩家会一边走一边补进来。
	draining  atomic.Bool
	drainOnce sync.Once

	loginOK   *atomic.Int64
	loginFail *atomic.Int64
	msgsIn    *atomic.Int64
	rejected  *atomic.Int64
}

// New 创建网关。
func New(cfg Config, w *world.World, m *observability.Metrics, log *slog.Logger) *Gate {
	g := &Gate{
		cfg:     cfg,
		log:     log,
		world:   w,
		metrics: m,
	}

	if m != nil {
		g.loginOK = m.Counter("gate_login_total", "successful logins")
		g.loginFail = m.Counter("gate_login_failed_total", "failed logins")
		g.msgsIn = m.Counter("gate_messages_in_total", "inbound messages received by gate")
		g.rejected = m.Counter("gate_rejected_while_draining_total", "requests rejected because gate is draining")
		m.Gauge("gate_online_sessions", "current online sessions", func() float64 {
			return float64(g.online.Load())
		})
	} else {
		g.loginOK, g.loginFail, g.msgsIn = &atomic.Int64{}, &atomic.Int64{}, &atomic.Int64{}
		g.rejected = &atomic.Int64{}
	}

	g.srv = net.NewServer(cfg.Addr, log, g.onMessage, g.onClose)

	if m != nil {
		m.Gauge("gate_active_conns", "current active tcp connections", func() float64 {
			return float64(g.srv.ActiveConns())
		})
		m.Gauge("gate_total_conns", "total accepted tcp connections", func() float64 {
			return float64(g.srv.TotalConns())
		})
	}
	return g
}

// Name 实现 core.Component。
func (g *Gate) Name() string { return "gate" }

// AttachMesh 挂上节点间链路。
//
// 分开设置而不是放进 New 的参数：World 需要 Gate 的 ResolveClient 才能
// 构造，Gate 需要 World 才能构造，两者本来就是个环。用"先建 World
// （resolver 是个捕获闭包）、再建 Gate、最后把 mesh 贴上去"的顺序解开，
// 比引入一个中间容器简单得多。
func (g *Gate) AttachMesh(m *mesh.Mesh) { g.msh = m }

// Start 开始监听。
func (g *Gate) Start(ctx context.Context) error {
	return g.srv.Start(ctx)
}

// Stop 关闭监听并断开所有玩家。
func (g *Gate) Stop(ctx context.Context) error {
	// 先发关服通知，再断连接，最后等场景收尾。
	g.sessions.Range(func(_, v any) bool {
		s := v.(*Session)
		_ = s.conn.Send(protocol.MsgS2CKick, (&protocol.Kick{
			Code: protocol.CodeKickServerShutdown, Reason: "server shutting down",
		}).Encode())
		return true
	})
	time.Sleep(100 * time.Millisecond)
	return g.srv.Stop(ctx)
}

// Addr 返回实际监听地址。
func (g *Gate) Addr() string { return g.srv.Addr() }

// Online 返回在线会话数。
func (g *Gate) Online() int64 { return g.online.Load() }

// Draining 返回是否已进入排水状态。
func (g *Gate) Draining() bool { return g.draining.Load() }

// BeginDrain 开始关服的"摘流 + 通知 + 到期踢人"三步。
//
// 顺序不能改：
//  1. 先置 draining，让新登录立刻被拒（摘流）
//  2. 再给在线玩家推倒计时公告，让打副本的人有时间打完
//  3. grace 到期才真正踢人断开
//
// 直接 kill 进程的全服存档就没了，这是关服流程里最贵的错误。
// 重复调用安全：只有第一次生效，避免控制面重试时重置倒计时。
func (g *Gate) BeginDrain(grace time.Duration) {
	g.drainOnce.Do(func() {
		g.draining.Store(true)
		if grace < 0 {
			grace = 0
		}

		notice := (&protocol.Notice{
			Title:   "服务器维护",
			Text:    "服务器将在倒计时结束后关闭，请尽快结束当前战斗并下线。",
			Seconds: uint32(grace / time.Second),
		}).Encode()

		notified := 0
		g.sessions.Range(func(_, v any) bool {
			_ = v.(*Session).Send(protocol.MsgS2CNotice, notice)
			notified++
			return true
		})
		g.log.Warn("gate draining", "grace", grace.String(), "notified", notified)

		if grace > 0 {
			time.AfterFunc(grace, func() { g.kickAll("server maintenance") })
		}
	})
}

// kickAll 给所有会话下发踢线原因并断开。
func (g *Gate) kickAll(reason string) {
	n := 0
	g.sessions.Range(func(_, v any) bool {
		s := v.(*Session)
		_ = s.Send(protocol.MsgS2CKick, (&protocol.Kick{
			Code: protocol.CodeKickServerShutdown, Reason: reason,
		}).Encode())
		n++
		return true
	})
	g.log.Warn("gate kicked all sessions", "count", n, "reason", reason)

	// 给发送队列一点时间把 Kick 包送出去，再断连接。
	//
	// 这里只关连接，不自己删 sessions 表的条目——onClose 会在连接真正
	// 关闭时做 LoadAndDelete 和离开场景。抢着删会导致 online 计数漏减、
	// 场景里留下一批幽灵实体。
	time.AfterFunc(200*time.Millisecond, func() {
		g.sessions.Range(func(_, v any) bool {
			v.(*Session).conn.Close()
			return true
		})
	})
}

// SessionCount 返回当前会话表大小。
func (g *Gate) SessionCount() int {
	n := 0
	g.sessions.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

func (g *Gate) onMessage(c *net.Conn, msgID uint16, body []byte) {
	g.msgsIn.Add(1)

	switch msgID {
	case protocol.MsgC2SLogin:
		g.handleLogin(c, body)
	case protocol.MsgC2SHeartbeat:
		g.handleHeartbeat(c, body)
	case protocol.MsgC2SEnterScene:
		g.handleEnterScene(c, body)
	case protocol.MsgC2SMove:
		g.handleMove(c, body)
	case protocol.MsgC2SAttack:
		g.handleAttack(c, body)
	case protocol.MsgC2SQuerySkills:
		g.handleQuerySkills(c, body)
	default:
		g.log.Debug("unknown message", "conn", c.ID, "msgID", msgID, "remote", c.Remote)
	}
}

func (g *Gate) session(c *net.Conn) (*Session, bool) {
	v, ok := g.sessions.Load(c.ID)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

func (g *Gate) handleLogin(c *net.Conn, body []byte) {
	if g.draining.Load() {
		g.rejected.Add(1)
		g.replyLogin(c, protocol.CodeServerBusy, 0, "", "server under maintenance")
		return
	}

	var req protocol.LoginReq
	if err := req.Decode(body); err != nil {
		g.replyLogin(c, protocol.CodeInvalidParam, 0, "", "bad request")
		return
	}

	playerID, ok := authtoken.Verify(g.cfg.Secret, req.Token, g.cfg.TokenTTL, time.Now())
	if !ok {
		g.loginFail.Add(1)
		g.log.Warn("login rejected", "conn", c.ID, "remote", c.Remote, "reason", "invalid token")
		g.replyLogin(c, protocol.CodeAuthFailed, 0, "", "invalid or expired token")
		return
	}

	if _, exists := g.session(c); exists {
		g.replyLogin(c, protocol.CodeInvalidParam, 0, "", "already logged in")
		return
	}

	s := &Session{
		id:       c.ID,
		playerID: playerID,
		conn:     c,
		node:     g.cfg.Node,
		loginAt:  time.Now(),
	}
	g.sessions.Store(c.ID, s)
	g.online.Add(1)
	g.loginOK.Add(1)

	g.log.Info("login ok", "conn", c.ID, "player", playerID, "remote", c.Remote)
	g.replyLogin(c, protocol.CodeOK, c.ID, playerID, "")
}

func (g *Gate) replyLogin(c *net.Conn, code uint8, sessionID uint64, playerID, reason string) {
	resp := &protocol.LoginResp{Code: code, SessionID: sessionID, PlayerID: playerID, Reason: reason}
	_ = c.Send(protocol.MsgS2CLoginResp, resp.Encode())
}

func (g *Gate) handleHeartbeat(c *net.Conn, body []byte) {
	s, ok := g.session(c)
	if !ok {
		c.Close()
		return
	}
	var req protocol.HeartbeatReq
	if err := req.Decode(body); err != nil {
		return
	}
	// 实体可能已经迁到别的节点：心跳要跟着转发，否则那边的 reapIdle
	// 会认为这个玩家挂机，把他从世界里清掉——而客户端还在正常呼吸。
	if rt := s.routeOf(); rt != nil && rt.entity != 0 {
		if rt.node != g.cfg.Node {
			s.relay(g, mesh.RelayHeartbeat, 0, 0, 0)
		} else if sc := s.scene.Load(); sc != nil {
			sc.Heartbeat(rt.entity)
		}
	}
	resp := &protocol.HeartbeatResp{Seq: req.Seq, ServerTime: time.Now().UnixMilli()}
	_ = c.Send(protocol.MsgS2CHeartbeat, resp.Encode())
}

func (g *Gate) handleEnterScene(c *net.Conn, body []byte) {
	s, ok := g.session(c)
	if !ok {
		c.Close()
		return
	}
	if s.entity() != 0 {
		return // 已在场景里，重复进入忽略
	}
	if g.draining.Load() {
		g.rejected.Add(1)
		_ = s.Send(protocol.MsgS2CEnterScene, (&protocol.EnterSceneResp{
			Code: protocol.CodeServerBusy, Reason: "server under maintenance",
		}).Encode())
		return
	}
	// 多场景部署时按玩家 ID 分流，同时拿到稳定的落点。
	// 落点跟着分流一起返回，而不是让场景自己散列——否则同一个玩家
	// 每次上线会出现在不同位置，看起来像随机传送。
	scene, x, z := g.world.Assign(s.playerID)
	if scene == nil {
		scene = g.world.Default()
	}
	if scene == nil {
		_ = s.Send(protocol.MsgS2CEnterScene, (&protocol.EnterSceneResp{
			Code: protocol.CodeServerBusy, Reason: "no scene available",
		}).Encode())
		return
	}

	s.scene.Store(scene)
	id := scene.EnterAt(s.playerID, s, x, z)
	if id == 0 {
		// 场景的 ID 空间耗尽（或进场指令被邮箱丢了）。此时会话没有实体，
		// 继续留在场景里只会收到一个永远不会来的 EnterSceneResp。
		s.scene.Store(nil)
		_ = s.Send(protocol.MsgS2CEnterScene, (&protocol.EnterSceneResp{
			Code: protocol.CodeServerBusy, Reason: "scene is full",
		}).Encode())
		return
	}
	s.setRoute(mesh.Peer{Node: scene.Node(), Scene: scene.ID()}, id)
	g.log.Info("enter scene", "player", s.playerID, "entity", id, "scene", scene.ID(), "node", g.cfg.Node)
}

func (g *Gate) handleMove(c *net.Conn, body []byte) {
	s, ok := g.session(c)
	if !ok {
		c.Close()
		return
	}
	rt := s.routeOf()
	if rt == nil || rt.entity == 0 {
		return
	}
	var req protocol.MoveReq
	if err := req.Decode(body); err != nil {
		return
	}
	// 实体在别的节点：转发语义而不是原始字节。让那边自己解协议，
	// 意味着每个节点的场景都要跟着客户端协议升级，而它其实只需要
	// "谁、要去哪"。
	if rt.node != g.cfg.Node {
		s.relay(g, mesh.RelayMove, req.X, req.Y, req.Z)
		return
	}
	if sc := s.scene.Load(); sc != nil {
		sc.Move(rt.entity, req.X, req.Y, req.Z)
	}
}

// handleAttack 把攻击指令投给实体所在场景。
//
// 和 Move 一样：实体在别的节点就转发语义（RelayAttack），在本节点就
// 直接投本地场景。攻击不返回结算结果给发起者——结算结果由实体所在
// 场景广播给视野内所有玩家，发起者作为视野内一员自然收得到。
func (g *Gate) handleAttack(c *net.Conn, body []byte) {
	s, ok := g.session(c)
	if !ok {
		c.Close()
		return
	}
	rt := s.routeOf()
	if rt == nil || rt.entity == 0 {
		return
	}
	var req protocol.AttackReq
	if err := req.Decode(body); err != nil {
		return
	}
	if rt.node != g.cfg.Node {
		s.relayAttack(g, rt.entity, req.TargetID, req.SkillID)
		return
	}
	if sc := s.scene.Load(); sc != nil {
		sc.Attack(rt.entity, req.TargetID, req.SkillID)
	}
}

// handleQuerySkills 把技能目录查询投给实体所在场景。实体在别的节点时
// 转发语义（RelaySkills），在本节点直接投本地场景。
func (g *Gate) handleQuerySkills(c *net.Conn, _ []byte) {
	s, ok := g.session(c)
	if !ok {
		c.Close()
		return
	}
	rt := s.routeOf()
	if rt == nil || rt.entity == 0 {
		return
	}
	if rt.node != g.cfg.Node {
		s.relayQuerySkills(g, rt.entity)
		return
	}
	if sc := s.scene.Load(); sc != nil {
		sc.QuerySkills(rt.entity)
	}
}

func (g *Gate) onClose(c *net.Conn) {
	v, ok := g.sessions.LoadAndDelete(c.ID)
	if !ok {
		return
	}
	s := v.(*Session)
	g.online.Add(-1)

	if rt := s.routeOf(); rt != nil && rt.entity != 0 {
		if rt.node != g.cfg.Node {
			// 实体不在本节点：不能直接调场景，只能发一条 Leave 过去。
			// 这条消息丢了也不要紧——落地时 lastHeartbeat 重置过，
			// 那边的心跳超时会在一个周期内把它清掉。
			s.relay(g, mesh.RelayLeave, 0, 0, 0)
		} else if sc := s.scene.Load(); sc != nil {
			sc.Leave(rt.entity)
		}
	}
	bi, bo, fo := c.Stats()
	g.log.Info("session closed", "player", s.playerID, "conn", c.ID,
		"duration", time.Since(s.loginAt).Round(time.Second).String(),
		"bytesIn", bi, "bytesOut", bo, "framesOut", fo)
}

// GateStats 是网关状态快照，供 /admin/state 使用。
type GateStats struct {
	Addr     string `json:"addr"`
	Online   int64  `json:"online"`
	Sessions int    `json:"sessions"`
	Conns    int64  `json:"activeConns"`
	TotalCon int64  `json:"totalConns"`
	LoginOK  int64  `json:"loginOk"`
	LoginErr int64  `json:"loginFailed"`
	MsgsIn   int64  `json:"messagesIn"`
	Draining bool   `json:"draining"`
	Rejected int64  `json:"rejectedWhileDraining"`
}

// Stats 返回网关状态快照。
func (g *Gate) Stats() GateStats {
	return GateStats{
		Addr:     g.Addr(),
		Online:   g.online.Load(),
		Sessions: g.SessionCount(),
		Conns:    g.srv.ActiveConns(),
		TotalCon: g.srv.TotalConns(),
		LoginOK:  g.loginOK.Load(),
		LoginErr: g.loginFail.Load(),
		MsgsIn:   g.msgsIn.Load(),
		Draining: g.draining.Load(),
		Rejected: g.rejected.Load(),
	}
}
