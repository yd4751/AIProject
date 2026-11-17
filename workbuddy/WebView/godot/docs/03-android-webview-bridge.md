# 03 · Android 原生 WebView 桥（零 AAR 路线）

> 日期：2026-11-12 · 引擎：Godot 4.7.2.stable · 状态：**桌面侧已实跑验证，真机侧待验证**

## 1. 为什么要换路线

上一版把「通用网页展示」押在 **v2 Android 插件 AAR** 上（`godot/android/plugins/SiteDockWebView.java`）。核查官方文档与本机导出模板后，发现两个硬约束：

| 约束 | 证据 |
|---|---|
| v2 插件**必须使用 Gradle 构建** | 官方文档「使用 v2 Android 插件」明确要求 `Use Gradle Build = true`；4.2 起废弃 `.gdap`，改用 `EditorExportPlugin` 包装（`plugin.cfg` + `_get_android_libraries`） |
| Gradle 模板**写死 NDK 版本** | `%APPDATA%\Godot\export_templates\4.7.2.stable\android_source.zip` 内 `config.gradle`：`ndkVersion '29.0.14206865'`、`compileSdk 36`、`targetSdk 36`、`javaVersion VERSION_17`；本机只有 build-tools 35.0.1 / platform android-35 |

也就是说 AAR 路线要再拉 NDK + CMake（约 1.5 GB）、36 系列 build-tools，以及 Gradle/Maven 依赖，成本与失败面都很大。

**替代路线**：Godot 4.4+ 提供 `AndroidRuntime` 单例与 `JavaClassWrapper`，允许 GDScript **直接调用 Android Java API**。4.7.2 在两者之上还加了 `create_proxy` / `create_sam_callback`。因此可以**不编译任何 AAR、不开 Gradle build、不装 NDK**，直接在脚本里创建 `android.webkit.WebView`。

> 决策：**主路径改为 JavaClassWrapper 直连系统 WebView**；v2 插件骨架保留但降级为备选（若真机层级有问题再启用）。用户既定路线 B「全量通用 WebView、不解析 HTML」不变。

## 2. 实检证据（不靠文档摘要）

`godot/probe_runtime.gd`，命令：

```bash
GD="/e/install/gobot/Godot_v4.7.2-stable_win64_console.exe"
P="$(cygpath -w "$PWD/godot")"
"$GD" --headless --path "$P" --script res://probe_runtime.gd
```

实跑输出（4.7.2.stable）：

```
[PROBE] has_setting quit_on_go_back=true      # 该内置设置存在，默认 true
[PROBE] quit_on_go_back=false                 # 项目设为 false 后生效
[PROBE] ConfigFile err=0 raw quit_on_go_back=false
[PROBE] Node.GO_BACK=1007
[PROBE] MainLoop.GO_BACK?=false               # 注意：不是 MainLoop 的常量
[PROBE] MainLoop.APP_PAUSED=2015 / APP_RESUMED=2014
[PROBE] JavaClassWrapper singleton=true
[PROBE] ClassDB JavaClass=true / JavaObject=true
[PROBE] OS.has_feature android=false
[PROBE] window_get_size=(0, 0)                # headless 无窗口
```

构建源码核查（`platform/android/java_class_wrapper.cpp`）：构造函数登记名是**类名按 `.` 切分后的最后一段，内部类保留 `$`**；内部键为 `""`。所以 `FrameLayout$LayoutParams` 必须用 `call("FrameLayout$LayoutParams", w, h)`，不能用 `call("LayoutParams", …)`。桥里 `_new()` 先试显式构造名，再兼容 `call("")`。

## 3. 实现

### 3.1 `godot/scripts/AndroidWebView.gd`

| 能力 | 做法 |
|---|---|
| 平台守卫 | `OS.has_feature("android")` + `Engine.has_singleton("AndroidRuntime")`；不满足时所有 public 方法空返回，桌面绝不触碰 JNI |
| UI 线程 | `_post()` 用 `AndroidRuntime.createRunnableFromGodotCallable(callable)` 包成 Java `Runnable`，再 `activity.runOnUiThread(runnable)`（官方 Toast 示例同款写法） |
| 创建 View | `JavaClassWrapper.wrap("android.webkit.WebView")` → `_new(cls, "WebView", [activity])` |
| 设置 | 开 JS / DOMStorage / Database / WideViewport / 缩放 / 媒体自动播放，`setMixedContentMode(0)`（`MIXED_CONTENT_ALWAYS_ALLOW`，保证 http、裸 IP、`:7000` 站点的混合内容可载） |
| 站内跳转 | 显式 `setWebViewClient(new WebViewClient())`，否则页面链接会交给系统浏览器 |
| 挂载 | `activity.addContentView(webView, new FrameLayout$LayoutParams(w, h))`，叠在 Godot 渲染层之上 |
| 定位 | `setLayoutParams()` 改宽高 + `setX()/setY()` 设偏移。**默认 `WEBVIEW_FULLSCREEN=true`：矩形 `(0,0, 全宽, 全高)`，网页铺满整屏（含沉浸状态栏区域）**；改成 `false` 才在顶部留 `NAV_HEIGHT=100` 给 Godot 画的返回栏 |
| 显隐 / 加载 | `setVisibility(0/8)`、`loadUrl()`、`stopLoading()` |
| 返回提示 | `toast()` 用 `android.widget.Toast.makeText(activity, msg, LENGTH_LONG).show()`；全屏后无可见返回入口，首次进入网页时提示「按返回键回到站点列表」 |
| 返回键 | `canGoBack()` 必须在 UI 线程查 → 结果用 `_back_seq/_back_done_seq` 跨线程回传，GDScript 侧最多轮询 300 ms |
| 生命周期 | `onPause()` / `onResume()` / `destroy()` + `getParent().removeView()` |
| 异常 | 每步后 `JavaClassWrapper.get_exception()` 写进 `last_error`，页面上可见 |

### 3.2 `godot/scripts/Main.gd` 接入

- 三级分派：**① 插件单例（若日后编译 AAR）→ ② `AndroidWebView` 桥（当前主路径）→ ③ 桌面 `OS.shell_open()` 降级**。
- **`WEBVIEW_FULLSCREEN=true`（默认）**：`_web_rect_px()` 返回 `(0,0, 全宽, 全高)`，原生 WebView 盖住包括返回栏在内的全部 Godot UI，网页就是整屏。改这一个常量为 `false` 即恢复「顶部 100 逻辑像素留返回栏」的旧布局。
- `_web_rect_px()`：`get_viewport().get_visible_rect().size`（逻辑）与 `DisplayServer.window_get_size()`（物理）算比例；headless / 窗口未就绪时回退到项目标称 420×900。
- 全屏后返回靠**系统返回键 / 手势**：网页内后退 → 无历史时回仪表盘 → 仪表盘再按退出；首次进入用原生 Toast 提示一次。
- `get_viewport().size_changed` 与 `APPLICATION_RESUMED` 时重新 `set_rect()`；`_open_editor()` 前主动 `hide()`，避免残留 WebView 盖住表单。
- `project.godot` 设 `application/config/quit_on_go_back=false`（实检默认 true，不改则返回键直接退出）。
- `_notification()` 处理：
  - `Node.NOTIFICATION_WM_GO_BACK_REQUEST`(1007)：编辑器表单 → 关表单；网页视图 → 先网页内后退，不能后退则回仪表盘；仪表盘 → 退出。
  - `NOTIFICATION_APPLICATION_PAUSED`(2015) / `RESUMED`(2014)：WebView 暂停/恢复。
  - `EXIT_TREE` / `PREDELETE`：销毁 WebView。

### 3.3 新增/修改文件

| 文件 | 变更 |
|---|---|
| `godot/scripts/AndroidWebView.gd` | 新增，WebView 桥（无 `class_name`，用 `preload`）；含 `toast()` |
| `godot/scripts/Main.gd` | 三级分派、`WEBVIEW_FULLSCREEN`、坐标换算、返回键、生命周期、Toast 提示 |
| `godot/project.godot` | `config/quit_on_go_back=false` |
| `godot/webview_bridge_check.gd` | 新增，14 项桥接层自检 |
| `godot/probe_runtime.gd` | 新增，运行时常量/设置实检探针 |
| `godot/scene_check.gd` | 加「脚本未挂载即硬中止」保护，避免解析失败后卡死 |
| `godot/preview-browse.png` | 重截（桌面降级占位区扩大到 540 高，更贴近整屏观感） |

## 4. 验证结果（2026-11-12 10:08，全屏版）

| 项 | 结果 |
|---|---|
| `selftest.gd` | **21 项全 PASS** |
| `scene_check.gd` | **22 项全 PASS**，节点 96 |
| `webview_bridge_check.gd` | **14 项全 PASS**：桌面降级、全屏坐标 `(0,0,420,900)`、`WEBVIEW_FULLSCREEN=true`、Toast 桌面安全、编辑器不被遮挡、返回键通知、暂停/恢复 |
| `--export-debug "Android"` | EXIT=0，`godot/build/SiteDock-debug.apk` **28,367,178 B** |
| 签名 | `apksigner verify --verbose`：v2 = true、v3 = true、Verifies（用 JDK17 直接调 `com.android.apksigner.ApkSignerTool`，`apksigner.bat` 会命中系统 Java 7） |
| 包信息 | `com.sitedock.app`、targetSdk 36、`INTERNET` + `ACCESS_NETWORK_STATE` |
| 包内容 | APK 内含 `assets/scripts/AndroidWebView.gdc`(17,588 B)、`Main.gdc` 内的「网页已全屏」Toast 文案，`assets/project.binary` 内含 `quit_on_go_back` |
| 截图 | `--snap-web` → `preview-browse.png` 420×900，err=0 |

## 5. 踩过的坑（可复用）

1. **`class_name` 在 headless `--script` 下不可用**：全局类缓存不扫描，会导致 `Could not find type "AndroidWebView"` 且自检卡死。改用 `preload` + `RefCounted` 动态调用。
2. **`project.godot` / ConfigFile 注释符是 `;`，不是 `#`/`##`**：写 `##` 会让其后一行设置解析不出来（`ConfigFile.get_value` 返回 `<missing>`）。
3. **常量归属与数值**：`NOTIFICATION_WM_GO_BACK_REQUEST` 在 `Node` 不在 `MainLoop`；`APPLICATION_PAUSED=2015`、`RESUMED=2014`（网上摘要常写反）。一律用符号常量。
4. **headless 的 root viewport 只有几十像素**：坐标换算要回退到项目标称尺寸，否则 WebView 会被算成 1 px 高。
5. **`apksigner.bat` 走系统 Java 7**：用 `"E:/install/jdk-17/bin/java.exe" -cp .../lib/apksigner.jar com.android.apksigner.ApkSignerTool verify --verbose <apk>`。
6. **`apksigner` 与 Gradle 无关**：非 Gradle 导出（当前）签名正常；只有切到 v2 插件路线才需要 `use_gradle_build=true` 与 NDK。

## 6. 仍未验证 / 真机核对清单

> **2026-11-12 10:15 追加（重要）**：用户目标手机是 HarmonyOS 7.0.0 纯血鸿蒙，**不支持 APK**，本清单的 `adb install` 无法在该手机上执行。真机核对需另找 Android 设备（本机 `E:\install\leidian` / `nox` 各自带 `adb.exe`，可作模拟器备选）。手机端主方向已改为 `harmony/` 鸿蒙原生工程，见 `docs/01` §14。

本机 `adb devices` 为空，以下**未在真机实跑，不标已完成**：

- [ ] `adb install -r godot/build/SiteDock-debug.apk` 后能启动、点卡片能看到**整屏**真实网页（含 SPA、JS、`:7000`、http 裸 IP），确认 `screen/immersive_mode=true` 下状态栏确实隐藏、网页延伸到屏幕顶。
- [ ] **全屏后的返回路径（最高风险）**：WebView 自己会消费 `KEYCODE_BACK` 做网页内后退，可能让 Godot 收不到 `NOTIFICATION_WM_GO_BACK_REQUEST`。需确认「网页内后退 → 无历史时回仪表盘 → 仪表盘再按退出」三级都可达；若返回键被 WebView 吞掉导致出不来，回退方案是把 `WEBVIEW_FULLSCREEN` 改 `false` 恢复可见返回栏，或加一个原生悬浮返回按钮。
- [ ] **View 层级**：Godot 的渲染 SurfaceView 若 `setZOrderOnTop(true)` 会盖住 `addContentView` 加的 WebView；若网页不可见，需改走 v2 插件 AAR 或在插件层调整 z-order。
- [ ] **坐标/状态栏偏移**：`DisplayServer.window_get_size()` 与 Android View 像素的换算在有挖孔屏 / 导航栏时是否准确；必要时调 `_web_rect_px()` 与 `setY()`。
- [ ] 返回键：`canGoBack()` 的 UI 线程结果回传链路、300 ms 轮询是否够；Toast 首次提示是否正常弹出。
- [ ] 生命周期：切后台 / 回前台后 WebView 是否继续播放、是否需要额外 `onPause/onResume` 配合。
- [ ] `setMixedContentMode(0)` 在 targetSdk 36 的 WebView 上的实际效果。

建议真机验证命令：

```bash
ADB="E:/install/android-sdk/platform-tools/adb.exe"
"$ADB" install -r E:/Project/workbuddy/WebView/godot/build/SiteDock-debug.apk
"$ADB" logcat -s godot:* GodotWebView:* AndroidRuntime:* | tee sitedock-logcat.txt
```

健康状态仍是演示值（`site.set_status` 写死），真实并发 `HTTPRequest` 检测、设计冻结清单、正式 Autoload 与场景拆分均属后续阶段。

---

## 7. 变更记录：改为默认整屏（2026-11-12 10:08）

用户看到「顶部 100 逻辑像素留给返回栏」后问「是不是做不到全屏」。**不是能力限制**——WebView 是 `addContentView()` 叠上去的独立原生 View，矩形完全由 `_web_rect_px()` 决定，之前只是留了返回栏的产品取舍。

本次改动：

| 项 | 改前 | 改后 |
|---|---|---|
| `_web_rect_px()` | `(0, 100*sy, w, h-100*sy)` | `(0, 0, w, h)`，由 `WEBVIEW_FULLSCREEN` 常量控制，改 `false` 可一键切回 |
| 返回入口 | Godot 画的蓝色返回栏可见可点 | 全屏无可见入口 → 系统返回键 / 手势 + 首次进入的原生 Toast 提示 |
| 编辑器防护 | 无 | `_open_editor()` 前主动 `hide()`，避免残留 WebView 盖住表单 |
| 桌面预览 | 占位卡片 320 高 | 占位卡片 540 高，更接近整屏观感；文案注明「真机整屏」 |

新增 `AndroidWebView.toast()`（`Toast.makeText(...).show()`，官方 JavaClassWrapper 示例同款）。自检从 12 项扩到 14 项，全 PASS；APK 重新导出为 28,367,178 B，v2/v3 签名 Verifies。

> 注意：`preview-browse.png` 是**桌面降级截图**，仍显示蓝色返回栏和占位卡片 —— 那是为了桌面可交互，真机上这两者都会被原生 WebView 完全覆盖，整屏只有网页。
