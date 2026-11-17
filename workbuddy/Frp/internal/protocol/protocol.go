// Package protocol 定义 frptun 的线缆协议。
//
// 帧格式:
//
//	[1 byte kind][4 bytes big-endian length][JSON body]
//
// kind:
//
//	0xC1 控制连接（长连接，承载认证/注册/心跳/new_conn 等信令）
//	0xD2 数据连接（短连接，头 8 字节为 big-endian connID，随后是原始字节流）
//
// 控制连接与数据连接复用同一个监听端口，靠首字节区分，部署时只需开放一个端口。
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	KindControl byte = 0xC1
	KindData    byte = 0xD2

	maxFrameSize = 1 << 20 // 1 MiB，防止恶意长度导致 OOM
)

// 消息类型
const (
	MsgAuth         = "auth"
	MsgAuthResp     = "auth_resp"
	MsgRegister     = "register"
	MsgRegisterResp = "register_resp"
	MsgNewConn      = "new_conn"
	MsgCloseConn    = "close_conn"
	MsgHeartbeat    = "heartbeat"
	MsgHeartbeatAck = "heartbeat_ack"

	// 实时端口管理：服务端下发权威映射列表，客户端确认；服务端通知撤销映射。
	MsgSyncServices = "sync_services"
	MsgServiceAck   = "service_ack"
)

// Service 描述一条端口映射。
type Service struct {
	Name       string `json:"name"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
}

// Message 是所有信令的载体。字段按用途分组，未使用的字段省略。
type Message struct {
	Type string `json:"type"`

	Token     string `json:"token,omitempty"`
	MachineID string `json:"machine_id,omitempty"` // 客户端机器码，用于授权与识别
	Hostname  string `json:"hostname,omitempty"`   // 客户端主机名，便于管理端展示

	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	Services []Service `json:"services,omitempty"`

	ConnID      uint64 `json:"conn_id,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	RemotePort  int    `json:"remote_port,omitempty"`

	RequestID string `json:"request_id,omitempty"` // 端口管理操作的关联 ID
}

// WriteKind 写入连接类型首字节。必须在任何 WriteFrame 之前调用一次。
func WriteKind(w io.Writer, kind byte) error {
	_, err := w.Write([]byte{kind})
	return err
}

// ReadKind 读取连接类型首字节。
func ReadKind(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// WriteFrame 写入一个长度前缀的 JSON 帧。
func WriteFrame(w io.Writer, msg *Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("编码消息失败: %w", err)
	}
	if len(body) > maxFrameSize {
		return fmt.Errorf("消息过大: %d bytes", len(body))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf := make([]byte, 0, len(hdr)+len(body))
	buf = append(buf, hdr[:]...)
	buf = append(buf, body...)
	_, err = w.Write(buf)
	return err
}

// ReadFrame 读取一个长度前缀的 JSON 帧。
func ReadFrame(r io.Reader) (*Message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxFrameSize {
		return nil, fmt.Errorf("非法帧长度: %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("解码消息失败: %w", err)
	}
	if msg.Type == "" {
		return nil, errors.New("消息缺少 type 字段")
	}
	return &msg, nil
}

// WriteConnID 写入数据连接的 8 字节 connID。
func WriteConnID(w io.Writer, id uint64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], id)
	_, err := w.Write(b[:])
	return err
}

// ReadConnID 读取数据连接的 8 字节 connID。
func ReadConnID(r io.Reader) (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}
