## 站点坞 · 阶段一可运行预览版
## 纯 Control 代码构建界面，不依赖 .tscn 里堆节点，便于 headless 验证。
## 浏览策略（用户 2026-11-11 拍板）：不做 HTML 解析，走通用网页展示 ——
##   Android 端优先用 AndroidWebView（JavaClassWrapper 直连系统 WebView，零 AAR），
##   若将来编译了 SiteDockWebView 插件 AAR 也可用插件单例接管；
##   桌面预览无 Android 运行时，降级为「状态说明 + 系统浏览器打开」。
## 2026-11-12：WebView 默认整屏铺满（像 APP），返回靠系统返回键 / 手势。
extends Control

const SiteStoreScript = preload("res://scripts/SiteStore.gd")
const AndroidWebViewScript = preload("res://scripts/AndroidWebView.gd")
const PLUGIN_NAME: String = "SiteDockWebView"
## true = WebView 铺满整屏；false = 顶部留 NAV_HEIGHT 给 Godot 画的返回栏
const WEBVIEW_FULLSCREEN: bool = true
const NAV_HEIGHT: float = 100.0

const BG: Color = Color("#f2f5f9")
const PRIMARY: Color = Color("#1769e0")
const TEXT: Color = Color("#1c2430")
const MUTED: Color = Color("#7a8798")
const ONLINE: Color = Color("#18a058")
const OFFLINE: Color = Color("#9aa2ac")
const DANGER: Color = Color("#d9534f")

var store_path: String = "user://sites.json"

var store: RefCounted
var _native_web: RefCounted = null
var _font: SystemFont
var _sites: Array[Dictionary] = []
var _cards: VBoxContainer
var _summary: Label
var _detail: Label
var _dashboard: Control
var _browser: Control
var _browser_title: Label
var _web_state: Label
var _web_url: Label
var _current_url: String = ""
var _toast_shown: bool = false
var _editor: Control
var _editor_title: Label
var _editor_hint: Label
var _f_name: LineEdit
var _f_url: LineEdit
var _f_group: LineEdit
var _f_pin: CheckBox
var _del_btn: Button
var _editing_id: String = ""
var _delete_pending: bool = false


func _ready() -> void:
	_font = SystemFont.new()
	_font.font_names = PackedStringArray(["Microsoft YaHei", "SimHei", "Noto Sans CJK SC", "sans-serif"])
	_native_web = AndroidWebViewScript.new()
	_build_ui()
	_load_data()
	_render()
	get_viewport().size_changed.connect(_on_viewport_resized)
	var args := OS.get_cmdline_user_args()
	if args.has("--snap") or args.has("--snap-browser") or args.has("--snap-web") \
			or args.has("--snap-add") or args.has("--snap-edit"):
		_capture()


# ---------------------------------------------------------------- 界面构建

func _build_ui() -> void:
	var bg := ColorRect.new()
	bg.color = BG
	bg.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	bg.mouse_filter = Control.MOUSE_FILTER_IGNORE
	add_child(bg)

	var col := VBoxContainer.new()
	col.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	col.add_theme_constant_override("separation", 0)
	add_child(col)

	# 顶部品牌头（右侧「＋ 添加」）
	var header := PanelContainer.new()
	var hs := StyleBoxFlat.new()
	hs.bg_color = PRIMARY
	header.add_theme_stylebox_override("panel", hs)
	header.custom_minimum_size = Vector2(0, 128)
	col.add_child(header)

	var hp := _pad(20, 22, 12, 16)
	header.add_child(hp)
	var hh := HBoxContainer.new()
	hh.add_theme_constant_override("separation", 8)
	hp.add_child(hh)
	var hv := VBoxContainer.new()
	hv.add_theme_constant_override("separation", 8)
	hv.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	hh.add_child(hv)
	hv.add_child(_label("站点坞", 26, Color.WHITE))
	_summary = _label("", 13, Color(1, 1, 1, 0.82))
	hv.add_child(_summary)

	var add := _text_button("＋", Color.WHITE, 30)
	add.custom_minimum_size = Vector2(48, 48)
	add.pressed.connect(_open_editor.bind(""))
	hh.add_child(add)

	# 内容滚动区
	var body := ScrollContainer.new()
	body.size_flags_vertical = Control.SIZE_EXPAND_FILL
	body.horizontal_scroll_mode = ScrollContainer.SCROLL_MODE_DISABLED
	col.add_child(body)
	var bp := _pad(16, 18, 16, 16)
	bp.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	body.add_child(bp)
	var bv := VBoxContainer.new()
	bv.add_theme_constant_override("separation", 12)
	bv.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bp.add_child(bv)

	_detail = _label("点卡片进通用网页视图 · 点「⋯」可编辑 / 删除", 13, MUTED)
	_detail.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	_detail.custom_minimum_size = Vector2(0, 56)
	bv.add_child(_detail)
	bv.add_child(_label("我的站点", 15, TEXT))

	_cards = VBoxContainer.new()
	_cards.add_theme_constant_override("separation", 10)
	_cards.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bv.add_child(_cards)

	# 底部 Tab
	var bar := PanelContainer.new()
	var bs := StyleBoxFlat.new()
	bs.bg_color = Color.WHITE
	bar.add_theme_stylebox_override("panel", bs)
	bar.custom_minimum_size = Vector2(0, 60)
	col.add_child(bar)
	var bh := HBoxContainer.new()
	bar.add_child(bh)
	bh.add_child(_tab_button("站点", true))
	bh.add_child(_tab_button("设置", false))

	_dashboard = col
	_build_browser()
	_build_editor()


func _pad(left: int, top: int, right: int, bottom: int) -> MarginContainer:
	var m := MarginContainer.new()
	m.add_theme_constant_override("margin_left", left)
	m.add_theme_constant_override("margin_top", top)
	m.add_theme_constant_override("margin_right", right)
	m.add_theme_constant_override("margin_bottom", bottom)
	return m


func _label(text: String, size: int, color: Color) -> Label:
	var l := Label.new()
	l.text = text
	l.add_theme_font_override("font", _font)
	l.add_theme_font_size_override("font_size", size)
	l.add_theme_color_override("font_color", color)
	return l


func _flat_style() -> StyleBoxFlat:
	var s := StyleBoxFlat.new()
	s.bg_color = Color(0, 0, 0, 0)
	return s


func _text_button(text: String, color: Color, size: int) -> Button:
	var b := Button.new()
	b.text = text
	b.add_theme_font_override("font", _font)
	b.add_theme_font_size_override("font_size", size)
	b.add_theme_color_override("font_color", color)
	b.add_theme_stylebox_override("normal", _flat_style())
	b.add_theme_stylebox_override("hover", _flat_style())
	b.add_theme_stylebox_override("pressed", _flat_style())
	return b


func _tab_button(text: String, active: bool) -> Button:
	var b := _text_button(text, PRIMARY if active else MUTED, 14)
	b.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	return b


func _make_card(site: Dictionary, index: int) -> Control:
	var card := PanelContainer.new()
	var sb := StyleBoxFlat.new()
	sb.bg_color = Color.WHITE
	sb.set_corner_radius_all(14)
	sb.shadow_color = Color(0, 0, 0, 0.08)
	sb.shadow_size = 6
	sb.content_margin_left = 14
	sb.content_margin_right = 10
	sb.content_margin_top = 12
	sb.content_margin_bottom = 12
	card.add_theme_stylebox_override("panel", sb)
	card.custom_minimum_size = Vector2(0, 88)
	card.size_flags_horizontal = Control.SIZE_EXPAND_FILL

	var hb := HBoxContainer.new()
	hb.add_theme_constant_override("separation", 10)
	card.add_child(hb)

	var icon := PanelContainer.new()
	var isb := StyleBoxFlat.new()
	isb.bg_color = PRIMARY if index == 0 else Color("#5b7fb9")
	isb.set_corner_radius_all(12)
	icon.add_theme_stylebox_override("panel", isb)
	icon.custom_minimum_size = Vector2(48, 48)
	var ic := _label(String(site["name"]).substr(0, 1), 22, Color.WHITE)
	ic.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	ic.vertical_alignment = VERTICAL_ALIGNMENT_CENTER
	icon.add_child(ic)
	hb.add_child(icon)

	var mid := VBoxContainer.new()
	mid.add_theme_constant_override("separation", 4)
	mid.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	hb.add_child(mid)
	mid.add_child(_label(String(site["name"]), 17, TEXT))
	mid.add_child(_label(String(site["url"]), 12, MUTED))

	var right := VBoxContainer.new()
	right.add_theme_constant_override("separation", 6)
	right.alignment = BoxContainer.ALIGNMENT_CENTER
	hb.add_child(right)
	var st: Dictionary = site.get("status", {})
	var online := bool(st.get("online", false))
	var badge := _label("● 在线" if online else "● 离线", 12, ONLINE if online else OFFLINE)
	badge.horizontal_alignment = HORIZONTAL_ALIGNMENT_RIGHT
	right.add_child(badge)
	var meta := _label("%s · %d ms" % [String(site["group"]), int(st.get("rtt", 0))], 11, MUTED)
	meta.horizontal_alignment = HORIZONTAL_ALIGNMENT_RIGHT
	right.add_child(meta)

	var more := _text_button("⋯", MUTED, 22)
	more.custom_minimum_size = Vector2(30, 48)
	more.pressed.connect(_open_editor.bind(String(site["id"])))
	hb.add_child(more)

	card.gui_input.connect(func(event: InputEvent) -> void:
		if event is InputEventMouseButton and event.pressed:
			_open_site(index)
	)
	return card


# ---------------------------------------------------------------- 数据与交互

func _load_data() -> void:
	store = SiteStoreScript.new(store_path)
	if store.count() == 0:
		store.create({"url": "https://bighat.icu:7000", "name": "大礼帽", "group": "影音", "pinned": true})
		store.create({"url": "https://www.example.com", "name": "示例站点", "group": "收藏"})
		store.create({"url": "http://43.139.101.12:5173", "name": "本地服务", "group": "调试"})
	# 阶段一演示状态；阶段二换成真实并发 HTTPRequest 检测
	var base: Array[Dictionary] = store.list()
	for i in range(base.size()):
		store.set_status(String(base[i]["id"]), {"online": i < 2, "rtt": 42 + i * 37})
	_sites = store.list()


func _render() -> void:
	for c in _cards.get_children():
		_cards.remove_child(c)
		c.queue_free()
	var online := 0
	for i in range(_sites.size()):
		var st: Dictionary = _sites[i].get("status", {})
		if bool(st.get("online", false)):
			online += 1
		_cards.add_child(_make_card(_sites[i], i))
	_summary.text = "%d 个站点 · %d 个在线 · 首屏检测 3.0 s" % [_sites.size(), online]


func _reload() -> void:
	_load_data()
	_render()


func _on_site_tapped(index: int) -> void:
	if index < 0 or index >= _sites.size():
		return
	var s: Dictionary = _sites[index]
	var st: Dictionary = s.get("status", {})
	_detail.text = "已选择：%s\n%s\n分组 %s · %s · RTT %d ms" % [
		String(s["name"]), String(s["url"]), String(s["group"]),
		"在线" if bool(st.get("online", false)) else "离线",
		int(st.get("rtt", 0)),
	]


# ---------------------------------------------------------------- 通用网页视图（不解析 HTML）

func _build_browser() -> void:
	_browser = Control.new()
	_browser.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	_browser.visible = false
	add_child(_browser)

	var bg := ColorRect.new()
	bg.color = BG
	bg.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	bg.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_browser.add_child(bg)

	var col := VBoxContainer.new()
	col.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	col.add_theme_constant_override("separation", 0)
	_browser.add_child(col)

	var nav := PanelContainer.new()
	var ns := StyleBoxFlat.new()
	ns.bg_color = PRIMARY
	nav.add_theme_stylebox_override("panel", ns)
	nav.custom_minimum_size = Vector2(0, 100)
	col.add_child(nav)
	var np := _pad(10, 18, 16, 12)
	nav.add_child(np)
	var nh := HBoxContainer.new()
	nh.add_theme_constant_override("separation", 8)
	np.add_child(nh)
	var back := _text_button("‹ 返回", Color.WHITE, 15)
	back.pressed.connect(_close_site)
	nh.add_child(back)
	_browser_title = _label("", 18, Color.WHITE)
	nh.add_child(_browser_title)

	var body := ScrollContainer.new()
	body.size_flags_vertical = Control.SIZE_EXPAND_FILL
	body.horizontal_scroll_mode = ScrollContainer.SCROLL_MODE_DISABLED
	col.add_child(body)
	var bp := _pad(16, 18, 16, 16)
	bp.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	body.add_child(bp)
	var bv := VBoxContainer.new()
	bv.add_theme_constant_override("separation", 12)
	bv.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bp.add_child(bv)

	_web_state = _label("", 13, PRIMARY)
	_web_state.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	bv.add_child(_web_state)
	_web_url = _label("", 12, MUTED)
	_web_url.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	bv.add_child(_web_url)

	# WebView 占位区：Android 上由原生 WebView 覆盖这一层
	var holder := PanelContainer.new()
	var hs := StyleBoxFlat.new()
	hs.bg_color = Color.WHITE
	hs.set_corner_radius_all(14)
	holder.add_theme_stylebox_override("panel", hs)
	holder.custom_minimum_size = Vector2(0, 540)
	holder.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bv.add_child(holder)
	var hp := _pad(20, 24, 20, 24)
	holder.add_child(hp)
	var hv := VBoxContainer.new()
	hv.add_theme_constant_override("separation", 10)
	hv.alignment = BoxContainer.ALIGNMENT_CENTER
	hp.add_child(hv)
	var h1 := _label("通用网页视图", 20, TEXT)
	h1.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	hv.add_child(h1)
	var h2 := _label("android.webkit.WebView 整屏原样渲染\n执行 JS · 支持 SPA · 不解析 HTML", 13, MUTED)
	h2.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	h2.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	hv.add_child(h2)

	var open_btn := _text_button("在系统浏览器打开（桌面预览降级）", PRIMARY, 14)
	open_btn.pressed.connect(_open_web_external)
	bv.add_child(open_btn)
	var re_btn := _text_button("重新加载网页", MUTED, 13)
	re_btn.pressed.connect(_call_webview.bind(""))
	bv.add_child(re_btn)
	var note := _label("桌面没有 Android 运行时，此处只是降级占位；真机上系统 WebView 铺满整屏（含状态栏沉浸），按系统返回键 / 手势回到站点列表。", 11, MUTED)
	note.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	note.custom_minimum_size = Vector2(0, 36)
	note.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bv.add_child(note)


func _call_webview(url: String) -> void:
	var target: String = url if not url.is_empty() else _current_url
	if target.is_empty():
		return
	# ① 日后若编译了 v2 插件 AAR，插件单例优先
	if Engine.has_singleton(PLUGIN_NAME):
		var plugin: Object = Engine.get_singleton(PLUGIN_NAME)
		plugin.call("showUrl", target)
		_web_state.text = "Android WebView 已接管 · 原样渲染，无 HTML 解析"
		return
	# ② 当前主路径：JavaClassWrapper 直连系统 WebView，零 AAR / 零 Gradle / 零 NDK
	if _native_web != null and _native_web.is_available():
		_native_web.show(target, _web_rect_px())
		var err: String = _native_web.get_last_error()
		_web_state.text = "Android 原生 WebView 全屏接管 · 执行 JS · 支持 SPA" if err.is_empty() \
				else "WebView 启动异常 · %s" % err
		return
	# ③ 桌面预览降级
	_web_state.text = "桌面预览 · Android 端由系统 WebView 整屏渲染"


## 把 Godot 逻辑坐标换算成 Android View 的物理像素矩形。
## WEBVIEW_FULLSCREEN=true 时从 (0,0) 铺满整屏；否则顶部留 NAV_HEIGHT 给返回栏。
## 无窗口信息时（headless）比例退化为 1，桌面路径不会用到它。
func _web_rect_px() -> Rect2i:
	var logical: Vector2 = get_viewport().get_visible_rect().size
	# headless / 窗口未就绪时 root viewport 可能只有几十像素，回退到项目标称尺寸
	if logical.x < 128.0 or logical.y < 128.0:
		logical = Vector2(
			float(ProjectSettings.get_setting("display/window/size/viewport_width", 420)),
			float(ProjectSettings.get_setting("display/window/size/viewport_height", 900)),
		)
	var physical: Vector2i = DisplayServer.window_get_size()
	var sx: float = float(physical.x) / maxf(logical.x, 1.0) if physical.x > 0 else 1.0
	var sy: float = float(physical.y) / maxf(logical.y, 1.0) if physical.y > 0 else 1.0
	var full_h: int = roundi(logical.y * sy)
	var top_px: int = 0 if WEBVIEW_FULLSCREEN else roundi(NAV_HEIGHT * sy)
	return Rect2i(
		0,
		top_px,
		maxi(roundi(logical.x * sx), 1),
		maxi(full_h - top_px, 1),
	)


func _sync_web_rect() -> void:
	if _native_web != null and _native_web.is_available() and _browser.visible:
		_native_web.set_rect(_web_rect_px())


func _on_viewport_resized() -> void:
	_sync_web_rect.call_deferred()


func _open_web_external() -> void:
	if _current_url.is_empty():
		return
	OS.shell_open(_current_url)


func _render_web(site: Dictionary) -> void:
	_current_url = String(site["url"])
	_browser_title.text = String(site["name"])
	_web_url.text = _current_url


func _open_site(index: int) -> void:
	if index < 0 or index >= _sites.size():
		return
	_on_site_tapped(index)
	_render_web(_sites[index])
	_dashboard.visible = false
	_browser.visible = true
	_call_webview(_current_url)
	_sync_web_rect.call_deferred()
	# 全屏后没有可见返回入口，首次进入用原生 Toast 告知返回方式
	if WEBVIEW_FULLSCREEN and not _toast_shown and _native_web != null and _native_web.is_available():
		_toast_shown = true
		_native_web.toast("网页已全屏 · 按返回键回到站点列表")


func _close_site() -> void:
	_browser.visible = false
	if Engine.has_singleton(PLUGIN_NAME):
		var plugin: Object = Engine.get_singleton(PLUGIN_NAME)
		plugin.call("hide")
	if _native_web != null:
		_native_web.hide()
	_dashboard.visible = true


# ---------------------------------------------------------------- Android 返回键 / 生命周期
# 实检（probe_runtime.gd, 4.7.2）：Node.NOTIFICATION_WM_GO_BACK_REQUEST=1007，
# MainLoop.NOTIFICATION_APPLICATION_PAUSED=2015 / RESUMED=2014。
# project.godot 必须设 application/config/quit_on_go_back=false，否则返回键直接退出。

func _notification(what: int) -> void:
	match what:
		Node.NOTIFICATION_WM_GO_BACK_REQUEST:
			_handle_android_back()
		Node.NOTIFICATION_APPLICATION_PAUSED:
			if _native_web != null:
				_native_web.pause()
		Node.NOTIFICATION_APPLICATION_RESUMED:
			if _native_web != null:
				_native_web.resume()
			_sync_web_rect.call_deferred()
		Node.NOTIFICATION_EXIT_TREE, Node.NOTIFICATION_PREDELETE:
			if _native_web != null:
				_native_web.destroy()


func _handle_android_back() -> void:
	if _editor.visible:
		_close_editor()
		return
	if _browser.visible:
		if _native_web != null and _native_web.is_available():
			_native_web.request_back()
			_await_back_result()
		else:
			_close_site()
		return
	get_tree().quit()


## canGoBack() 必须在 UI 线程查询，结果异步回传，最多等 300 ms。
func _await_back_result() -> void:
	var deadline: int = Time.get_ticks_msec() + 300
	while not _native_web.is_back_done() and Time.get_ticks_msec() < deadline:
		await get_tree().process_frame
	if _native_web.is_back_done() and _native_web.was_back_consumed():
		return
	_close_site()


# ---------------------------------------------------------------- 添加 / 编辑站点

func _build_editor() -> void:
	_editor = Control.new()
	_editor.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	_editor.visible = false
	add_child(_editor)

	var bg := ColorRect.new()
	bg.color = BG
	bg.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	bg.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_editor.add_child(bg)

	var col := VBoxContainer.new()
	col.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	col.add_theme_constant_override("separation", 0)
	_editor.add_child(col)

	var nav := PanelContainer.new()
	var ns := StyleBoxFlat.new()
	ns.bg_color = PRIMARY
	nav.add_theme_stylebox_override("panel", ns)
	nav.custom_minimum_size = Vector2(0, 100)
	col.add_child(nav)
	var np := _pad(10, 18, 12, 12)
	nav.add_child(np)
	var nh := HBoxContainer.new()
	nh.add_theme_constant_override("separation", 8)
	np.add_child(nh)
	var cancel := _text_button("‹ 取消", Color.WHITE, 15)
	cancel.pressed.connect(_close_editor)
	nh.add_child(cancel)
	_editor_title = _label("添加站点", 18, Color.WHITE)
	_editor_title.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	nh.add_child(_editor_title)
	var save := _text_button("保存", Color.WHITE, 16)
	save.pressed.connect(_save_editor)
	nh.add_child(save)

	var body := ScrollContainer.new()
	body.size_flags_vertical = Control.SIZE_EXPAND_FILL
	body.horizontal_scroll_mode = ScrollContainer.SCROLL_MODE_DISABLED
	col.add_child(body)
	var bp := _pad(18, 20, 18, 20)
	bp.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	body.add_child(bp)
	var form := VBoxContainer.new()
	form.add_theme_constant_override("separation", 12)
	form.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	bp.add_child(form)

	_f_name = _add_field(form, "站点名称", "留空则自动用域名")
	_f_url = _add_field(form, "URL", "https://bighat.icu:7000")
	_f_group = _add_field(form, "分组", "默认")
	_f_pin = CheckBox.new()
	_f_pin.text = "置顶显示"
	_f_pin.add_theme_font_override("font", _font)
	_f_pin.add_theme_font_size_override("font_size", 14)
	form.add_child(_f_pin)

	_editor_hint = _label("", 12, DANGER)
	_editor_hint.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	form.add_child(_editor_hint)
	var tip := _label("未写协议时自动补 https；安卓端允许 http / 裸 IP / 非标端口。", 11, MUTED)
	tip.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	tip.custom_minimum_size = Vector2(0, 34)
	tip.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	form.add_child(tip)

	_del_btn = _text_button("删除站点", DANGER, 15)
	_del_btn.pressed.connect(_on_delete_pressed)
	form.add_child(_del_btn)


func _add_field(parent: VBoxContainer, caption: String, placeholder: String) -> LineEdit:
	parent.add_child(_label(caption, 13, MUTED))
	var e := LineEdit.new()
	e.placeholder_text = placeholder
	e.custom_minimum_size = Vector2(0, 46)
	e.add_theme_font_override("font", _font)
	e.add_theme_font_size_override("font_size", 15)
	var sb := StyleBoxFlat.new()
	sb.bg_color = Color.WHITE
	sb.set_corner_radius_all(10)
	sb.content_margin_left = 12
	sb.content_margin_right = 12
	e.add_theme_stylebox_override("normal", sb)
	e.add_theme_stylebox_override("focus", sb)
	parent.add_child(e)
	return e


func _open_editor(site_id: String) -> void:
	# 原生 WebView 叠在窗口最上层，进编辑器前必须隐藏，否则会盖住表单
	if _native_web != null:
		_native_web.hide()
	_editing_id = site_id
	_delete_pending = false
	_editor_hint.text = ""
	_del_btn.text = "删除站点"
	_del_btn.visible = not site_id.is_empty()
	_editor_title.text = "添加站点" if site_id.is_empty() else "编辑站点"
	if site_id.is_empty():
		_f_name.text = ""
		_f_url.text = ""
		_f_group.text = "默认"
		_f_pin.button_pressed = false
	else:
		var s: Dictionary = store.get_by_id(site_id)
		_f_name.text = String(s.get("name", ""))
		_f_url.text = String(s.get("url", ""))
		_f_group.text = String(s.get("group", "默认"))
		_f_pin.button_pressed = bool(s.get("pinned", false))
	_dashboard.visible = false
	_browser.visible = false
	_editor.visible = true


func _close_editor() -> void:
	_editor.visible = false
	_dashboard.visible = true


func _save_editor() -> void:
	var name_text: String = _f_name.text.strip_edges()
	var url_text: String = _f_url.text.strip_edges()
	var group_text: String = _f_group.text.strip_edges()
	if url_text.is_empty():
		_editor_hint.text = "URL 不能为空"
		return
	if group_text.is_empty():
		group_text = "默认"
	var data: Dictionary = {
		"name": name_text,
		"url": url_text,
		"group": group_text,
		"pinned": _f_pin.button_pressed,
	}
	if _editing_id.is_empty():
		var new_id: String = store.create(data)
		if new_id.is_empty():
			_editor_hint.text = "保存失败：URL 无效或已达 500 条上限"
			return
	else:
		if not store.update(_editing_id, data):
			_editor_hint.text = "保存失败：站点不存在"
			return
	_reload()
	_close_editor()


func _on_delete_pressed() -> void:
	if not _delete_pending:
		_delete_pending = true
		_del_btn.text = "再点一次确认删除"
		_editor_hint.text = "删除后不可恢复"
		return
	store.remove(_editing_id)
	_delete_pending = false
	_del_btn.text = "删除站点"
	_reload()
	_close_editor()


# ---------------------------------------------------------------- 自检 / 截图接口

func card_count() -> int:
	return _cards.get_child_count()


func summary_text() -> String:
	return _summary.text


func detail_text() -> String:
	return _detail.text


func simulate_tap(index: int) -> void:
	_on_site_tapped(index)


func simulate_open(index: int) -> void:
	_open_site(index)


func simulate_back() -> void:
	_close_site()


func browser_visible() -> bool:
	return _browser.visible


func browser_title() -> String:
	return _browser_title.text


func web_url_text() -> String:
	return _web_url.text


func web_state_text() -> String:
	return _web_state.text


func editor_visible() -> bool:
	return _editor.visible


func editor_title() -> String:
	return _editor_title.text


func editor_hint() -> String:
	return _editor_hint.text


func site_names() -> Array[String]:
	var out: Array[String] = []
	for s in _sites:
		out.append(String(s["name"]))
	return out


func simulate_open_editor_add() -> void:
	_open_editor("")


func simulate_open_editor_edit(index: int) -> void:
	_open_editor(String(_sites[index]["id"]))


func simulate_add(data: Dictionary) -> void:
	_open_editor("")
	_f_name.text = String(data.get("name", ""))
	_f_url.text = String(data.get("url", ""))
	_f_group.text = String(data.get("group", "默认"))
	_f_pin.button_pressed = bool(data.get("pinned", false))
	_save_editor()


func simulate_edit(index: int, new_name: String) -> void:
	_open_editor(String(_sites[index]["id"]))
	_f_name.text = new_name
	_save_editor()


func simulate_delete(index: int) -> void:
	_open_editor(String(_sites[index]["id"]))
	_on_delete_pressed()
	_on_delete_pressed()


func _capture() -> void:
	var args := OS.get_cmdline_user_args()
	if args.has("--snap-browser") or args.has("--snap-web"):
		_open_site(0)
	elif args.has("--snap-add"):
		_open_editor("")
	elif args.has("--snap-edit"):
		_open_editor(String(_sites[0]["id"]))
	await get_tree().create_timer(0.5).timeout
	var img := get_viewport().get_texture().get_image()
	var name: String = "preview.png"
	if args.has("--snap-browser") or args.has("--snap-web"):
		name = "preview-browse.png"
	elif args.has("--snap-add"):
		name = "preview-add.png"
	elif args.has("--snap-edit"):
		name = "preview-edit.png"
	var err := img.save_png("res://" + name)
	print("[SNAP] %s err=%d size=%s" % [name, err, str(img.get_size())])
	get_tree().quit()
