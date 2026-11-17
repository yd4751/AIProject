class_name OWHud
extends CanvasLayer
## 调试 HUD：连接状态、实体数、收包数、RTT、FPS、自己的血量等级。
## 纯代码构建，不依赖 .tscn，方便 headless 与版本管理。

var _panel: PanelContainer
var _lines: Dictionary = {}
var _status_dot: ColorRect

func _ready() -> void:
	_panel = PanelContainer.new()
	var style := StyleBoxFlat.new()
	style.bg_color = Color(0.05, 0.07, 0.11, 0.82)
	style.set_content_margin_all(10)
	_panel.add_theme_stylebox_override("panel", style)
	_panel.position = Vector2(12, 12)

	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 4)
	_panel.add_child(vbox)

	var title := Label.new()
	title.text = "OpenWorld · Godot 客户端"
	title.add_theme_font_size_override("font_size", 16)
	title.add_theme_color_override("font_color", Color(0.45, 0.85, 1.0))
	vbox.add_child(title)

	for key in ["state", "stats", "self", "skill", "help", "msg"]:
		var l := Label.new()
		l.add_theme_font_size_override("font_size", 13)
		vbox.add_child(l)
		_lines[key] = l

	add_child(_panel)
	set_line("help", "WASD/方向键移动 · 点击实体攻击 · 点击空地移动 · 数字键/技能树切换技能")
	set_line("msg", "")

func set_line(key: String, text: String) -> void:
	if _lines.has(key):
		_lines[key].text = text

func set_state(text: String, ok: bool) -> void:
	if _lines.has("state"):
		_lines["state"].text = "状态: " + text
		_lines["state"].add_theme_color_override("font_color",
			Color(0.35, 0.95, 0.55) if ok else Color(1.0, 0.45, 0.4))
