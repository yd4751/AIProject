extends SceneTree
## 仅做编译/解析校验：preload 会强制 GDScript 编译这些脚本，
## 语法或类型错误会让 Godot 报错并以非 0 退出。不依赖服务端、不建窗口。
const MAIN  := preload("res://scripts/game/main.gd")
const SKILL := preload("res://scripts/game/skill_tree.gd")
const MINI  := preload("res://scripts/game/minimap.gd")
const HUD   := preload("res://scripts/game/hud.gd")
const WV    := preload("res://scripts/game/world_view.gd")
const EN    := preload("res://scripts/game/entity_node.gd")
const NET   := preload("res://scripts/net/ow_net_client.gd")
const MSG   := preload("res://scripts/protocol/ow_messages.gd")
const PROTO := preload("res://scripts/protocol/ow_proto.gd")

func _initialize() -> void:
	print("compile OK: main/skill_tree/minimap/hud/world_view/entity_node/net/protocol")
	quit(0)
