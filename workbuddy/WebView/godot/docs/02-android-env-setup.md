# Android 导出环境安装与验证（Q1 收口）

> 目标：让本机 Godot 4.7.2-stable 能导出可安装的 Android APK，补全之前卡住的三项硬前提。
> 用户约定：软件装到 `E:\install`，**各组件独立文件夹**，不改既有工程目录。

---

## 1. 目录布局（实测）

| 路径 | 内容 | 为什么放这里 |
|---|---|---|
| `E:\install\jdk-17\` | OpenJDK 17 GA（`17+35-2724`），含 `java.exe` / `keytool.exe` | 用户指定的独立目录 |
| `E:\install\android-sdk\` | Android SDK：`cmdline-tools\latest`、`platform-tools`、`build-tools\35.0.1`、`platforms\android-35`、`licenses` | 用户指定的独立目录 |
| `E:\install\_downloads\` | 三个安装包 + 8 个 `.part` 分片缓存，可留作离线重装 | 下载缓存，与运行目录分离 |
| `%APPDATA%\Godot\export_templates\4.7.2.stable\` | **导出模板**：`version.txt`、`android_debug.apk`、`android_release.apk` 等 | Godot 只从自己的 editor data 目录读模板，**不能**改放到 `E:\install` |
| `%APPDATA%\Godot\keystores\debug.keystore` | debug 签名（`androiddebugkey` / 口令 `android`） | Godot 编辑器设置的默认路径 |

`E:\install\gobot\` 里的 Godot 4.7.2 可执行文件保持原样，未改动。

---

## 2. 官方要求（Godot 4.7 文档原文）

`godot-docs/4.7/tutorials/export/exporting_for_android.rst`：

- **OpenJDK 17**（更高版本也支持，但推荐 17）
- `sdkmanager "platform-tools" "build-tools;35.0.1" "platforms;android-35" "cmdline-tools;latest" "cmake;3.10.2.4988404" "ndk;28.1.13356709"`
- Editor Settings 里两项：`Java SDK Path`、`Android SDK Path`（该目录下应有 `platform-tools/adb`）

本次只装了**预构建 APK 导出**必需的 3 个组件 + 已手动装好的 `cmdline-tools;latest`。
`NDK r28b` 与 `CMake 3.10.2` 是 **Gradle build / C++ 编译**才需要的，约 1.5 GB；等做 WebView 插件 AAR 时再补，不提前下载。

---

## 3. 安装来源与实测踩坑

| 组件 | 首选源 | 结果 | 最终采用 |
|---|---|---|---|
| JDK 17 | `aka.ms/download-jdk/microsoft-jdk-17-windows-x64.zip`（187 MB） | HTTP 200，但 12 分钟只下 80 KB，卡死 | **华为云** `repo.huaweicloud.com/openjdk/17/openjdk-17_windows-x64_bin.zip`（185 MB，20.8 s） |
| Android cmdline-tools | `dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip`（153 MB） | 16.8 s 下完 | 直接采用，解压后把内部 `cmdline-tools/` 改名为 `android-sdk/cmdline-tools/latest/` |
| 导出模板 `.tpz` | GitHub release（1.28 GB） | 单连接约 0.3 MB/s，12 分钟仅 176 MB | `ghfast.top` 镜像（约 1.2 MB/s）+ `godot/tools/fetch-templates.py` 8 线程 Range 并发 |

**两个环境限制**（写进脚本注释，避免复现时再踩）：

1. Git Bash 没有 `curl` / `wget`；PowerShell 工具不允许 `Add-Type` 动态编译 .NET 代码，也不允许显式 `cmd /c`。
   → 下载统一用系统内置的 `[System.Net.HttpWebRequest]` 流式请求；`sdkmanager.bat` 本质只是
   `java -cp sdkmanager-classpath.jar com.android.sdklib.tool.sdkmanager.SdkManagerCli`，**直接调 java.exe 绕开 .bat**。
2. Python 的 `urllib` 会走系统代理，而该代理拒绝直连 GitHub（502）；且个别镜像会忽略 `Range` 返回 200 全量。
   → 下载器只用可达镜像、每次重试轮换镜像、强制要求 `206`、按分片长度截断写入。

---

## 4. 配置改动

- 用户级持久环境变量：`JAVA_HOME=E:\install\jdk-17`、`ANDROID_HOME=ANDROID_SDK_ROOT=E:\install\android-sdk`。
  **没有改 PATH**：系统 PATH 里的 Java 1.7 保留给老程序，Godot 用绝对 `java_sdk_path`，sdkmanager 用进程内 `JAVA_HOME`，互不干扰。
- `%APPDATA%\Godot\editor_settings-4.7.tres` 两行：
  ```
  export/android/java_sdk_path = "E:/install/jdk-17"
  export/android/android_sdk_path = "E:/install/android-sdk"
  ```
- `debug.keystore` 用 JDK 17 的 `keytool` 生成（RSA 2048、10000 天），与编辑器里 `debug_keystore_pass = "android"` 匹配。
- `godot/export_presets.cfg`：新增 Android 预设，包名 `com.sitedock.app`，`gradle_build/use_gradle_build=false`，仅 `arm64-v8a`，导出到 `godot/build/SiteDock-debug.apk`。

---

## 5. 复现与验证命令

```powershell
# 环境自检（19 项）
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
& "E:\Project\workbuddy\WebView\godot\tools\verify-android-env.ps1"

# 模板包断点续传下载（镜像 + 8 线程 Range）
python -u godot/tools/fetch-templates.py --out "E:\install\_downloads\Godot_templates_parallel.tpz" --parts 8 --size 1281349702
```

```bash
# 导出 debug APK
GD="/e/install/gobot/Godot_v4.7.2-stable_win64_console.exe"
P="$(cygpath -w "$PWD/godot")"
"$GD" --headless --path "$P" --export-debug "Android" "E:/Project/workbuddy/WebView/godot/build/SiteDock-debug.apk"
```

---

## 6. 实测结果（2026-11-11 17:55，全部实跑）

| 验证项 | 命令 / 工具 | 结果 |
|---|---|---|
| 环境自检 | `godot/tools/verify-android-env.ps1` | **18 项 PASS / 0 FAIL，EXIT=0** |
| 模板包完整性 | `zipfile` 读全部 Android 条目（读时校验 CRC） | 35 个条目；`android_debug.apk` 127,260,725 B、`android_release.apk` 104,803,333 B、`android_source.zip` 214,418,211 B、`icudt_godot.dat`、`version.txt` 全部 CRC OK；整包 1,281,349,702 B |
| 模板安装 | 解压 5 个 Android 必需条目到 `%APPDATA%\Godot\export_templates\4.7.2.stable\` | 431 MiB，`version.txt = 4.7.2.stable`，与目录名一致 |
| 导出 debug APK | `--headless --export-debug "Android" ...` | **EXIT=0**，约 16–18 s，日志 `对齐 APK → 签名调试 APK → 校验 APK → [DONE] export` |
| APK 产物 | `godot/build/SiteDock-debug.apk` | 28,350,327 B（27.0 MiB），另有 `.idsig` 230,834 B |
| 签名校验 | `apksigner.bat verify --verbose` | `Verifies`，v2=true、v3=true，签名者 `CN=Android Debug`；EXIT=0 |
| 包信息 | `aapt2 dump badging` | `com.sitedock.app` / `SiteDock` / minSdk 24 / targetSdk 36 / `INTERNET` + `ACCESS_NETWORK_STATE` |
| 回归 | `selftest.gd` / `scene_check.gd` | 21 项 PASS / 22 项 PASS，退出码 0 |

### 6.1 导出过程中修掉的 4 个真实问题

1. `--export-debug` 首次失败：`"最小 SDK"/"目标 SDK" 只有在启用"使用 Gradle 构建"时才能覆盖` → 从 `export_presets.cfg` 删掉 `gradle_build/min_sdk` 与 `gradle_build/target_sdk`，用模板默认（minSdk 24 / targetSdk 36）。
2. 同次失败：`导出 Android 版本需要 ETC2/ASTC 纹理压缩格式` → `project.godot` 的 `[rendering]` 加 `textures/vram_compression/import_etc2_astc=true`。
3. 第一版导出成功的 APK **没有任何 `uses-permission`**（装上也上不了网）→ `export_presets.cfg` 加 `permissions/internet=true` 与 `permissions/access_network_state=true`，重新导出后 `aapt2` 确认两条权限都在。这是核心功能必需，不能漏。
4. 手工跑 `apksigner.bat` 时报 `UnsupportedClassVersionError: ... major.minor version 52.0`：因为 Bash 里 `PATH` 仍是系统 Java 7。**Godot 自己导出不受影响**（它用 `java_sdk_path` 的 JDK 17）；手工校验要先 `export PATH="/e/install/jdk-17/bin:$PATH"`。

### 6.2 两个非致命警告（如实记录）

- 导出日志：`Could not find version of build tools that matches Target SDK, using 35.0.1` —— 模板 targetSdk 是 36，本 SDK 只装了 35.0.1；预构建 APK 导出正常，若以后做 Gradle build 需补 36 系列 build-tools。
- `aapt2 dump badging`：`mipmap/themed_icon` 引用了一个不存在的 XML —— Godot 模板默认图标缺项，不影响安装与运行。

---

## 7. 仍未完成（不是环境问题）

> **2026-11-12 更新**：本节第 1 条已由零 AAR 方案解除，详见 `docs/03-android-webview-bridge.md`。当前导出仍用 `use_gradle_build=false`，**不需要 NDK / CMake / 36 系列 build-tools**。

- ~~**WebView 插件 AAR 未编译**~~ —— 官方 v2 插件强制 Gradle build（`config.gradle` 写死 `ndkVersion '29.0.14206865'`），成本约 1.5 GB。2026-11-12 改用 `JavaClassWrapper` + `AndroidRuntime` 在 GDScript 里直接创建系统 WebView，插件骨架保留为备选，不再是阻塞项。
> **2026-11-12 10:15 追加**：用户手机为 HarmonyOS 7.0.0 纯血鸿蒙，不支持 APK，因此「真机验证」不能在该设备上进行；Android 环境本身仍完好，可用于 Android 设备 / 模拟器验证。手机端主方向已转鸿蒙原生（`harmony/`）。

- **未做真机验证**：APK 只在本地生成并校验，没有 `adb install` / 真机运行；WebView 与 Godot 渲染 SurfaceView 的层级遮挡、返回键、生命周期、状态栏坐标偏移都要在真机核对。
- 桌面预览的网页区域仍是容器 + `OS.shell_open()` 降级，健康状态仍是演示值 —— 属于阶段二功能，与本次环境安装无关。

