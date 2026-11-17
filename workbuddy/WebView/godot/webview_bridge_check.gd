## WebView 桥接层自检：桌面降级路径 + 返回键 / 生命周期通知 + 坐标换算。
## 运行：godot --headless --path <godot目录> --script res://webview_bridge_check.gd
## 说明：Android 真机 JNI 路径在此环境下无法执行，本脚本只验证「非 Android 侧不崩」。
extends SceneTree

const TEST_PATH: String = "user://bridge-check-sites.json"
var _assertions: int = 0


func _init() -> void:
	var failures: Array[String] = []
	_wipe()

	var bridge_script := load("res://scripts/AndroidWebView.gd") as GDScript
	_check(failures, bridge_script != null, "AndroidWebView.gd 可解析加载")
	if bridge_script == null:
		_finish(failures)
		return

	var bridge: RefCounted = bridge_script.new()
	_check(failures, not bridge.is_available(), "桌面环境 is_available()=false（安全降级）")
	# 以下调用在桌面必须全部空返回，不得触碰 JNI
	bridge.show("https://bighat.icu:7000", Rect2i(0, 100, 420, 800))
	bridge.set_rect(Rect2i(0, 120, 420, 780))
	bridge.request_back()
	bridge.pause()
	bridge.resume()
	bridge.hide()
	bridge.toast("网页已全屏 · 按返回键回到站点列表")
	bridge.destroy()
	_check(failures, true, "show/set_rect/request_back/pause/resume/hide/toast/destroy 桌面无异常")
	_check(failures, not bridge.is_back_done(), "未投递返回键时 is_back_done()=false")
	_check(failures, not bridge.was_back_consumed(), "默认 was_back_consumed()=false")
	_check(failures, bridge.get_last_error().is_empty(), "桌面降级不产生 Java 异常记录")

	_check(failures, ProjectSettings.get_setting("application/config/quit_on_go_back", true) == false,
			"quit_on_go_back=false（返回键由项目接管）")

	var scene := load("res://scenes/Main.tscn") as PackedScene
	var main := scene.instantiate()
	main.set("store_path", TEST_PATH)
	root.add_child(main)
	await process_frame

	if not main.has_method("card_count"):
		printerr("[FAIL] Main.gd 脚本未挂载，中止自检")
		_finish(failures)
		return

	_check(failures, main.get("_native_web") != null, "Main 已实例化 WebView 桥")
	var rect: Rect2i = main.call("_web_rect_px")
	_check(failures, rect == Rect2i(0, 0, 420, 900),
			"全屏模式坐标换算 = (0,0,420,900)，实得 %s" % str(rect))
	_check(failures, main.get_script().get_script_constant_map().get("WEBVIEW_FULLSCREEN", false) == true,
			"WEBVIEW_FULLSCREEN=true（网页铺满整屏）")

	main.simulate_open(0)
	_check(failures, "桌面预览" in main.web_state_text(), "桌面浏览页显示降级状态而非插件状态")
	main.simulate_open_editor_add()
	_check(failures, main.editor_visible(), "编辑器可打开且不被残留 WebView 遮挡（桌面路径）")
	main.call("_close_editor")
	main.simulate_open(0)
	# 直接投递 Android 返回键通知（Node.NOTIFICATION_WM_GO_BACK_REQUEST=1007，实检值）
	main.notification(Node.NOTIFICATION_WM_GO_BACK_REQUEST)
	await process_frame
	_check(failures, not main.browser_visible(), "返回键通知关闭网页视图回到仪表盘")

	main.notification(MainLoop.NOTIFICATION_APPLICATION_PAUSED)
	main.notification(MainLoop.NOTIFICATION_APPLICATION_RESUMED)
	_check(failures, true, "暂停/恢复生命周期通知在桌面无异常")

	_wipe()
	_finish(failures)


func _finish(failures: Array[String]) -> void:
	if failures.is_empty():
		print("[RESULT] %d 项断言全部 PASS，退出码 0" % _assertions)
	else:
		printerr("[RESULT] 失败 %d 项: %s" % [failures.size(), str(failures)])
	quit(0 if failures.is_empty() else 1)


func _check(failures: Array[String], ok: bool, label: String) -> void:
	_assertions += 1
	print(("[PASS] " if ok else "[FAIL] ") + label)
	if not ok:
		failures.append(label)


func _wipe() -> void:
	if FileAccess.file_exists(TEST_PATH):
		DirAccess.remove_absolute(ProjectSettings.globalize_path(TEST_PATH))
