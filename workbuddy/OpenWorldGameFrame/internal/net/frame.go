package net

import (
	"encoding/binary"
	"errors"

	"openworld/internal/protocol"
)

var (
	// ErrClosed 表示连接已关闭。
	ErrClosed = errors.New("net: connection closed")
	// ErrSendQueueFull 表示发送队列积压超过水位，连接被主动断开。
	ErrSendQueueFull = errors.New("net: send queue full")
)

// PackFrame 按协议格式组帧。返回新分配的切片，调用方可以安全持有。
func PackFrame(msgID uint16, body []byte) []byte {
	frame := make([]byte, protocol.HeaderSize+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(2+len(body)))
	binary.BigEndian.PutUint16(frame[4:6], msgID)
	copy(frame[protocol.HeaderSize:], body)
	return frame
}

// UnpackHeader 从 6 字节帧头解析出消息 ID 和包体长度。
// 返回 ok=false 表示长度字段非法，连接应当直接断开。
func UnpackHeader(hdr []byte) (msgID uint16, bodyLen int, ok bool) {
	if len(hdr) < protocol.HeaderSize {
		return 0, 0, false
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	if n < 2 || n > protocol.MaxFrameSize {
		return 0, 0, false
	}
	return binary.BigEndian.Uint16(hdr[4:6]), int(n) - 2, true
}
