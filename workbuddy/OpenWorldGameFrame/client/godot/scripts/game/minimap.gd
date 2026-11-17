class_name OWMinimap
extends CanvasLayer
## 小地图：以自己为中心，把视野内的实体画成相对点。
##
## 数据来自 world.entities 的实时快照（每 4Hz 由 main 推一次），不另开协议——
## 服务端已经在广播 EntityState，客户端本地重投影即可，省一次下行。
## 自己恒在中心，其他实体按相对坐标缩放后画点；超出半径的裁剪掉。

var _self_pos: Vector2 = Vector2.ZERO
var _entities: Dictionary = {}
var _self_id: int = 0
var _scale: float = 2.0     # 像素 / 米
var _radius_m: float = 110.0 # 半边长（米），对应 220px 见方

var _surface: Control

func _ready() -> void:
	_surface = Control.new()
	_surface.custom_minimum_size = Vector2(220, 220)
	_surface.size = Vector2(220, 220)
	_surface.mouse_filter = Control.MOUSE_FILTER_IGNORE
	add_child(_surface)
	_surface.draw.connect(_on_draw)
	if get_viewport() != null:
		get_viewport().size_changed.connect(_reposition)
	_reposition()

func _reposition() -> void:
	var vp := get_viewport()
	var sz := Vector2(220, 220)
	if vp != null:
		sz = vp.get_visible_rect().size
	_surface.position = Vector2(sz.x - 232, sz.y - 232)

func set_data(self_pos: Vector2, entities: Dictionary, self_id: int) -> void:
	_self_pos = self_pos
	_entities = entities
	_self_id = self_id
	_surface.queue_redraw()

func _on_draw() -> void:
	var w := _surface.size.x
	var h := _surface.size.y
	var cx := w * 0.5
	var cy := h * 0.5

	# 背景 + 边框
	_surface.draw_rect(Rect2(0, 0, w, h), Color(0.04, 0.06, 0.10, 0.82))
	_surface.draw_rect(Rect2(0, 0, w, h), Color(0.4, 0.7, 1.0, 0.4), false, 1.0)
	# 中心十字（自己朝向参考）
	_surface.draw_line(Vector2(cx - 6, cy), Vector2(cx + 6, cy), Color(0.3, 0.7, 1.0, 0.6), 1.0)
	_surface.draw_line(Vector2(cx, cy - 6), Vector2(cx, cy + 6), Color(0.3, 0.7, 1.0, 0.6), 1.0)

	# 实体点
	for id in _entities:
		if int(id) == _self_id:
			continue
		var n = _entities[id]
		var dx: float = (n.position.x - _self_pos.x) * _scale
		var dy: float = (n.position.y - _self_pos.y) * _scale
		var px: float = cx + dx
		var py: float = cy + dy
		if px < 3 or px > w - 3 or py < 3 or py > h - 3:
			continue
		var col := Color(0.95, 0.42, 0.32) if not n.is_self else Color(0.25, 0.72, 1.0)
		_surface.draw_circle(Vector2(px, py), 3.0, col)

	# 自己（中心，蓝）
	_surface.draw_circle(Vector2(cx, cy), 4.0, Color(0.3, 0.72, 1.0))
