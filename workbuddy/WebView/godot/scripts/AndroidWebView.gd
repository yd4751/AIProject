## Android 原生 WebView 桥（Godot 4.4+ 零插件路线）
##
## 原理：Godot 4.4 起提供 AndroidRuntime 单例与 JavaClassWrapper，GDScript 可以直接
## 调用 Android Java 类，无需编译 AAR、无需 Gradle build、无需 NDK/CMake。
## 用 Activity.addContentView() 把 android.webkit.WebView 叠在 Godot 渲染层之上，
## 按 GDScript 给的像素矩形定位，从而保留 Godot 画的顶部返回栏。
##
## 所有 View 操作必须通过 AndroidRuntime.createRunnableFromGodotCallable() 投递到
## Android UI 线程（官方 Toast 示例同款写法），否则 WebView 会抛线程异常。
##
## 边界：桌面 / 非 Android 平台全部安全降级（is_available() == false），不触碰 JNI。
## 真机上的 View 层级遮挡、状态栏偏移、dpi 换算仍需 adb 实机核对，见 docs/03。
## 注意：不使用 class_name —— headless `--script` 自检模式不会扫描全局类缓存，
## 否则 Main.gd 里的类型标注会解析失败。调用方一律用 preload。
extends RefCounted

const RUNTIME_SINGLETON: String = "AndroidRuntime"

## Android View 可见性常量
const VIEW_VISIBLE: int = 0
const VIEW_GONE: int = 8

var _runtime: Object = null
var _activity: Object = null
var _lp_class: Object = null
var _view: Object = null

var _rect_px: Rect2i = Rect2i(0, 0, -1, -1)
var _pending_url: String = ""
var _pending_toast: String = ""
var _visible: bool = false

## 返回键结果跨线程回传（UI 线程写、Godot 主线程读）
var _back_seq: int = 0
var _back_done_seq: int = -1
var _back_consumed: bool = false

var last_error: String = ""


# ---------------------------------------------------------------- 对外 API

func is_available() -> bool:
	if not OS.has_feature("android"):
		return false
	if not Engine.has_singleton(RUNTIME_SINGLETON):
		return false
	if _runtime == null:
		_runtime = Engine.get_singleton(RUNTIME_SINGLETON)
	if _runtime == null:
		return false
	if _activity == null:
		_activity = _runtime.call("getActivity")
		if _activity == null:
			last_error = "AndroidRuntime.getActivity() 返回空"
	return _activity != null


## 显示并加载 URL；rect_px 是 Android View 坐标系（物理像素）的矩形。
func show(url: String, rect_px: Rect2i) -> void:
	if not is_available():
		return
	_pending_url = url
	_rect_px = rect_px
	_visible = true
	_post(Callable(self, "_ui_show"))


func set_rect(rect_px: Rect2i) -> void:
	if not is_available():
		return
	_rect_px = rect_px
	if _visible:
		_post(Callable(self, "_ui_rect"))


func hide() -> void:
	if not is_available():
		return
	_visible = false
	_post(Callable(self, "_ui_hide"))


## 异步请求「网页内后退」；用 is_back_done()/was_back_consumed() 取结果。
func request_back() -> void:
	if not is_available():
		return
	_back_seq += 1
	_post(Callable(self, "_ui_back"))


func is_back_done() -> bool:
	return _back_done_seq >= _back_seq and _back_seq > 0


func was_back_consumed() -> bool:
	return _back_consumed


func pause() -> void:
	if is_available() and _view != null:
		_post(Callable(self, "_ui_pause"))


func resume() -> void:
	if is_available() and _view != null:
		_post(Callable(self, "_ui_resume"))


func destroy() -> void:
	if not is_available():
		return
	_visible = false
	_post(Callable(self, "_ui_destroy"))


func get_last_error() -> String:
	return last_error


## 原生 Toast（官方 JavaClassWrapper 示例同款写法）。
## 全屏 WebView 没有可见返回入口，用它提示「按返回键回到站点列表」。
func toast(message: String) -> void:
	if not is_available():
		return
	_pending_toast = message
	_post(Callable(self, "_ui_toast"))


# ---------------------------------------------------------------- JNI 辅助

func _post(c: Callable) -> void:
	if not is_available():
		return
	var runnable: Variant = _runtime.call("createRunnableFromGodotCallable", c)
	if runnable == null:
		last_error = "createRunnableFromGodotCallable 返回空"
		push_warning(last_error)
		return
	_activity.call("runOnUiThread", runnable)


## 构造 Java 对象。JavaClassWrapper 把构造函数登记在类名最后一段
## （内部类保留 $）；同时兼容空方法名这一内部键，避免不同版本差异。
func _new(cls: Object, ctor_name: String, args: Array) -> Object:
	if cls == null:
		return null
	var obj: Variant = cls.callv(ctor_name, args)
	if obj == null:
		obj = cls.callv("", args)
	return obj as Object


func _check_java(tag: String) -> void:
	if not OS.has_feature("android"):
		return
	var err: Variant = JavaClassWrapper.get_exception()
	if err != null:
		last_error = "%s: Java 异常 %s" % [tag, str(err)]
		push_warning(last_error)


# ---------------------------------------------------------------- UI 线程执行体

func _ui_create() -> void:
	if _view != null:
		return
	if _lp_class == null:
		_lp_class = JavaClassWrapper.wrap("android.widget.FrameLayout$LayoutParams")

	var web_cls: Object = JavaClassWrapper.wrap("android.webkit.WebView")
	var v: Object = _new(web_cls, "WebView", [_activity])
	if v == null:
		last_error = "android.webkit.WebView 构造失败"
		_check_java("WebView ctor")
		push_warning(last_error)
		return
	_view = v

	var s: Variant = _view.call("getSettings")
	if s != null:
		s.call("setJavaScriptEnabled", true)
		s.call("setDomStorageEnabled", true)
		s.call("setDatabaseEnabled", true)
		s.call("setUseWideViewPort", true)
		s.call("setLoadWithOverviewMode", true)
		s.call("setBuiltInZoomControls", true)
		s.call("setDisplayZoomControls", false)
		s.call("setMediaPlaybackRequiresUserGesture", false)
		# MIXED_CONTENT_ALWAYS_ALLOW：允许 http / 裸 IP / 非标端口站点里的混合内容
		s.call("setMixedContentMode", 0)
		_check_java("WebSettings")

	# 不设 WebViewClient 时页面内跳转会交给系统浏览器，必须显式设置
	var client_cls: Object = JavaClassWrapper.wrap("android.webkit.WebViewClient")
	var client: Object = _new(client_cls, "WebViewClient", [])
	if client != null:
		_view.call("setWebViewClient", client)
		_check_java("setWebViewClient")

	var lp: Object = _new(_lp_class, "FrameLayout$LayoutParams", [
		maxi(_rect_px.size.x, 1), maxi(_rect_px.size.y, 1),
	])
	if lp == null:
		last_error = "FrameLayout.LayoutParams 构造失败"
		_check_java("LayoutParams ctor")
		return
	_activity.call("addContentView", _view, lp)
	_view.call("setVisibility", VIEW_GONE)
	_check_java("addContentView")


func _ui_rect() -> void:
	if _view == null or _lp_class == null:
		return
	var lp: Object = _new(_lp_class, "FrameLayout$LayoutParams", [
		maxi(_rect_px.size.x, 1), maxi(_rect_px.size.y, 1),
	])
	if lp != null:
		_view.call("setLayoutParams", lp)
	_view.call("setX", float(_rect_px.position.x))
	_view.call("setY", float(_rect_px.position.y))
	_check_java("setRect")


func _ui_show() -> void:
	if _view == null:
		_ui_create()
	if _view == null:
		return
	_ui_rect()
	_view.call("setVisibility", VIEW_VISIBLE)
	if not _pending_url.is_empty():
		_view.call("loadUrl", _pending_url)
		_check_java("loadUrl")


func _ui_toast() -> void:
	var toast_cls: Object = JavaClassWrapper.wrap("android.widget.Toast")
	if toast_cls == null:
		return
	# Toast.LENGTH_LONG = 1；静态方法 makeText(Context, CharSequence, int)
	var t: Variant = toast_cls.call("makeText", _activity, _pending_toast, 1)
	if t != null:
		t.call("show")
		_check_java("Toast")


func _ui_hide() -> void:
	if _view == null:
		return
	_view.call("stopLoading")
	_view.call("setVisibility", VIEW_GONE)


func _ui_back() -> void:
	_back_consumed = false
	if _view != null:
		var can: Variant = _view.call("canGoBack")
		if can == true:
			_view.call("goBack")
			_back_consumed = true
			_check_java("goBack")
	_back_done_seq = _back_seq


func _ui_pause() -> void:
	if _view != null:
		_view.call("onPause")


func _ui_resume() -> void:
	if _view != null:
		_view.call("onResume")


func _ui_destroy() -> void:
	if _view == null:
		return
	_view.call("stopLoading")
	_view.call("setVisibility", VIEW_GONE)
	var parent: Variant = _view.call("getParent")
	if parent != null:
		parent.call("removeView", _view)
	_view.call("destroy")
	_view = null
