## headless 自检：验证 SiteStore 的 CRUD、URL 规范化、排序、状态分离与上限保护。
## 运行：godot --headless --path <godot目录> --script res://selftest.gd
## 预期：全部 [PASS]，末行 [RESULT] N 项断言全部 PASS，退出码 0。
## 若你的版本未在 _init() 执行本脚本，把 func _init() 改为 func _initialize() 再试（以官方文档当前版本为准）。
extends SceneTree

const SiteStore = preload("res://scripts/SiteStore.gd")

const TEST_PATH: String = "user://selftest-sites.json"
const BULK_PATH: String = "user://selftest-bulk.json"

var _assertions: int = 0


func _init() -> void:
	var failures: Array[String] = []
	_wipe()

	var store = SiteStore.new(TEST_PATH)

	_check(failures, store.count() == 0, "空仓库初始为空")

	var id_a: String = store.create({"url": "bighat.icu:7000", "name": "大礼帽", "group": "调试"})
	_check(failures, not id_a.is_empty(), "create 返回非空 id")

	var a: Dictionary = store.get_by_id(id_a)
	_check(failures, a.get("url", "") == "https://bighat.icu:7000", "未写协议自动补 https")
	_check(failures, SiteStore.host_of("https://bighat.icu:7000/api/sites") == "bighat.icu", "host 去掉端口与路径")

	var id_b: String = store.create({"url": "http://43.139.101.12:5173"})
	var b: Dictionary = store.get_by_id(id_b)
	_check(failures, b.get("url", "") == "http://43.139.101.12:5173", "保留显式 http / 裸 IP / 非标端口")
	_check(failures, b.get("name", "") == "43.139.101.12", "空名称回退为 host")
	_check(failures, id_a != id_b, "两个站点 id 不冲突")

	_check(failures, store.update(id_b, {"name": "测试站", "group": "调试"}), "update 返回 true")
	_check(failures, store.get_by_id(id_b).get("name", "") == "测试站", "update 生效")
	_check(failures, store.get_by_id(id_b).get("group", "") == "调试", "分组写入")

	store.toggle_pin(id_b)
	var ordered: Array[Dictionary] = store.list()
	_check(failures, not ordered.is_empty() and ordered[0]["id"] == id_b, "置顶项排首位")
	_check(failures, store.list("调试").size() == 2, "按分组过滤")
	_check(failures, store.groups().size() == 1, "分组去重")

	store.set_status(id_a, {"online": true, "rtt": 128})
	_check(failures, store.get_status(id_a).get("rtt", 0) == 128, "状态缓存可读")

	var reloaded = SiteStore.new(TEST_PATH)
	_check(failures, reloaded.count() == 2, "落盘重载后数量一致")
	_check(failures, reloaded.get_by_id(id_a).get("url", "") == "https://bighat.icu:7000", "落盘重载后 URL 一致")
	_check(failures, reloaded.get_status(id_a).is_empty(), "状态缓存不写进 JSON")
	_check(failures, reloaded.remove(id_a), "remove 返回 true")
	_check(failures, reloaded.count() == 1, "remove 生效")

	# 极值验算：站点数上限 MAX_SITES = 500，第 501 条必须拒绝
	var bulk = SiteStore.new(BULK_PATH)
	for i in range(SiteStore.MAX_SITES):
		bulk.create({"url": "https://example.com/%d" % i})
	_check(failures, bulk.count() == SiteStore.MAX_SITES, "可存满 500 条")
	_check(failures, bulk.create({"url": "https://example.com/over"}) == "", "第 501 条被上限拒绝")

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


func _wipe() -> void:
	for p in [TEST_PATH, BULK_PATH]:
		var path := String(p)
		if FileAccess.file_exists(path):
			DirAccess.remove_absolute(ProjectSettings.globalize_path(path))
