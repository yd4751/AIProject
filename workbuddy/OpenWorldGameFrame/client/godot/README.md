# Godot 客户端框架（OpenWorld Client）

用 **Godot 4.7.2** 实现的 2D 俯视角游戏客户端，对接 `openworld` 服务端的二进制 TCP 协议。
位置：`E:/install/gobot/Godot_v4.7.2-stable_win64_console.exe`（引擎二进制）；本项目客户端工程在 `client/godot/`。

> 这是「客户端框架」——验证从 socket 连接到 2D 渲染的完整链路能跑通，不是完整的玩法客户端。
> 后续可在 `main.gd` 基础上接真正的玩法（技能 UI、背包、任务、地图等）。

---

## 1. 怎么跑

### 先起服务端
```bash
# 在工作区根目录执行；gate 7001 / admin 7002，刷 50 只怪
./bin/server.exe -gate-addr :7001 -admin-addr :7002 -mobs 50
```
> 客户端默认连 `127.0.0.1:7001`（见 `client.cfg`）。**服务端换了端口，客户端必须同步加
> `--ow-port <端口>`**，否则会一直连不上——这是最容易踩的一个坑。

### 图形客户端（带 UI）
```bash
# Windows：console 版带日志输出；headless 自测加 --headless，正常玩省略即可
E:/install/gobot/Godot_v4.7.2-stable_win64_console.exe --path client/godot
```
- `WASD` / 方向键移动；鼠标左键点地移动、点实体攻击
- 数字键 `1`~`6` 或右侧技能树面板切换技能（技能树由服务端技能目录实时构建，三态着色）
- 右下小地图以自己为中心投影视野内实体
- 左上 HUD 显示连接状态、实体数、收包数、RTT、FPS、自身属性
- **出生点离怪群中心 (0,0) 有距离**，进游戏先往地图中心走，才能看到怪并开打

### 命令行参数（前缀 `--ow-` 避免和引擎参数冲突）
| 参数 | 默认 | 说明 |
|---|---|---|
| `--ow-host` | `127.0.0.1` | 网关地址 |
| `--ow-port` | `7001` | 网关端口（对应服务端 `-gate-addr`） |
| `--ow-secret` | `dev-secret-change-me` | HMAC 签名密钥（须与服务端 `-secret` 一致） |
| `--ow-player` | `godot-player` | 玩家名（即 token 里的 subject） |
| `--ow-no-reconnect` | 关 | 关闭断线自动重连 |
| `--ow-verify` | 关 | 验证模式：跑 4s 收真实数据后写 `verify.log` 并退出 |

### 自测
```bash
GODOT="E:/install/gobot/Godot_v4.7.2-stable_win64_console.exe"

# 1) 协议层字节级自测（无需服务端，离线比对 Go 黄金字节，76 断言）
$GODOT --headless --path client/godot --script res://selftest/protocol_test.gd

# 2) 全脚本编译冒烟（无需服务端）
$GODOT --headless --path client/godot --script res://selftest/compile_check.gd

# 3) 端到端集成自测（需先起服务端，9 断言；端口走 OW_TEST_PORT，默认 7300）
OW_TEST_PORT=7001 $GODOT --headless --path client/godot --script res://selftest/integration_test.gd

# 4) 图形主场景运行时验证（需先起服务端，写 verify.log 后退出）
$GODOT --headless --path client/godot --ow-port 7001 --ow-player godot-gui --ow-no-reconnect --ow-verify
```
> 第 4 条在本机沙箱里**必须后台跑**（`--headless` 完整主场景前台会被 SIGTERM）。

---

## 2. 目录结构

```
client/godot/
├── project.godot              # 工程配置（默认场景 = scenes/main.tscn）
├── client.cfg                 # 连接配置（net/player），可被命令行覆盖
├── icon.svg
├── scenes/
│   └── main.tscn              # 主场景：只挂一个 Node2D(main.gd)
├── scripts/
│   ├── protocol/              # 协议层（纯算法，零依赖、可单测）
│   │   ├── ow_writer.gd       # 大端写入（对齐 internal/protocol/buf.go）
│   │   ├── ow_reader.gd       # 大端读取
│   │   ├── ow_proto.gd        # 消息 ID 常量 + 帧打包/解包（对齐 protocol.go/frame.go）
│   │   ├── ow_auth.gd         # HMAC-SHA256 token 签发（对齐 pkg/authtoken）
│   │   └── ow_messages.gd     # 17 条消息 Encode/Decode（对齐 protocol/messages.go）
│   ├── net/
│   │   └── ow_net_client.gd   # 网络层：TCP 连接 + 状态机 + 信号（对齐 tools/bot）
│   └── game/
│       ├── main.gd            # 主控制器：组装 + 输入 + 配置
│       ├── world_view.gd      # 2D 世界容器：实体增删改、血条、伤害飘字、摄像机
│       ├── entity_node.gd     # 单实体可视化节点
│       ├── hud.gd             # 调试 HUD
│       ├── skill_tree.gd      # 技能树面板（服务端目录驱动，三态着色）
│       └── minimap.gd         # 小地图（复用 EntityState 广播）
└── selftest/
    ├── probe.gd               # 引擎 API 探针（HMAC / 大端缓冲 / TCP）
    ├── protocol_test.gd       # 协议层字节级断言（解码 Go 黄金字节 + 编码 hex 比对，76 项）
    ├── integration_test.gd    # 连真实服务端走完整链路（9 项）
    └── compile_check.gd       # 全脚本编译冒烟
```

---

## 3. 协议对齐（与服务端逐字节一致）

| 项 | 服务端（Go） | 客户端（GDScript） |
|---|---|---|
| 字节序 | `binary.BigEndian` | `StreamPeerBuffer.big_endian = true` |
| 帧格式 | `u32 len(含自身) + u16 msgID + body` | `OWProto.pack()` 同源构造 |
| 帧长度兜底 | 读 1MB 截断防事故 | 同 `max_frame_size = 1<<20` |
| 字符串 | UTF-8 + `u32 len` 前缀 | `put_string` 同源（`put_u32(len) + put_data(bytes)`） |
| 登录 token | `HMAC-SHA256(secret, "subject:exp")[:32hex]` | `OWAuth.make_token()` 同一算法 |
| 状态机 | gate: TCP→Login→EnterScene→Ready | `NetClient` 同序（CONNECTING→LOGGING_IN→ENTERING→READY→CLOSED） |
| 心跳 | `MsgC2SHeartbeat` + `MsgS2CHeartbeat` | 每 5s 发、收回应答记 RTT |
| 移动 | `MoveReq` 每 100ms 上报意图 | `send_move` 同间隔、本地预测 |

**17 条消息**全部覆盖：LoginReq/Resp、EnterSceneReq/Resp、MoveReq、Heartbeat×2、AttackReq、EntityEnter/State/Leave、EntityAttr、CombatHit、Notice、Kick，以及 M9 新增的 QuerySkills / SkillList（技能目录）。

自测方式：Go 端生成「黄金字节」（hex），Godot 端解码断言字段 + 编码回 hex 比对，确保一个字节都不错位（含中文 UTF-8、负数字段、token HMAC、`SkillList` 编解码）。

---

## 4. 架构要点

- **协议层零依赖、可单测**：`ow_writer/reader/proto/auth/messages` 全是不依赖场景树的 `RefCounted`，离线即可断言。
- **网络层用信号解耦**：`NetClient` 对外发 `connected / login_ok / entered / entity_enter / entity_state / entity_leave / entity_attr / combat_hit / kicked / notice / rtt_ms / disconnected`，主场景只接信号、不碰 socket。
- **服务端权威 + 客户端预测**：移动是「意图」——每 100ms 上报目标坐标，本地先插值跟手，服务端 `EntityState` 回来再纠正（与 `tools/bot` 一致）。
- **表现层用代码构建**：不手搓复杂 `.tscn`，`main.gd` 在 `_ready` 里动态搭 `world_view / camera / hud / net`，headless 与版本管理都省事。
- **重连**：`CLOSED` 后默认 3s 自动重连（保留 entity_id 续命），`--ow-no-reconnect` 关闭。

---

## 5. 已知边界 / 下一步

- **2D 俯视角**：坐标 (x, z) → Godot 的 (x, y)。服务端是 3D 坐标但玩法只用 x/z 平面，客户端按 2D 渲染足够。要真 3D 把 `world_view` 换成 `Node3D` 即可，协议层不变。
- **无资源美术**：实体是色块 + 血条 + 伤害飘字，纯验证渲染管线。正式美术替换 `entity_node.gd` 的 `ColorRect`/`Label` 为 `Sprite2D`/`AnimatedSprite2D`。
- **已接玩法 UI**：技能树面板（M9，服务端目录驱动）、小地图（复用广播）。**背包、任务、聊天等未接**——服务端没有物品系统，按"协议 + 表现两层齐了才做"的原则搁置。
- **服务端 MySQL 最终档案（M10）已落地**，客户端无需改动；etcd 服务发现适配器仍搁置。
