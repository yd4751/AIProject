## 场景级自检：真正实例化 Main.tscn，验证界面构建、数据绑定与点击交互。
## 运行：godot --headless --path <godot目录> --script res://scene_check.gd
## 预期：5 项断言全部 PASS，输出节点总数，退出码 0。
extends SceneTree

const TEST_PATH: String = "user://scene-check-sites.json"

var _assertions: int = 0


func _init() -> void:
	var failures: Array[String] = []
	_wipe()

	var scene := load("res://scenes/Main.tscn") as PackedScene
	if scene == null:
		printerr("[FAIL] Main.tscn 无法加载")
		quit(1)
		return

	var main := scene.instantiate()
	main.set("store_path", TEST_PATH)
	root.add_child(main)
	# 在 --script 的 _init() 阶段，节点刚入树时 _ready() 尚未触发，必须等一帧
	await process_frame

	# 脚本若解析失败会一直卡在后续 await / 动态调用上，先硬中止
	if not main.has_method("card_count"):
		printerr("[FAIL] Main.gd 脚本未挂载，中止自检")
		_wipe()
		quit(1)
		return

	_check(failures, main.has_method("card_count"), "Main.tscn 脚本已挂载")
	_check(failures, main.card_count() == 3, "仪表盘渲染出 3 张站点卡片")
	_check(failures, "3 个站点" in main.summary_text(), "在线率汇总已计算")
	_check(failures, "2 个在线" in main.summary_text(), "在线数正确")

	main.simulate_tap(0)
	_check(failures, "大礼帽" in main.detail_text(), "点击卡片更新详情")
	_check(failures, "bighat.icu:7000" in main.detail_text(), "详情绑定真实站点 URL")

	main.simulate_open(0)
	_check(failures, main.browser_visible(), "点卡片进入浏览页")
	_check(failures, main.browser_title() == "大礼帽", "浏览页标题绑定站点名")
	main.simulate_back()
	_check(failures, not main.browser_visible(), "返回后回到仪表盘")

	# 通用网页视图（不做 HTML 解析）
	main.simulate_open(0)
	_check(failures, main.web_url_text() == "https://bighat.icu:7000", "通用网页视图绑定目标 URL")
	_check(failures, "WebView" in main.web_state_text() or "桌面预览" in main.web_state_text(), "网页视图给出插件 / 降级状态")
	main.simulate_back()

	# 添加站点：入口、校验、真实落盘
	main.simulate_open_editor_add()
	_check(failures, main.editor_visible(), "＋ 打开添加站点表单")
	_check(failures, main.editor_title() == "添加站点", "表单标题为「添加站点」")
	main.simulate_add({"name": "空地址", "url": ""})
	_check(failures, main.card_count() == 3, "空 URL 被拒绝，不产生脏数据")
	_check(failures, "URL 不能为空" in main.editor_hint(), "空 URL 给出校验提示")

	main.simulate_add({"name": "测试新站", "url": "example.org:8080", "group": "自建"})
	_check(failures, main.card_count() == 4, "保存后仪表盘出现第 4 张卡片")
	_check(failures, main.site_names().has("测试新站"), "新站点写入本地仓库")
	_check(failures, "4 个站点" in main.summary_text(), "在线率汇总随站点数更新")

	# 编辑：预填 + 改名
	main.simulate_open_editor_edit(3)
	_check(failures, main.editor_title() == "编辑站点", "⋯ 打开编辑表单")
	main.simulate_edit(3, "改名后的站点")
	_check(failures, main.site_names().has("改名后的站点"), "编辑后名称已更新")

	# 删除（两步确认）
	main.simulate_delete(3)
	_check(failures, main.card_count() == 3, "删除后回到 3 张卡片")
	_check(failures, not main.editor_visible(), "删除后关闭表单回到仪表盘")

	print("[TREE] 节点总数 = %d" % _count(main))
	print("[DATA] 汇总 = %s" % main.summary_text())
	print("[DATA] 详情 = %s" % main.detail_text().replace("\n", " | "))

	_wipe()
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


func _count(node: Node) -> int:
	var n := 1
	for c in node.get_children():
		n += _count(c)
	return n


func _wipe() -> void:
	if FileAccess.file_exists(TEST_PATH):
		DirAccess.remove_absolute(ProjectSettings.globalize_path(TEST_PATH))
