## 站点仓库（F1 数据层原型）
## 纯 RefCounted，不依赖任何 UI 节点，可在 headless 下独立自检。
## Godot 4.x 语法；版本以官方文档当前版本为准（见 01-gdd-android-sitedock.md Q1）。
##
## 设计约定：
##   1. 站点数据与「健康检测结果」分离存储 —— 状态缓存不写进 JSON，重启后重新检测。
##   2. 安卓端允许显式 http / 裸 IP / 非标端口（小程序不允许），仅对「未写协议」的补 https。
##   3. 所有 list/get 返回副本，外部改不到内部数组。
extends RefCounted

signal sites_changed
signal status_changed(site_id: String, status: Dictionary)

const MAX_SITES: int = 500

var _path: String = "user://sites.json"
var _sites: Array[Dictionary] = []
var _statuses: Dictionary = {}
var _seq: int = 0


func _init(path: String = "user://sites.json") -> void:
	_path = path
	load_from_disk()


# ---------------------------------------------------------------- 持久化

func load_from_disk() -> int:
	if not FileAccess.file_exists(_path):
		_sites = []
		sites_changed.emit()
		return 0
	var f := FileAccess.open(_path, FileAccess.READ)
	if f == null:
		push_warning("SiteStore: 读取失败 " + _path)
		return 0
	var text := f.get_as_text()
	f.close()
	var parsed: Variant = JSON.parse_string(text)
	if typeof(parsed) != TYPE_ARRAY:
		push_warning("SiteStore: JSON 格式非法，按空仓库处理")
		_sites = []
		sites_changed.emit()
		return 0
	_sites = []
	for entry in parsed:
		if typeof(entry) == TYPE_DICTIONARY:
			_sites.append(_normalize(Dictionary(entry)))
	if _sites.size() > MAX_SITES:
		var trimmed: Array[Dictionary] = []
		for i in range(MAX_SITES):
			trimmed.append(_sites[i])
		_sites = trimmed
	sites_changed.emit()
	return _sites.size()


func save_to_disk() -> bool:
	var f := FileAccess.open(_path, FileAccess.WRITE)
	if f == null:
		push_error("SiteStore: 写入失败 " + _path)
		return false
	f.store_string(JSON.stringify(_sites, "\t"))
	f.close()
	return true


# ---------------------------------------------------------------- CRUD

func create(data: Dictionary) -> String:
	if _sites.size() >= MAX_SITES:
		push_warning("SiteStore: 已达站点上限 %d" % MAX_SITES)
		return ""
	var item := _normalize(data)
	var url := String(item["url"])
	if url.is_empty() or host_of(url).is_empty():
		return ""
	item["order"] = _sites.size()
	_sites.append(item)
	_persist_and_notify()
	return String(item["id"])


func update(site_id: String, patch: Dictionary) -> bool:
	var idx := _index_of(site_id)
	if idx < 0:
		return false
	var merged: Dictionary = _sites[idx].duplicate(true)
	for key in patch.keys():
		if key == "id":
			continue
		merged[key] = patch[key]
	_sites[idx] = _normalize(merged)
	_persist_and_notify()
	return true


func remove(site_id: String) -> bool:
	var idx := _index_of(site_id)
	if idx < 0:
		return false
	_sites.remove_at(idx)
	_statuses.erase(site_id)
	_persist_and_notify()
	return true


func list(group: String = "") -> Array[Dictionary]:
	var out: Array[Dictionary] = []
	for item in _sites:
		if group != "" and String(item["group"]) != group:
			continue
		var copy: Dictionary = item.duplicate(true)
		var st: Dictionary = _statuses.get(item["id"], {})
		copy["status"] = st.duplicate(true)
		out.append(copy)
	out.sort_custom(func(a: Dictionary, b: Dictionary) -> bool:
		if bool(a["pinned"]) != bool(b["pinned"]):
			return bool(a["pinned"])
		return int(a["order"]) < int(b["order"])
	)
	return out


func get_by_id(site_id: String) -> Dictionary:
	var idx := _index_of(site_id)
	return {} if idx < 0 else _sites[idx].duplicate(true)


func groups() -> Array[String]:
	var out: Array[String] = []
	for item in _sites:
		var g := String(item["group"])
		if not out.has(g):
			out.append(g)
	out.sort()
	return out


func count() -> int:
	return _sites.size()


func toggle_pin(site_id: String) -> bool:
	var idx := _index_of(site_id)
	if idx < 0:
		return false
	_sites[idx]["pinned"] = not bool(_sites[idx]["pinned"])
	_persist_and_notify()
	return bool(_sites[idx]["pinned"])


# ---------------------------------------------------------------- 状态缓存（不落盘）

func set_status(site_id: String, status: Dictionary) -> void:
	_statuses[site_id] = status.duplicate(true)
	status_changed.emit(site_id, status)


func get_status(site_id: String) -> Dictionary:
	var st: Dictionary = _statuses.get(site_id, {})
	return st.duplicate(true)


# ---------------------------------------------------------------- 工具

static func normalize_url(raw: String) -> String:
	var s := raw.strip_edges()
	if s.is_empty():
		return ""
	if not (s.begins_with("http://") or s.begins_with("https://")):
		s = "https://" + s
	return s


static func host_of(url: String) -> String:
	var s := url
	var scheme := s.find("://")
	if scheme >= 0:
		s = s.substr(scheme + 3)
	var cut := s.find("/")
	if cut >= 0:
		s = s.substr(0, cut)
	var colon := s.find(":")
	if colon >= 0:
		s = s.substr(0, colon)
	return s


func _normalize(raw: Dictionary) -> Dictionary:
	var item := {
		"id": String(raw.get("id", "")),
		"url": normalize_url(String(raw.get("url", ""))),
		"name": String(raw.get("name", "")),
		"group": String(raw.get("group", "默认")),
		"pinned": bool(raw.get("pinned", false)),
		"order": int(raw.get("order", 0)),
		"created_at": int(raw.get("created_at", 0)),
	}
	if String(item["id"]).is_empty():
		item["id"] = _new_id()
	if String(item["name"]).is_empty():
		item["name"] = host_of(String(item["url"]))
	if int(item["created_at"]) == 0:
		item["created_at"] = int(Time.get_unix_time_from_system())
	return item


func _index_of(site_id: String) -> int:
	for i in range(_sites.size()):
		if String(_sites[i]["id"]) == site_id:
			return i
	return -1


func _new_id() -> String:
	_seq += 1
	return "s%d_%d" % [int(Time.get_unix_time_from_system() * 1000), _seq]


func _persist_and_notify() -> void:
	save_to_disk()
	sites_changed.emit()
