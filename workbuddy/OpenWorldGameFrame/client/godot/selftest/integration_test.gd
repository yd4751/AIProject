extends SceneTree
## 端到端集成自测：连一个**真实运行的** Go 服务端，走完整链路并断言。
##
##   TCP → Login → LoginResp → EnterScene → EnterSceneResp
##      → 收 EntityEnter/EntityState/EntityAttr → 发 Move/Attack → 心跳 RTT
##
## 与 protocol_test.gd 的区别：那个是纯字节级（不需要服务端），这个是
## 真正的 socket 互通——证明客户端框架能连上这个 Go 世界。
##
## 运行前先起服务端（端口由 OW_TEST_PORT 指定，默认 7300）：
##   server.exe -gate-addr :7300 -admin-addr :7301 -mobs 50
##   godot --headless --path client/godot --script res://selftest/integration_test.gd
## 退出码 0 = 全通过，1 = 有失败。

const NetClient := preload("res://scripts/net/ow_net_client.gd")

var client: Node
var checks := 0
var failures := 0
var elapsed := 0.0

var got_connected := false
var got_login := false
var got_entered := false
var got_skill_list := false
var entity_id := 0
var entity_packets := 0
var attr_packets := 0
var hit_packets := 0
var rtt := -1
var disc_reason := ""
var sent_move := false
var sent_attack := false
var target_id := 0
var _move_accum := 0.0

func _ck(cond: bool, label: String) -> void:
	checks += 1
	if cond:
		print("ok    ", label)
	else:
		failures += 1
		print("FAIL  ", label)

func _initialize() -> void:
	_run()

func _run() -> void:
	var port := 7300
	var env_port := OS.get_environment("OW_TEST_PORT")
	if env_port != "":
		port = int(env_port)
	var host := OS.get_environment("OW_TEST_HOST")
	if host == "":
		host = "127.0.0.1"

	client = NetClient.new()
	client.host = host
	client.port = port
	client.secret = "dev-secret-change-me"
	client.player_name = "godot-selftest"
	client.auto_reconnect = false
	root.add_child(client)
	# 手动驱动：不覆盖 SceneTree._process（那会截断引擎帧循环），
	# 而是关掉节点自动处理，自己在 await process_frame 的循环里 tick。
	client.set_process(false)
	_wire(client)
	client.connect_to_server()
	print("[test] connecting to %s:%d ..." % [host, port])

	var start := Time.get_ticks_msec()
	while Time.get_ticks_msec() - start < 20000:
		client.tick(0.05)
		elapsed += 0.05
		if client.state == NetClient.State.READY and elapsed > 1.0:
			# 持续朝场景中心（怪群刷怪点）上报移动意图，直到收到实体广播。
			# 这同时验证了两件事：MoveReq 被服务端接受、玩家走进 AOI 视野后
			# 收到 EntityEnter。出生点按玩家名散列，可能落在怪群之外。
			_move_accum += 0.05
			if _move_accum >= 0.1:
				_move_accum = 0.0
				if not sent_move:
					print("[test] sending MoveReq toward scene center")
				sent_move = true
				client.send_move(256.0, 256.0)
		if target_id != 0 and not sent_attack:
			sent_attack = true
			client.send_attack(target_id, 1)
			print("[test] sent AttackReq target=%d" % target_id)
		await process_frame
		if disc_reason != "":
			break
		if got_entered and entity_packets >= 3 and rtt >= 0 and sent_move and got_skill_list:
			break

	_report()

func _wire(n: Node) -> void:
	n.connected.connect(func():
		self.got_connected = true
		print("[evt] connected"))
	n.login_ok.connect(func(pid, sid):
		self.got_login = true
		print("[evt] login_ok player=%s session=%d" % [str(pid), int(sid)]))
	n.login_failed.connect(func(code, reason):
		print("[evt] login_failed code=%d reason=%s" % [int(code), str(reason)]))
	n.entered.connect(func(eid, sid, x, z):
		self.got_entered = true
		self.entity_id = int(eid)
		print("[evt] entered entity=%d scene=%d pos=(%.1f,%.1f)" % [int(eid), int(sid), float(x), float(z)])
		# 进场后向服务端要技能目录（对齐 main.gd 的真实行为）。
		n.send_query_skills())
	n.entity_enter.connect(func(id, x, z):
		self.entity_packets += 1
		if int(id) != self.entity_id:
			self.target_id = int(id))
	n.entity_state.connect(func(id, x, z):
		self.entity_packets += 1)
	n.entity_attr.connect(func(id, hp, mhp, lv):
		self.attr_packets += 1)
	n.combat_hit.connect(func(a, t, s, d, hp, k, c):
		self.hit_packets += 1)
	n.rtt_ms.connect(func(ms):
		self.rtt = int(ms))
	n.kicked.connect(func(code, reason):
		print("[evt] kicked code=%d reason=%s" % [int(code), str(reason)]))
	n.notice.connect(func(title, text, sec):
		print("[evt] notice title=%s text=%s" % [str(title), str(text)]))
	n.skill_list.connect(func(catalog, learned):
		self.got_skill_list = true
		print("[evt] skill_list catalog=%d learned=%d" % [int(catalog.size()), int(learned)]))
	n.disconnected.connect(func(reason):
		self.disc_reason = str(reason)
		print("[evt] disconnected reason=%s" % str(reason)))

func _report() -> void:
	print("=== integration result ===")
	print("[info] entity_packets=%d attr_packets=%d combat_hits=%d frames_in=%d rtt=%dms state=%d" % [
		entity_packets, attr_packets, hit_packets, client.frames_in, rtt, client.state])
	_ck(got_connected, "TCP connected")
	_ck(got_login, "login_ok (token HMAC accepted by Go server)")
	_ck(got_entered and entity_id > 0, "entered scene with entity_id")
	_ck(entity_packets > 0, "received entity broadcasts from Go world")
	_ck(rtt >= 0, "heartbeat round-trip measured")
	_ck(sent_move, "sent MoveReq without disconnect")
	_ck(got_skill_list, "received skill catalog (S2C_SkillList) from Go server")
	_ck(client.state == NetClient.State.READY, "still READY at end")
	_ck(disc_reason == "", "no unexpected disconnect")
	if failures == 0:
		print("=== ALL %d CHECKS PASSED ===" % checks)
		quit(0)
	else:
		printerr("=== %d/%d CHECKS FAILED ===" % [failures, checks])
		quit(1)
