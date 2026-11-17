// Package client 实现 frptun 客户端：运行在内网，主动连接服务端并把请求转发到本地服务。
package client

import (
	"bufio"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"frptun/internal/config"
	"frptun/internal/machine"
	"frptun/internal/protocol"
	"frptun/internal/tunnel"
)

// Client 客户端实例。
type Client struct {
	cfg *config.Config

	connID atomic.Uint64
	closed atomic.Bool

	writeMu sync.Mutex // 保护控制连接的写
	ctrl    net.Conn   // 当前控制连接

	svcMu      sync.RWMutex
	runtimeSvc map[string]protocol.Service // 服务端下发的权威映射表（含实时新增）

	closeOnce sync.Once
	stop      chan struct{}
	wg        sync.WaitGroup
}

// New 创建客户端实例。
func New(cfg *config.Config) *Client {
	c := &Client{cfg: cfg, stop: make(chan struct{}), runtimeSvc: make(map[string]protocol.Service)}
	// 初始用配置里的服务填充运行时表
	for _, svc := range cfg.Services {
		c.runtimeSvc[svc.Name] = protocol.Service(svc)
	}
	return c
}

// Run 启动客户端并持续维持与服务端的连接，直到 Close 被调用。
func (s *Client) Run() error {
	addr := net.JoinHostPort(s.cfg.ServerAddr, strconv.Itoa(s.cfg.ServerPort))
	log.Printf("[frpc] 服务端地址 %s，映射服务 %d 个", addr, len(s.cfg.Services))

	backoff := time.Duration(s.cfg.ReconnectInterval) * time.Second
	maxBackoff := time.Duration(s.cfg.MaxReconnectInterval) * time.Second

	for !s.closed.Load() {
		err := s.session(addr)
		if s.closed.Load() {
			break
		}
		if err != nil {
			log.Printf("[frpc] 会话中断: %v", err)
		}

		// 指数退避 + 抖动，避免断线风暴与惊群。
		wait := backoff
		if wait > 0 {
			jitter := time.Duration(rand.Int63n(int64(wait/2) + 1))
			wait += jitter
		}
		log.Printf("[frpc] %v 后重连...", wait)
		select {
		case <-time.After(wait):
		case <-s.stop:
			return nil
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return nil
}

// session 建立一次完整的控制会话，连接断开时返回错误。
func (s *Client) session(addr string) error {
	dialer := net.Dialer{Timeout: time.Duration(s.cfg.ConnectTimeout) * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("连接服务端失败: %w", err)
	}
	tunnel.Optimize(conn)
	_ = conn.SetDeadline(time.Now().Add(time.Duration(s.cfg.ConnectTimeout) * time.Second))

	if err := protocol.WriteKind(conn, protocol.KindControl); err != nil {
		_ = conn.Close()
		return err
	}
	hostname, _ := os.Hostname()
	if err := protocol.WriteFrame(conn, &protocol.Message{
		Type: protocol.MsgAuth, Token: s.cfg.Token,
		MachineID: machine.ID(), Hostname: hostname,
	}); err != nil {
		_ = conn.Close()
		return err
	}

	br := bufio.NewReader(conn)
	resp, err := protocol.ReadFrame(br)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("读取认证响应失败: %w", err)
	}
	if !resp.OK {
		_ = conn.Close()
		return fmt.Errorf("认证被拒绝: %s", resp.Error)
	}

	services := make([]protocol.Service, 0, len(s.cfg.Services))
	for _, svc := range s.cfg.Services {
		services = append(services, protocol.Service(svc))
	}
	if err := protocol.WriteFrame(conn, &protocol.Message{
		Type: protocol.MsgRegister, Services: services,
	}); err != nil {
		_ = conn.Close()
		return err
	}
	resp, err = protocol.ReadFrame(br)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("读取注册响应失败: %w", err)
	}
	if !resp.OK {
		_ = conn.Close()
		return fmt.Errorf("注册服务被拒绝: %s", resp.Error)
	}

	// 注册成功，进入长连接模式。
	_ = conn.SetDeadline(time.Time{})
	s.writeMu.Lock()
	s.ctrl = conn
	s.writeMu.Unlock()
	defer func() {
		s.writeMu.Lock()
		if s.ctrl == conn {
			s.ctrl = nil
		}
		s.writeMu.Unlock()
		_ = conn.Close()
	}()

	log.Printf("[frpc] 已连接服务端，%d 个隧道注册成功", len(services))

	heartbeatDone := make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.heartbeat(heartbeatDone)
	}()
	defer close(heartbeatDone)

	for {
		msg, err := protocol.ReadFrame(br)
		if err != nil {
			return fmt.Errorf("控制连接读取失败: %w", err)
		}
		switch msg.Type {
		case protocol.MsgNewConn:
			s.wg.Add(1)
			go func(m *protocol.Message) {
				defer s.wg.Done()
				s.handleNewConn(m)
			}(msg)
		case protocol.MsgHeartbeatAck:
			// 保活确认，无需处理
		case protocol.MsgSyncServices:
			s.applySync(msg)
		}
	}
}

// applySync 用服务端下发的权威映射表替换运行时表，并回复确认。
func (s *Client) applySync(msg *protocol.Message) {
	s.svcMu.Lock()
	s.runtimeSvc = make(map[string]protocol.Service, len(msg.Services))
	for _, svc := range msg.Services {
		s.runtimeSvc[svc.Name] = svc
	}
	s.svcMu.Unlock()
	log.Printf("[frpc] 已同步端口映射 %d 个", len(msg.Services))

	ack := &protocol.Message{Type: protocol.MsgServiceAck, OK: true, RequestID: msg.RequestID}
	s.writeMu.Lock()
	conn := s.ctrl
	var err error
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err = protocol.WriteFrame(conn, ack)
	}
	s.writeMu.Unlock()
	if err != nil && conn != nil {
		_ = conn.Close()
	}
}

// heartbeat 周期性发送心跳，写入失败即关闭连接以触发重连。
func (s *Client) heartbeat(done <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(s.cfg.HeartbeatInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.writeMu.Lock()
			conn := s.ctrl
			var err error
			if conn != nil {
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err = protocol.WriteFrame(conn, &protocol.Message{Type: protocol.MsgHeartbeat})
			}
			s.writeMu.Unlock()
			if err != nil {
				if conn != nil {
					_ = conn.Close()
				}
				return
			}
		case <-done:
			return
		case <-s.stop:
			return
		}
	}
}

// handleNewConn 为一条公网连接建立"服务端数据通道 + 本地服务"的双向隧道。
func (s *Client) handleNewConn(msg *protocol.Message) {
	// 解析服务名 -> 本地地址（优先使用服务端下发的运行时映射表）
	var localAddr string
	s.svcMu.RLock()
	if svc, ok := s.runtimeSvc[msg.ServiceName]; ok {
		localAddr = net.JoinHostPort(svc.LocalIP, strconv.Itoa(svc.LocalPort))
	}
	s.svcMu.RUnlock()
	if localAddr == "" {
		// 回退到静态配置，兼容尚未下发同步的旧服务端
		for _, svc := range s.cfg.Services {
			if svc.Name == msg.ServiceName {
				localAddr = net.JoinHostPort(svc.LocalIP, strconv.Itoa(svc.LocalPort))
				break
			}
		}
	}
	if localAddr == "" {
		log.Printf("[frpc] connID=%d 找不到服务 %q", msg.ConnID, msg.ServiceName)
		s.notifyClose(msg.ConnID)
		return
	}

	// 1) 拨号服务端数据通道并上报 connID
	dialer := net.Dialer{Timeout: time.Duration(s.cfg.ConnectTimeout) * time.Second}
	dataConn, err := dialer.Dial("tcp", net.JoinHostPort(s.cfg.ServerAddr, strconv.Itoa(s.cfg.ServerPort)))
	if err != nil {
		log.Printf("[frpc] connID=%d 连接服务端数据通道失败: %v", msg.ConnID, err)
		s.notifyClose(msg.ConnID)
		return
	}
	if err := protocol.WriteKind(dataConn, protocol.KindData); err != nil {
		_ = dataConn.Close()
		s.notifyClose(msg.ConnID)
		return
	}
	if err := protocol.WriteConnID(dataConn, msg.ConnID); err != nil {
		_ = dataConn.Close()
		s.notifyClose(msg.ConnID)
		return
	}
	_ = dataConn.SetDeadline(time.Time{})

	// 2) 拨号本地服务
	localConn, err := dialer.Dial("tcp", localAddr)
	if err != nil {
		log.Printf("[frpc] connID=%d 连接本地服务 %s 失败: %v", msg.ConnID, localAddr, err)
		_ = dataConn.Close()
		s.notifyClose(msg.ConnID)
		return
	}

	// 3) 双向转发
	tunnel.Pipe(dataConn, localConn)
}

// notifyClose 尽力通知服务端释放一条配对超时的连接。
func (s *Client) notifyClose(connID uint64) {
	s.writeMu.Lock()
	conn := s.ctrl
	var err error
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err = protocol.WriteFrame(conn, &protocol.Message{Type: protocol.MsgCloseConn, ConnID: connID})
	}
	s.writeMu.Unlock()
	if err != nil && conn != nil {
		_ = conn.Close()
	}
}

// Close 优雅关闭客户端。
func (s *Client) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.stop)
		s.writeMu.Lock()
		if s.ctrl != nil {
			_ = s.ctrl.Close()
		}
		s.writeMu.Unlock()
	})
	s.wg.Wait()
}
