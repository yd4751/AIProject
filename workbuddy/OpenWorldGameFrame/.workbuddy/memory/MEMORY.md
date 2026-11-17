# OpenWorldGameFrame 项目长期记忆

Go 1.23.1，module `openworld`，位于 `E:/Project/workbuddy/OpenWorldGameFrame`。
核心路径（协议/ECS/AOI/拓扑）手写零依赖、组件池零反射；外部依赖只有玩家档案的两个后端：
`github.com/redis/go-redis/v9 v9.16.0`（M6.11）与 `github.com/go-sql-driver/mysql v1.8.1`（M10）。
**版本都要钉死**：go-redis 最新版要求 go 1.26 会触发 toolchain 自动升级，v9.16.0 要求 go 1.18；
mysql v1.8.1 要求 go 1.18，都兼容本机 go 1.23.1。tidy / 所有 Go 命令用 `GOTOOLCHAIN=local` 防自动升级。

## 硬约束（改代码前先看）

- **单写者**：一个 Scene 一个 goroutine，实体无锁。任何跨 goroutine 改实体都是 bug。
- **控制指令走 mailbox，移动走意图缓冲**（`intent.go`，后写覆盖先写），不要给移动排队。
- **连接在哪进来就永远在哪**：TCP 不能跨进程搬。跨进程时是包跟着连接走
  （`MsgClientSend` 下行、`MsgRelay` 上行、`MsgRebind` 改路由）。绕过 `world.Client` 直接写 socket 的改动会在跨进程时失效。
- **实体 ID 由 World 统一分配**，`id = seq × stride + offset`，`stride = maxNodes` **一次定死**、`offset = 配置里的固定槽位`；
  外来 ID 删除时发 `MsgReleaseID` 给所有者，本地不回收。任何"按当前节点数算步长"的改动都会让历史 ID 静默撞号。
- **配置是期望，注册中心是实际**：场景归属写在配置里（改它要迁移玩家），只有"谁活着/地址"是动态的；
  成员变化只影响可达性（不可达 → 地块退出路由、玩家被夹住），绝不动拓扑。
- **迁移走三阶段 Prepare→Ready→Commit（3 秒超时），必须先问再摘**；`detach()` 不回收 ID。
- **拓扑变更必须带版本号**：归属每变一次 +1（`SceneCtl.Ver` / `TopoEntry.Ver`），广播和摘要都带它。
  每 2 秒互换整份摘要（`MsgTopoDigest 0x1031`），规则只有三条：对方版本高 → 采用；低 → 推回去（限流 1/s）；
  相同但归属不同 → 取字典序小的节点名 + 双方各自 +1（独立收敛，不需要协商）。
- **对账不插手正在搬迁/接管的场景**：中间态看着就是"两边都是主人"。判断只能读原子标志
  （`Scene.Moving()` / `Scene.Standby()`），读 `s.evac` 是数据竞争。
- **本地副本被收走时先清空再停**：走 `msgRetire` 让 tick 协程逐个 `removeEntity`（回收 ID）再退休。
  直接 `Stop` 会把实体连同 ID 一起丢，实测 `live` 永远停在 100。
- **判死的节点不发广播/摘要**：拨号失败要卡满 `dialTimeout` 1s，串行循环会拖慢排在后面的活节点
  （现场实测广播比对账晚 0.5s）。它回来时 discovery 的 `back` 分支会定向补发一次。
- **重分区只有一种回滚：搬人之前**。Prepare 失败可撤销；一旦开始排空只能往前（先发后摘、失败只停这一帧）；
  Activate 无限重发。任何"搬到一半回滚"的改动都会造成一块地两个主人。
- **拓扑切换必须有先后**：目标先建 standby（能收 Commit、不在路由里）→ 激活 → 广播 → 源摘。
  各节点各改各的 = 双写者，实体表会分叉。
- **接管死节点的地和搬迁活节点的地是两条路径**：前者 O(1) 只改路由（`Incoming=0`，工厂会重新填充内容），
  后者要逐个搬实体（`Incoming>0`，工厂不得再刷怪，否则翻倍）。
- Chunk（512m，归属单位，可跨进程）与 AOI Cell（16m，进程内广播单位）解耦；矩形半开区间、块对齐。
- 节点链路拥塞：**队列满只丢当前帧不断链**（`TrySend`）；客户端连接才是满即断开。
- **节点链路双优先级队列**：控制/握手走高优先（`sendq`），数据（Relay/ClientSend/ClientBatch）走低优先（`sendqLow`）；
  `isControlMsg` 默认高优先（宁给新握手走快通道，不漏分类）。`controlDropped` 恒 0 才算健康。
- **跨节点下行在 mesh 层批量合并**：`SendClientSend` 零拷贝入口 → link 聚合缓冲 → flushLoop 2ms 冲刷成 `MsgClientBatch 0x1024`；
  单批超 256 条立即 flush。别在 remoteClient（每玩家一实例）做。
- **自动重分区只搬活主人自己的超载场景**，不接管死节点的地（主人可能只是分区，自动接管会和回来后的主人撞车）；
  决策硬条件 `target+1 < myScenes`（防振荡）；inflight 判落地用 `PeerOf` 切主，**不用 `Moving()`**（~1ms 窗口误清）。
- **存档走 mailbox、codec 链、玩家不恢复**：`SaveWorld`/`RestoreWorld` 走 `msgSave`/`msgRestore` 由 tick 协程采集
  （场景未 Start 时直接读，无并发）。玩法数据用 **codec 链**（`AddTransferCodec`，creature 存属性+AI、combat 存战斗+Buff），
  blob = u16 段数 + 每段 [u16 长度 + 字节]。**只恢复非玩家实体**（玩家连接是运行时的，重新登录重建）；
  **拓扑只恢复版本号、不恢复归属**（配置是权威，重分区是临时运维动作，重启回配置）。原子落盘 = tmp + rename。
- **运维接口审计 + RBAC + 热力图都在 observability 包，别 import world**：world import observability，反向会成环。
  热力图数据经 `OnTopo(func() any)` 回调传入，observability 用本地结构体（JSON 标签对齐 `world.ChunkInfo`）接收。
  RBAC 是「token 换角色」（readonly/operator/admin），中间件按路径+方法判角色，探针（healthz/readyz）永远免鉴权；
  **不配 `-admin-token` 即全开放（开发模式），配了才锁**。审计用环形缓冲（256 条），别用 slog（受众/生命周期不同）。
  实测统一走 PowerShell `Invoke-WebRequest`——本机 Windows bash 里 curl 不可用，一直返回空。
- **战斗扩展的依赖方向**：combat import creature（resolve.go 读属性），所以 creature 不能 import combat。玩家档案的聚合
  （属性+战斗）放在独立包 `game/player`（import 两者），不在 creature 也不在 combat——放哪边都成环。
  **默认玩家暴击率 0**（暴击是进阶属性），否则测试随机触发暴击 flaky。连招只记「上一个技能+时间」，
  不记整条链（两段式够用）。技能树：基础技能（无 Requires/Level）天生会，技能树技能要 Learned 位图解锁。
- **拓扑对账增量摘要**：`SendTopoDigest(nodes)`——nodes 空 = 周期广播走增量（`digestSent` 水位，只发版本变过或
  **从未发过**的场景），nodes 非空 = 定向推送走全量（节点回归/回推旧节点）。增量判据必须「!seen 或 ver 变了」取或，
  否则新场景 ver=0 会漏发。增量不需要全量兜底——对账规则「版本高谁赢」本身保证最终一致。
- **玩家档案落库 = 复用 OnEntityLeave 钩子 + persist.Store 接口**：`detach` 里早就调了 `fireLeave`（M2 就埋了），
  M5.5 是第一个消费它的玩法。Store 值用 []byte（Store 不知道档案字段），MemoryStore/FileStore/RedisStore 三个实现，
  接最终数据库（MySQL 等）只换实现。档案 JSON（跨系统可读），world 存档二进制（进程内紧凑）——两种格式各有其用。
  落库失败静默（尽力而为，不阻塞离场）。
  **周期批量回写**（M6.8）：`EnablePeriodicSave` 挂 StageLogic 时间闸门系统，定期扫本场景在线玩家落库，
  和离场落库互补（离场=精确最后一笔，周期=崩溃兜底）。周期扫描而非"变化即写"——属性每帧都变，
  同步落盘（tmp+rename 两次 IO）会拖垮 tick。默认 10s。
  **Redis 后端**（M6.11）：`persist/redis.go` 的 `RedisStore`（key `<prefix><player>` 默认 `player:`，可选 TTL，
  构造时 Ping 3s 超时连不上报错）。`Get` 里 redis.Nil 和网络错误都归"没读到"（返回 false）——档案是尽力而为，
  Redis 抖动不拒绝玩家进场。`cmd/server` 加 `-redis-addr/-password/-db/-prefix`，优先级 Redis > 文件目录 > 不落；
  优雅退出用极简 `redisComponent` 包 Close()（注册在 gate 之前，逆序停止时网关停后、场景停前释放连接）。
- **升级系统（M6.7）**：XP 放 `creature.Attributes`（等级的伴生字段），升级逻辑放 `combat/level.go`（击杀在
  combat 的 dealDamage，combat 已 import creature，无环）。击杀给 xpForKill=10，阈值 xpToLevel(level)=level*20，
  升级 MaxHP+100 回满、攻击+10，封顶 maxLevel=20。**升级不改 Learned 位图**——等级是"能不能用"的前置
  （skillAvailable 判 `sk.Level > 0 && playerLevel < sk.Level`），Learned 是"会不会"，两者解耦。
  creature transfer codec v2→v3（属性段 18→22 字节，Brain 段后移 4），Decode 按版本号分偏移，旧 v2 档仍还原。
- **控制面实时推送用 SSE 不用 WebSocket**（M6.10）：控制台是"服务端单向推状态"，没有双向需求
  （写操作走 POST），SSE 语义正好匹配且标准库 `http.Flusher` 即可实现——项目 go.mod 零 require 是
  硬约束，手写 WebSocket（RFC 6455 握手+帧+掩码+关闭）会引入大块易错代码却只换来"能推消息"。
  `handleStream` 抽 `buildOverview()` 与轮询共用组装逻辑，两通道数据结构必须一致。
- Windows 下 `time.Now()` 粒度约 1ms，`/admin/state` 的 `lastTickNs` 只能取 0/1ms，耗时结论一律看 benchmark。
- 本机无 gcc，`go test -race` 不可用。

## 当前进度

M1 骨架 / M2 AOI+ECS+移动合并 / M3.1 ID 回收 / M3.2 多场景迁移 / M3.3 跨进程迁移 / M3.4 服务发现 / M3.5 世界重分区 / M3.6 拓扑对账 / M3.7 自动重分区 / M3.8 链路优化（优先级+批量合并）/ M4 战斗（技能+伤害+Buff+死亡回收）/ M4.5 战斗扩展（技能树+连招+驱散+暴击+护盾）/ M5 存档（世界快照+拓扑版本持久化）/ M5.5 玩家档案落库 / M6 控制面（单机版 + 审计/RBAC/拓扑热力图）/ M6.6 增量对账 / M8 Godot 客户端框架 / M9 客户端玩法 UI（技能目录协议+技能树面板+小地图）/ M10 玩家档案接 MySQL（最终档案 + Redis 缓存分层） 均已完成并压测。

- 常用命令：`go build ./... && go vet ./... && gofmt -l . && go test ./...`
- 单进程：`./bin/server.exe -gate-addr :7001 -admin-addr :7002 -mobs 50`（**没有 `-addr` 这个 flag**；客户端默认连 7001，换端口要同步给 `--ow-port`）
- 多进程：`./bin/server.exe -node n1 -node-addr 127.0.0.1:7003 -world configs/world-2node.json -gate-addr :7001 -admin-addr :7002`
- 热加入：上面再加 `-disc file -disc-dir run/nodes`（三个节点共用同一目录，配置用 `configs/world-3node.json`）
- 重分区：`POST /admin/repartition?scene=3&to=n1`（202，进度看 `/admin/state` 的 `world.scenes[].move`）
- 对账：多进程下自动开（`topo-reconcile` 组件），统计看 `/admin/state` 的 `world.topology`
  （`applied` 应长期为 0，非 0 就是修复过；`conflicts` 非 0 说明丢过广播但已裁决）
- 自动重分区：`-auto-rebalance`（每节点自平衡，只搬活主人自己的超载场景）；统计看 `/admin/state` 的 `scheduler`
  （`attempted` 应稳定不增长，`rejected` 是"评估了但不该动"）；演示配置 `configs/world-5scene-3node.json`
- 打怪压测：`bot.exe -attack`（攻击视野内目标）；战斗统计看 CombatHit 广播数（bot 的 hits/kills）
- 存档：`-save-dir run/save -save-interval 30s`（每节点一份 `world-save-<node>.json`，周期+关服落盘，重启恢复）
  统计看 `/admin/state` 的 `save`（saves/restored/lastError）
- 运维安全：`-admin-token <token>`（配了才锁写接口）；审计看 `GET /admin/audit?token=...`；热力图 `GET /admin/topo?token=...`
- 玩家档案：`-save-players run/players`（离场落库、重连恢复，每玩家一个 JSON 文件）；
  周期批量回写 `-save-players-interval 10s`（`EnablePeriodicSave`，StageLogic 时间闸门，只写在线玩家）

- **Godot 客户端框架（M8）+ 玩法 UI（M9）**：引擎在 `E:/install/gobot/Godot_v4.7.2-stable_win64_console.exe`（目录名 gobot 实为 Godot 4.7.2）。
  工程在 `client/godot/`，2D 俯视角对接二进制 TCP 协议。分层：协议层（ow_writer/reader/proto/auth/messages，
  大端+帧格式+17 消息，与 `internal/protocol` 逐字节一致）→ 网络层（ow_net_client，状态机+信号，对齐 tools/bot）
  → 表现层（world_view/entity_node/hud/main）。自测：`selftest/protocol_test.gd`（Go 黄金字节比对 76 断言）、
  `integration_test.gd`（连真服务端 9 断言）、`compile_check.gd`（全脚本编译冒烟）。headless 跑法：`Godot --headless --path client/godot --script res://selftest/xxx.gd`。
  **协议字节级交叉验证是核心手段**——Go 生成黄金字节、Godot 逐字段断言，确保异语言客户端协议不漂移。
- **M9 客户端玩法 UI 硬约束**：① 技能目录经场景 goroutine 下发（`Scene.QuerySkills`→mailbox→tick 协程读 `Combat.Learned` 现编目录回包，绝不在 Gate goroutine 直读实体组件）；
  ② combat 用 `Scene.SetSkillQueryHandler` 注入（与 `SetAttackHandler` 对称，避免 world↔combat 环）；
  ③ 跨节点 `mesh.RelaySkills` 只借 Entity 路由字段（Target/Skill 不用）镜像 `RelayAttack`；
  ④ 客户端 `skill_tree._is_available` 逐字段镜像 `combat.skillAvailable`，保证两边"能否用"一致；
  ⑤ 小地图复用 EntityState 广播、不新增协议，客户端本地以自己为中心重投影；
  ⑥ GDScript `CanvasLayer` 用 `offset` 定位（无 `position` 属性）——踩坑点；
  ⑦ `Godot --headless` 完整主场景在沙箱被 SIGTERM，verify 用 `run_in_background` 后台跑。
  **背包 UI 仍待做**：服务端无物品系统支撑，按"协议+表现两层走"原则推到物品系统就绪。
- **M10 玩家档案分层（MySQL 最终档案 + Redis 缓存）硬约束**：① `Store` 值恒为 `[]byte`，后端不知道档案字段，
  别引 ORM；② `LayeredStore.Put` 必须 **final 先、cache 后**——反过来会留下"缓存新、真相旧"的永久脏读，
  而读回填里的 `cache.Put` 错误要忽略（缓存是加速不是正确性依赖）；③ `Get` 里"查无此行"和"网络报错"
  都返回 `(nil,false)`，后端抖动不能拒绝玩家进场；④ 优雅退出按 `persist.Closer` 接口断言注册
  `playerStoreComponent`，**别按具体类型**（换 MySQL / 叠 Layered 后会静默漏 Close）；
  ⑤ `-mysql-dsn` 优先级：MySQL+Redis→Layered > 仅 MySQL > 仅 Redis > 文件 > 不落；
  ⑥ 表 `CREATE TABLE IF NOT EXISTS` 自建、无 schema 版本管理，加字段要手工 ALTER；
  ⑦ 换后端不动 IO 路径（落库仍走 `OnEntityLeave` + 周期批量回写，都在 tick 外）。
  本机 MySQL（root 无密码 / 3306）与 Redis（6379）都开着，真集成测试能跑；Redis 老版本握手的
  `maintnotifications disabled` 警告不是故障。

下一步：etcd 适配器（用户已说可下开源 etcd 二进制，但 SSL/TLS 下载在本机不稳定，暂搁置）、
客户端背包 UI（等物品系统就绪，M9 已实现技能目录协议+技能树面板+小地图）。
玩家档案最终库已落地（MySQL 权威 + Redis 缓存分层），再接别的数据库只换 `Store` 实现。
踩坑记录全在 `docs/ARCHITECTURE.md`（第十节 + 各里程碑小节），新增坑必须补进去。
