# 站点坞 · 鸿蒙工程真编译 / 本地签名 / 真机调试记录

> 2026-11-12 11:20–11:47 实跑记录。所有结论以本机命令输出为准，未实跑的一律标注。

## 1. 工具链落地（已完成，无需 DevEco Studio / 无需华为账号）

| 项 | 实跑结果 |
|---|---|
| 安装包 | 华为官方带签名直链 `commandline-tools-windows-x64-6.1.1.268.zip`，2,599,585,816 B，size + SHA256 双校验通过 |
| 解压 | `E:\install\harmony-commandline-tools\`，49,191 个文件 / 0 失败（另有 4,965 个 NDK 头文件按脚本策略跳过，不影响 ArkTS 构建） |
| `hdc` | `sdk/default/openharmony/toolchains/hdc.exe`，`hdc -v` = **Ver: 3.2.0d** |
| `hvigorw` | `bin/hvigorw.bat`，`--version` = **6.24.1** |
| SDK 版本 | 实读 `sdk/default/sdk-pkg.json`：`platformVersion 6.1.1` / `apiVersion 24` / `releaseType Beta1` / `displayName HarmonyOS 6.1.1` |
| 结论修正 | 之前推测「可能不含 API 26」**已证实**：这套工具链只到 **API 24**，不是鸿蒙 7 的 API 26 |

`compatibleSdkVersion 24` 编出的 HAP 可向下兼容运行在 API 26 真机；工程只用 ArkWeb / http / preferences 等旧接口，不依赖 API 26 新特性。装好 DevEco Studio 26 + API 26 SDK 后，把 `build-profile.json5` 三个 SDK 字段一并改成 `"7.0.0(26)"` 即可升级。

## 2. 真编译（已完成，BUILD SUCCESSFUL）

```powershell
$cli = 'E:\install\harmony-commandline-tools\command-line-tools'
$env:JAVA_HOME = 'E:\install\jdk-17'
$env:PATH = 'E:\install\jdk-17\bin;' + $env:PATH
Push-Location 'E:\Project\workbuddy\WebView\harmony'
& "$cli\bin\hvigorw.bat" --mode module -p product=default assembleHap --no-daemon
```

`CompileArkTS` 13.6 s 通过，`PackageHap` / `PackingCheck` / `SignHap` 全过，`BUILD SUCCESSFUL in 13 s 216 ms`。
产物 `harmony/entry/build/default/outputs/default/entry-default-unsigned.hap`，**121 KB**，内含 `ets/modules.abc`(86,744 B)、`resources.index`、`module.json`、图标。

### 编译过程中修掉的 3 个真 bug

| # | 报错 | 原因 | 修复 |
|---|---|---|---|
| 1 | `00306042 Specification Limit Violation`：SDK 版本必须形如 `'5.0.0(12)'` | 原配置写 `"26.0.0"`，HarmonyOS 配置模式要求 `x.y.z(api)` | 三字段改 `"6.1.1(24)"`；`verify.py` 断言改为正则校验格式 + 三字段一致 |
| 2 | `00303038 Schema validate failed: missingProperty 'deliveryWithInstall'` | `module.json5` 缺必填字段 | 补 `deliveryWithInstall: true`、`installationFree: false` |
| 3 | `00308018 UnsupportedClassVersionError: ohos/CompressEntrance 52.0` | 系统 `java` 是 **1.7.0_80**，打包器是 Java 8 编译 | 构建前设 `JAVA_HOME=E:\install\jdk-17` 并把 `bin` 前置到 PATH |

`hvigor` 会尝试调用 `wmic.exe`，本机安全策略拦截它；实测不影响构建成功（只是末尾一条提示），**不要为此去改安全策略**。

### 仍存在的无害 WARN（不影响功能）
- `app_name` 在 `AppScope` 与 `entry` 各声明一次 → 资源合并取先声明者。
- `getContext` / `showToast` / `showDialog` 在 API 24 标记 deprecated，以及 3 处 `Function may throw exceptions` 提示。
- `No signingConfig found for product default` → 由下面的本地签名脚本补上。

## 3. 本地签名（已完成，绕开华为账号自动签名）

工具包自带 `sdk/default/openharmony/toolchains/lib/` 里的 `hap-sign-tool.jar`、`OpenHarmony.p12`（公开口令 `123456`）、`OpenHarmonyProfileDebug.pem`、`UnsgnedDebugProfileTemplate.json`。

```bash
python harmony/tools/sign_hap.py                    # UDID 留空
python harmony/tools/sign_hap.py --fetch-udid       # 授权后自动从 hdc 读取 UDID 再签
```

链路（全部 exit=0）：

1. 从 p12 导出 `openharmony application ca` / `openharmony application root ca` 证书。
2. **`generate-app-cert -outForm certChain`**：以 `openharmony application release` 私钥重新签发一张「由 Application CA 签发」的应用证书链。
   > 坑：p12 里现成的 release 证书是**自签名**（issuer == subject），直接拼成链会报 `11013002 Signature does not match`；只给单张证书则报 `11013004 Profile cert must a cert chain`。
3. 按 `com.sitedock.app` 生成 debug Provision Profile（UUID 重生成、有效期 1 年、`development-certificate` 换成新 leaf），`sign-profile` → `.p7b`。
4. `sign-app` 把 unsigned HAP 签成 `harmony/build/entry-default-signed.hap`（**158 KB / 161,641 B**）。
5. `verify-app -outCertChain ... -outProfile ...` → exit=0。

> 注意：`verify-app` 的必填参数是 `-outCertChain` / `-outProfile`，不是 `-outFile`；传错会报 `11011005 Param is not trusted`。

**风险如实标注**：这是 OpenHarmony 社区自签证书，**HarmonyOS 商业真机是否接受尚未实测**。若 `hdc install` 报证书不受信，仍需 DevEco Studio 26 登录华为账号做自动签名；届时 `build-profile.json5` 的 `signingConfigs[]` 与 `products[0].signingConfig` 会由 IDE 写回。debug profile 的 `device-ids` 目前为空，拿到真机 UDID 后应重新签一次。

## 4. 真机链路现状（本轮关键变化：设备掉线了）

| 时间 | 实检 |
|---|---|
| 11:13 | `send pubkey over`，`E000002` 公钥已发、等待手机确认 |
| 11:23 | 变 `E000003 The user denied the access for the device` → 手机端把配对记为拒绝 |
| 11:24 | 备份旧密钥（`~/.harmony/hdckey*` → `.bak.111223`）、`hdc kill` / `start -r`，生成新密钥，状态回到 `E000002` 等待确认 |
| 11:24–11:25 | 45 秒等待窗口内手机未确认；`list targets -v` 始终 `Unauthorized` |
| **11:46** | **USB 设备掉线**：设备管理器 `HDC Device` 状态由 `OK` → `Unknown`，`hdc list targets -v` 连续三次只剩 `COM1 UART Ready`；`hdc install` 报 `Not match target founded, check connect-key please` |

结论：**目前不是签名或工程问题，设备物理/系统层已不在线**。USB 枚举曾正常（`USB\VID_12D1&PID_5000\6CT9K26128033976`），握手和公钥发送都成功过，说明驱动与线材可用。

### 手机端必须做的（我无法代劳）
1. **重新插拔数据线**，保持屏幕解锁、亮屏、停在桌面（锁屏会让授权弹窗被系统自动拒绝）。
2. 设置 → 系统 → 开发者选项：确认 **USB 调试** 打开；先点一次「**撤销 USB 调试授权**」清掉 11:23 那条拒绝记录；把 USB 连接模式从「仅充电」切到「**传输文件**」。
3. 插线后屏幕弹出「是否允许 USB 调试」→ 勾「始终允许」→ 点 **允许**。

### 授权后我这边的一键链路
```bash
python harmony/tools/device_check.py                 # 读系统版本 / 型号 / API / UDID
python harmony/tools/sign_hap.py --fetch-udid        # 带真机 UDID 重签
python harmony/tools/device_check.py --install harmony/build/entry-default-signed.hap
hdc -t <key> shell hilog | grep SiteDock             # 验证全屏 ArkWeb、返回键、页面加载
```

`device_check.py` 本轮已修两处实际缺陷：`list targets -v` 的双 tab 状态字段解析（曾把 `USB` 误当状态），以及 hdc server 偶发 `Connect server failed` 的 4 次重试 + `kill/start -r`。

## 5. 仍未验证（不标已完成）
- 真机安装、全屏 ArkWeb 显示、返回键三级链路、http 健康检测、`hilog` 日志。
- 手机真实系统版本 / API 版本（未授权读不到），「鸿蒙 7 = API 26」仍来自公开资料而非实机。
- OpenHarmony 自签证书在 HarmonyOS 商业真机上的接受度。
- ArkTS 编译通过 ≠ 运行时逻辑正确；上述 WARNING 未清理。
