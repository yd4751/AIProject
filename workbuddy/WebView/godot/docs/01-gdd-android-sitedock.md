# 站点坞 · Godot Android 端 —— 阶段一策划案（待确认，未冻结）

> 文档编号：`01-gdd-android-sitedock.md`　｜　生成时间：2026-11-11  
> 状态：**待用户确认**。按工作流硬门禁，确认后才产出「设计冻结清单（frozen）」并进入阶段二工程；**本轮不搭正式工程目录**。  
> 引擎已核实：`E:\install\gobot\Godot_v4.7.2-stable_win64.exe`（**"gobot" 是安装目录名，不是笔误**），`--version` 输出 `4.7.2.stable.official.ed1daf0bf`。本轮 GDScript 已在该版本 headless 下实跑自检，**21 项断言全部 PASS、退出码 0**。不确定 API 仍以官方文档当前版本为准。

---

## 0. 结论先行

**换 Godot 是对的方向，而且刚好解掉微信小程序上无解的那个痛点。** 小程序里「任意站点原样渲染」被三重限制卡死（不能执行网页 JS / 必须备案且登记合法域名 / 个人主体不能用 web-view），所以才退化成「文字预览」。Android 上这些限制全部不存在：

- 不需要 ICP 备案、不需要合法域名白名单、不需要企业主体；
- 可以用 `http://`、裸 IP、非标端口 `:7000`、自签证书（按需配置网络安全策略）；
- 通过 **Android 原生 WebView 插件**可以真正执行 JS、渲染 SPA，这才是「像 APP 一样能浏览」的完整解。

**代价与现状**：Godot 4 没有内置 WebView 节点（实检见 §8.1），Android 端要走 Android 插件（Java/Kotlin + AAR）。**Q1 导出环境已于 2026-11-11 17:55 补齐并实跑导出成功**：JDK 17、Android SDK、4.7.2.stable 模板全部就位，产出 `godot/build/SiteDock-debug.apk`（27 MiB，v2/v3 签名校验通过）。安装与验证全过程见 `docs/02-android-env-setup.md`。

> **重要修正**：JDK 17 + Android SDK **不是 WebView 插件的额外代价**，而是 Godot 导出**任何** Android APK 的必需前提。所以路线 C（混合）相对路线 A（纯 Control）并没有更高的环境门槛 —— 环境一旦装好，插件只是再写一个 AAR。这让「推荐 C」更成立。

---

## 1. 需求收敛表

| 分类                | 内容                                                                                                                                                                                                                                        |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **已明确**           | ① 弃用微信小程序方案，改做 **Android 手机端 App**；② 产品延续「站点坞」：管理并浏览站点，体验像 APP 而不是浏览器；③ 现有站点 `https://bighat.icu:7000`（大礼帽），自带 JSON 接口；④ 用户已明确拒绝「只把网页拆成文字」的预览；⑤ 之前选择过「零服务端」路线；⑥ 小程序工程保留在本仓库，不删除；⑦ 引擎为 **Godot 4.7.2-stable**，路径 `E:\install\gobot\`，已实跑验证 |
| **按假设补**（均需 Q 确认） | A1 沿用 **Godot 4.x 语法**（已按 4.7.2 实跑，无需再猜版本）；A2 这是**自用单用户工具类应用**，不是游戏，无内购无上架；A3 竖屏、本地侧载 APK、不上应用商店；A4 目标 Android 9(API 28) ~ 15(API 35)；A5 数据仍存**本机**（`user://` JSON），不引入云数据库；A6 沿用「站点坞」名称与浅色主题                                             |
| **待确认**           | 见文末 `Q1`–`Q4`；未列出的美术风格、音效、多语言一律按「本阶段不做」处理，不擅自扩范围                                                                                                                                                                                          |

> 说明：这是**工具类应用而非游戏**，所以专家工作流里的「战斗数值 / 关卡曲线」适配为「工程规格与极值验算」（§5），「核心循环」适配为「核心用户流程」（§3）。不生搬游戏数值框架。

---

## 2. 产品定位

| 项      | 取值                              | 备注                                |
| ------ | ------------------------------- | --------------------------------- |
| 品类     | Android 工具类应用（站点管理器 + 通用网页客户端）  | 非游戏，Godot 只用 Control / 网络 / 音频子系统 |
| 目标平台   | Android 手机，竖屏                   | A3/A4                             |
| 目标用户   | 自用，单人                           | A2                                |
| 单次使用时长 | 1–5 分钟（查状态 / 进站点 / 播放）          | 假设                                |
| 分发与变现  | 本地侧载 APK，无内购无广告                 | A2/A3                             |
| 核心差异点  | **自有站点走原生界面，任意第三方站点走真 WebView** | 相对小程序的核心升级                        |

---

## 3. 核心用户流程（替代「核心循环」）

> **打开 → 仪表盘看在线状态 → 点站点卡片进入（自有站点切原生应用视图 / 其它站点切 WebView）→ 浏览·搜索·播放 → 返回或切站**

| 环节           | 目标耗时    | 判定            |
| ------------ | ------- | ------------- |
| 冷启动 → 仪表盘可交互 | ≤ 2.0 s | 设计目标，需真机实测    |
| 首屏状态出结果      | ≤ 3.0 s | 由 §5 的并发公式保证  |
| 点卡片 → 内容可见   | ≤ 1.5 s | 缓存命中时 ≤ 0.3 s |
| 返回仪表盘        | ≤ 0.2 s | 页面常驻，不重建      |

---

## 4. 功能模块条目

| 编号     | 模块     | 内容                                                   | 优先级 | 依赖                   |
| ------ | ------ | ---------------------------------------------------- | --- | -------------------- |
| **F1** | 站点仓库   | CRUD、分组、置顶、排序、URL 规范化、`user://` JSON 持久化；状态缓存与数据文件分离 | P0  | 无（本轮已给原型）            |
| **F2** | 健康检测   | 并发 GET、RTT、在线/离线/超时三态、结果缓存、失败重试上限                    | P0  | `HTTPRequest`        |
| **F3** | 仪表盘    | 卡片网格（虚拟化）、在线率、状态徽标、长按菜单、空态                           | P0  | F1/F2                |
| **F4** | 通用浏览   | **原生卡片视图 / Android WebView 原样渲染 / 阅读模式**三层，加载失败自动回退  | P0  | Android 插件（`Q2`）     |
| **F5** | 自有站点加速 | 探测 JSON 接口后渲染曲库/搜索/任务/播放控件，`AudioStreamPlayer` 播放    | P1  | `bighat.icu:7000` 接口 |

> F5 沿用小程序里已验证的接口事实：`/api/sites`、`/api/browse`、`/api/site/search`、`/api/site/play`、`/api/stream` 等确实存在并返回真实数据。Godot 端直接复用这套契约，不需要重新摸接口。



---

## 5. 工程规格表（可复算，含极值验算）

| 项          | 变量    | 设计取值    | 公式                                 | 极值验算                                                                     |
| ---------- | ----- | ------- | ---------------------------------- | ------------------------------------------------------------------------ |
| 并发检测数      | `C`   | 4       | 最坏批次耗时 `= ceil(N/C) × T`           | `N=50, T=3s` → `ceil(50/4)=13` × 3 = **39 s**，首屏不可接受                     |
| 首屏检测集      | `N₀`  | 8（置顶项）  | `ceil(8/4) × 1.5s`                 | **3 s** ✓；其余站点转入后台异步批次，不阻塞首屏                                             |
| 单站超时       | `T`   | 1500 ms | 最多 1 次重试 → 最坏 3.0 s                | 单站最坏 3.0 s，因 `HTTPRequest` 异步不卡主线程                                       |
| 站点数上限      | `MAX` | 500     | 单条约 320 B                          | `500 × 320 B = 160,000 B ≈ 156 KiB`，远低于内存预算                              |
| 缩略图缓存      | `K`   | 30 张    | `64×64×4 B = 16 KiB/张`             | `30 × 16 KiB = 480 KiB ≈ 0.47 MiB`                                       |
| 滚动帧预算      | `F`   | 60 fps  | `1000/60 = 16.67 ms/帧`             | 500 项若全量实例化 Control → 每帧遍历 500 节点；必须虚拟化，可见项 `V=12` → `12/500 = 2.4%` 节点量 |
| 单页 HTML 上限 | `L`   | 2 MiB   | 超出截断                               | 防止解析阶段 OOM                                                               |
| 包体估算       | `S`   | —       | Godot 4 arm64 导出模板约 30–60 MiB + 资源 | WebView 用系统组件，**不额外打包 Chromium**，插件增量约几十 KiB                             |

> 以上均为**设计取值（假设）**，不是实测结论。真机需用 Godot Profiler / Android Studio Profiler 复核；我不替实测下断言。

---

## 6. 目标平台规格（Android）

| 项     | 取值                                                            | 备注                                                         |
| ----- | ------------------------------------------------------------- | ---------------------------------------------------------- |
| 引擎    | **Godot 4.7.2-stable**                                        | 已核实 exe 与 `--version`，本轮 headless 实跑通过                     |
| 系统范围  | Android 9 (API 28) ~ 15 (API 35)                              | A4，以官方文档当前版本为准                                             |
| 基准屏幕  | 1080×2400 竖屏，适配安全区/刘海                                         | 假设                                                         |
| 权限    | `INTERNET`、`ACCESS_NETWORK_STATE`；后台播放另需 `FOREGROUND_SERVICE` | 明文 HTTP 需 `usesCleartextTraffic` 或 network security config |
| 导出链   | Android 导出模板 + JDK 17 + Android SDK，自签名 APK                   | **已装齐**，Q1 已解除（2026-11-11），见 §6.1 状态更新与 `docs/02-android-env-setup.md` |
| 网络特殊性 | 目标站点是 `:7000` 非标端口 HTTPS；证书为 TrustAsia DV（2027-02-04 到期）      | 安卓端不校验备案，但证书仍走系统信任链                                        |

**三档可行性结论**

| 档位    | 内容                                                                 |
| ----- | ------------------------------------------------------------------ |
| 直接做   | F1 / F2 / F3 / 设置：纯 Control + `HTTPRequest`，零插件、零原生代码（**代码层**可直接做） |
| 要裁剪   | F5 后台音频（前台服务 + 音频焦点）、500 项大列表（虚拟化）                                 |
| 建议换形态 | 无 —— Android 原生 WebView 可行，不必退回小程序或加服务端浏览器                         |
| 硬前提   | **导出 APK 前必须先补齐 §6.1 三项环境**，与选哪条渲染路线无关                             |

### 6.1 本机导出环境实测（2026-11-11）

| 检查项          | 实测结果                                                                               | 结论                                                           |
| ------------ | ---------------------------------------------------------------------------------- | ------------------------------------------------------------ |
| Godot exe    | `E:\install\gobot\Godot_v4.7.2-stable_win64.exe`，`4.7.2.stable.official.ed1daf0bf` | ✅ 可用，已跑 headless 自检                                          |
| Android 导出模板 | `%APPDATA%\Godot\export_templates\` 存在但**目录为空**                                    | ❌ 需在编辑器「管理导出模板」下载安装 4.7.2.stable                             |
| JDK          | `java version "1.7.0_80"`（Java 7）                                                  | ❌ Godot 4 需 JDK 17，Java 7 无法用于 Android 导出                    |
| Android SDK  | `ANDROID_HOME` / `ANDROID_SDK_ROOT` 未设置；常见路径均无 SDK                                 | ❌ 需装 cmdline-tools / platform-tools / build-tools / platform |
| 调试签名         | 未见 `debug.keystore`                                                                | ⚠️ 导出前需生成或用 Godot 自动生成                                       |

> **状态更新（2026-11-11 17:55，Q1 已解决）**：上表是**安装前**的实测记录，现三项已全部补齐并实跑验证：
> `E:\install\jdk-17`（OpenJDK 17+35）、`E:\install\android-sdk`（cmdline-tools 12.0 / platform-tools / build-tools 35.0.1 / platform android-35）、`%APPDATA%\Godot\export_templates\4.7.2.stable`（431 MiB，Android 5 个条目）；另生成 `debug.keystore`、写好编辑器两项路径与 `export_presets.cfg`。`--export-debug "Android"` 已产出可签名 APK，环境自检 18/18 PASS。详见 `docs/02-android-env-setup.md`。

> 这三项是 **Godot 导出任何 Android APK 的通用前提**，不是 WebView 路线特有的负担。换句话说：为了做出能装到手机上的 APK，这些原本就得装；装完之后，Android 原生 WebView 插件只是额外写一层 Java/Kotlin + AAR。

---

## 7. 节点树与信号清单（**草案，冻结后定稿**）

> ⚠️ **Autoload 划分与场景根节点结构属于「改了难回退」项**，按工作流先给方案、等你确认，不在本轮落地。

```
Main (Control)                        # 唯一主场景，页面切换不重建
├── Dashboard (Control)               # F3 仪表盘
│   ├── StatusHeader                  # 在线率 / 汇总
│   ├── SiteGrid (虚拟列表)  ── 实例化 SiteCard.tscn
│   └── EmptyHint
├── BrowserHost (Control)             # F4 浏览容器
│   ├── NativeView (Control)          # F5 自有站点原生视图
│   ├── WebViewHolder (Control)       # Android WebView 插件挂载点
│   └── ReadingView (RichTextLabel)   # 阅读回退
├── SiteEdit (Control)                # F1 表单
└── Settings (Control)

Autoload（方案，待确认）
├── SiteStore       # 数据层（本轮 SiteStore.gd 原型）
├── HealthChecker   # F2 并发检测
└── Nav             # 页面路由与返回栈
```

| 发送方             | 信号                                | 接收方                           | 触发时机      |
| --------------- | --------------------------------- | ----------------------------- | --------- |
| `SiteStore`     | `sites_changed`                   | `Dashboard`                   | 任意 CRUD 后 |
| `SiteStore`     | `status_changed(site_id, status)` | `Dashboard`                   | 检测结果写入    |
| `HealthChecker` | `check_finished(results)`         | `Dashboard`                   | 一批检测结束    |
| `Dashboard`     | `site_open_requested(site)`       | `Nav`                         | 点击卡片      |
| `BrowserHost`   | `open_failed(reason)`             | `Dashboard`                   | 三种浏览方式全失败 |
| `Settings`      | `config_changed(key, value)`      | `HealthChecker` / `SiteStore` | 超时、清缓存    |

---

## 8. 通用浏览路线（Q2 已拍板，以 §12 变更记录为准）

| 路线                       | 实现                                                                | 任意站点 / 执行 JS | 是否解决上轮「文字预览」    | 工作量 |
| ------------------------ | ----------------------------------------------------------------- | ------------ | --------------- | --- |
| **A 原生 Control 渲染**      | `HTTPRequest` 拉 HTML → 解析 → Control / `RichTextLabel`             | ❌ SPA 只剩骨架文案 | ❌ **正是上轮踩的坑**   | 低   |
| **B 全量 Android WebView** | Java/Kotlin `android.webkit.WebView` → Godot Android Plugin (AAR) | ✅ 真浏览器       | ✅               | 中高  |
| C 混合（备选，未采用）             | 自有站点走原生 JSON 接口（Control 原生界面）；其它站点走 B                             | ✅            | ✅ 且自有站点体验最像 APP | 中高  |

> **2026-11-11 用户决策（Q2 已拍板）**：「不需要跟小程序一样做网页解析了，直接用通用方法展示网页，这是 app 没有那么多限制」。
> 因此采用 **B 全量通用 WebView**：HTML 抓取 / 解析 / 抽文字 / 卡片化 / 阅读模式 / F5 自有站点 JSON 加速 **全部废弃**，所有站点统一由 Android 原生 WebView 原样渲染。上表 C 混合降为备选。

### 8.1 实检：Godot 4.7.2 没有内置 WebView

用 `godot/check_webview.gd` 实跑 `ClassDB.class_exists()`：

```
WebView=false  Webview=false  WebviewNode=false  GodotWebView=false  WebBrowser=false  TextureRect=true
OS.name=Windows  android=false  has shell_open=true
```

结论：**Godot 4 没有内置 WebView 节点**，「通用展示网页」必须走 Android 原生能力；桌面只能做容器占位 + `OS.shell_open()` 降级。

> **2026-11-12 勘误**：这里的「必须走 Android 原生插件」收窄为「必须走 Android 原生能力」。Godot 4.4+ 的 `JavaClassWrapper` + `AndroidRuntime` 允许 GDScript 直接创建系统 WebView，**不必编译 AAR**（见 `docs/03-android-webview-bridge.md`）。路线 B 不变，实现手段已换。

### 8.2 插件骨架（v2 格式，源码已给、未编译）

官方规范：`GodotPlugin(Godot godot)` 构造、重写 `getPluginName()` 返回 `"SiteDockWebView"`、`@UsedByGodot` 暴露方法、AAR 的 `AndroidManifest.xml` 用 `org.godotengine.plugin.v2.<PluginName>` metadata 注册；GDScript 侧 `Engine.has_singleton()` / `Engine.get_singleton()` 拿到 `JNISingleton` 调用。

| 文件 | 内容 |
|---|---|
| `godot/android/plugins/SiteDockWebView.java` | `showUrl(url)` 建系统 WebView（开 JS / DOMStorage / 媒体自动播放）、`setRect(l,t,w,h)` 限定区域避免遮顶栏、`hide()`、`back()`、`destroy()` |
| `godot/android/plugins/AndroidManifest.xml` | v2 注册片段；AAR 放 `res://android/plugins/SiteDockWebView-release.aar` |
| `godot/scripts/Main.gd` | `PLUGIN_NAME` + `_call_webview()`：有插件则 `plugin.call("showUrl", url)`；无插件显示容器与「系统浏览器打开」降级 |

> ⚠️ 骨架**未编译、未真机验证**；且 2026-11-12 起它已**降级为备选路径**：官方 v2 插件强制 `Use Gradle Build = true`，模板 `config.gradle` 写死 `ndkVersion '29.0.14206865'`，需再补约 1.5 GB NDK/CMake 与 36 系列 build-tools。当前主路径改用零 AAR 的 `JavaClassWrapper` 方案（`docs/03`）。骨架按约定保留，不删除。

**§7 草案相应调整**：`BrowserHost` 只保留 `WebViewHolder` + 桌面降级提示，删掉 `NativeView` / `ReadingView`；§4 的 F5 模块移除。

---

## 9. 编号待确认清单

| 编号     | 我需要什么                                                                                                     | 拿到后我会改哪一处                                      |
| ------ | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| **Q1** | ✅ **已解决**：用户授权按 `E:\install` 各组件独立目录自行安装；JDK 17 / Android SDK / 4.7.2.stable 模板已装好，18 项环境自检全 PASS，已实跑导出 `SiteDock-debug.apk` | 阶段二「能产出可安装 APK」已解锁，不再是阻塞项；渲染路线不变 |
| **Q2** | ✅ **已确认（B 全量通用 WebView）**：见 §12，废弃 HTML 解析/阅读模式/自有站点 JSON 加速 | 定 §8 路线；F4 节点树与阶段二工作量已明确 |
| **Q3** | 「浏览」主要面向 **自有播放器站点** 还是 **任意外部站点**（后者占比大致多少）                                                              | 调整 F4/F5 优先级与首屏默认视图；若外部站点为主，把 WebView 提到 P0 首位 |
| **Q4** | 小程序工程是**仅保留不维护**，还是要我把可复用资产（接口清单、站点数据、图标）迁进 Godot 目录                                                      | 决定是否新增 `docs/接口契约.md` 与资源迁移步骤；不影响本轮代码          |

> 确认后我会：产出带编号的**设计冻结清单并置为 frozen** → 按 §7 定稿节点树与 Autoload → 进入阶段二搭工程。冻结前不动工程结构。

---

## 10. 验证证据（本轮实跑，非静态推断）

命令（Git Bash）：

```bash
GD="/e/install/gobot/Godot_v4.7.2-stable_win64_console.exe"
"$GD" --version
"$GD" --headless --path "$(cygpath -w "$PWD/godot")" --script res://selftest.gd
```

实际输出（摘录）：

```
4.7.2.stable.official.ed1daf0bf
Godot Engine v4.7.2.stable.official.ed1daf0bf
[PASS] 未写协议自动补 https
[PASS] host 去掉端口与路径
[PASS] 保留显式 http / 裸 IP / 非标端口
[PASS] 置顶项排首位
[PASS] 状态缓存不写进 JSON
[PASS] 可存满 500 条
WARNING: SiteStore: 已达站点上限 500      ← 第 501 条拦截，属预期行为
[PASS] 第 501 条被上限拒绝
[RESULT] 21 项断言全部 PASS，退出码 0
EXIT=0
```

覆盖：CRUD、URL 规范化、host 解析、分组/置顶排序、状态缓存不落盘、落盘重载、500 条上限极值。自检会自行清理 `user://selftest-*.json`。

### 10.1 主场景真实运行与界面截图

新增可运行界面：`scenes/Main.tscn`（根节点挂脚本）、`scripts/Main.gd`（纯 Control 代码构建 UI，避免手堆 .tscn 节点）、`scene_check.gd`（场景级自检）。`project.godot` 已配 `run/main_scene`、420×900 竖屏、`gl_compatibility`。

```bash
"$GD" --headless --path "$P" --script res://scene_check.gd   # 22 项场景断言
"$GD" --headless --path "$P" --script res://selftest.gd       # 21 项数据层断言
timeout 30 "$GD" --path "$P" -- --snap           # → preview.png（仪表盘）
timeout 30 "$GD" --path "$P" -- --snap-web       # → preview-browse.png（通用网页视图）
timeout 30 "$GD" --path "$P" -- --snap-add       # → preview-add.png（添加站点）
timeout 30 "$GD" --path "$P" -- --snap-edit      # → preview-edit.png（编辑站点）
```

| 项 | 结果 |
|---|---|
| 场景自检 | **22 项断言全部 PASS、EXIT=0**，节点总数 **96**；覆盖仪表盘、网页视图绑定、添加 / 校验 / 落盘 / 编辑 / 删除全链路 |
| 数据层自检 | **21 项断言全部 PASS、EXIT=0**（CRUD / URL 规范化 / 状态分离 / 500 条上限） |
| 窗口渲染 | `OpenGL API 3.3.0 NVIDIA ... Compatibility`，**四张** 420×900 PNG 均 `err=0` |
| 界面 | 仪表盘（＋ / ⋯）、通用网页视图（状态 / URL / WebView 容器 / 桌面降级）、添加与编辑表单（预填 / 置顶 / 删除二次确认） |
| 中文字体 | `SystemFont` 指定「Microsoft YaHei」，截图实测**无方块、无空白** |

**真实排障记录**（都已修复）：

1. `--script` 的 `_init()` 里 `add_child` 后 `_ready()` 未触发 → 自检需 `await process_frame`。
2. `for t in [字面量]` 的 Variant 导致 `:=` 推断失败 → 显式 `PackedStringArray` + `String(t)`（该段已随解析方案废弃）。
3. `_ready()` 只认 `--snap` → 改为识别全部四个截图参数。
4. 长说明 Label 未换行撑出水平滚动条并截断文字 → 加 `autowrap_mode` + `custom_minimum_size` + `SIZE_EXPAND_FILL`，并给三个 `ScrollContainer` 设 `SCROLL_MODE_DISABLED`；修复后截图确认滚动条消失、文字完整。

**边界**：这是**阶段一预览版**。健康状态仍是演示值（真实并发 `HTTPRequest` 检测待阶段二）；桌面无 Android 插件，网页区域只是容器 + `OS.shell_open()` 降级；插件 AAR 未编译。**Q1 环境已补齐，debug APK 已导出并签名验证通过**（见 `docs/02-android-env-setup.md` §6），但尚未真机安装。Autoload 与场景拆分仍待冻结。

---

## 11. 本轮小结

**本轮产出**：本文档（阶段一策划案 + §8 路线决策 + §10.1 运行验证 + §12 变更记录）；`godot/scripts/SiteStore.gd`；`godot/selftest.gd`（21 项）；`godot/scenes/Main.tscn` + `godot/scripts/Main.gd`（仪表盘 / 站点 CRUD / 通用网页视图）；`godot/scene_check.gd`（22 项）；`godot/project.godot`；`godot/android/plugins/`（插件骨架）；四张真实渲染截图。追加：`godot/export_presets.cfg`、`godot/build/SiteDock-debug.apk`（27 MiB，v2/v3 签名）、`godot/docs/02-android-env-setup.md`、`godot/tools/verify-android-env.ps1`、`godot/tools/fetch-templates.py`。整套仍属**阶段一预览版**。

**已完成**：Q1 导出环境（JDK 17 / Android SDK / 4.7.2.stable 模板 / debug keystore / 导出预设）、Q2 渲染路线拍板、可运行预览工程与 4 张截图、debug APK 实跑导出与签名校验。

**未完成**：设计冻结清单、正式工程与 Autoload、真机实测与性能复核、真实并发健康检测。**插件 AAR 已不再是必须项**（2026-11-12 改为零 AAR 方案，见 `docs/03`）。

**建议下一步**：`adb install` 真机核对 WebView 与 Godot 视图层级遮挡、返回键、生命周期与坐标偏移；若层级被 SurfaceView 盖住，回退到已保留的 v2 插件骨架路线。

---

## 12. 变更记录：站点 CRUD + 通用网页视图（2026-11-11 17:10）

### 12.1 两条用户指令

1. 「没有添加站点功能吗」→ 补齐站点添加 / 编辑 / 删除 / 置顶界面。
2. 「不需要跟小程序一样做网页解析了，直接用通用方法展示网页，这是 app 没有那么多限制」→ **Q2 定为 B 全量通用 WebView**，废弃所有 HTML 解析。

### 12.2 已落地能力

| 能力 | 实现 | 验证 |
|---|---|---|
| 添加站点 | 品牌头「＋」→ 表单（名称 / URL / 分组 / 置顶）→ `store.create()` 落盘 → 仪表盘即时刷新 | 22 项断言中的新增、空 URL 校验、汇总更新 |
| 编辑 / 删除 | 卡片「⋯」→ 预填表单 → `store.update()`；删除需**两次点击确认** → `store.remove()` | 编辑后改名、删除后回到 3 张卡片 |
| URL 规则 | 未写协议自动补 https；显式 `http://` / 裸 IP / `:7000` 保留 | `selftest.gd` 21 项覆盖 |
| 通用网页视图 | 删除曲库 / 资源站点等解析演示内容，改为 WebView 容器；Android 有插件则 `plugin.call("showUrl", url)`，桌面降级到 `OS.shell_open()` | 网页视图绑定 URL 断言 + 截图 |
| 插件骨架 | v2 格式 Java 类 + manifest 片段（§8.2） | 未编译（缺 JDK 17 / SDK） |

### 12.3 覆盖声明

以下早期结论**以本章为准**：§4 的 F5 自有站点 JSON 加速移除；§7 的 `NativeView` / `ReadingView` 移除；§8 的「推荐 C」改为「采用 B」；§9 的 `Q2` 已确认、`Q3` 不再影响路线；§10.1 的 9 项 / 105 节点 / 两张图更新为 22 项 / 96 节点 / 四张图。

### 12.4 剩余工作（Q1 硬阻塞已解除）

**Q1 已解除**：2026-11-11 装齐导出环境并实跑导出成功，APK 已签名（`docs/02-android-env-setup.md` §6）。现在剩下的都不是环境问题：

1. ~~**WebView 插件 AAR 未编译**~~ —— **2026-11-12 已解除**：官方 v2 插件强制 Gradle build + NDK，改走零 AAR 的 `JavaClassWrapper` 路线，见 `docs/03-android-webview-bridge.md`。本节以 §13 为准。
2. **未做真机验证** —— `adb install`、WebView 与 Godot 视图层级遮挡、返回键、生命周期都要真机核对。
3. 桌面预览无法显示真实网页，这一点不因环境补齐而改变；健康状态仍是演示值。

---

## 13. 变更记录：零 AAR 原生 WebView 桥（2026-11-12）

### 13.1 覆盖声明

本章覆盖 §8.2、§11、§12.4 中关于「必须编译插件 AAR」的表述。**Q2 路线 B 不变**（全量通用 WebView、不解析 HTML），只换实现手段：

| 旧 | 新 |
|---|---|
| v2 Android 插件 AAR + `use_gradle_build=true` + NDK/CMake | `AndroidRuntime` + `JavaClassWrapper` 直接在 GDScript 创建 `android.webkit.WebView`，**零 AAR / 零 NDK / 非 Gradle 导出** |
| `Engine.has_singleton("SiteDockWebView")` 为唯一 Android 路径 | 三级分派：插件单例（备选）→ WebView 桥（主路径）→ 桌面 `OS.shell_open()` |

理由与实检证据见 `docs/03-android-webview-bridge.md` §1–§2。插件骨架按项目约定保留，未删除。

### 13.2 本轮落地

- 新增 `scripts/AndroidWebView.gd`（UI 线程投递、`addContentView`、定位、返回键、生命周期、Java 异常回传）、`webview_bridge_check.gd`（12 项）、`probe_runtime.gd`（运行时实检）。
- `scripts/Main.gd` 接入三级分派、像素矩形换算、返回键与生命周期通知；`project.godot` 设 `config/quit_on_go_back=false`。
- `scene_check.gd` 加解析失败硬中止；`preview-browse.png` 重截。
- **回归**：selftest 21 项、scene_check 22 项、bridge_check 12 项全 PASS；重导出 `SiteDock-debug.apk` 28,363,082 B，v2/v3 签名 Verifies，包内含 `assets/scripts/AndroidWebView.gdc`。

> **2026-11-12 10:08 追加**：应用户「要全屏」的要求，`WEBVIEW_FULLSCREEN=true` 成为默认，`_web_rect_px()` 返回 `(0,0,全宽,全高)`，网页铺满整屏；返回靠系统返回键 + 首次进入的原生 Toast 提示。bridge_check 扩到 14 项全 PASS，APK 重导出 28,367,178 B。详见 `docs/03` §7。

### 13.3 边界

真机侧（`adb install`、View 层级遮挡、状态栏偏移、返回键轮询、生命周期）**尚未验证**，不标已完成。健康状态仍是演示值；设计冻结、Autoload、正式工程结构仍待推进。

---

## 14. 重大变更：目标手机是 HarmonyOS 7，主方向改鸿蒙原生（2026-11-12 10:15）

用户接上手机准备真机调试，告知手机系统是 **HarmonyOS 7.0.0**（纯血鸿蒙）。实检与核查：

- 设备管理器显示 `HDC Device`（`USB\VID_12D1&PID_5000`，WinUSB 驱动），`adb devices` 为空；本机无 `hdc.exe` / DevEco Studio / 鸿蒙 SDK。
- HarmonyOS 7（HDC 2026 发布）**移除 AOSP 兼容层、不再支持 APK**，只运行 HAP；调试走 `hdc` + DevEco Studio，不是标准 `adb`。
- Godot 4.7.2 官方无 HarmonyOS 导出模板，`android.webkit.WebView` 在鸿蒙上不存在 → **本工程的 debug APK 无法安装到用户手机**。

用户据此拍板：**手机端主方向改为鸿蒙原生（ArkTS + ArkUI + ArkWeb）**，按项目约定新建独立目录 `harmony/`；Godot 工程保留，但不再是手机端主方向。

| 项 | Godot Android 版 | 鸿蒙原生版 |
|---|---|---|
| 系统 | Android / AOSP 兼容的旧鸿蒙 | HarmonyOS 7（API 26.0.0） |
| 包格式 | APK | HAP（必须签名才能装） |
| 调试 | `adb` + `logcat` | `hdc` + DevEco Studio / `hilog` |
| 网页渲染 | `android.webkit.WebView`（`JavaClassWrapper` 桥） | ArkWeb `Web` + `WebviewController` |
| 全屏 / 返回 | `addContentView` 矩形 + `quit_on_go_back=false` | `setWindowLayoutFullScreen` + `onBackPress()` |

`harmony/` 已实现：站点 CRUD / 分组置顶 / 500 上限 / Preferences 本地持久化（与 Godot 数据契约一致）、真实并发健康检测（`@kit.NetworkKit` http，置顶 8 个、并发 4、超时 1.5 s）、仪表盘、整屏 ArkWeb 浏览、返回键三级链路、INTERNET 权限与网页远程调试开关。`harmony/tools/verify.py` 静态自检 **67 项全 PASS**（本机暂无 hvigor，**未真编译、未真机运行**）。

**仍需用户完成**：下载 DevEco Studio 26 或 Command Line Tools（华为账号登录）、手机开 USB 调试、DevEco 自动签名；之后由 `hdc` 安装 HAP 并抓 `hilog` 验证。工具脚本：`harmony/tools/fetch_cli.py`（下载）、`harmony/tools/device_check.py`（设备与版本自检）。

