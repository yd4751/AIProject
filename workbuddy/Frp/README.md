# frptun

轻量、稳定、支持高负载的端口转发 / 内网穿透工具。

单个二进制文件，通过配置决定以**服务端**或**客户端**模式运行：

- 服务端部署在公网服务器，接收公网访问并转发给客户端
- 客户端运行在内网，主动连接服务端，把请求转给本地服务
- 内嵌 Web 管理端：实时连接、流量监控、IP/机器码黑白名单授权，SQLite 持久化
- 纯 Go 实现（无 CGO），一条命令交叉编译出 Windows / Linux / macOS 多平台二进制

---

## 架构

```
公网访问者 ──TCP──> [frps 服务端 :映射端口]
                          │  connID 配对
                          │  控制通道(new_conn 信令)
                          v
                    [frpc 客户端 ──主动拨号──> frps 控制端口]
                          │
                          v
                     本地服务 127.0.0.1:8080
```

**设计要点（稳定性 / 高负载）**

- 控制信令与数据流分离：控制通道长连接只传心跳和 new_conn，数据走独立连接，互不阻塞
- 单端口复用：控制连接(0xC1)与数据连接(0xD2)共用一个监听端口，防火墙只开一个端口
- 每连接一对 goroutine 双向转发，TCP 半关闭(CloseWrite)保证 EOF 正确传播
- 32KB 缓冲区池(`sync.Pool`)复用，降低 GC 压力
- TCP_NODELAY + KeepAlive；原子计数限流；pending 连接超时自动回收
- 客户端指数退避 + 抖动重连，避免断线风暴
- 心跳读超时检测僵死连接；服务端端口冲突检测与资源回滚

## 快速开始

### 1. 构建

```bash
# 当前平台
go build -o frptun ./cmd/frptun

# 全平台（输出到 dist/）
bash build.sh        # Linux/macOS
build.bat            # Windows
```

### 2. 服务端（公网机器）

```json
// conf/server.json
{
  "role": "server",
  "bind_addr": "0.0.0.0",
  "bind_port": 7000,
  "token": "<强随机字符串>",
  "db_path": "frptun.db",
  "auth_mode": "off",
  "admin": {
    "enabled": true,
    "addr": "0.0.0.0",
    "port": 7500,
    "user": "admin",
    "password": "<管理端密码>"
  },
  "heartbeat_timeout": 30,
  "max_connections": 10000
}
```

```bash
./frptun -c conf/server.json
```

### 3. 客户端（内网机器）

```json
// conf/client.json
{
  "role": "client",
  "server_addr": "<公网服务器IP>",
  "server_port": 7000,
  "token": "<与服务端一致>",
  "heartbeat_interval": 10,
  "services": [
    { "name": "web", "local_ip": "127.0.0.1", "local_port": 8080, "remote_port": 9080 }
  ]
}
```

```bash
./frptun -c conf/client.json
```

访问 `<公网IP>:9080` 即可到达内网 `127.0.0.1:8080`。

## 配置项

### 通用

| 字段 | 说明 | 默认值 |
|---|---|---|
| `role` | `server` 或 `client` | 必填 |
| `token` | 通信密钥，两端必须一致 | 必填 |
| `connect_timeout` | 建连超时（秒） | 10 |

### 服务端

| 字段 | 说明 | 默认值 |
|---|---|---|
| `bind_addr` | 监听地址 | 0.0.0.0 |
| `bind_port` | 控制/数据复用端口 | 7000 |
| `heartbeat_timeout` | 心跳超时（秒） | 30 |
| `max_connections` | 最大并发隧道数 | 10000 |
| `db_path` | SQLite 数据库路径 | frptun.db |
| `auth_mode` | `off` / `whitelist` / `blacklist` | off |
| `admin.enabled` | 是否启用管理端 | false |
| `admin.addr` | 管理端监听地址 | 0.0.0.0 |
| `admin.port` | 管理端端口 | 7500 |
| `admin.user` / `admin.password` | 管理端 Basic Auth | admin / - |

### 客户端

| 字段 | 说明 | 默认值 |
|---|---|---|
| `server_addr` | 服务端地址 | 必填 |
| `server_port` | 服务端端口 | 7000 |
| `heartbeat_interval` | 心跳间隔（秒，需小于服务端超时） | 10 |
| `reconnect_interval` | 重连退避基数（秒） | 1 |
| `max_reconnect_interval` | 重连退避上限（秒） | 30 |
| `services[].name` | 服务名（唯一） | svc-<remote_port> |
| `services[].local_ip` | 本地服务地址 | 127.0.0.1 |
| `services[].local_port` | 本地服务端口 | 必填 |
| `services[].remote_port` | 服务端映射端口 | 必填 |

## Web 管理端

浏览器打开 `http://<公网IP>:7500`，输入 Basic Auth 账号密码。

- **总览**：在线客户端、活跃隧道、端口映射数、累计连接与流量；24 小时流量趋势图
- **端口管理**：实时新增 / 关闭端口映射，无需重启；展示来源（配置/动态）、运行状态与客户端
- **实时连接**：每秒通过 SSE 推送当前活跃隧道的来源 IP、机器码、上下行字节、时长
- **客户端**：已知客户端列表（机器码、主机名、IP、在线状态、累计流量）
- **授权管理**：
  - 切换授权模式（关闭 / 白名单 / 黑名单）
  - 增删启停规则，类型支持 IP 或机器码
  - IP 支持精确值、`192.168.1.*` 通配、`10.0.0.0/8` CIDR
  - 机器码支持精确值或 `*` 通配
- **流量记录**：历史隧道的连接明细

所有规则、模式配置、客户端信息与流量历史均持久化到 SQLite。

### 管理端 REST API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/overview` | 总览统计 |
| GET | `/api/connections` | 实时活跃连接 |
| GET | `/api/clients` | 客户端列表 |
| GET | `/api/traffic?limit=100` | 流量历史 |
| GET | `/api/hourly?hours=24` | 按小时聚合流量 |
| GET | `/api/rules` | 授权规则列表 |
| POST | `/api/rules` | 添加规则 |
| PUT | `/api/rules` | 启停规则 |
| DELETE | `/api/rules?id=N` | 删除规则 |
| GET/POST | `/api/settings` | 读取/设置授权模式 |
| GET | `/api/ports` | 端口映射列表 |
| POST | `/api/ports` | 实时新增映射 |
| DELETE | `/api/ports?machine_id=M&remote_port=P` | 实时关闭映射 |
| GET | `/api/events` | SSE 实时推送 |

## 实时端口管理

在管理端「端口管理」页选择在线客户端，填写本地目标与远端端口即可**立即生效**，无需重启任何进程。

实现要点：

- 服务端维护权威映射表（客户端配置 + 管理端动态新增），任何变更都重新对账监听器并全量下发 `sync_services` 给客户端，客户端 ACK 确认后才返回成功
- 动态映射写入 SQLite，**客户端重连自动回放**；被实时关闭的静态映射会记录抑制项，重连后不会偷偷恢复
- 关闭端口会同时断开该端口上的所有活跃隧道与待配对连接，并释放端口占用
- 端口冲突检测覆盖在线监听与持久化映射，避免误占用
- 客户端运行时路由表由服务端下发驱动，新增端口无需改客户端配置文件

```bash
# 新增映射（curl）
curl -u admin:<密码> -X POST http://<公网IP>:7500/api/ports \
  -H 'Content-Type: application/json' \
  -d '{"machine_id":"<机器码>","name":"api2","local_ip":"127.0.0.1","local_port":3000,"remote_port":9081}'

# 关闭映射
curl -u admin:<密码> -X DELETE \
  'http://<公网IP>:7500/api/ports?machine_id=<机器码>&remote_port=9081'
```

## 机器码

客户端机器码由 `hostname + 网卡 MAC` 计算 SHA256 生成，跨平台稳定，Windows / Linux 通用，不依赖任何平台特有机制。管理端可据此做机器级授权。

## 跨平台

- 纯 Go 实现，SQLite 使用 `modernc.org/sqlite` 纯 Go 驱动，`CGO_ENABLED=0`
- 一条命令交叉编译：Linux(amd64/arm64)、Windows(amd64)、macOS(amd64/arm64)，见 `build.sh` / `build.bat`；Windows/arm64 因 SQLite 依赖暂不支持
- 路径、信号、机器码生成均无平台依赖

## 测试

```bash
go build -o frptun ./cmd/frptun
python tests/e2e.py        # 核心功能 + 管理端 + 黑白名单 + 持久化
python tests/ports_test.py # 实时端口管理（新增/关闭/回放）
```

测试覆盖：转发连通、管理端 API、活跃连接、流量统计与落库、黑白名单拒绝、配置持久化；以及动态端口即时生效、关闭即断、重连回放、静态端口抑制。
