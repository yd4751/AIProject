package net

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"openworld/internal/protocol"
)

// SendQueueSize 是单连接待发送帧的缓冲深度。
// 60 帧约为 3 秒的量，超过说明客户端消费不过来，直接断开比堆积更安全。
const SendQueueSize = 256

// Conn 封装一条客户端连接。
//
// 并发模型：读一个 goroutine、写一个 goroutine，业务层通过 Send 投递，
// 不直接在任意 goroutine 里写 socket。
type Conn struct {
	ID     uint64
	Remote string

	raw   net.Conn
	sendq chan []byte // 高优先：控制 / 握手，拥塞时也要尽量送达
	// sendqLow 是低优先数据帧的队列。拥塞时先丢它，保证高优先帧不被挤掉。
	sendqLow chan []byte
	done     chan struct{}
	once     sync.Once

	onMessage func(*Conn, uint16, []byte)
	onClose   func(*Conn)

	bytesIn    atomic.Int64
	bytesOut   atomic.Int64
	framesOut  atomic.Int64
	writeErrs  atomic.Int64
	closedFlag atomic.Bool
}

func newConn(id uint64, raw net.Conn, onMsg func(*Conn, uint16, []byte), onClose func(*Conn)) *Conn {
	return newConnSized(id, raw, 0, onMsg, onClose)
}

// newConnSized 允许指定发送队列深度，0 表示用默认值。
func newConnSized(id uint64, raw net.Conn, queueSize int, onMsg func(*Conn, uint16, []byte), onClose func(*Conn)) *Conn {
	if queueSize <= 0 {
		queueSize = SendQueueSize
	}
	return &Conn{
		ID:        id,
		Remote:    raw.RemoteAddr().String(),
		raw:       raw,
		sendq:     make(chan []byte, queueSize),
		sendqLow:  make(chan []byte, queueSize),
		done:      make(chan struct{}),
		onMessage: onMsg,
		onClose:   onClose,
	}
}

// Serve 阻塞运行读循环，直到连接断开。写循环在内部协程里跑。
func (c *Conn) Serve() {
	go c.writeLoop()
	c.readLoop()
}

func (c *Conn) readLoop() {
	defer c.Close()

	hdr := make([]byte, protocol.HeaderSize)
	// 读超时要大于心跳间隔，否则正常在线玩家会被误杀。
	// 心跳 10s 一次，这里给 90s 冗余。
	const readTimeout = 90 * time.Second

	for {
		_ = c.raw.SetReadDeadline(time.Now().Add(readTimeout))

		if _, err := io.ReadFull(c.raw, hdr); err != nil {
			return
		}
		msgID, bodyLen, ok := UnpackHeader(hdr)
		if !ok {
			return
		}

		var body []byte
		if bodyLen > 0 {
			body = make([]byte, bodyLen)
			if _, err := io.ReadFull(c.raw, body); err != nil {
				return
			}
		}
		c.bytesIn.Add(int64(protocol.HeaderSize + bodyLen))

		// 回调必须在当前 goroutine 同步执行，保证同一连接的消息严格有序。
		if c.onMessage != nil {
			c.onMessage(c, msgID, body)
		}
	}
}

func (c *Conn) writeLoop() {
	for {
		// 先排空高优先队列，再处理低优先数据帧。用非阻塞读保证：
		// 只要有高优先帧在排队，数据帧就得让路——这才是"优先"的含义。
		select {
		case frame := <-c.sendq:
			if !c.writeFrame(frame) {
				return
			}
			continue
		default:
		}
		select {
		case frame := <-c.sendq:
			if !c.writeFrame(frame) {
				return
			}
		case frame := <-c.sendqLow:
			if !c.writeFrame(frame) {
				return
			}
		case <-c.done:
			return
		}
	}
}

// writeFrame 写一帧；失败时关闭连接并返回 false（调用方应退出写循环）。
func (c *Conn) writeFrame(frame []byte) bool {
	_ = c.raw.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.raw.Write(frame); err != nil {
		c.writeErrs.Add(1)
		c.Close()
		return false
	}
	c.bytesOut.Add(int64(len(frame)))
	c.framesOut.Add(1)
	return true
}

// Send 把消息投递到发送队列。非阻塞：队列满说明对端跟不上，立刻断开。
func (c *Conn) Send(msgID uint16, body []byte) error {
	return c.SendPriority(msgID, body, true)
}

// SendPriority 与 Send 相同，但可以指定优先级。
//
// high=false 的消息进低优先队列，在链路拥塞时先于高优先消息被丢弃。
// 客户端连接（gate）只有一种流量，全部走高优先，行为和原来一致；
// 只有节点间链路会用 low 通道承载数据帧。
func (c *Conn) SendPriority(msgID uint16, body []byte, high bool) error {
	if err := c.TrySendPriority(msgID, body, high); err != nil {
		if errors.Is(err, ErrSendQueueFull) {
			// 客户端跟不上就断开：宁可踢掉一个卡住的玩家，
			// 也不能让内存无限堆积（他的每一个包都在制造广播）。
			c.Close()
		}
		return err
	}
	return nil
}

// TrySend 与 Send 相同，但队列满时**只丢弃这一帧，不断开连接**。
//
// 节点间链路必须用这个：一条链路同时承载迁移握手和成千上万条转发包，
// 拥堵时按"关连接"处理会把队列里那两三百帧一起丢掉——其中包括正在
// 握手的 Prepare。实测就是这样丢了 8 次迁移：数据帧把队列顶满，
// 连接被关，排在后面的 Prepare 一起蒸发，源场景 3 秒后超时回滚。
//
// 丢一帧数据包的代价是玩家少看一次别人的位置更新，下一帧就补上了；
// 丢一帧 Prepare 的代价是一次失败的迁移。两者不该用同一种策略。
func (c *Conn) TrySend(msgID uint16, body []byte) error {
	return c.TrySendPriority(msgID, body, true)
}

// TrySendPriority 与 TrySend 相同，但可以指定优先级。
// high=false 的消息进低优先队列，拥塞时最先被丢弃。
func (c *Conn) TrySendPriority(msgID uint16, body []byte, high bool) error {
	if c.closedFlag.Load() {
		return ErrClosed
	}
	frame := PackFrame(msgID, body)
	if high {
		select {
		case c.sendq <- frame:
			return nil
		case <-c.done:
			return ErrClosed
		default:
			return ErrSendQueueFull
		}
	}
	select {
	case c.sendqLow <- frame:
		return nil
	case <-c.done:
		return ErrClosed
	default:
		return ErrSendQueueFull
	}
}

// Close 幂等关闭，会触发一次 onClose 回调。
func (c *Conn) Close() {
	c.once.Do(func() {
		c.closedFlag.Store(true)
		close(c.done)
		_ = c.raw.Close()
		if c.onClose != nil {
			c.onClose(c)
		}
	})
}

// Closed 返回连接是否已关闭。
func (c *Conn) Closed() bool { return c.closedFlag.Load() }

// Stats 返回这条连接的收发统计，供 /admin/state 展示。
func (c *Conn) Stats() (bytesIn, bytesOut, framesOut int64) {
	return c.bytesIn.Load(), c.bytesOut.Load(), c.framesOut.Load()
}
