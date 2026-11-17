class_name OWMessages
extends RefCounted

const Writer := preload("res://scripts/protocol/ow_writer.gd")
const Reader := preload("res://scripts/protocol/ow_reader.gd")
const Proto := preload("res://scripts/protocol/ow_proto.gd")

## 消息编解码，逐字段对齐 Go `internal/protocol/messages.go` 的 15 条消息。
##
## GDScript 没有 struct，用 Dictionary 承载消息字段。约定：
##   - encode_* 返回 PackedByteArray（body，不含帧头）
##   - decode_* 返回 Dictionary，字段名用 Go 风格 PascalCase 的驼峰小写版
##   - decode(msg_id, body) 统一分发，返回 {"type","data","ok"}
##
## 客户端只上报"意图"（去哪、打谁），伤害/位置由服务端算——这个约定
## 在协议里就定死了，客户端不能自己报伤害数字。

# ---------------------------------------------------------------- C2S ---

static func encode_login(token: String) -> PackedByteArray:
	var w := Writer.new()
	return w.string(token).bytes()

static func encode_heartbeat(seq: int, client_time_ms: int) -> PackedByteArray:
	var w := Writer.new()
	return w.u64(seq).i64(client_time_ms).bytes()

static func encode_enter_scene() -> PackedByteArray:
	return PackedByteArray()

static func encode_move(x: float, y: float, z: float, client_time_ms: int) -> PackedByteArray:
	var w := Writer.new()
	return w.f32(x).f32(y).f32(z).i64(client_time_ms).bytes()

static func encode_attack(target_id: int, skill_id: int) -> PackedByteArray:
	var w := Writer.new()
	return w.u64(target_id).u8(skill_id).bytes()

static func encode_query_skills() -> PackedByteArray:
	# 上行查询：空 body，服务端按当前玩家会话回填技能目录。
	return PackedByteArray()

# ---------------------------------------------------------------- S2C ---

static func decode_login_resp(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {
		"code": r.u8(), "session_id": r.u64(),
		"player_id": r.string(), "reason": r.string(),
	}
	return _finish(r, "login_resp", d)

static func decode_heartbeat_resp(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"seq": r.u64(), "server_time": r.i64()}
	return _finish(r, "heartbeat_resp", d)

static func decode_enter_scene_resp(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {
		"code": r.u8(), "entity_id": r.u64(), "scene_id": r.u32(),
		"x": r.f32(), "y": r.f32(), "z": r.f32(), "reason": r.string(),
	}
	return _finish(r, "enter_scene_resp", d)

static func decode_entity_state(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"entity_id": r.u64(), "x": r.f32(), "y": r.f32(), "z": r.f32()}
	return _finish(r, "entity_state", d)

static func decode_entity_enter(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"entity_id": r.u64(), "x": r.f32(), "y": r.f32(), "z": r.f32()}
	return _finish(r, "entity_enter", d)

static func decode_entity_leave(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"entity_id": r.u64()}
	return _finish(r, "entity_leave", d)

static func decode_entity_attr(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {
		"entity_id": r.u64(), "hp": r.i32(), "max_hp": r.i32(), "level": r.u8(),
	}
	return _finish(r, "entity_attr", d)

static func decode_kick(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"code": r.u8(), "reason": r.string()}
	return _finish(r, "kick", d)

static func decode_notice(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {"title": r.string(), "text": r.string(), "seconds": r.u32()}
	return _finish(r, "notice", d)

static func decode_combat_hit(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var d := {
		"attacker_id": r.u64(), "target_id": r.u64(), "skill_id": r.u8(),
		"damage": r.i32(), "target_hp": r.i32(),
		"killed": r.u8() != 0, "crit": r.u8() != 0,
	}
	return _finish(r, "combat_hit", d)

## decode_skill_list 解析服务端下发的技能目录。
##
## body 布局（大端与 Go 端一一对应）：
##   count u8
##   每个技能：id u8, name(string u16len+utf8), damage i32, range f32,
##            cooldownMs u32, aoe u8, aoeRad f32, level u8, requires u8,
##            comboFrom u8, comboScale i32, dispel u8
##   learned u64
##
## 返回 {"catalog":[...], "learned":int}；catalog 每个元素是带全部字段的 Dictionary。
static func decode_skill_list(b: PackedByteArray) -> Dictionary:
	var r := Reader.new(b)
	var count: int = int(r.u8())
	var catalog := []
	for _i in range(count):
		var s := {
			"id": r.u8(),
			"name": r.string(),
			"damage": r.i32(),
			"range": r.f32(),
			"cooldownMs": r.u32(),
			"aoe": r.u8() != 0,
			"aoeRad": r.f32(),
			"level": r.u8(),
			"requires": r.u8(),
			"comboFrom": r.u8(),
			"comboScale": r.i32(),
			"dispel": r.u8() != 0,
		}
		catalog.append(s)
	var learned: int = int(r.u64())
	return _finish(r, "skill_list", {"catalog": catalog, "learned": learned})

# ------------------------------------------------------------ 分发 ---

## 按消息 ID 解一帧 body。返回 {"type","data","ok"}；未知 ID 或解码失败
## ok=false，调用方可以记日志但不该断连（协议向前兼容）。
static func decode(msg_id: int, body: PackedByteArray) -> Dictionary:
	match msg_id:
		Proto.S2C_LOGIN_RESP: return decode_login_resp(body)
		Proto.S2C_HEARTBEAT: return decode_heartbeat_resp(body)
		Proto.S2C_ENTER_SCENE: return decode_enter_scene_resp(body)
		Proto.S2C_ENTITY_STATE: return decode_entity_state(body)
		Proto.S2C_ENTITY_ENTER: return decode_entity_enter(body)
		Proto.S2C_ENTITY_LEAVE: return decode_entity_leave(body)
		Proto.S2C_ENTITY_ATTR: return decode_entity_attr(body)
		Proto.S2C_KICK: return decode_kick(body)
		Proto.S2C_NOTICE: return decode_notice(body)
		Proto.S2C_COMBAT_HIT: return decode_combat_hit(body)
		Proto.S2C_SKILL_LIST: return decode_skill_list(body)
		_: return {"type": "unknown", "data": {"msg_id": msg_id}, "ok": false}

static func _finish(r: Reader, type_name: String, d: Dictionary) -> Dictionary:
	return {"type": type_name, "data": d, "ok": not r.failed}
