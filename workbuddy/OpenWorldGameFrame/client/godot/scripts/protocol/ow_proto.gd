class_name OWProto
extends RefCounted

# preload 显式引用：headless 直接 --script 跑自测时不依赖 global_script_class_cache
const Writer := preload("res://scripts/protocol/ow_writer.gd")
const Reader := preload("res://scripts/protocol/ow_reader.gd")

## 线协议常量与帧打包，逐字节对齐 Go：
##   internal/protocol/protocol.go（消息 ID / 错误码 / 帧常量）
##   internal/net/frame.go（PackFrame / UnpackHeader）
##
## 帧格式（大端）：
##   +----------+----------+----------------+
##   | len u32  | msgID u16| body (len-2)   |
##   +----------+----------+----------------+
## len 包含 msgID 的 2 字节，不包含 len 自身。

const C2S_LOGIN := 0x0001
const S2C_LOGIN_RESP := 0x0002
const C2S_HEARTBEAT := 0x0003
const S2C_HEARTBEAT := 0x0004
const C2S_ENTER_SCENE := 0x0005
const S2C_ENTER_SCENE := 0x0006
const C2S_MOVE := 0x0007
const S2C_ENTITY_STATE := 0x0008
const S2C_ENTITY_ENTER := 0x0009
const S2C_ENTITY_LEAVE := 0x000A
const S2C_KICK := 0x000B
const S2C_NOTICE := 0x000C
const S2C_ENTITY_ATTR := 0x000D
const C2S_ATTACK := 0x000E
const S2C_COMBAT_HIT := 0x000F
const C2S_QUERY_SKILLS := 0x0010
const S2C_SKILL_LIST := 0x0011

const HEADER_SIZE := 6
const MAX_FRAME_SIZE := 64 * 1024

const CODE_OK := 0
const CODE_AUTH_FAILED := 1
const CODE_INVALID_PARAM := 2
const CODE_SERVER_BUSY := 3
const CODE_KICK_HEARTBEAT_TIMEOUT := 4
const CODE_KICK_DUPLICATE_LOGIN := 5
const CODE_KICK_SERVER_SHUTDOWN := 6

## 组帧。body 可以为空（EnterSceneReq 就是空包）。
static func pack_frame(msg_id: int, body: PackedByteArray) -> PackedByteArray:
	var w := Writer.new()
	w.u32(2 + body.size()).u16(msg_id)
	var out := w.bytes()
	out.append_array(body)
	return out

## 解析 6 字节帧头。ok=false 表示长度字段非法，连接应直接断开。
static func unpack_header(hdr: PackedByteArray) -> Dictionary:
	if hdr.size() < HEADER_SIZE:
		return {"ok": false}
	var r := Reader.new(hdr)
	var n := r.u32()
	var msg_id := r.u16()
	if r.failed or n < 2 or n > MAX_FRAME_SIZE:
		return {"ok": false}
	return {"ok": true, "msg_id": msg_id, "body_len": int(n) - 2}

static func name_of(msg_id: int) -> String:
	match msg_id:
		C2S_LOGIN: return "C2S_Login"
		S2C_LOGIN_RESP: return "S2C_LoginResp"
		C2S_HEARTBEAT: return "C2S_Heartbeat"
		S2C_HEARTBEAT: return "S2C_HeartbeatResp"
		C2S_ENTER_SCENE: return "C2S_EnterScene"
		S2C_ENTER_SCENE: return "S2C_EnterSceneResp"
		C2S_MOVE: return "C2S_Move"
		S2C_ENTITY_STATE: return "S2C_EntityState"
		S2C_ENTITY_ENTER: return "S2C_EntityEnter"
		S2C_ENTITY_LEAVE: return "S2C_EntityLeave"
		S2C_KICK: return "S2C_Kick"
		S2C_NOTICE: return "S2C_Notice"
		S2C_ENTITY_ATTR: return "S2C_EntityAttr"
		C2S_ATTACK: return "C2S_Attack"
		S2C_COMBAT_HIT: return "S2C_CombatHit"
		C2S_QUERY_SKILLS: return "C2S_QuerySkills"
		S2C_SKILL_LIST: return "S2C_SkillList"
		_: return "Unknown(0x%04X)" % msg_id
