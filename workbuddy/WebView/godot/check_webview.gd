extends SceneTree

func _init() -> void:
	for n in ["WebView", "Webview", "WebviewNode", "GodotWebView", "WebBrowser", "TextureRect"]:
		print("[CLASSDB] " + n + "=" + str(ClassDB.class_exists(n)))
	print("[OS] name=" + OS.get_name() + " android=" + str(OS.has_feature("android")))
	print("[OS] has shell_open=" + str(OS.has_method("shell_open")))
	quit(0)
