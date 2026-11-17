extends SceneTree

const Messages := preload("res://scripts/protocol/ow_messages.gd")
const Proto := preload("res://scripts/protocol/ow_proto.gd")
const Writer := preload("res://scripts/protocol/ow_writer.gd")
const Reader := preload("res://scripts/protocol/ow_reader.gd")
const Auth := preload("res://scripts/protocol/ow_auth.gd")

## 协议层字节级自测：黄金字节由 Go 端 internal/protocol 生成，用来证明
## GDScript 的编解码和服务端逐字节对齐（不是"自己编自己解"的假往返）。
##
## 运行：godot --headless --path client/godot --script res://selftest/protocol_test.gd
## 退出码 0 = 全通过，1 = 有失败。

var checks := 0
var failures := 0

func _ck(cond: bool, label: String) -> void:
	checks += 1
	if cond:
		print("ok    ", label)
	else:
		failures += 1
		print("FAIL  ", label)

func _eq(got, want, label: String) -> void:
	_ck(got == want, "%s (got=%s want=%s)" % [label, str(got), str(want)])

func _hex(s: String) -> PackedByteArray:
	var out := PackedByteArray()
	var i := 0
	while i + 1 < s.length():
		out.append(("0x" + s.substr(i, 2)).hex_to_int())
		i += 2
	return out

func _initialize() -> void:
	print("=== protocol golden test ===")

	# ---- S2C 解码：用 Go 生成的黄金字节断言每个字段 ----
	var lr := Messages.decode_login_resp(_hex("0000000000075bcd150005616c6963650000"))
	_ck(lr["ok"], "login_resp decode ok")
	_eq(lr["data"]["code"], 0, "login_resp.code")
	_eq(lr["data"]["session_id"], 123456789, "login_resp.session_id")
	_eq(lr["data"]["player_id"], "alice", "login_resp.player_id")
	_eq(lr["data"]["reason"], "", "login_resp.reason")

	var esr := Messages.decode_enter_scene_resp(_hex("00000000000000002a000000033fc0000040000000c05000000000"))
	_ck(esr["ok"], "enter_scene_resp decode ok")
	_eq(esr["data"]["code"], 0, "enter_scene_resp.code")
	_eq(esr["data"]["entity_id"], 42, "enter_scene_resp.entity_id")
	_eq(esr["data"]["scene_id"], 3, "enter_scene_resp.scene_id")
	_ck(is_equal_approx(esr["data"]["x"], 1.5), "enter_scene_resp.x")
	_ck(is_equal_approx(esr["data"]["y"], 2.0), "enter_scene_resp.y")
	_ck(is_equal_approx(esr["data"]["z"], -3.25), "enter_scene_resp.z")

	var ea := Messages.decode_entity_attr(_hex("0000000000000007000000fa000003e805"))
	_ck(ea["ok"], "entity_attr decode ok")
	_eq(ea["data"]["entity_id"], 7, "entity_attr.entity_id")
	_eq(ea["data"]["hp"], 250, "entity_attr.hp")
	_eq(ea["data"]["max_hp"], 1000, "entity_attr.max_hp")
	_eq(ea["data"]["level"], 5, "entity_attr.level")

	var ch := Messages.decode_combat_hit(_hex("000000000000000100000000000000070200000025000000d50001"))
	_ck(ch["ok"], "combat_hit decode ok")
	_eq(ch["data"]["attacker_id"], 1, "combat_hit.attacker_id")
	_eq(ch["data"]["target_id"], 7, "combat_hit.target_id")
	_eq(ch["data"]["skill_id"], 2, "combat_hit.skill_id")
	_eq(ch["data"]["damage"], 37, "combat_hit.damage")
	_eq(ch["data"]["target_hp"], 213, "combat_hit.target_hp")
	_eq(ch["data"]["killed"], false, "combat_hit.killed")
	_eq(ch["data"]["crit"], true, "combat_hit.crit")

	var st := Messages.decode_entity_state(_hex("0000000000000063c14800000000000041000000"))
	_ck(st["ok"], "entity_state decode ok")
	_eq(st["data"]["entity_id"], 99, "entity_state.entity_id")
	_ck(is_equal_approx(st["data"]["x"], -12.5), "entity_state.x")
	_ck(is_equal_approx(st["data"]["z"], 8.0), "entity_state.z")

	# 中文 UTF-8 字符串（u16 长度按字节数，不是字符数）
	var nt := Messages.decode_notice(_hex("0006e7bbb4e68aa4000f363020e7a792e5908ee585b3e69c8d0000003c"))
	_ck(nt["ok"], "notice decode ok")
	_eq(nt["data"]["title"], "维护", "notice.title")
	_eq(nt["data"]["text"], "60 秒后关服", "notice.text")
	_eq(nt["data"]["seconds"], 60, "notice.seconds")

	# S2C_SKILL_LIST：目录 + 已解锁位图（Golden 由 Go internal/protocol 生成）
	var sl := Messages.decode_skill_list(_hex("0201000550756e63680000006440400000000000000000000000000000000000000002000442617368000000fa4040000000000bb8000000000002010100000096000000000000000002"))
	_ck(sl["ok"], "skill_list decode ok")
	_eq(sl["data"]["learned"], 2, "skill_list.learned (bit1 set)")
	var cat: Array = sl["data"]["catalog"]
	_eq(cat.size(), 2, "skill_list count")
	var s0: Dictionary = cat[0]
	_eq(s0["id"], 1, "skill[0].id")
	_eq(s0["name"], "Punch", "skill[0].name")
	_eq(s0["damage"], 100, "skill[0].damage")
	_ck(is_equal_approx(s0["range"], 3.0), "skill[0].range")
	_eq(s0["cooldownMs"], 0, "skill[0].cooldownMs")
	_eq(s0["level"], 0, "skill[0].level")
	_eq(s0["requires"], 0, "skill[0].requires")
	_eq(s0["aoe"], false, "skill[0].aoe")
	_eq(s0["dispel"], false, "skill[0].dispel")
	var s1: Dictionary = cat[1]
	_eq(s1["id"], 2, "skill[1].id")
	_eq(s1["name"], "Bash", "skill[1].name")
	_eq(s1["damage"], 250, "skill[1].damage")
	_eq(s1["cooldownMs"], 3000, "skill[1].cooldownMs")
	_eq(s1["level"], 2, "skill[1].level")
	_eq(s1["requires"], 1, "skill[1].requires")
	_eq(s1["comboFrom"], 1, "skill[1].comboFrom")
	_eq(s1["comboScale"], 150, "skill[1].comboScale")
	_eq(s1["dispel"], false, "skill[1].dispel")

	# ---- C2S 编码：hex 必须和 Go 完全一致 ----
	_eq(Messages.encode_login("alice:1730000000:deadbeef").hex_encode(),
		"0019616c6963653a313733303030303030303a6465616462656566", "encode_login bytes")
	_eq(Messages.encode_move(10.0, 0.0, -20.0, 1730000000123).hex_encode(),
		"4120000000000000c1a0000000000192cc09147b", "encode_move bytes")
	_eq(Messages.encode_attack(7, 3).hex_encode(),
		"000000000000000703", "encode_attack bytes")
	_eq(Messages.encode_enter_scene().size(), 0, "encode_enter_scene empty")
	_eq(Messages.encode_query_skills().size(), 0, "encode_query_skills empty")

	# ---- 帧打包 / 解包 ----
	var body := Messages.encode_attack(7, 3)
	var frame := Proto.pack_frame(Proto.C2S_ATTACK, body)
	_eq(frame.size(), Proto.HEADER_SIZE + body.size(), "frame size")
	var hdr := Proto.unpack_header(frame.slice(0, Proto.HEADER_SIZE))
	_ck(hdr["ok"], "unpack_header ok")
	_eq(hdr["msg_id"], Proto.C2S_ATTACK, "unpack_header.msg_id")
	_eq(hdr["body_len"], body.size(), "unpack_header.body_len")
	_ck(not Proto.unpack_header(PackedByteArray()).ok, "short header rejected")
	var bad := Writer.new().u32(999999).u16(1).bytes()
	_ck(not Proto.unpack_header(bad).ok, "oversize frame rejected")

	# ---- 大端 / 有符号往返 ----
	var w := Writer.new()
	w.u8(0xAB).u16(0x0102).u32(0x01020304).u64(0x0102030405060708)
	w.i32(-37).i64(-123456789).f32(-3.25).string("hi")
	var r := Reader.new(w.bytes())
	_eq(r.u8(), 0xAB, "roundtrip u8")
	_eq(r.u16(), 0x0102, "roundtrip u16")
	_eq(r.u32(), 0x01020304, "roundtrip u32")
	_eq(r.u64(), 0x0102030405060708, "roundtrip u64")
	_eq(r.i32(), -37, "roundtrip i32 (negative)")
	_eq(r.i64(), -123456789, "roundtrip i64 (negative)")
	_ck(is_equal_approx(r.f32(), -3.25), "roundtrip f32")
	_eq(r.string(), "hi", "roundtrip string")
	_ck(not r.failed, "roundtrip no read error")
	_eq(r.remaining(), 0, "roundtrip consumed all bytes")

	# ---- token 签发：与 Go authtoken.Sign 交叉验证过的值 ----
	_eq(Auth.sign_token("dev-secret-change-me", "player1", 1730000000),
		"player1:1730000000:adeeb955dff724cd641882a998c599b9335f7493e83371d2f4d5e7df0530840a",
		"token HMAC matches Go")

	# ---- 结果 ----
	if failures == 0:
		print("=== ALL %d CHECKS PASSED ===" % checks)
		quit(0)
	else:
		printerr("=== %d/%d CHECKS FAILED ===" % [failures, checks])
		quit(1)
