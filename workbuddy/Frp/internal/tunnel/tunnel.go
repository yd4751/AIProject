// Package tunnel 提供两个 net.Conn 之间的双向字节流转发。
package tunnel

import (
	"io"
	"net"
	"sync"
	"time"
)

const bufferSize = 32 * 1024

var bufferPool = sync.Pool{
	New: func() any { return make([]byte, bufferSize) },
}

// halfCloseGrace 是一侧结束后等待另一侧收尾的时间，防止半关闭场景下永久阻塞。
const halfCloseGrace = 5 * time.Second

// Pipe 在 a、b 之间双向转发，直到双方都结束。
//
// 关键点：
//   - 每方向一个 goroutine，互不阻塞；
//   - 一侧 EOF 后对端执行 CloseWrite，保留另一方向的传输能力（TCP 半关闭）；
//   - 另一侧设置读超时作为兜底，避免对端不关闭时 goroutine 泄漏。
func Pipe(a, b net.Conn) {
	Optimize(a)
	Optimize(b)

	done := make(chan struct{}, 2)
	go transfer(a, b, done)
	go transfer(b, a, done)

	<-done // 一个方向结束
	_ = a.SetReadDeadline(time.Now().Add(halfCloseGrace))
	_ = b.SetReadDeadline(time.Now().Add(halfCloseGrace))
	<-done // 等另一个方向结束

	_ = a.Close()
	_ = b.Close()
}

func transfer(dst, src net.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	buf := bufferPool.Get().([]byte)
	_, _ = io.CopyBuffer(dst, src, buf)
	bufferPool.Put(buf)

	// 把 EOF 传递给对端，同时保留反方向的写能力。
	if tc, ok := dst.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	} else {
		_ = dst.Close()
	}
}

// Optimize 调整 TCP 参数以适应高负载长连接。
func Optimize(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
}
