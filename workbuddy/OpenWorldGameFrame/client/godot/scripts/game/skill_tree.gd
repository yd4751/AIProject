class_name OWSkillTree
extends CanvasLayer
## 技能树面板：列出服务端下发的技能目录，按"可用 / 已学·等级不足 / 锁定"着色，
## 点击可用技能切换当前施放技能。
##
## 数据来自 S2C_SKILL_LIST（combat 包在场景 goroutine 里现编），客户端只负责
## 展示与点击。可用性判定完全镜像服务端 combat.skillAvailable：
##   基础技能（无前置、无等级门槛）→ 永远可用；
##   技能树技能 → 等级达标 + 前置已学 + 自身已学 才可用。
## 这样客户端点一个"锁定"技能也不会让服务端报错（服务端也会再校验一次）。

signal skill_selected(skill_id: int)

var _catalog: Array = []
var _learned: int = 0
var _level: int = 1
var _active: int = 1

var _rows: VBoxContainer

func _ready() -> void:
	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 4)
	add_child(vbox)

	var title := Label.new()
	title.text = "技能树"
	title.add_theme_font_size_override("font_size", 14)
	title.add_theme_color_override("font_color", Color(0.45, 0.85, 1.0))
	vbox.add_child(title)

	_rows = VBoxContainer.new()
	_rows.add_theme_constant_override("separation", 3)
	vbox.add_child(_rows)

	# 默认贴右侧。窗口大小变化时重新贴边。
	# 注意：CanvasLayer 用 offset 定位（没有 position 属性）。
	offset = Vector2(980, 80)
	if get_viewport() != null:
		get_viewport().size_changed.connect(_reposition)
	_reposition()

func _reposition() -> void:
	var vp := get_viewport()
	if vp == null:
		return
	var sz := vp.get_visible_rect().size
	offset = Vector2(sz.x - 300, 80)

func set_data(catalog: Array, learned: int, level: int, active: int) -> void:
	_catalog = catalog
	_learned = learned
	_level = level
	_active = active
	_rebuild()

func set_level(level: int) -> void:
	if _level == level:
		return
	_level = level
	_rebuild()

func set_active(skill_id: int) -> void:
	_active = skill_id
	_rebuild()

func _is_available(s: Dictionary) -> bool:
	var requires: int = int(s["requires"])
	var lvl: int = int(s["level"])
	var id: int = int(s["id"])
	if requires == 0 and lvl == 0:
		return true
	if lvl > 0 and _level < lvl:
		return false
	if requires != 0 and (_learned & (1 << requires)) == 0:
		return false
	return (_learned & (1 << id)) != 0

func _rebuild() -> void:
	for c in _rows.get_children():
		_rows.remove_child(c)
		c.free()
	for s in _catalog:
		var id: int = int(s["id"])
		var available: bool = _is_available(s)
		var learned: bool = (_learned & (1 << id)) != 0
		var btn := Button.new()
		var meta := ""
		if int(s["level"]) > 0:
			meta += "  Lv%d" % int(s["level"])
		if int(s["requires"]) > 0:
			meta += "  需#%d" % int(s["requires"])
		var state := "可用" if available else ("已学·等级不足" if learned else "锁定")
		btn.text = "#%d %s%s  [%s]" % [id, s["name"], meta, state]
		btn.disabled = not available
		btn.size_flags_horizontal = Control.SIZE_EXPAND_FILL
		if available:
			btn.add_theme_color_override("font_color", Color(0.4, 0.95, 0.6))
		elif learned:
			btn.add_theme_color_override("font_color", Color(0.95, 0.8, 0.3))
		else:
			btn.add_theme_color_override("font_color", Color(0.6, 0.6, 0.66))
		if id == _active:
			var sb := StyleBoxFlat.new()
			sb.bg_color = Color(0.15, 0.35, 0.55, 0.9)
			btn.add_theme_stylebox_override("normal", sb)
		btn.pressed.connect(_on_press.bind(id))
		_rows.add_child(btn)

func _on_press(id: int) -> void:
	skill_selected.emit(id)
