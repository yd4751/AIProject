extends Node2D
## 主控制器：把网络层、世界视图、HUD、摄像机、输入接起来。
##
## 整个 UI 用代码构建，不使用复杂 .tscn —— headless 环境与版本管理都更省事。
## 配置优先级：命令行 > res://client.cfg > 代码默认值。
##
## 位置是服务端权威：客户端每 100ms 上报一次"意图"（和目标 bot 一致），
## 本地只做视觉插值，服务端 EntityState 回来后再纠正。

const NetClient := preload("res://scripts/net/ow_net_client.gd")
const WorldView := preload("res://scripts/game/world_view.gd")
const Hud := preload("res://scripts/game/hud.gd")
const SkillTree := preload("res://scripts/game/skill_tree.gd")
const Minimap := preload("res://scripts/game/minimap.gd")

const MOVE_SPEED := 5.0          # m/s，服务端上限 6，留余量避免被钳制
const MOVE_SEND_INTERVAL := 0.1  # 100ms，对齐 tools/bot

var net: Node
var world: Node
var hud: Node
var skill_tree: Node
var minimap: Node
var camera: Camera2D

var _host: String = "127.0.0.1"
var _port: int = 7001
var _secret: String = "dev-secret-change-me"
var _player: String = "godot-player"
var _no_reconnect: bool = false
var _verify_mode: bool = false
const _VERIFY_PATH := "E:/Project/workbuddy/OpenWorldGameFrame/client/godot/verify.log"

var _self_pos: Vector2 = Vector2.ZERO
var _click_target: Vector2 = Vector2.ZERO
var _has_click_target: bool = false
var _skill: int = 1
var _skill_catalog: Array = []   # 服务端下发的技能目录
var _skill_learned: int = 0      # 已解锁位图
var _skill_list_ok: bool = false
var _move_accum: float = 0.0
var _hud_accum: float = 0.0
var _rtt: int = 0
var _self_hp: int = 0
var _self_max_hp: int = 0
var _self_level: int = 1
var _scene_id: int = 0

func _ready() -> void:
	RenderingServer.set_default_clear_color(Color(0.07, 0.09, 0.13))
	_load_cfg()

	world = WorldView.new()
	add_child(world)

	camera = Camera2D.new()
	camera.zoom = Vector2(1.0, 1.0)
	add_child(camera)

	hud = Hud.new()
	add_child(hud)

	skill_tree = SkillTree.new()
	add_child(skill_tree)
	skill_tree.skill_selected.connect(func(id): _select_skill(id))

	minimap = Minimap.new()
	add_child(minimap)

	net = NetClient.new()
	net.host = _host
	net.port = _port
	net.secret = _secret
	net.player_name = _player
	net.auto_reconnect = not _no_reconnect
	add_child(net)
	_wire(net)
	hud.set_line("skill", "技能: [%d] %s  (数字键/点击技能树切换)" % [_skill, _skill_name(_skill)])
	net.connect_to_server()
	hud.set_state("连接中 %s:%d" % [_host, _port], false)
	_refresh_hud()
	if _verify_mode:
		# 验证模式：跑几秒收真实数据流，把 2D 表现层状态写盘后退出。
		_report_verify()

func _load_cfg() -> void:
	var cfg := ConfigFile.new()
	if cfg.load("res://client.cfg") == OK:
		_host = str(cfg.get_value("net", "host", _host))
		_port = int(cfg.get_value("net", "port", _port))
		_secret = str(cfg.get_value("net", "secret", _secret))
		_player = str(cfg.get_value("player", "name", _player))
	# 命令行覆盖，前缀 ow- 避免和 Godot 引擎参数冲突。
	# 两个来源都解析：直接跟在引擎参数后的、以及 `--` 之后的用户参数。
	_parse_args(OS.get_cmdline_args())
	_parse_args(OS.get_cmdline_user_args())

func _parse_args(args: PackedStringArray) -> void:
	var i := 0
	while i < args.size():
		var a: String = args[i]
		if a == "--ow-no-reconnect":
			_no_reconnect = true
		elif a == "--ow-verify":
			_verify_mode = true
		elif a.begins_with("--ow-") and i + 1 < args.size():
			match a.substr(5):
				"host": _host = args[i + 1]
				"port": _port = int(args[i + 1])
				"secret": _secret = args[i + 1]
				"player": _player = args[i + 1]
			i += 1
		i += 1

func _wire(n: Node) -> void:
	n.connected.connect(func(): hud.set_state("已连接 · 登录中", true))
	n.login_ok.connect(func(pid, _sid): hud.set_state("登录成功 · " + str(pid), true))
	n.login_failed.connect(func(code, reason):
		hud.set_state("登录失败 code=%d %s" % [code, reason], false))
	n.entered.connect(func(eid, sid, x, z): _on_entered(eid, sid, x, z))
	n.skill_list.connect(func(catalog, learned): _on_skill_list(catalog, learned))
	n.entity_enter.connect(func(id, x, z): world.upsert(id, x, z))
	n.entity_state.connect(func(id, x, z): world.upsert(id, x, z))
	n.entity_leave.connect(func(id): world.remove_entity(id))
	n.entity_attr.connect(func(id, hp, mhp, lv): _on_attr(id, hp, mhp, lv))
	n.combat_hit.connect(func(_a, t, _s, dmg, _thp, killed, crit):
		world.hit(t, dmg, crit)
		if killed:
			world.remove_entity(t))
	n.kicked.connect(func(_code, reason): hud.set_line("msg", "被踢: " + str(reason)))
	n.notice.connect(func(title, text, _sec):
		hud.set_line("msg", "[%s] %s" % [str(title), str(text)]))
	n.rtt_ms.connect(func(ms): _rtt = int(ms))
	n.disconnected.connect(func(reason): hud.set_state("断开: " + str(reason), false))

func _on_entered(eid: int, sid: int, x: float, z: float) -> void:
	world.self_id = eid
	_scene_id = sid
	_self_pos = Vector2(x, z)
	world.add_entity(eid, x, z, true)
	camera.position = _self_pos
	hud.set_state("READY · 场景 %d" % sid, true)
	hud.set_line("msg", "进场景成功 entity_id=%d" % eid)
	# 进场后向服务端要技能目录（含已解锁位图），驱动技能树面板。
	net.send_query_skills()

func _on_attr(id: int, hp: int, mhp: int, lv: int) -> void:
	world.set_attr(id, hp, mhp, lv)
	if id == net.entity_id:
		_self_hp = hp
		_self_max_hp = mhp
		_self_level = lv
		if skill_tree != null:
			skill_tree.set_level(lv)

func _unhandled_input(event: InputEvent) -> void:
	if event is InputEventKey and event.pressed and not event.echo:
		# 数字键 1..6 直接选技能（服务端会再校验可用性）。
		if event.keycode >= KEY_1 and event.keycode <= KEY_6:
			_select_skill(event.keycode - KEY_1 + 1)
			return
		return

	if event is InputEventMouseButton and event.pressed and event.button_index == MOUSE_BUTTON_LEFT:
		var p := get_global_mouse_position()
		# 命中实体 → 攻击；否则 → 点地移动。
		var best_id := 0
		var best_d := 30.0
		for id in world.entities:
			var d: float = world.entities[id].position.distance_to(p)
			if d < best_d:
				best_d = d
				best_id = id
		if best_id != 0 and best_id != net.entity_id:
			net.send_attack(best_id, _skill)
			hud.set_line("msg", "攻击 #%d 技能 %d" % [best_id, _skill])
		else:
			_click_target = p
			_has_click_target = true

func _process(delta: float) -> void:
	# --- 移动意图 ---
	var dir := Vector2.ZERO
	if Input.is_key_pressed(KEY_A) or Input.is_key_pressed(KEY_LEFT):
		dir.x -= 1
	if Input.is_key_pressed(KEY_D) or Input.is_key_pressed(KEY_RIGHT):
		dir.x += 1
	if Input.is_key_pressed(KEY_W) or Input.is_key_pressed(KEY_UP):
		dir.y -= 1
	if Input.is_key_pressed(KEY_S) or Input.is_key_pressed(KEY_DOWN):
		dir.y += 1
	if dir != Vector2.ZERO:
		_has_click_target = false
		_self_pos += dir.normalized() * MOVE_SPEED * delta
	elif _has_click_target:
		var to := _click_target - _self_pos
		var step := MOVE_SPEED * delta
		if to.length() <= step:
			_self_pos = _click_target
			_has_click_target = false
		else:
			_self_pos += to.normalized() * step

	if net.state == NetClient.State.READY:
		_move_accum += delta
		if _move_accum >= MOVE_SEND_INTERVAL:
			_move_accum = 0.0
			net.send_move(_self_pos.x, _self_pos.y)
			# 本地预测：让画面跟手，服务端 EntityState 回来会纠正。
			world.upsert(net.entity_id, _self_pos.x, _self_pos.y)

	# --- 摄像机跟随 ---
	if world.self_id != 0:
		var self_n = world.self_node()
		if self_n != null:
			camera.position = camera.position.lerp(self_n.position, clampf(delta * 8.0, 0.0, 1.0))

	# --- HUD 4Hz 刷新 ---
	_hud_accum += delta
	if _hud_accum >= 0.25:
		_hud_accum = 0.0
		_refresh_hud()

func _report_verify() -> void:
	# 等 4 秒收数据，再写报告并退出。
	await get_tree().create_timer(4.0).timeout
	var lines := PackedStringArray()
	lines.append("VERIFY entity_count=%d" % world.entities.size())
	lines.append("VERIFY frames_in=%d" % net.frames_in)
	lines.append("VERIFY state=%d (4=READY)" % net.state)
	lines.append("VERIFY self_id=%d" % net.entity_id)
	lines.append("VERIFY rtt=%d" % _rtt)
	lines.append("VERIFY self_node_exists=%s" % str(world.self_node() != null))
	lines.append("VERIFY skill_catalog=%d" % _skill_catalog.size())
	lines.append("VERIFY skill_list_ok=%s" % str(_skill_list_ok))
	var ok: bool = world.entities.size() > 0 and net.state == NetClient.State.READY and net.entity_id != 0 and _skill_list_ok
	lines.append("VERIFY_RESULT=%s" % ("PASS" if ok else "FAIL"))
	var f := FileAccess.open(_VERIFY_PATH, FileAccess.WRITE)
	if f != null:
		f.store_string("\n".join(lines) + "\n")
		f.close()
	get_tree().quit(0 if ok else 1)

func _select_skill(id: int) -> void:
	_skill = id
	if skill_tree != null:
		skill_tree.set_active(id)
	hud.set_line("skill", "技能: [%d] %s  (数字键/点击技能树切换)" % [id, _skill_name(id)])

func _skill_name(id: int) -> String:
	for s in _skill_catalog:
		if int(s["id"]) == id:
			return str(s["name"])
	return "技能%d" % id

func _count_learned(mask: int) -> int:
	var n: int = 0
	var m: int = mask
	while m > 0:
		n += m & 1
		m = m >> 1
	return n

func _on_skill_list(catalog: Array, learned: int) -> void:
	_skill_catalog = catalog
	_skill_learned = learned
	_skill_list_ok = catalog.size() > 0
	if skill_tree != null:
		skill_tree.set_data(catalog, learned, _self_level, _skill)
	hud.set_line("msg", "收到技能目录：%d 个技能，已解锁 %d" % [catalog.size(), _count_learned(learned)])

func _refresh_hud() -> void:
	var state_name := "IDLE"
	if net != null:
		match net.state:
			NetClient.State.CONNECTING: state_name = "CONNECTING"
			NetClient.State.LOGGING_IN: state_name = "LOGGING_IN"
			NetClient.State.ENTERING: state_name = "ENTERING"
			NetClient.State.READY: state_name = "READY"
			NetClient.State.CLOSED: state_name = "CLOSED"
	hud.set_line("stats", "实体 %d · 收包 %d · RTT %dms · FPS %d" % [
		world.entities.size(), net.frames_in if net != null else 0, _rtt,
		Engine.get_frames_per_second()])
	hud.set_line("self", "我 #%d · HP %d/%d · Lv.%d · 场景 %d · 状态 %s" % [
		net.entity_id if net != null else 0, _self_hp, _self_max_hp,
		_self_level, _scene_id, state_name])
	hud.set_line("skill", "技能: [%d] %s  (数字键/点击技能树切换)" % [_skill, _skill_name(_skill)])
	if minimap != null:
		minimap.set_data(_self_pos, world.entities, net.entity_id if net != null else 0)
