class_name OWEntityNode
extends Node2D
## 一个实体的 2D 表现：圆点 + 血条 + 名字/等级标签。
##
## 位置由服务端权威下发，客户端只做平滑插值（lerp 到 _target），不做
## 本地物理预测——避免"客户端觉得在这儿、服务端说在那儿"的打架。
## 自己用蓝色 + 外圈高亮，其他实体用橙红色。

const SCALE := 1.0

var entity_id: int = 0
var is_self: bool = false
var hp: int = 100
var max_hp: int = 100
var level: int = 1

var _target: Vector2 = Vector2.ZERO
var _label: Label
var _flash: float = 0.0

func setup(id: int, self_flag: bool, x: float, z: float) -> void:
	entity_id = id
	is_self = self_flag
	_target = Vector2(x * SCALE, z * SCALE)
	position = _target
	if _label == null:
		_label = Label.new()
		_label.position = Vector2(-50, -40)
		_label.add_theme_font_size_override("font_size", 12)
		add_child(_label)
	_refresh_label()

func set_target(x: float, z: float) -> void:
	_target = Vector2(x * SCALE, z * SCALE)

func set_attr(new_hp: int, new_max_hp: int, new_level: int) -> void:
	hp = new_hp
	max_hp = new_max_hp
	level = new_level
	_refresh_label()
	queue_redraw()

func hit() -> void:
	_flash = 1.0
	queue_redraw()

func _refresh_label() -> void:
	if _label != null:
		_label.text = ("#%d  Lv.%d" % [entity_id, level]) if not is_self else ("我 #%d" % entity_id)

func _process(delta: float) -> void:
	# 20Hz 的服务端位置 + 60fps 的本地插值，视觉上连续。
	position = position.lerp(_target, clampf(delta * 12.0, 0.0, 1.0))
	if _flash > 0.0:
		_flash = maxf(0.0, _flash - delta * 4.0)
		queue_redraw()

func _draw() -> void:
	var col := Color(0.25, 0.72, 1.0) if is_self else Color(0.95, 0.42, 0.32)
	if _flash > 0.0:
		col = col.lerp(Color.WHITE, _flash)
	draw_circle(Vector2.ZERO, 12.0, col)
	draw_arc(Vector2.ZERO, 13.5, 0.0, TAU, 24,
		Color(1, 1, 1, 0.85) if is_self else Color(0, 0, 0, 0.35), 2.0 if is_self else 1.0)
	# 血条：服务端 EntityAttr 下发 HP/MaxHP，客户端直接画百分比。
	var w := 30.0
	var ratio := 0.0 if max_hp <= 0 else clampf(float(hp) / float(max_hp), 0.0, 1.0)
	draw_rect(Rect2(-w / 2.0, -24.0, w, 4.0), Color(0.08, 0.08, 0.08, 0.85))
	draw_rect(Rect2(-w / 2.0, -24.0, w * ratio, 4.0),
		Color(0.2, 0.85, 0.35) if ratio > 0.3 else Color(0.95, 0.3, 0.25))
