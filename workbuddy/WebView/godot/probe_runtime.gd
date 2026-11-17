extends SceneTree

## 一次性实检：确认 Godot 4.7.2 的 Android 运行时 API 与常量，避免凭记忆硬编码。

func _init() -> void:
	print("[PROBE] godot=", Engine.get_version_info()["string"])
	print("[PROBE] has_setting quit_on_go_back=", ProjectSettings.has_setting("application/config/quit_on_go_back"))
	print("[PROBE] quit_on_go_back=", ProjectSettings.get_setting("application/config/quit_on_go_back", "<unset>"))
	print("[PROBE] app name=", ProjectSettings.get_setting("application/config/name", "<unset>"))
	print("[PROBE] viewport_width setting=", ProjectSettings.get_setting("display/window/size/viewport_width", "<unset>"))
	var cf := ConfigFile.new()
	var err: int = cf.load("res://project.godot")
	print("[PROBE] ConfigFile err=", err, " raw quit_on_go_back=", cf.get_value("application", "config/quit_on_go_back", "<missing>"))
	print("[PROBE] viewport visible rect=", root.get_visible_rect())
	print("[PROBE] Node.GO_BACK=", Node.NOTIFICATION_WM_GO_BACK_REQUEST)
	print("[PROBE] MainLoop.GO_BACK?=", _has_const("NOTIFICATION_WM_GO_BACK_REQUEST"))
	print("[PROBE] MainLoop.APP_PAUSED=", MainLoop.NOTIFICATION_APPLICATION_PAUSED)
	print("[PROBE] MainLoop.APP_RESUMED=", MainLoop.NOTIFICATION_APPLICATION_RESUMED)
	print("[PROBE] Node.APP_PAUSED=", Node.NOTIFICATION_APPLICATION_PAUSED)
	print("[PROBE] JavaClassWrapper singleton=", Engine.has_singleton("JavaClassWrapper"))
	print("[PROBE] ClassDB JavaClass=", ClassDB.class_exists("JavaClass"))
	print("[PROBE] ClassDB JavaObject=", ClassDB.class_exists("JavaObject"))
	print("[PROBE] OS.has_feature android=", OS.has_feature("android"))
	print("[PROBE] window_get_size=", DisplayServer.window_get_size())
	quit()


func _has_const(name: String) -> bool:
	return ClassDB.class_get_integer_constant_list("MainLoop").has(name)
