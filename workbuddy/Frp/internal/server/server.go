// Package server 实现 frptun 服务端：运行在公网，接受公网用户连接并转发给内网客户端。
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"frptun/internal/auth"
	"frptun/internal/config"
	"frptun/internal/protocol"
	"frptun/internal/stats"
	"frptun/internal/store"
	"frptun/internal/tunnel"
)

const (
	pendingTimeout = 10 * time.Second
	dialTimeout    = 10 * time.Second
	ackTimeout     = 3 * time.Second
)

// Server 服务端实例。
type Server struct {
	cfg *config.Config
	ln  net.Listener

	connID atomic.Uint64
	active atomic.Int64

	store   *store.Store
	checker *auth.Checker
	hub     *stats.Hub

	mu      sync.RWMutex
	clients map[uint64]*clientConn
	owners  map[int]uint64 // remotePort -> sessionID

	pendingMu sync.Mutex
	pending   map[uint64]*pendingConn

	syncSeq atomic.Uint64
	acksMu  sync.Mutex
	acks    map[string]chan *protocol.Message

	tunnelsMu sync.Mutex
	tunnels   map[uint64]*tunnelHandle

	closed    atomic.Bool
	closeOnce sync.Once
	wg        sync.WaitGroup
}

type clientConn struct {
	id        uint64
	conn      net.Conn
	br        *bufio.Reader
	ip        string
	machineID string
	hostname  string

	staticServices []protocol.Service // 来自客户端配置
	svcMu          sync.Mutex
	services       map[string]*runningService // name -> 运行中的映射
	dynamicNames   map[string]bool            // 哪些服务来自 DB
	dbIDs          map[string]int64

	lastSeen  atomic.Int64
	writeMu   sync.Mutex
	closeOnce sync.Once
}

type runningService struct {
	spec     protocol.Service
	listener net.Listener
	stop     context.CancelFunc
	dynamic  bool
}

type tunnelHandle struct {
	service   string
	sessionID uint64
	conns     []net.Conn
}

type pendingConn struct {
	userConn  net.Conn
	sessionID uint64
	machineID string
	hostname  string
	service   string
	timer     *time.Timer
}

// PortStatus 管理端端口视图。
type PortStatus struct {
	ID         int64  `json:"id"`
	MachineID  string `json:"machine_id"`
	Hostname   string `json:"hostname"`
	Name       string `json:"name"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
	Source     string `json:"source"` // static | dynamic
	Active     bool   `json:"active"`
	Online     bool   `json:"online"`
	DBID       int64  `json:"db_id"`
}

// New 创建服务端实例。
func New(cfg *config.Config, st *store.Store) *Server {
	return &Server{
		cfg: cfg, store: st, checker: auth.New(st), hub: stats.NewHub(),
		clients: make(map[uint64]*clientConn),
		owners:  make(map[int]uint64),
		pending: make(map[uint64]*pendingConn),
		acks:    make(map[string]chan *protocol.Message),
		tunnels: make(map[uint64]*tunnelHandle),
	}
}

// Hub 返回实时连接统计中心，供管理端使用。
func (s *Server) Hub() *stats.Hub { return s.hub }

// ActiveTunnels 当前活跃隧道数。
func (s *Server) ActiveTunnels() int64 { return s.active.Load() }

// ConnectedClients 在线客户端快照。
func (s *Server) ConnectedClients() []ClientView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ClientView, 0, len(s.clients))
	now := time.Now()
	for _, cc := range s.clients {
		cc.svcMu.Lock()
		svcs := make([]protocol.Service, 0, len(cc.services))
		for _, rs := range cc.services {
			svcs = append(svcs, rs.spec)
		}
		cc.svcMu.Unlock()
		out = append(out, ClientView{
			ID: cc.id, MachineID: cc.machineID, Hostname: cc.hostname, IP: cc.ip,
			Services: svcs, LastSeen: cc.lastSeen.Load(),
			OnlineSec: int64(now.Sub(time.Unix(0, cc.lastSeen.Load())).Seconds()),
		})
	}
	return out
}

// ClientView 在线客户端视图。
type ClientView struct {
	ID        uint64             `json:"id"`
	MachineID string             `json:"machine_id"`
	Hostname  string             `json:"hostname"`
	IP        string             `json:"ip"`
	Services  []protocol.Service `json:"services"`
	LastSeen  int64              `json:"last_seen"`
	OnlineSec int64              `json:"online_sec"`
}

// Run 启动服务端并阻塞，直到 Close 被调用。
func (s *Server) Run() error {
	addr := net.JoinHostPort(s.cfg.BindAddr, strconv.Itoa(s.cfg.BindPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	s.ln = ln
	log.Printf("[frps] 控制端口已监听 %s (最大隧道数 %d)", addr, s.cfg.MaxConnections)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closed.Load() {
				return nil
			}
			log.Printf("[frps] accept 错误: %v", err)
			continue
		}
		tunnel.Optimize(conn)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleIncoming(conn)
		}()
	}
}

func (s *Server) handleIncoming(conn net.Conn) {
	kind, err := protocol.ReadKind(conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	switch kind {
	case protocol.KindControl:
		s.handleControl(conn)
	case protocol.KindData:
		s.handleData(conn)
	default:
		log.Printf("[frps] 未知连接类型 0x%02X，已拒绝", kind)
		_ = conn.Close()
	}
}

// handleControl 处理客户端长连接：认证 → 授权 → 注册 → 心跳 → 端口同步。
func (s *Server) handleControl(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	br := bufio.NewReader(conn)

	msg, err := protocol.ReadFrame(br)
	if err != nil || msg.Type != protocol.MsgAuth {
		log.Printf("[frps] 认证阶段读取失败: %v", err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(msg.Token), []byte(s.cfg.Token)) != 1 {
		_ = s.sendRaw(conn, &protocol.Message{Type: protocol.MsgAuthResp, Error: "token 不匹配"})
		log.Printf("[frps] 拒绝认证：token 不匹配，来自 %s", conn.RemoteAddr())
		return
	}

	ip := conn.RemoteAddr().String()
	machineID := msg.MachineID
	hostname := msg.Hostname
	if machineID == "" {
		machineID = "unknown-" + ip
	}

	mode := s.store.GetSetting("auth_mode", auth.ModeOff)
	if allowed, reason := s.checker.Check(mode, ip, machineID); !allowed {
		_ = s.sendRaw(conn, &protocol.Message{Type: protocol.MsgAuthResp, Error: "授权被拒绝: " + reason})
		log.Printf("[frps] 拒绝授权: ip=%s machine=%s 原因=%s", ip, machineID, reason)
		return
	}

	if err := s.store.UpsertClient(machineID, ip, hostname); err != nil {
		log.Printf("[frps] 记录客户端信息失败: %v", err)
	}

	if err := s.sendRaw(conn, &protocol.Message{Type: protocol.MsgAuthResp, OK: true}); err != nil {
		return
	}

	msg, err = protocol.ReadFrame(br)
	if err != nil || msg.Type != protocol.MsgRegister {
		log.Printf("[frps] 注册阶段读取失败: %v", err)
		return
	}
	cc := &clientConn{
		conn: conn, br: br, ip: ip, machineID: machineID, hostname: hostname,
		services:     make(map[string]*runningService),
		dynamicNames: make(map[string]bool), dbIDs: make(map[string]int64),
	}
	if err := s.register(cc, msg.Services); err != nil {
		_ = s.sendRaw(conn, &protocol.Message{Type: protocol.MsgRegisterResp, Error: err.Error()})
		log.Printf("[frps] 注册失败: %v", err)
		return
	}
	s.mu.Lock()
	s.clients[cc.id] = cc
	s.mu.Unlock()
	if err := s.sendRaw(conn, &protocol.Message{Type: protocol.MsgRegisterResp, OK: true}); err != nil {
		s.closeClient(cc)
		return
	}

	_ = conn.SetDeadline(time.Time{})
	cc.lastSeen.Store(time.Now().UnixNano())
	log.Printf("[frps] 客户端 #%d [%s] %s 已注册 %d 个映射", cc.id, shortID(machineID), ip, len(cc.services))

	defer s.closeClient(cc)
	// 把权威映射列表（静态+动态）下发给客户端，使其运行时路由表与实际监听一致。
	_, _ = s.sendSync(cc, false)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(s.cfg.HeartbeatTimeout) * time.Second))
		m, err := protocol.ReadFrame(br)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Printf("[frps] 客户端 #%d 心跳超时", cc.id)
			} else {
				log.Printf("[frps] 客户端 #%d 控制连接断开: %v", cc.id, err)
			}
			return
		}
		switch m.Type {
		case protocol.MsgHeartbeat:
			cc.lastSeen.Store(time.Now().UnixNano())
			_ = s.send(cc, &protocol.Message{Type: protocol.MsgHeartbeatAck})
		case protocol.MsgCloseConn:
			s.dropPending(m.ConnID)
		case protocol.MsgServiceAck:
			s.acksMu.Lock()
			ch := s.acks[m.RequestID]
			s.acksMu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		}
	}
}

// register 合并静态配置与持久化动态映射，分配 sessionID 并绑定所有监听。
func (s *Server) register(cc *clientConn, static []protocol.Service) error {
	s.mu.Lock()
	cc.id = uint64(activeSession.Add(1))
	cc.staticServices = static

	desired, dyn, dbIDs := s.computeDesired(cc.machineID, static)
	cc.dynamicNames, cc.dbIDs = dyn, dbIDs

	for port := range desired {
		if owner, exists := s.owners[port]; exists {
			s.mu.Unlock()
			return fmt.Errorf("远端端口 %d 已被客户端 #%d 占用", port, owner)
		}
	}
	for port := range desired {
		s.owners[port] = cc.id
	}
	s.mu.Unlock()

	// 绑定监听失败时回滚已占用端口与已建监听
	var started []*runningService
	for _, spec := range desired {
		rs, err := s.startListener(cc, spec, dyn[spec.Name], dbIDs[spec.Name])
		if err != nil {
			for _, r := range started {
				r.stop()
				_ = r.listener.Close()
			}
			s.mu.Lock()
			for p := range desired {
				if s.owners[p] == cc.id {
					delete(s.owners, p)
				}
			}
			s.mu.Unlock()
			return fmt.Errorf("监听远端端口 %d 失败: %w", spec.RemotePort, err)
		}
		started = append(started, rs)
	}
	return nil
}

var activeSession atomic.Uint64

// computeDesired 计算某客户端应启用的映射集合 = 静态 + 动态(启用) - 禁用端口。
func (s *Server) computeDesired(machineID string, static []protocol.Service) (
	map[int]protocol.Service, map[string]bool, map[string]int64) {
	desired := make(map[int]protocol.Service)
	dynamic := make(map[string]bool)
	dbIDs := make(map[string]int64)

	for _, svc := range static {
		desired[svc.RemotePort] = svc
	}
	if mappings, err := s.store.ListPortMappings(machineID); err == nil {
		for _, m := range mappings {
			if !m.Enabled {
				continue
			}
			desired[m.RemotePort] = protocol.Service{
				Name: m.Name, LocalIP: m.LocalIP, LocalPort: m.LocalPort, RemotePort: m.RemotePort,
			}
			dynamic[m.Name] = true
			dbIDs[m.Name] = m.ID
		}
	}
	if disabled, err := s.store.ListDisabledPorts(machineID); err == nil {
		for port := range disabled {
			delete(desired, port)
		}
	}
	return desired, dynamic, dbIDs
}

// startListener 绑定一个远端端口并启动 accept 循环。
func (s *Server) startListener(cc *clientConn, spec protocol.Service, dynamic bool, dbID int64) (*runningService, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.BindAddr, strconv.Itoa(spec.RemotePort)))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	rs := &runningService{spec: spec, listener: ln, stop: cancel, dynamic: dynamic}
	cc.svcMu.Lock()
	cc.services[spec.Name] = rs
	if dynamic {
		cc.dynamicNames[spec.Name] = true
		cc.dbIDs[spec.Name] = dbID
	}
	cc.svcMu.Unlock()

	s.wg.Add(1)
	go s.acceptLoop(ctx, cc, rs)
	return rs, nil
}

func (s *Server) acceptLoop(ctx context.Context, cc *clientConn, rs *runningService) {
	defer s.wg.Done()
	spec := rs.spec
	log.Printf("[frps] 隧道 %q 已就绪 :%d -> 客户端 #%d 的 %s:%d",
		spec.Name, spec.RemotePort, cc.id, spec.LocalIP, spec.LocalPort)
	for {
		userConn, err := rs.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			return
		}
		if s.closed.Load() || ctx.Err() != nil || s.active.Load() >= int64(s.cfg.MaxConnections) {
			_ = userConn.Close()
			continue
		}
		tunnel.Optimize(userConn)

		id := s.connID.Add(1)
		s.active.Add(1)
		p := &pendingConn{
			userConn: userConn, sessionID: cc.id,
			machineID: cc.machineID, hostname: cc.hostname, service: spec.Name,
		}
		s.pendingMu.Lock()
		s.pending[id] = p
		s.pendingMu.Unlock()
		p.timer = time.AfterFunc(pendingTimeout, func() { s.dropPending(id) })

		notify := &protocol.Message{
			Type: protocol.MsgNewConn, ConnID: id,
			ServiceName: spec.Name, RemotePort: spec.RemotePort,
		}
		if err := s.send(cc, notify); err != nil {
			s.dropPending(id)
		}
	}
}

func (s *Server) handleData(conn net.Conn) {
	id, err := protocol.ReadConnID(conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})

	p := s.takePending(id)
	if p == nil {
		log.Printf("[frps] 数据连接 connID=%d 配对失败", id)
		_ = conn.Close()
		return
	}

	remoteIP := p.userConn.RemoteAddr().String()
	if h, _, err := net.SplitHostPort(remoteIP); err == nil {
		remoteIP = h
	}
	_, counter := s.hub.Add(id, p.machineID, p.hostname, p.service, remoteIP, "")
	counted := stats.Wrap(p.userConn, counter)

	th := &tunnelHandle{service: p.service, sessionID: p.sessionID, conns: []net.Conn{counted, conn}}
	s.tunnelsMu.Lock()
	s.tunnels[id] = th
	s.tunnelsMu.Unlock()

	defer func() {
		s.tunnelsMu.Lock()
		delete(s.tunnels, id)
		s.tunnelsMu.Unlock()
		up, down, durMs := s.hub.Remove(id)
		s.active.Add(-1)
		if err := s.store.AddTraffic(store.TrafficRecord{
			ConnID: id, MachineID: p.machineID, Service: p.service, RemoteIP: remoteIP,
			BytesUp: up, BytesDown: down, DurationSec: durMs / 1000,
		}); err != nil {
			log.Printf("[frps] 记录流量失败: %v", err)
		}
		_ = s.store.AddClientTraffic(p.machineID, up, down)
	}()

	tunnel.Pipe(counted, conn)
}

func (s *Server) takePending(id uint64) *pendingConn {
	s.pendingMu.Lock()
	p := s.pending[id]
	delete(s.pending, id)
	s.pendingMu.Unlock()
	if p != nil && p.timer != nil {
		p.timer.Stop()
	}
	return p
}

func (s *Server) dropPending(id uint64) {
	if p := s.takePending(id); p != nil {
		_ = p.userConn.Close()
		s.active.Add(-1)
	}
}

func (s *Server) dropPendingByService(sessionID uint64, service string) {
	s.pendingMu.Lock()
	var ids []uint64
	for id, p := range s.pending {
		if p.sessionID == sessionID && p.service == service {
			ids = append(ids, id)
		}
	}
	s.pendingMu.Unlock()
	for _, id := range ids {
		s.dropPending(id)
	}
}

// closeTunnelsByService 关闭指定服务上的所有活跃隧道（用于实时关闭端口）。
func (s *Server) closeTunnelsByService(sessionID uint64, service string) int {
	s.tunnelsMu.Lock()
	var conns []net.Conn
	for _, th := range s.tunnels {
		if th.sessionID == sessionID && th.service == service {
			conns = append(conns, th.conns...)
		}
	}
	s.tunnelsMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return len(conns) / 2
}

func (s *Server) closeClient(cc *clientConn) {
	cc.closeOnce.Do(func() {
		s.mu.Lock()
		if s.clients[cc.id] == cc {
			delete(s.clients, cc.id)
		}
		cc.svcMu.Lock()
		names := make([]string, 0, len(cc.services))
		for name, rs := range cc.services {
			names = append(names, name)
			rs.stop()
			_ = rs.listener.Close() // 必须真正关闭 listener：context 取消无法打断阻塞中的 Accept
			delete(cc.services, name)
			if s.owners[rs.spec.RemotePort] == cc.id {
				delete(s.owners, rs.spec.RemotePort)
			}
		}
		cc.svcMu.Unlock()
		s.mu.Unlock()

		for _, name := range names {
			s.dropPendingByService(cc.id, name)
		}
		_ = cc.conn.Close()
		log.Printf("[frps] 客户端 #%d 资源已清理", cc.id)
	})
}

// ---- 实时端口管理 ----

func (s *Server) clientByMachine(machineID string) *clientConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, cc := range s.clients {
		if cc.machineID == machineID {
			return cc
		}
	}
	return nil
}

// serviceByPort 在客户端运行时映射中按远端端口查找。
func serviceByPort(cc *clientConn, remotePort int) *runningService {
	cc.svcMu.Lock()
	defer cc.svcMu.Unlock()
	for _, rs := range cc.services {
		if rs.spec.RemotePort == remotePort {
			return rs
		}
	}
	return nil
}

// AddPort 新增一条端口映射：持久化 + 在线则立即生效，返回 (dbID, 客户端是否确认, error)。
func (s *Server) AddPort(machineID, name, localIP string, localPort, remotePort int, note string) (int64, bool, error) {
	if remotePort < 1 || remotePort > 65535 || localPort < 1 || localPort > 65535 {
		return 0, false, fmt.Errorf("端口必须在 1-65535 范围内")
	}
	if machineID == "" {
		return 0, false, fmt.Errorf("machine_id 不能为空")
	}
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	if name == "" {
		name = fmt.Sprintf("dyn-%d", remotePort)
	}

	// 端口不能被其他机器占用（线上监听或持久化映射）
	s.mu.RLock()
	for _, cc := range s.clients {
		if rs := serviceByPort(cc, remotePort); rs != nil && cc.machineID != machineID {
			s.mu.RUnlock()
			return 0, false, fmt.Errorf("远端端口 %d 已被在线客户端 %s 占用", remotePort, shortID(cc.machineID))
		}
	}
	s.mu.RUnlock()
	owner, exists, err := s.store.RemotePortOwner(remotePort)
	if err != nil {
		return 0, false, err
	}
	if exists && owner != machineID {
		return 0, false, fmt.Errorf("远端端口 %d 已被客户端 %s 的持久化映射占用", remotePort, shortID(owner))
	}

	// 检查本机在线服务的名字冲突
	if cc := s.clientByMachine(machineID); cc != nil {
		cc.svcMu.Lock()
		if existing := cc.services[name]; existing != nil && existing.spec.RemotePort != remotePort {
			cc.svcMu.Unlock()
			return 0, false, fmt.Errorf("服务名 %q 已存在", name)
		}
		cc.svcMu.Unlock()
	}

	_ = s.store.RemoveDisabledPort(machineID, remotePort)
	id, err := s.store.AddPortMapping(store.MachineIDAndPort{
		MachineID: machineID, Name: name, LocalIP: localIP,
		LocalPort: localPort, RemotePort: remotePort, Note: note,
	})
	if err != nil {
		return 0, false, fmt.Errorf("持久化映射失败: %w", err)
	}

	spec := protocol.Service{Name: name, LocalIP: localIP, LocalPort: localPort, RemotePort: remotePort}
	acked := false
	if cc := s.clientByMachine(machineID); cc != nil {
		if rs := serviceByPort(cc, remotePort); rs != nil {
			s.stopService(cc, rs.spec.Name) // 同名端口已经在跑：先停掉再用新配置启动
		}
		if _, err := s.startListener(cc, spec, true, id); err != nil {
			_ = s.store.DeletePortMapping(id)
			return 0, false, fmt.Errorf("监听远端端口 %d 失败: %w", remotePort, err)
		}
		s.mu.Lock()
		s.owners[remotePort] = cc.id
		s.mu.Unlock()
		acked, err = s.sendSync(cc, true)
		if err != nil {
			return id, false, err
		}
	}
	log.Printf("[frps] 新增端口映射 :%d -> %s:%d (机器 %s, 在线=%v, 确认=%v)",
		remotePort, localIP, localPort, shortID(machineID), s.clientByMachine(machineID) != nil, acked)
	return id, acked, nil
}

// RemovePort 关闭一条映射：停止监听、断开活跃隧道、更新客户端运行时列表与持久化状态。
func (s *Server) RemovePort(machineID string, remotePort int) error {
	cc := s.clientByMachine(machineID)
	if cc != nil {
		if rs := serviceByPort(cc, remotePort); rs != nil {
			name := rs.spec.Name
			s.stopService(cc, name)
			s.closeTunnelsByService(cc.id, name)
			s.dropPendingByService(cc.id, name)
		}
		_, _ = s.sendSync(cc, true)
	}

	// 持久化：动态映射直接删除；静态配置则记录禁用，避免重连后恢复。
	found := false
	if mappings, err := s.store.ListPortMappings(machineID); err == nil {
		for _, m := range mappings {
			if m.RemotePort == remotePort {
				_ = s.store.DeletePortMapping(m.ID)
				found = true
			}
		}
	}
	if !found {
		_ = s.store.AddDisabledPort(machineID, remotePort)
	}
	log.Printf("[frps] 关闭端口映射 :%d (机器 %s)", remotePort, shortID(machineID))
	return nil
}

// stopService 停止单个映射的监听并清理归属。
func (s *Server) stopService(cc *clientConn, name string) {
	cc.svcMu.Lock()
	rs := cc.services[name]
	delete(cc.services, name)
	delete(cc.dynamicNames, name)
	delete(cc.dbIDs, name)
	cc.svcMu.Unlock()
	if rs == nil {
		return
	}
	rs.stop()
	_ = rs.listener.Close()
	s.mu.Lock()
	if s.owners[rs.spec.RemotePort] == cc.id {
		delete(s.owners, rs.spec.RemotePort)
	}
	s.mu.Unlock()
	log.Printf("[frps] 隧道 %q :%d 已关闭", rs.spec.Name, rs.spec.RemotePort)
}

// Ports 汇总管理端端口视图：在线服务的实际状态 + 持久化映射（含离线）。
func (s *Server) Ports() []PortStatus {
	s.mu.RLock()
	snap := make([]*clientConn, 0, len(s.clients))
	for _, cc := range s.clients {
		snap = append(snap, cc)
	}
	s.mu.RUnlock()

	online := make(map[string]string)
	out := make([]PortStatus, 0)
	for _, cc := range snap {
		online[cc.machineID] = cc.hostname
		cc.svcMu.Lock()
		for _, rs := range cc.services {
			src := "static"
			var dbID int64
			if cc.dynamicNames[rs.spec.Name] {
				src = "dynamic"
				dbID = cc.dbIDs[rs.spec.Name]
			}
			out = append(out, PortStatus{
				MachineID: cc.machineID, Hostname: cc.hostname, Name: rs.spec.Name,
				LocalIP: rs.spec.LocalIP, LocalPort: rs.spec.LocalPort,
				RemotePort: rs.spec.RemotePort, Source: src, Active: true, Online: true, DBID: dbID,
			})
		}
		cc.svcMu.Unlock()
	}

	// 持久化但当前未激活（离线客户端或被关闭）的映射
	if mappings, err := s.store.ListPortMappings(""); err == nil {
		for _, m := range mappings {
			active := false
			for _, p := range out {
				if p.MachineID == m.MachineID && p.RemotePort == m.RemotePort && p.Active {
					active = true
					break
				}
			}
			if active {
				continue
			}
			out = append(out, PortStatus{
				ID: m.ID, MachineID: m.MachineID, Hostname: online[m.MachineID],
				Name: m.Name, LocalIP: m.LocalIP, LocalPort: m.LocalPort,
				RemotePort: m.RemotePort, Source: "dynamic", Active: false,
				Online: online[m.MachineID] != "", DBID: m.ID,
			})
		}
	}
	return out
}

// sendSync 下发权威映射列表，wait=true 时等待客户端 ACK。
func (s *Server) sendSync(cc *clientConn, wait bool) (bool, error) {
	cc.svcMu.Lock()
	specs := make([]protocol.Service, 0, len(cc.services))
	for _, rs := range cc.services {
		specs = append(specs, rs.spec)
	}
	cc.svcMu.Unlock()

	reqID := fmt.Sprintf("sync-%d", s.syncSeq.Add(1))
	var ch chan *protocol.Message
	if wait {
		ch = make(chan *protocol.Message, 1)
		s.acksMu.Lock()
		s.acks[reqID] = ch
		s.acksMu.Unlock()
		defer func() {
			s.acksMu.Lock()
			delete(s.acks, reqID)
			s.acksMu.Unlock()
		}()
	}

	msg := &protocol.Message{Type: protocol.MsgSyncServices, Services: specs, RequestID: reqID}
	if err := s.send(cc, msg); err != nil {
		return false, err
	}
	if !wait {
		return false, nil
	}
	select {
	case ack := <-ch:
		return ack.OK, nil
	case <-time.After(ackTimeout):
		return false, nil
	}
}

func (s *Server) send(cc *clientConn, msg *protocol.Message) error {
	cc.writeMu.Lock()
	defer cc.writeMu.Unlock()
	return protocol.WriteFrame(cc.conn, msg)
}

func (s *Server) sendRaw(conn net.Conn, msg *protocol.Message) error {
	_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
	return protocol.WriteFrame(conn, msg)
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Close 优雅关闭服务端。
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.ln != nil {
			_ = s.ln.Close()
		}
		s.mu.RLock()
		clients := make([]*clientConn, 0, len(s.clients))
		for _, cc := range s.clients {
			clients = append(clients, cc)
		}
		s.mu.RUnlock()
		for _, cc := range clients {
			s.closeClient(cc)
		}
	})
}
