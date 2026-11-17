class_name OWNetClient
extends Node
## 网络层：一条 TCP 连接的完整状态机 + 对外信号。
##
## 时序严格复刻 tools/bot/main.go（它是被实测压过的参考实现）：
##   TCP 连接 → Login(token) → LoginResp → EnterScene(空) → EnterSceneResp → READY
## READY 之后才发 Move / Attack / Heartbeat。
##
## 收包用 StreamPeerTCP 的非阻塞 poll：累积缓冲 → 按 6 字节帧头切帧 →
## 交给 OWMessages 解码 → emit 信号。半包留在缓冲里等下一帧，粘包一次
## 循环处理完。所有解析都在主线程，表现层可以直接连信号改节点。
##
## `poll()` 是 public 的：正常游戏由 _process 自动调，headless 自测可以在
## SceneTree._process 里手动调，不依赖引擎的帧循环细节。

const Proto := preload("res://scripts/protocol/ow_proto.gd")
const Messages := preload("res://scripts/protocol/ow_messages.gd")
const Auth := preload("res://scripts/protocol/ow_auth.gd")

# ---------------------------------------------------------------- 信号 ---
signal connected
signal disconnected(reason: String)
signal login_ok(player_id: String, session_id: int)
signal login_failed(code: int, reason: String)
signal entered(entity_id: int, scene_id: int, x: float, z: float)
signal entity_enter(id: int, x: float, z: float)
signal entity_state(id: int, x: float, z: float)
signal entity_leave(id: int)
signal entity_attr(id: int, hp: int, max_hp: int, level: int)
signal combat_hit(attacker_id: int, target_id: int, skill_id: int, damage: int, target_hp: int, killed: bool, crit: bool)
signal kicked(code: int, reason: String)
signal notice(title: String, text: String, seconds: int)
signal rtt_ms(ms: int)
signal skill_list(catalog: Array, learned: int)

enum State { IDLE, CONNECTING, LOGGING_IN, ENTERING, READY, CLOSED }

# ---------------------------------------------------------------- 配置 ---
@export var host: String = "127.0.0.1"
@export var port: int = 7001
@export var secret: String = "dev-secret-change-me"
@export var player_name: String = "player1"
@export var heartbeat_interval: float = 5.0
@export var auto_reconnect: bool = true
@export var reconnect_delay: float = 3.0

# ---------------------------------------------------------------- 状态 ---
var state: State = State.IDLE
var entity_id: int = 0
var last_error: String = ""
var frames_in: int = 0

var _tcp: StreamPeerTCP
var _recv: PackedByteArray = PackedByteArray()
var _hb_elapsed: float = 0.0
var _hb_seq: int = 0
var _hb_sent_ms: int = 0
var _reconnect_elapsed: float = 0.0
var _manual_close: bool = false

func _ready() -> void:
	set_process(true)

# ---------------------------------------------------------------- 入口 ---
func connect_to_server() -> void:
	_close_socket()
	_manual_close = false
	_tcp = StreamPeerTCP.new()
	# 不设 TCP_NODELAY：Windows 上连接未完全建立时 set_no_delay 会告警，
	# 而本地/局域网回环下这点延迟差异可以忽略，省掉一个平台差异点。
	var err: int = _tcp.connect_to_host(host, port)
	if err != OK:
		last_error = "connect_to_host err=%d" % err
		_on_disconnected(last_error)
		return
	state = State.CONNECTING
	_recv = PackedByteArray()
	_hb_elapsed = 0.0
	_reconnect_elapsed = 0.0

func disconnect_from_server() -> void:
	_manual_close = true
	_close_socket()
	state = State.CLOSED
	entity_id = 0

func _close_socket() -> void:
	if _tcp != null:
		_tcp.disconnect_from_host()
		_tcp = null

func _process(delta: float) -> void:
	tick(delta)

## 驱动一次"轮询 + 心跳 + 重连"。拆成 public 是为了 headless 自测：
## 那些脚本 extends SceneTree 并 await process_frame，若覆盖 _process 会
## 截断引擎的帧循环，所以自测里 set_process(false) 后手动按步长 tick。
func tick(delta: float) -> void:
	poll()
	if state == State.READY:
		_hb_elapsed += delta
		if _hb_elapsed >= heartbeat_interval:
			_hb_elapsed = 0.0
			_send_heartbeat()
	if state == State.CLOSED and auto_reconnect and not _manual_close:
		_reconnect_elapsed += delta
		if _reconnect_elapsed >= reconnect_delay:
			_reconnect_elapsed = 0.0
			connect_to_server()

# ---------------------------------------------------------------- 收包 ---
## 驱动一次网络轮询。拆成 public 是为了让自测能精确控制时序。
func poll() -> void:
	if _tcp == null:
		return
	_tcp.poll()
	var st: int = _tcp.get_status()
	if state == State.CONNECTING:
		if st == StreamPeerTCP.STATUS_CONNECTED:
			_on_connected()
		elif st == StreamPeerTCP.STATUS_ERROR:
			_on_disconnected("connect failed")
			return
	elif st == StreamPeerTCP.STATUS_NONE or st == StreamPeerTCP.STATUS_ERROR:
		if state != State.CLOSED:
			_on_disconnected("connection lost")
		return
	_drain()

func _drain() -> void:
	var avail: int = _tcp.get_available_bytes()
	if avail <= 0:
		return
	var ret: Array = _tcp.get_data(avail)
	if ret[0] != OK:
		_on_disconnected("read error")
		return
	_recv.append_array(ret[1] as PackedByteArray)

	# 一次把缓冲里所有完整帧处理完（粘包）；半包留着等下次。
	while _recv.size() >= Proto.HEADER_SIZE:
		var hdr: Dictionary = Proto.unpack_header(_recv.slice(0, Proto.HEADER_SIZE))
		if not hdr["ok"]:
			_on_disconnected("bad frame header")
			return
		var total: int = Proto.HEADER_SIZE + int(hdr["body_len"])
		if _recv.size() < total:
			break
		var body := _recv.slice(Proto.HEADER_SIZE, total)
		_recv = _recv.slice(total)
		frames_in += 1
		_dispatch(int(hdr["msg_id"]), body)

func _on_connected() -> void:
	state = State.LOGGING_IN
	connected.emit()
	# 开发模式：客户端自己签发 token（secret 必须和服务端 -secret 一致）。
	var token := Auth.sign_now(secret, player_name)
	_send(Proto.C2S_LOGIN, Messages.encode_login(token))

func _on_disconnected(reason: String) -> void:
	last_error = reason
	_close_socket()
	state = State.CLOSED
	entity_id = 0
	_reconnect_elapsed = 0.0
	disconnected.emit(reason)

func _dispatch(msg_id: int, body: PackedByteArray) -> void:
	var m: Dictionary = Messages.decode(msg_id, body)
	if not m["ok"]:
		push_warning("OWNetClient: bad frame %s" % Proto.name_of(msg_id))
		return
	var d: Dictionary = m["data"]
	match m["type"]:
		"login_resp": _on_login_resp(d)
		"enter_scene_resp": _on_enter_scene_resp(d)
		"heartbeat_resp": _on_heartbeat_resp(d)
		"entity_enter":
			entity_enter.emit(int(d["entity_id"]), float(d["x"]), float(d["z"]))
		"entity_state":
			entity_state.emit(int(d["entity_id"]), float(d["x"]), float(d["z"]))
		"entity_leave":
			entity_leave.emit(int(d["entity_id"]))
		"entity_attr":
			entity_attr.emit(int(d["entity_id"]), int(d["hp"]), int(d["max_hp"]), int(d["level"]))
		"combat_hit":
			combat_hit.emit(
				int(d["attacker_id"]), int(d["target_id"]), int(d["skill_id"]),
				int(d["damage"]), int(d["target_hp"]), bool(d["killed"]), bool(d["crit"]))
		"kick": _on_kick(d)
		"notice":
			notice.emit(str(d["title"]), str(d["text"]), int(d["seconds"]))
		"skill_list":
			skill_list.emit(d["catalog"], int(d["learned"]))

func _on_login_resp(d: Dictionary) -> void:
	if int(d["code"]) != Proto.CODE_OK:
		login_failed.emit(int(d["code"]), str(d["reason"]))
		_manual_close = true
		_on_disconnected("login rejected: " + str(d["reason"]))
		return
	login_ok.emit(str(d["player_id"]), int(d["session_id"]))
	state = State.ENTERING
	_send(Proto.C2S_ENTER_SCENE, Messages.encode_enter_scene())

func _on_enter_scene_resp(d: Dictionary) -> void:
	if int(d["code"]) != Proto.CODE_OK:
		_manual_close = true
		_on_disconnected("enter scene rejected: " + str(d["reason"]))
		return
	entity_id = int(d["entity_id"])
	state = State.READY
	entered.emit(entity_id, int(d["scene_id"]), float(d["x"]), float(d["z"]))

func _on_heartbeat_resp(d: Dictionary) -> void:
	if _hb_sent_ms > 0:
		rtt_ms.emit(Time.get_ticks_msec() - _hb_sent_ms)
		_hb_sent_ms = 0

func _on_kick(d: Dictionary) -> void:
	kicked.emit(int(d["code"]), str(d["reason"]))
	# 被踢（重复登录 / 关服 / 心跳超时）不自动重连，否则会和服务器互相拉扯。
	_manual_close = true
	_on_disconnected("kicked: " + str(d["reason"]))

# ---------------------------------------------------------------- 发包 ---
func _send(msg_id: int, body: PackedByteArray) -> void:
	if _tcp == null:
		return
	var err: int = _tcp.put_data(Proto.pack_frame(msg_id, body))
	if err != OK:
		push_warning("OWNetClient: send %s failed err=%d" % [Proto.name_of(msg_id), err])

func _send_heartbeat() -> void:
	_hb_seq += 1
	_hb_sent_ms = Time.get_ticks_msec()
	_send(Proto.C2S_HEARTBEAT,
		Messages.encode_heartbeat(_hb_seq, int(Time.get_unix_time_from_system() * 1000)))

## 上报移动意图。y 在 2D 俯视角里恒为 0（服务端用 x/z 平面）。
func send_move(x: float, z: float) -> void:
	if state != State.READY:
		return
	_send(Proto.C2S_MOVE,
		Messages.encode_move(x, 0.0, z, int(Time.get_unix_time_from_system() * 1000)))

func send_attack(target_id: int, skill_id: int) -> void:
	if state != State.READY:
		return
	_send(Proto.C2S_ATTACK, Messages.encode_attack(target_id, skill_id))

## 请求服务端下发技能目录（含当前玩家已解锁位图）。
func send_query_skills() -> void:
	if state != State.READY:
		return
	_send(Proto.C2S_QUERY_SKILLS, Messages.encode_query_skills())
