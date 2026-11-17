# 站点坞 · 项目长期笔记

## 产品定义
「站点坞」= 站点管理器 + 站点浏览客户端，管理并浏览自有/收藏站点，浏览体验要像 APP 而不是浏览器。核心站点：`https://bighat.icu:7000`（大礼帽，自带 JSON 接口，/nginx 7000~7010 → 6000~6010 TLS 卸载反代，TrustAsia 免费 DV 证书 2027-02-04 到期，ICP 备案曾在进行中）。

## 当前技术栈（2026-11-12 起）
- **主方向：HarmonyOS 7 原生 App（ArkTS + ArkUI + ArkWeb），独立目录 `harmony/`**（用户 2026-11-12 10:15 拍板）。原因：目标手机是 HarmonyOS 7.0.0 纯血鸿蒙，移除 AOSP、**不支持 APK**，Godot Android 版装不上。目标 SDK API 26（DevEco Studio 26.0.0.865）；**本机现有工具链只到 API 24，工程暂以 `6.1.1(24)` 编译**（见 `harmony/docs/01-build-and-debug.md`）。
- **Godot 4.7.2 Android 版（`godot/`）保留但不再是手机端主方向**，引擎路径 `E:\install\gobot\Godot_v4.7.2-stable_win64.exe`；其 debug APK 只在 Android 设备/模拟器有效。
- 微信小程序方案（`miniprogram/`）**已弃用但保留**，不再继续开发；其可复用资产：站点数据契约、图标、`/api/sites` 等接口清单、通用渲染解析思路。
- 不用服务端浏览器 / 抓取代理（`proxy-app/` 不部署）。

## 关键架构决策
- **鸿蒙原生实现（2026-11-12）**：`harmony/` 是 Stage 模型 / API 26.0.0 工程。全屏通用网页用 ArkWeb `Web` + `WebviewController`（「不解析 HTML」的产品口径不变）；`onBackPress()` 三级返回（网页内 `accessBackward/backward` → 回列表 → 退出），`setWindowLayoutFullScreen(true)` 沉浸式但**保留系统栏与手势返回**（避免 Godot 版「出不来」的风险）。数据层 Preferences 持久化、真实 http 健康检测（置顶 8 / 并发 4 / 超时 1.5 s / 500 上限）、站点 CRUD/分组置顶，与 Godot 数据契约一致。`harmony/tools/verify.py` 静态自检 **67 PASS / 0 FAIL**；**未真编译、未真机运行**。HAP 必须签名才能 `hdc install`，自动签名需华为账号登录，用户需亲自完成。
- 通用浏览**已定为 B 全量通用 WebView**（用户 2026-11-11 17:10 拍板）：所有站点统一由 Android 原生 WebView 原样渲染；**废弃 HTML 抓取/解析/抽文字/卡片化/阅读模式/F5 自有站点 JSON 加速**。原因：小程序那套解析对 SPA 只能得到界面标签文案，而 App 没有备案/合法域名限制。
- 实检：Godot 4.7.2 `ClassDB.class_exists("WebView")` 等全为 false → 无内置 WebView 节点，必须走 Android 原生能力；桌面预览只能容器占位 + `OS.shell_open()` 降级。
- **2026-11-12 实现手段变更：零 AAR 路线**（`godot/docs/03-android-webview-bridge.md`）。官方 v2 插件强制 `Use Gradle Build=true`，模板 `config.gradle` 写死 `ndkVersion 29.0.14206865`，需再补 ~1.5 GB NDK/CMake + 36 build-tools；改用 Godot 4.4+ 的 `AndroidRuntime` + `JavaClassWrapper` 在 GDScript 直接创建 `android.webkit.WebView`（UI 线程 Runnable → `activity.addContentView()`），**不编译 AAR、不用 Gradle、不需 NDK**。插件骨架 `godot/android/plugins/` 保留为备选（若真机 z-order 被 SurfaceView 盖住再启用）。路线 B 不变。
- 三级分派：插件单例（备选）→ `AndroidWebView` 桥（主路径）→ 桌面 `OS.shell_open()`。`project.godot` 需 `config/quit_on_go_back=false` 才能接管返回键。
- **默认整屏（2026-11-12 10:08）**：`Main.gd` 的 `WEBVIEW_FULLSCREEN=true` 让 `_web_rect_px()` 返回 `(0,0,全宽,全高)`，网页铺满整屏、盖住 Godot 画的返回栏；改 `false` 恢复「顶部留 100 逻辑像素返回栏」。全屏后返回靠系统返回键/手势（网页内后退 → 回仪表盘 → 退出）+ 首次进入的原生 Toast 提示。桌面预览截图里的蓝色返回栏只是降级 UI，真机上会被覆盖。
- 数据层：`user://` JSON 本地持久化，站点数据与健康检测状态缓存分离存储；站点数上限 500；首屏只测置顶 8 个、并发 4、超时 1.5s。
- Q2 路线已确认；**Q1 已于 2026-11-11 解决**（JDK17 / Android SDK / 模板 / debug keystore，18 项自检 PASS）。剩余工作：真机 `adb install` 验证 WebView 层级/返回键/生命周期/坐标、真实并发健康检测、Autoload 与正式工程结构冻结。插件 AAR **不再是阻塞项**。

## 环境事实（本机）
- Godot 4.7.2 可用，但 PATH 无 `godot`，headless 自检命令用绝对路径 + `--path "$(cygpath -w "$PWD/godot")" --script res://selftest.gd`。
- **Android 导出环境已装齐（2026-11-11）**：`E:\install\jdk-17`（OpenJDK 17+35）、`E:\install\android-sdk`（cmdline-tools 12.0 / platform-tools / build-tools 35.0.1 / platform android-35）、`%APPDATA%\Godot\export_templates\4.7.2.stable`（431 MiB Android 条目）、`%APPDATA%\Godot\keystores\debug.keystore`。用户级 JAVA_HOME/ANDROID_HOME/ANDROID_SDK_ROOT 已设；**PATH 未改**，系统 Java 1.7 仍在，手工用 java/apksigner 要前置 `/e/install/jdk-17/bin`。详见 `godot/docs/02-android-env-setup.md`。
- 安装/验证工具：`godot/tools/verify-android-env.ps1`（18 项）、`godot/tools/fetch-templates.py`（8 线程 Range 下载）、`godot/export_presets.cfg`（Android 预设，必须含 `permissions/internet=true` 否则 APK 无联网权限）。
- 导出命令：`"$GD" --headless --path "$P" --export-debug "Android" "E:/Project/workbuddy/WebView/godot/build/SiteDock-debug.apk"`。
- **目标设备硬约束（2026-11-12 10:15）**：用户手机是 **HarmonyOS 7.0.0 纯血鸿蒙**（移除 AOSP、不支持 APK、仅 HAP，调试走 hdc/DevEco Studio）。实检：设备管理器只有 `HDC Device`（`USB\VID_12D1&PID_5000`，WinUSB），`adb devices` 为空。Godot 官方无鸿蒙导出模板；社区 `godothub/godot-ohos` 可导出 HAP 但未验证，且 `AndroidWebView.gd` 依赖的 `android.webkit.WebView` 在鸿蒙不存在。
- **本机鸿蒙工具链（2026-11-12 实跑）**：Windows Server 2016 build 14393、RAM 32 GB、C 盘 3.9 GB、E 盘 153 GB → 工具一律装 `E:\install` 独立目录。华为官方带签名直链 Command Line Tools 6.1.1.268（2,599,585,816 B，SHA256 校验通过）已全量解压（49,191 文件 / 0 失败）到 `E:\install\harmony-commandline-tools\`；`hdc -v`=3.2.0d、`hvigorw --version`=6.24.1。**实读 `sdk/default/sdk-pkg.json`：SDK = HarmonyOS 6.1.1 / API 24 / Beta1，不含 API 26**；工程三字段已改 `"6.1.1(24)"`（API 24 编出的 HAP 可在 API 26 真机运行），装 DevEco 26 后再升 `"7.0.0(26)"`。
- **已真编译 + 已本地签名（2026-11-12 11:46）**：构建前必须 `JAVA_HOME=E:\install\jdk-17` 且 PATH 前置（系统 java 1.7 会让打包器报 `UnsupportedClassVersionError 52.0`）；`hvigorw --mode module -p product=default assembleHap --no-daemon` → BUILD SUCCESSFUL，unsigned HAP 121 KB。`harmony/tools/sign_hap.py` 用工具包自带 `OpenHarmony.p12` + `hap-sign-tool.jar` 完成 `generate-app-cert -outForm certChain` → `sign-profile` → `sign-app` → `verify-app` 全绿，产出 `harmony/build/entry-default-signed.hap` 158 KB，**无需华为账号**（但商业真机是否接受自签证书未实测）。编译修过 3 个真 bug：SDK 版本串须 `x.y.z(api)`、`module.json5` 缺 `deliveryWithInstall`、Java 7→17；坑：p12 现成 release 证书是自签的，拼链报 `11013002`，必须用 generate-app-cert 重签。
- **真机状态（11:46）**：11:13 公钥已发 `E000002` 等确认 → 11:23 变 `E000003 The user denied` → 备份 `~/.harmony/hdckey*` 并 `hdc kill/start -r` 换密钥后回到 E000002 → **11:46 设备掉线**：PnP `HDC Device` 由 OK→Unknown，`hdc list targets -v` 只剩 `COM1`，`hdc install` 报 `Not match target founded`。需用户重新插线、解锁亮屏、开发者选项点「撤销 USB 调试授权」、USB 模式切「传输文件」、弹窗勾「始终允许」。授权后链路：`device_check.py` → `sign_hap.py --fetch-udid` → `device_check.py --install`。`device_check.py` 本轮修过双 tab 状态解析与 hdc server 4 次重试。
- `E:\install\leidian\LDPlayer9\adb.exe` 与 `E:\install\nox\Nox\bin\adb.exe` 存在（第三方 Android 模拟器），可作 Godot APK 真机验证的备选。
- **实检常量（4.7.2）**：`Node.NOTIFICATION_WM_GO_BACK_REQUEST=1007`（不在 MainLoop）；`MainLoop.APPLICATION_PAUSED=2015` / `RESUMED=2014`；`quit_on_go_back` 内置默认 true。`probe_runtime.gd` 可复跑。
- **坑**：① `project.godot`/ConfigFile 注释符是 `;`，写 `##` 会让后一行设置解析不出来；② headless `--script` 不扫描 `class_name` 全局类，改用 `preload`；③ `apksigner.bat` 会命中系统 Java 7，改用 `E:/install/jdk-17/bin/java.exe -cp <sdk>/build-tools/35.0.1/lib/apksigner.jar com.android.apksigner.ApkSignerTool verify --verbose <apk>`；④ headless root viewport 只有几十像素，坐标换算要回退到项目标称 420×900。

## 预览版现状（2026-11-12）
- `godot/` 下已有**可运行**的阶段一预览工程：`scenes/Main.tscn` + `scripts/Main.gd` 代码构建 UI；仪表盘（＋ 添加 / ⋯ 编辑删除）→ 通用网页视图 → 返回；`selftest.gd` 21 项、`scene_check.gd` 22 项、`webview_bridge_check.gd` 14 项断言均实跑通过。
- 四张真实渲染截图 `preview.png` / `preview-browse.png` / `preview-add.png` / `preview-edit.png`（420×900）。启动：`"$GD" --path <godot目录>`；截图参数 `-- --snap` / `-- --snap-web` / `-- --snap-add` / `-- --snap-edit`。
- **可安装 debug APK** `godot/build/SiteDock-debug.apk`（28,367,178 B，v2/v3 签名，INTERNET + ACCESS_NETWORK_STATE，内含 `assets/scripts/AndroidWebView.gdc`）。Android 端网页区已接真实系统 WebView 且**默认整屏**（未真机验证），桌面仍是容器 + 系统浏览器降级。仍属预览版：健康状态是演示值、**未真机安装验证**、Autoload 与场景拆分待冻结。真机最大风险：全屏后 WebView 可能吞掉返回键导致出不来（`docs/03` §6）。

## 约定
- 不擅自删除既有工程目录；换技术栈时新建独立目录（`godot/`）。
- 数值/规格必须可复算并附极值验算；未实跑的东西不标「已完成」。
