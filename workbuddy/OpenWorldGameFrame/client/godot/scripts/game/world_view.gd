class_name OWWorldView
extends Node2D
## 2D 俯视角世界：网格背景 + 实体表 + 伤害飘字。
##
## 世界用 (x, z) 平面，1 米 = 1 像素。网格每 64m 一条线（AOI Cell 是
## 16m，画 64m 是为了不糊成一片）。场景边界按单场景 512m 画一个方框，
## 跨场景迁移时坐标会跳到新场景，摄像机跟随自己实体即可。

const SCALE := 1.0
const EntityNode := preload("res://scripts/game/entity_node.gd")

var self_id: int = 0
var entities: Dictionary = {}  # entity_id -> OWEntityNode

func _draw() -> void:
	# 网格画在世界坐标里，摄像机移动时 Node2D 变换自动跟随，不用重画。
	var grid := Color(1, 1, 1, 0.055)
	for i in range(-1024, 1025, 64):
		draw_line(Vector2(i, -1024), Vector2(i, 1024), grid, 1.0)
		draw_line(Vector2(-1024, i), Vector2(1024, i), grid, 1.0)
	# 单场景 512m 边界（Chunk 单位）。
	draw_rect(Rect2(-512, -512, 1024, 1024), Color(0.35, 0.75, 1.0, 0.35), false, 2.0)
	# 原点十字，方便看坐标。
	draw_line(Vector2(-30, 0), Vector2(30, 0), Color(1, 1, 1, 0.3), 1.0)
	draw_line(Vector2(0, -30), Vector2(0, 30), Color(1, 1, 1, 0.3), 1.0)

func self_node() -> Node:
	return entities.get(self_id)

func self_position() -> Vector2:
	var n = self_node()
	return n.position if n != null else Vector2.ZERO

func add_entity(id: int, x: float, z: float, as_self: bool = false) -> void:
	if entities.has(id):
		return
	var n: Node = EntityNode.new()
	add_child(n)
	n.setup(id, as_self, x, z)
	entities[id] = n

func upsert(id: int, x: float, z: float) -> void:
	if entities.has(id):
		entities[id].set_target(x, z)
	else:
		add_entity(id, x, z, id == self_id)

func remove_entity(id: int) -> void:
	if entities.has(id):
		entities[id].queue_free()
		entities.erase(id)

func set_attr(id: int, hp: int, max_hp: int, level: int) -> void:
	if entities.has(id):
		entities[id].set_attr(hp, max_hp, level)

func hit(id: int, damage: int, crit: bool) -> void:
	var n = entities.get(id)
	if n == null:
		return
	n.hit()
	var pos: Vector2 = n.position
	var text := ("-%d 暴击!" % damage) if crit else ("-%d" % damage)
	var col := Color(1.0, 0.85, 0.2) if crit else Color(1.0, 0.45, 0.4)
	float_text(pos + Vector2(0, -30), text, col)

func float_text(world_pos: Vector2, text: String, color: Color) -> void:
	var l := Label.new()
	l.text = text
	l.add_theme_font_size_override("font_size", 17 if "暴击" in text else 14)
	l.add_theme_color_override("font_color", color)
	l.position = world_pos
	add_child(l)
	var tw := create_tween()
	tw.tween_property(l, "position", world_pos + Vector2(0, -42), 0.8)
	tw.parallel().tween_property(l, "modulate:a", 0.0, 0.8)
	tw.tween_callback(l.queue_free)
