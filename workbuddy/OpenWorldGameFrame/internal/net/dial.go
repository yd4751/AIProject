package net

import (
	"net"
	"time"
)

// Dial 主动建立一条连接，返回的 Conn 已经在后台跑读写协程。
//
// 和 Server 侧的 Conn 是同一个类型：节点间链路、压测工具、未来的跨区
// 转发都走这一套封帧和队列，不在每个调用点自己写一遍 read 循环。
// queueSize 为 0 时用默认深度。
func Dial(addr string, log logSink, timeout time.Duration, queueSize int, onMessage func(*Conn, uint16, []byte), onClose func(*Conn)) (*Conn, error) {
	raw, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := raw.(*net.TCPConn); ok {
		// 节点间消息是低频小包（迁移握手、会话转发），延迟敏感，
		// 关掉 Nagle 免得被攒够 40ms 才发出去。
		_ = tc.SetNoDelay(true)
	}
	c := newConnSized(0, raw, queueSize, onMessage, onClose)
	go c.Serve()
	if log != nil {
		log.Info("tcp dial ok", "addr", addr, "remote", raw.RemoteAddr().String())
	}
	return c, nil
}

// logSink 是 Dial 需要的最小日志接口，避免把 *slog.Logger 硬编进签名。
type logSink interface {
	Info(msg string, args ...any)
}
