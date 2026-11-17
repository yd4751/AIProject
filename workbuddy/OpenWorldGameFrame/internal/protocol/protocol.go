// Package protocol 定义客户端与服务端之间的线协议。
//
// 帧格式（大端）：
//
//	+----------+----------+----------------+
//	| len u32  | msgID u16| body (len-2)   |
//	+----------+----------+----------------+
//
// len 包含 msgID 的 2 字节，不包含 len 自身。
//
// M1 阶段用手写二进制而不是 protobuf，原因有两个：
// 一是环境里的 protoc 是 2.4.1，不支持 Go 插件；
// 二是手写编解码零依赖、零反射，压测时不会把 CPU 烧在序列化上。
// 后续换 protobuf 只需要替换 codec 实现，上层不用动。
package protocol

// 消息 ID。C2S 为客户端上行，S2C 为服务端下行。
const (
	MsgC2SLogin       uint16 = 0x0001
	MsgS2CLoginResp   uint16 = 0x0002
	MsgC2SHeartbeat   uint16 = 0x0003
	MsgS2CHeartbeat   uint16 = 0x0004
	MsgC2SEnterScene  uint16 = 0x0005
	MsgS2CEnterScene  uint16 = 0x0006
	MsgC2SMove        uint16 = 0x0007
	MsgS2CEntityState uint16 = 0x0008
	MsgS2CEntityEnter uint16 = 0x0009
	MsgS2CEntityLeave uint16 = 0x000A
	MsgS2CKick        uint16 = 0x000B
	MsgS2CNotice      uint16 = 0x000C
	MsgS2CEntityAttr  uint16 = 0x000D
	MsgC2SAttack      uint16 = 0x000E
	MsgS2CCombatHit   uint16 = 0x000F
	MsgC2SQuerySkills uint16 = 0x0010
	MsgS2CSkillList   uint16 = 0x0011
)

// 帧常量。
const (
	// HeaderSize 是 len(4) + msgID(2)。
	HeaderSize = 6
	// MaxFrameSize 防止恶意长度字段把内存打爆。
	MaxFrameSize = 64 * 1024
)

// 业务错误码。
const (
	CodeOK                   uint8 = 0
	CodeAuthFailed           uint8 = 1
	CodeInvalidParam         uint8 = 2
	CodeServerBusy           uint8 = 3
	CodeKickHeartbeatTimeout uint8 = 4
	CodeKickDuplicateLogin   uint8 = 5
	CodeKickServerShutdown   uint8 = 6
)

// Name 返回消息 ID 的可读名，用于日志和指标。
func Name(msgID uint16) string {
	switch msgID {
	case MsgC2SLogin:
		return "C2S_Login"
	case MsgS2CLoginResp:
		return "S2C_LoginResp"
	case MsgC2SHeartbeat:
		return "C2S_Heartbeat"
	case MsgS2CHeartbeat:
		return "S2C_Heartbeat"
	case MsgC2SEnterScene:
		return "C2S_EnterScene"
	case MsgS2CEnterScene:
		return "S2C_EnterScene"
	case MsgC2SMove:
		return "C2S_Move"
	case MsgS2CEntityState:
		return "S2C_EntityState"
	case MsgS2CEntityEnter:
		return "S2C_EntityEnter"
	case MsgS2CEntityLeave:
		return "S2C_EntityLeave"
	case MsgS2CKick:
		return "S2C_Kick"
	case MsgS2CNotice:
		return "S2C_Notice"
	case MsgS2CEntityAttr:
		return "S2C_EntityAttr"
	case MsgC2SAttack:
		return "C2S_Attack"
	case MsgS2CCombatHit:
		return "S2C_CombatHit"
	case MsgC2SQuerySkills:
		return "C2S_QuerySkills"
	case MsgS2CSkillList:
		return "S2C_SkillList"
	default:
		return "Unknown"
	}
}
