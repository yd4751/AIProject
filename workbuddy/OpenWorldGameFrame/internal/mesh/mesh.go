package mesh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/net"
)

// dialTimeout 是拨号上限。
//
// 取 1 秒而不是 3 秒：迁移的 Prepare 只给对方 3 秒（handoverTimeout），
// 拨号最多只能用掉其中三分之一，剩下的要留给对端处理。超过 1 秒还没连上，
// 这次迁移注定失败，早点失败比卡在边界上等强。
const dialTimeout = time.Second

// peerQueueSize 是节点间链路的发送队列深度，比客户端连接的 256 大得多。
//
// 一条节点链路同时承载迁移握手、上行转发和全部跨节点玩家的下行广播，
// 峰值帧率是客户端连接的十倍以上。用 256 时实测一小时级别的压测里
// 队列溢出过 299 次——丢掉的帧里既有广播也有玩家的位移包，
// 后者会让服务端和客户端的位置短暂错位（表现为一串 moveRejected）。
//
// 4096 帧 × 8 字节的槽位 = 32KB/链路，对"节点数是个位数"的部署可以忽略。
const peerQueueSize = 4096

// ErrUnknownPeer 表示目标节点没有配置地址。
//
// 单独定义而不是返回 nil：多节点部署时"忘了配 peers"是最常见的错误，
// 静默成功会让消息凭空消失，然后表现为"玩家走到边界就卡住"。
var ErrUnknownPeer = errors.New("mesh: unknown peer node")

// ErrNotRunning 表示 Mesh 还没启动或已经停止。
var ErrNotRunning = errors.New("mesh: not running")

// Handler 接收本节点收到的所有节点间消息。
//
// 一条消息一个回调，不做分发注册表：节点间只有六七种消息，
// 一个 switch 就写完了，而注册表会引入"忘了注册导致消息静默丢失"的坑。
type Handler interface {
	Deliver(msgID uint16, body []byte)
}

// Mesh 是一个节点的收发端点。
type Mesh struct {
	self string
	log  *slog.Logger

	mu    sync.RWMutex
	links map[string]*link
	srv   *net.Server

	handler atomic.Value // Handler

	running atomic.Bool
	sent    atomic.Int64
	recv    atomic.Int64
	dropped atomic.Int64
	// controlDropped 是控制/握手消息被丢弃的次数。
	// 理想情况下恒为 0：拥塞时应该只丢数据帧，不丢握手。
	controlDropped atomic.Int64
	sendFail       atomic.Int64
	dialFail       atomic.Int64

	// batched 统计下行批量合并的效果，供 /admin/state 展示。
	batched      atomic.Int64 // 实际打包成批的下行消息条数
	batchedFlush atomic.Int64 // 发出的批次数

	// flushTicker 驱动下行批量合并的定期冲刷。
	flushTicker *time.Ticker
	flushStop   chan struct{}
	flushOnce   sync.Once
}

// link 是到某个对等节点的一条出向连接。
type link struct {
	mu   sync.Mutex
	name string
	addr string
	conn *net.Conn

	// batch 是发给这个节点的下行消息聚合缓冲。
	// 只承载 MsgClientSend，攒够一批或过了 flush 窗口就合并成一条 MsgClientBatch。
	batchMu    sync.Mutex
	batchItems []ClientSend
	batchBytes int
}

// New 创建端点。self 是本节点标识，必须与拓扑配置里的节点名一致。
func New(self string, log *slog.Logger) *Mesh {
	if log == nil {
		log = slog.Default()
	}
	return &Mesh{
		self:      self,
		log:       log,
		links:     make(map[string]*link),
		flushStop: make(chan struct{}),
	}
}

// batchWindow 是下行批量合并的最大驻留时间。
//
// 取 2ms：游戏下行目标是低延迟，攒批不能把包扣在手里太久。2ms 窗口内
// 同一批广播（同一个 tick 里多个玩家）大概率已经全部到齐，合并后发出去，
// 延迟损失远小于一帧（50ms）。没有这一层的话，每个广播包都是
// 一次组帧 + 一次锁竞争，跨节点玩家上千时线性放大。
const batchWindow = 2 * time.Millisecond

// batchMaxItems 是单批的最大条数，防止一个极端 tick 里攒出巨批。
const batchMaxItems = 256

// Self 返回本节点标识。
func (m *Mesh) Self() string { return m.self }

// SetHandler 设置入向消息的处理者。必须在 Start 之前调用。
func (m *Mesh) SetHandler(h Handler) {
	m.handler.Store(&handlerBox{h: h})
}

// handlerBox 包一层，避免 typed-nil 把一个 nil 指针的 Handler 存成非 nil 接口。
type handlerBox struct{ h Handler }

// AddPeer 登记一个对等节点的地址。
//
// 只登记地址，不建立连接：连接在第一次真正要发消息时才拨。
// 节点数多于实际场景跨节点需求时，这样能省掉一批永远不用的连接。
//
// 地址变化时必须断开旧连接：M3.4 之后地址来自注册中心，节点换机器重启
// 是很普通的运维动作。只改 addr 不关连接的话，那条已经指向旧机器的
// TCP 会一直被复用——表现为"注册中心里地址是新的，消息还发到旧的"。
func (m *Mesh) AddPeer(node, addr string) {
	if node == "" || node == m.self || addr == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.links[node]; ok {
		l.mu.Lock()
		if l.addr != addr && l.conn != nil {
			l.conn.Close()
			l.conn = nil
			m.log.Info("peer address changed, link reset", "node", node, "old", l.addr, "new", addr)
		}
		l.addr = addr
		l.mu.Unlock()
		return
	}
	m.links[node] = &link{name: node, addr: addr}
}

// Peers 返回已配置的对等节点地址表。
func (m *Mesh) Peers() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.links))
	for n, l := range m.links {
		l.mu.Lock()
		out[n] = l.addr
		l.mu.Unlock()
	}
	return out
}

// Start 开始在 listenAddr 上接受其它节点的连接。
func (m *Mesh) Start(ctx context.Context, listenAddr string) error {
	if listenAddr == "" {
		return errors.New("mesh: empty listen address")
	}
	m.srv = net.NewServerQueued(listenAddr, m.log, peerQueueSize, func(_ *net.Conn, msgID uint16, body []byte) {
		m.dispatch(msgID, body)
	}, func(c *net.Conn) {
		m.log.Info("peer link closed", "remote", c.Remote)
	})
	if err := m.srv.Start(ctx); err != nil {
		return err
	}
	m.running.Store(true)
	m.flushTicker = time.NewTicker(batchWindow)
	go m.flushLoop()
	return nil
}

// Stop 关闭监听和所有出向连接。
func (m *Mesh) Stop(ctx context.Context) error {
	m.running.Store(false)
	m.flushOnce.Do(func() { close(m.flushStop) })
	if m.flushTicker != nil {
		m.flushTicker.Stop()
	}
	m.mu.Lock()
	links := make([]*link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.Unlock()
	for _, l := range links {
		l.mu.Lock()
		if l.conn != nil {
			l.conn.Close()
			l.conn = nil
		}
		l.mu.Unlock()
	}
	if m.srv != nil {
		return m.srv.Stop(ctx)
	}
	return nil
}

// Addr 返回监听地址。
func (m *Mesh) Addr() string {
	if m.srv == nil {
		return ""
	}
	return m.srv.Addr()
}

func (m *Mesh) dispatch(msgID uint16, body []byte) {
	m.recv.Add(1)
	v := m.handler.Load()
	if v == nil {
		m.log.Warn("peer message dropped: no handler", "msgID", fmt.Sprintf("0x%04X", msgID))
		return
	}
	box := v.(*handlerBox)
	if box.h == nil {
		m.log.Warn("peer message dropped: nil handler", "msgID", fmt.Sprintf("0x%04X", msgID))
		return
	}
	box.h.Deliver(msgID, body)
}

// Send 把一条消息投给 node。
//
// 用 TrySend 而不是 Send：链路拥塞时只丢当前这一帧，不断开连接。
// 断开会把队列里排着的两三百帧一起丢掉，而里面可能有正在握手的 Prepare
// （实测就这么丢过 8 次迁移，表现为源场景 3 秒超时回滚，日志里只有
// "handover aborted: timeout"，看不出和拥塞有关系）。
//
// 丢帧不重试：迁移有超时兜底，数据帧下一帧就补上了。重试会把
// 3 秒的迁移预算耗在一条已经堵住的链路上。
func (m *Mesh) Send(node string, msgID uint16, body []byte) error {
	if node == m.self || node == "" {
		return nil // 本节点走 mailbox，不该到这里
	}
	// 跨节点下行广播走批量合并：攒进 link 的聚合缓冲，由 flushLoop
	// 定期合并成 MsgClientBatch 一次发出。其余消息走即时发送。
	if msgID == MsgClientSend {
		item, ok := DecodeClientSend(body)
		if !ok {
			m.sendFail.Add(1)
			return errors.New("mesh: bad client send body")
		}
		return m.enqueueBatch(node, *item)
	}
	return m.sendRaw(node, msgID, body)
}

// sendRaw 是即时发送路径：控制/握手、上行转发、会话重绑等。
func (m *Mesh) sendRaw(node string, msgID uint16, body []byte) error {
	m.mu.RLock()
	l, ok := m.links[node]
	m.mu.RUnlock()
	if !ok {
		m.sendFail.Add(1)
		return fmt.Errorf("%w: %s", ErrUnknownPeer, node)
	}

	c, err := m.conn(l)
	if err != nil {
		m.dialFail.Add(1)
		return err
	}
	if err := c.TrySendPriority(msgID, body, isControlMsg(msgID)); err != nil {
		if errors.Is(err, net.ErrSendQueueFull) {
			// 拥塞丢帧：链路是好的，只是对端一时消化不了。
			m.dropped.Add(1)
			if isControlMsg(msgID) {
				m.controlDropped.Add(1)
			}
			return err
		}
		// 连接坏了就丢掉，下一次重新拨。
		l.mu.Lock()
		if l.conn == c {
			l.conn = nil
		}
		l.mu.Unlock()
		c.Close()
		m.sendFail.Add(1)
		return err
	}
	m.sent.Add(1)
	return nil
}

// SendClientSend 把一条跨节点下行消息投给接入节点，走批量合并。
//
// 它是 Send(node, MsgClientSend, ...) 的零拷贝版本：直接接收结构，
// 不做 encode→decode 的往返。remoteClient.Send 的每一条下行广播都走这里，
// 是热路径——省掉一次 Encode + 一次 Decode（含 body 拷贝）值得。
func (m *Mesh) SendClientSend(ref ClientRef, msgID uint16, body []byte) error {
	if ref.Node == m.self || ref.Node == "" {
		return nil // 本地会话不该走 mesh
	}
	return m.enqueueBatch(ref.Node, ClientSend{Client: ref, MsgID: msgID, Body: body})
}

// enqueueBatch 把一条下行消息攒进目标节点的聚合缓冲。
//
// item 由调用方构造（SendClientSend 直接传结构，Send 的 MsgClientSend 分支
// 先 Decode 一次）。攒够 batchMaxItems 条就立即冲刷，否则留给 flushLoop。
func (m *Mesh) enqueueBatch(node string, item ClientSend) error {
	m.mu.RLock()
	l, ok := m.links[node]
	m.mu.RUnlock()
	if !ok {
		m.sendFail.Add(1)
		return fmt.Errorf("%w: %s", ErrUnknownPeer, node)
	}

	l.batchMu.Lock()
	l.batchItems = append(l.batchItems, item)
	l.batchBytes += len(item.Body)
	overflow := len(l.batchItems) >= batchMaxItems
	l.batchMu.Unlock()

	if overflow {
		m.flushLink(l)
	}
	return nil
}

// flushLoop 定期冲刷所有聚合缓冲。
func (m *Mesh) flushLoop() {
	for {
		select {
		case <-m.flushStop:
			return
		case <-m.flushTicker.C:
			m.flushAll()
		}
	}
}

// flushAll 冲刷所有有积压的 link。
func (m *Mesh) flushAll() {
	m.mu.RLock()
	links := make([]*link, 0, len(m.links))
	for _, l := range m.links {
		links = append(links, l)
	}
	m.mu.RUnlock()
	for _, l := range links {
		m.flushLink(l)
	}
}

// flushLink 取出某节点的积压，合并成一条 MsgClientBatch 发出。
func (m *Mesh) flushLink(l *link) {
	l.batchMu.Lock()
	if len(l.batchItems) == 0 {
		l.batchMu.Unlock()
		return
	}
	items := l.batchItems
	l.batchItems = nil
	l.batchBytes = 0
	l.batchMu.Unlock()

	body := EncodeClientBatch(&ClientBatch{Items: items})
	m.batched.Add(int64(len(items)))
	m.batchedFlush.Add(1)
	_ = m.sendRaw(l.name, MsgClientBatch, body)
}

// isControlMsg 判断一条节点间消息属于"控制/握手"还是"数据"。
//
// 控制/握手消息（迁移三阶段、场景控制、拓扑摘要、会话重绑、ID 回收）
// 走高优先队列：拥塞时宁可丢数据帧也不能丢它们——丢一帧 Prepare 是一次
// 失败的迁移（3 秒超时回滚），丢一帧拓扑 Move 会让全网路由永久不一致。
//
// 数据消息（上行转发、跨节点下行广播）走低优先队列：丢一帧的代价是
// 玩家少看一次位置更新，下一帧就补上了，没有跨帧状态。
//
// 默认返回 true（高优先）：宁可多给几个消息号走快通道，也不能让一条
// 新加的握手类消息因为漏进这个 switch 而和广播帧一起被挤掉。
func isControlMsg(msgID uint16) bool {
	switch msgID {
	case MsgRelay, MsgClientSend, MsgClientBatch:
		return false
	default:
		return true
	}
}

// conn 返回可用的连接，必要时拨一条新的。
func (m *Mesh) conn(l *link) (*net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil && !l.conn.Closed() {
		return l.conn, nil
	}
	if !m.running.Load() {
		return nil, ErrNotRunning
	}
	c, err := net.Dial(l.addr, m.log, dialTimeout, peerQueueSize, func(_ *net.Conn, msgID uint16, body []byte) {
		// 对端也往这条连接上发消息（双向各拨一条时不会走到这里，
		// 但一条连接被两端复用的情况要能处理）。
		m.dispatch(msgID, body)
	}, func(*net.Conn) {})
	if err != nil {
		return nil, err
	}
	l.conn = c
	return c, nil
}

// Stat 是节点间链路的统计快照，供 /admin/state 展示。
type Stat struct {
	Self    string            `json:"self"`
	Addr    string            `json:"addr"`
	Peers   map[string]string `json:"peers"`
	Sent    int64             `json:"sent"`
	Recv    int64             `json:"recv"`
	Dropped int64             `json:"dropped"`
	// ControlDropped 是控制/握手消息被丢弃的次数，恒为 0 才算健康。
	ControlDropped int64 `json:"controlDropped"`
	SendFail       int64 `json:"sendFail"`
	DialFail       int64 `json:"dialFail"`
	// Batched 是下行批量合并的消息条数，BatchedFlush 是发出的批次数。
	// 两者相除就是平均每批的条数，越大说明合并越有效。
	Batched      int64 `json:"batched"`
	BatchedFlush int64 `json:"batchedFlush"`
}

// Stat 返回统计快照。
func (m *Mesh) Stat() Stat {
	return Stat{
		Self:           m.self,
		Addr:           m.Addr(),
		Peers:          m.Peers(),
		Sent:           m.sent.Load(),
		Recv:           m.recv.Load(),
		Dropped:        m.dropped.Load(),
		ControlDropped: m.controlDropped.Load(),
		SendFail:       m.sendFail.Load(),
		DialFail:       m.dialFail.Load(),
		Batched:        m.batched.Load(),
		BatchedFlush:   m.batchedFlush.Load(),
	}
}
