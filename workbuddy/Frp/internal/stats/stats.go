// Package stats 提供逐连接的字节计数与活跃连接实时注册表。
package stats

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Counter 原子字节计数器，方向以"公网访问者"视角为准：
// Up = 访问者上传（服务端从 userConn 读），Down = 访问者下载（服务端向 userConn 写）。
type Counter struct {
	up   atomic.Int64
	down atomic.Int64
}

func (c *Counter) AddUp(n int64)   { c.up.Add(n) }
func (c *Counter) AddDown(n int64) { c.down.Add(n) }
func (c *Counter) Up() int64       { return c.up.Load() }
func (c *Counter) Down() int64     { return c.down.Load() }

// countedConn 在 net.Conn 上叠加计数：Read 记为 Up，Write 记为 Down。
type countedConn struct {
	net.Conn
	c *Counter
}

func Wrap(c net.Conn, counter *Counter) net.Conn {
	return &countedConn{Conn: c, c: counter}
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.c.AddUp(int64(n))
	}
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.c.AddDown(int64(n))
	}
	return n, err
}

// LiveConn 一条当前活跃的隧道连接。
type LiveConn struct {
	ID         uint64 `json:"id"`
	MachineID  string `json:"machine_id"`
	Hostname   string `json:"hostname"`
	Service    string `json:"service"`
	RemoteIP   string `json:"remote_ip"`
	LocalAddr  string `json:"local_addr"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	DurationMs int64  `json:"duration_ms"`

	counter *Counter
	start   time.Time
}

// Hub 维护当前活跃连接注册表，供管理端实时查询。
type Hub struct {
	mu    sync.RWMutex
	conns map[uint64]*LiveConn
}

func NewHub() *Hub { return &Hub{conns: make(map[uint64]*LiveConn)} }

// Add 注册一条新连接，返回对应的 LiveConn 与计数器。
func (h *Hub) Add(id uint64, machineID, hostname, service, remoteIP, localAddr string) (*LiveConn, *Counter) {
	c := &Counter{}
	lc := &LiveConn{
		ID: id, MachineID: machineID, Hostname: hostname, Service: service,
		RemoteIP: remoteIP, LocalAddr: localAddr, counter: c, start: time.Now(),
	}
	h.mu.Lock()
	h.conns[id] = lc
	h.mu.Unlock()
	return lc, c
}

// Snapshot 刷新计数后返回所有活跃连接的快照。
func (h *Hub) Snapshot() []LiveConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]LiveConn, 0, len(h.conns))
	now := time.Now()
	for _, lc := range h.conns {
		lc.Up = lc.counter.Up()
		lc.Down = lc.counter.Down()
		lc.DurationMs = now.Sub(lc.start).Milliseconds()
		out = append(out, *lc)
	}
	return out
}

// Remove 移除连接并回填最终计数。
func (h *Hub) Remove(id uint64) (finalUp, finalDown, durationMs int64) {
	h.mu.Lock()
	lc := h.conns[id]
	delete(h.conns, id)
	h.mu.Unlock()
	if lc != nil {
		return lc.counter.Up(), lc.counter.Down(), time.Since(lc.start).Milliseconds()
	}
	return 0, 0, 0
}

// Count 当前活跃连接数。
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}
