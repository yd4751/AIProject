package net

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
)

// Server 是一个基于 TCP 的连接接入器。
//
// 它只负责 accept 和连接生命周期，不含任何业务逻辑，
// 消息处理通过 OnMessage 回调交给上层。这样 Gate 和压测工具可以复用同一套代码。
type Server struct {
	addr string
	log  *slog.Logger

	onMessage func(*Conn, uint16, []byte)
	onClose   func(*Conn)

	ln     net.Listener
	nextID atomic.Uint64
	conns  sync.Map // connID -> *Conn

	// queueSize 是每条连接发送队列的深度。0 表示用默认值。
	queueSize int

	closed   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	active atomic.Int64
	total  atomic.Int64
}

// NewServer 创建接入器。addr 形如 ":7001"。
func NewServer(addr string, log *slog.Logger, onMessage func(*Conn, uint16, []byte), onClose func(*Conn)) *Server {
	return NewServerQueued(addr, log, 0, onMessage, onClose)
}

// NewServerQueued 与 NewServer 相同，但可以指定发送队列深度。
//
// 节点间链路要更大的队列：它同时承载迁移握手和成千上万条转发包，
// 队列一满就丢的帧里可能夹着正在握手的 Prepare。加大队列把
// "写入方偶尔被调度器饿死"这段抖动吸收掉，而不是让它变成丢包。
func NewServerQueued(addr string, log *slog.Logger, queueSize int, onMessage func(*Conn, uint16, []byte), onClose func(*Conn)) *Server {
	return &Server{
		addr:      addr,
		log:       log,
		onMessage: onMessage,
		onClose:   onClose,
		queueSize: queueSize,
		closed:    make(chan struct{}),
	}
}

// Start 开始监听并接受连接。满足 core.Component 的签名约定。
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.log.Info("tcp server listening", "addr", ln.Addr().String())

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		raw, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("accept failed", "err", err)
			continue
		}

		// 关掉 Nagle，游戏包要低延迟。
		if tc, ok := raw.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
		}

		id := s.nextID.Add(1)
		c := newConnSized(id, raw, s.queueSize, s.onMessage, func(c *Conn) {
			s.conns.Delete(c.ID)
			s.active.Add(-1)
			if s.onClose != nil {
				s.onClose(c)
			}
		})
		s.conns.Store(id, c)
		s.active.Add(1)
		s.total.Add(1)

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			c.Serve()
		}()
	}
}

// Stop 关闭监听并断开所有在连连接。
func (s *Server) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		close(s.closed)
		if s.ln != nil {
			_ = s.ln.Close()
		}
		s.conns.Range(func(_, v any) bool {
			v.(*Conn).Close()
			return true
		})
	})

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ActiveConns 返回当前在线连接数。
func (s *Server) ActiveConns() int64 { return s.active.Load() }

// TotalConns 返回累计接受过的连接数。
func (s *Server) TotalConns() int64 { return s.total.Load() }

// Addr 返回实际监听地址（端口为 0 时由系统分配）。
func (s *Server) Addr() string {
	if s.ln == nil {
		return s.addr
	}
	return s.ln.Addr().String()
}
