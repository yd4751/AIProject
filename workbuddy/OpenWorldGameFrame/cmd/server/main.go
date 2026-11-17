package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"openworld/internal/core"
	"openworld/internal/discovery"
	"openworld/internal/game/combat"
	"openworld/internal/game/creature"
	"openworld/internal/game/player"
	"openworld/internal/gate"
	"openworld/internal/mesh"
	"openworld/internal/observability"
	"openworld/internal/persist"
	"openworld/internal/world"
)

func main() {
	var (
		gateAddr   = flag.String("gate-addr", ":7001", "网关监听地址")
		adminAddr  = flag.String("admin-addr", ":7002", "运维接口监听地址")
		secret     = flag.String("secret", "dev-secret-change-me", "登录令牌签名密钥")
		tickMs     = flag.Int("tick-ms", 50, "逻辑帧间隔，单位毫秒")
		tokenTTL   = flag.Duration("token-ttl", 5*time.Minute, "登录令牌有效期")
		logLevel   = flag.String("log-level", "info", "日志级别：debug/info/warn/error")
		instanceID = flag.String("instance-id", "", "实例标识，由控制面注入，用于状态上报")
		zoneID     = flag.String("zone", "", "区服标识，由控制面注入")
		mobs       = flag.Int("mobs", 0, "每个场景生成的怪物数量（演示 ECS 玩法层，0 表示不生成）")
		partition  = flag.String("partition", "1x1", "世界拓扑：行x列，例如 1x3 表示一行三个场景（不配 -world 时生效）")
		sceneSize  = flag.Float64("scene-size", 512, "每个场景的边长（米），必须是 Chunk(512m) 的整数倍")
		nodeID     = flag.String("node", "", "本节点标识，多进程部署时必填，需与 -world 配置里的节点名一致")
		nodeAddr   = flag.String("node-addr", "", "节点间链路监听地址，例如 :7003；为空表示不参与跨进程")
		nodeAdv    = flag.String("node-advertise", "", "注册到发现中心的链路地址；为空则把 -node-addr 的通配地址换成本机地址")
		worldCfg   = flag.String("world", "", "世界拓扑配置文件；配置后忽略 -partition 与 -scene-size")
		discKind   = flag.String("disc", "static", "节点发现方式：static（静态成员表）| file（共享目录心跳，支持热加入/退出）")
		discDir    = flag.String("disc-dir", "", "file 发现方式的共享目录，例如 run/nodes")
		discTTL    = flag.Duration("disc-ttl", 3*time.Second, "file 发现方式的记录过期时间，建议不低于 3 倍心跳周期")
		autoRebal  = flag.Bool("auto-rebalance", false, "开启自动重分区：每节点按场景数自平衡，只搬自己拥有的地")
		rebalEval  = flag.Duration("rebalance-eval", 5*time.Second, "自动重分区的评估周期")
		rebalCool  = flag.Duration("rebalance-cooldown", 15*time.Second, "同一场景两次搬迁的最小间隔，需大于一次搬迁耗时(~300ms)")
		rebalSlack = flag.Int("rebalance-slack", 0, "允许超出公平份额的场景数；0 表示严格按 ceil(总数/活节点) 收敛")
		saveDir    = flag.String("save-dir", "", "存档目录；为空表示不落盘。存拓扑归属+版本号+怪物实体")
		saveEvery  = flag.Duration("save-interval", 30*time.Second, "周期存档间隔（需配合 -save-dir）")
		adminToken = flag.String("admin-token", "", "运维接口鉴权 token；为空表示开发模式（所有接口开放）")
		playerDir  = flag.String("save-players", "", "玩家档案目录；为空表示不落玩家档案。跨进程/跨区重连时恢复血量等级")
		playerSave = flag.Duration("save-players-interval", 10*time.Second, "玩家档案周期回写间隔；配合 -save-players，把在线玩家的属性变化定期落盘，缩小进程崩溃时的丢失窗口")
		redisAddr  = flag.String("redis-addr", "", "玩家档案的 Redis 地址（host:port）；配置后档案存 Redis 而非文件，多节点共享同一份档案")
		redisPass  = flag.String("redis-password", "", "Redis 密码；无密码留空")
		redisDB    = flag.Int("redis-db", 0, "Redis 数据库编号")
		redisPref  = flag.String("redis-prefix", "player:", "玩家档案在 Redis 里的键前缀，避免和其他数据冲突")
		mysqlDSN   = flag.String("mysql-dsn", "", "玩家档案的 MySQL DSN（如 root@tcp(127.0.0.1:3306)/openworld?parseTime=true）；配置后档案以 MySQL 为最终存档，可选叠加 Redis 做在线缓存")
	)
	flag.Parse()

	logger := newLogger(*logLevel)
	metrics := observability.NewMetrics()
	app := core.New("openworld-server", logger)
	if *instanceID != "" {
		app.SetStatus("instanceId", *instanceID)
	}
	if *zoneID != "" {
		app.SetStatus("zoneId", *zoneID)
	}
	if *nodeID != "" {
		app.SetStatus("nodeId", *nodeID)
	}

	// 节点间链路必须在 World 和 Gate 之前就位：两者的 resolver 与投递
	// 都依赖它，先建对象后补链路会让"第一帧就迁移"的路径拿到 nil。
	var msh *mesh.Mesh
	if *nodeID != "" && *nodeAddr != "" {
		msh = mesh.New(*nodeID, logger)
	}

	// resolver 用闭包捕获 gate 变量：World 先建、Gate 后建，两者互为依赖。
	// 调用发生在 Start 之后，那时 gate 一定已经赋值。
	var g *gate.Gate

	// 场景来源二选一：静态拓扑配置（多进程）或 -partition（单进程）。
	// 先读配置再建 World，是因为 Chunk 边长要在建场景之前定下来
	// （它决定边界对齐的判定），事后改会让已登记的映射对不上。
	var cfg *world.Config
	if *worldCfg != "" {
		c, err := world.LoadConfig(*worldCfg)
		if err != nil {
			logger.Error("load world config failed", "path", *worldCfg, "err", err)
			os.Exit(1)
		}
		cfg = c
	}

	tickDur := time.Duration(*tickMs) * time.Millisecond

	worldOpts := []world.WorldOption{
		world.WithNodeID(*nodeID),
		world.WithMesh(msh),
		world.WithClientResolver(func(ref mesh.ClientRef) world.Client {
			if g == nil {
				return nil
			}
			return g.ResolveClient(ref)
		}),
	}
	// 场景工厂：重分区接管一块地时用它原地造场景。
	//
	// 必须和启动时的建场景路径**完全一致**（边界、容量、玩法系统、刷怪），
	// 否则搬过去的场景会缺一半系统，而这种偏差只在重分区之后才暴露。
	// 闭包捕获后面的 w，赋值发生在 NewWorld 返回之后——工厂只在运行期被调用。
	// playerStore 也在这里提前声明：工厂闭包要引用它，但它的构造在 flag 解析
	// 之后（和存档目录相关）。闭包运行期才读，所以声明位置只需在闭包定义前。
	var w *world.World
	var playerStore persist.Store
	worldOpts = append(worldOpts, world.WithSceneFactory(func(spec world.SceneSpec) (*world.Scene, error) {
		if w == nil {
			return nil, fmt.Errorf("world 尚未就绪")
		}
		opts := []world.Option{world.WithBounds(spec.RectOf())}
		if spec.Capacity > 0 {
			opts = append(opts, world.WithCapacity(spec.Capacity))
		}
		sc := w.NewSceneDetached(spec.ID, tickDur, metrics, logger, opts...)
		if err := creature.Register(sc); err != nil {
			return nil, fmt.Errorf("注册玩法系统失败: %w", err)
		}
		creature.EnableTransfer(sc)
		if err := combat.Register(sc); err != nil {
			return nil, fmt.Errorf("注册战斗系统失败: %w", err)
		}
		combat.EnableTransfer(sc)
		// 玩家档案：进场恢复、离场落库 + 周期批量回写。必须在 creature/combat
		// 之后注册，这样进场钩子在最后跑，能用存档覆盖默认属性。
		player.EnablePlayerSave(sc, playerStore)
		player.EnablePeriodicSave(sc, playerStore, *playerSave)
		// 只有"接管一块空地"才需要重新填充玩法内容。正在搬迁的场景会把
		// 自己的怪一起带过来，这里再刷一批就是双份。
		if *mobs > 0 && spec.Incoming == 0 {
			b := spec.RectOf()
			cx, cz := (b.MinX+b.MaxX)/2, (b.MinZ+b.MaxZ)/2
			creature.SpawnHorde(sc, *mobs, cx, cz, 200)
			logger.Info("mobs spawned on adopted scene", "scene", spec.ID, "count", *mobs)
		}
		return sc, nil
	}))
	if cfg != nil {
		worldOpts = append(worldOpts,
			world.WithChunkSize(cfg.ChunkSize),
			world.WithNodeSlots(cfg.SlotTable()),
		)
		stride, offset := cfg.Partition(*nodeID)
		worldOpts = append(worldOpts, world.WithIDPartition(stride, offset))
		logger.Info("id partition", "stride", stride, "offset", offset, "node", *nodeID,
			"maxNodes", cfg.MaxNodes)
	}
	w = world.NewWorld(logger, worldOpts...)

	// 节点成员视图：谁活着、它的链路地址是什么。
	//
	// 默认 static 就是 -world 里声明的节点，一个不多一个不少，与 M3.3 行为一致。
	// 切到 file 之后成员由共享目录里的心跳决定：新节点启动即被发现，
	// 挂掉的节点在一个 TTL 内从路由表里消失。
	reg, err := newRegistry(*discKind, *discDir, *discTTL, *nodeID, cfg, logger)
	if err != nil {
		logger.Error("init discovery failed", "kind", *discKind, "err", err)
		os.Exit(1)
	}
	var disc *discoveryComponent
	if *nodeID != "" {
		adv := *nodeAdv
		if adv == "" {
			adv = advertiseAddr(*nodeAddr)
		}
		disc = &discoveryComponent{
			reg:  reg,
			w:    w,
			msh:  msh,
			self: discovery.Node{Name: *nodeID, Addr: adv},
			log:  logger,
		}
	}

	// 自动重分区调度器：只在多节点部署下有意义（单进程没东西可搬）。
	// 它跑在每个节点上，只决策自己拥有的场景，所以不需要选主、不会撞车。
	var sched *world.Scheduler
	if *autoRebal && *nodeID != "" && msh != nil {
		sched = w.NewScheduler(
			world.WithEvalInterval(*rebalEval),
			world.WithCooldown(*rebalCool),
			world.WithSlack(*rebalSlack),
			world.WithLogger(logger),
		)
	}

	var scenes []*world.Scene
	if cfg != nil {
		if msh != nil {
			for node, addr := range cfg.Peers(*nodeID) {
				msh.AddPeer(node, addr)
			}
		}
		scenes = cfg.Apply(w, *nodeID, tickDur, metrics, logger)
		if len(scenes) == 0 {
			logger.Error("node owns no scene", "node", *nodeID, "config", *worldCfg)
			os.Exit(1)
		}
		for _, spec := range cfg.Remote(*nodeID) {
			logger.Info("remote scene declared", "scene", spec.ID, "node", spec.Node,
				"bounds", spec.RectOf().String())
		}
	} else {
		rows, cols := parsePartition(*partition)
		scenes = w.Partition(rows, cols, float32(*sceneSize), tickDur, metrics, logger, 1)
		if len(scenes) == 0 {
			logger.Error("invalid partition", "partition", *partition)
			os.Exit(1)
		}
	}

	// 玩法层在每个场景上各挂一份：world 包不知道"怪物"是什么，只提供 System 插槽。
	// 多场景不是"一个场景多线程"，而是"多个独立的单线程场景"——
	// 每个场景有自己的实体表、AOI 索引、组件存储和 tick 协程，互不共享。
	//
	// 存档优先：配置了 -save-dir 且存档文件存在时，怪物从存档恢复，
	// 不再用 -mobs 刷怪——否则恢复出来的怪和冷启动刷的怪会重复一份。
	hasSave := false
	if *saveDir != "" {
		_, err := os.Stat(saveFilePath(*saveDir, *nodeID))
		hasSave = err == nil
	}
	// 玩家档案存储，优先级：MySQL(最终档案) + Redis(在线缓存) > 仅 MySQL >
	// 仅 Redis > 文件目录 > 不落。
	//
	// 蓝图三层"内存→Redis→MySQL"在这里落地：MySQL 是权威、持久的真相，
	// Redis 是扛高频读的在线缓存。两者都配时叠成 LayeredStore（读先查 Redis，
	// 未命中再查 MySQL 并回填；写先落 MySQL 再落 Redis；删两层都删）。只配
	// 其一则各自独立工作；都不配则不落（重连回默认值，单机演示够用）。
	var mysqlStore *persist.MySQLStore
	if *mysqlDSN != "" {
		ms, err := persist.NewMySQLStore(*mysqlDSN)
		if err != nil {
			logger.Error("connect player mysql", "dsn", *mysqlDSN, "err", err)
			os.Exit(1)
		}
		mysqlStore = ms
	}
	if mysqlStore != nil && *redisAddr != "" {
		rs, err := persist.NewRedisStore(*redisAddr, *redisPass, *redisDB, *redisPref)
		if err != nil {
			logger.Error("connect player redis (cache)", "addr", *redisAddr, "err", err)
			os.Exit(1)
		}
		playerStore = persist.NewLayeredStore(rs, mysqlStore)
		logger.Info("player records on mysql (final) + redis (cache)")
	} else if mysqlStore != nil {
		playerStore = mysqlStore
		logger.Info("player records on mysql")
	} else if *redisAddr != "" {
		rs, err := persist.NewRedisStore(*redisAddr, *redisPass, *redisDB, *redisPref)
		if err != nil {
			logger.Error("connect player redis", "addr", *redisAddr, "err", err)
			os.Exit(1)
		}
		playerStore = rs
		logger.Info("player records on redis", "addr", *redisAddr, "prefix", *redisPref)
	} else if *playerDir != "" {
		if err := os.MkdirAll(*playerDir, 0o755); err != nil {
			logger.Error("create player save dir", "dir", *playerDir, "err", err)
			os.Exit(1)
		}
		playerStore = persist.NewFileStore(*playerDir)
	}
	for _, sc := range scenes {
		if err := creature.Register(sc); err != nil {
			logger.Error("register creature systems", "scene", sc.ID(), "err", err)
			os.Exit(1)
		}
		// 迁移是可选能力，所以单独开。单场景部署时开不开都一样。
		creature.EnableTransfer(sc)
		if err := combat.Register(sc); err != nil {
			logger.Error("register combat systems", "scene", sc.ID(), "err", err)
			os.Exit(1)
		}
		combat.EnableTransfer(sc)
		// 玩家档案：进场恢复、离场落库 + 周期批量回写。必须在 creature/combat
		// 之后注册，这样进场钩子在最后跑，能用存档覆盖默认属性。
		player.EnablePlayerSave(sc, playerStore)
		player.EnablePeriodicSave(sc, playerStore, *playerSave)
		if *mobs > 0 && !hasSave {
			// 必须在 app.Run（即 Scene.Start）之前生成：Spawn 直接改场景内部
			// 集合，只能在 tick 协程里调用，启动前调是安全的。
			b := sc.Bounds()
			cx, cz := (b.MinX+b.MaxX)/2, (b.MinZ+b.MaxZ)/2
			n := creature.SpawnHorde(sc, *mobs, cx, cz, 200)
			logger.Info("mobs spawned", "scene", sc.ID(), "count", n, "radius", 200)
		}
	}

	g = gate.New(gate.Config{
		Addr:     *gateAddr,
		Secret:   *secret,
		TokenTTL: *tokenTTL,
		Node:     *nodeID,
	}, w, metrics, logger)
	if msh != nil {
		g.AttachMesh(msh)
		// 一条链路两种消息：世界层的迁移/转发交给 World，会话层的
		// 下行回写与路由变更交给 Gate。分开是因为它们跑在不同的锁域上。
		msh.SetHandler(&peerMux{w: w, g: g})
	}

	// saver 提前声明：buildState 闭包要把它暴露到 /admin/state，
	// 而它本身在 admin 创建之后才真正构造。闭包捕获的是变量引用，
	// 运行时读到的是赋值后的值。
	var saver *saveComponent

	admin := observability.NewAdmin(*adminAddr, logger, metrics, app.Ready, func() any {
		return buildState(app, g, w, msh, disc, sched, saver)
	})
	// 关服流程由控制面通过 HTTP 驱动，而不是 kill 进程。
	// 详见 docs/ARCHITECTURE.md 第五节。
	// 场景也要标记排水：否则跨场景迁移会把人往一个马上要关的场景里送。
	admin.OnDrain(func(grace time.Duration) {
		for _, sc := range scenes {
			sc.SetDraining(true)
		}
		g.BeginDrain(grace)
	})
	admin.OnShutdown(app.Shutdown)
	// 重分区：把一块地搬到另一个节点。搬迁是异步的（要排空实体），
	// 这个回调只负责"接单"，进度看 /admin/state 里的 move 字段。
	admin.OnRepartition(w.MoveScene)
	// 拓扑热力图：/admin/topo 渲染 Chunk 归属 + 负载。
	admin.OnTopo(func() any { return w.Chunks() })
	// 审计：危险操作留痕，/admin/audit 可查。
	admin.SetAudit(observability.NewAudit(256))
	// 鉴权：配了 token 就锁写接口。同一个 token 给运维角色（能改），
	// 只读角色留给纯监控的脚本。开发模式（没配 token）保持开放。
	if *adminToken != "" {
		store := observability.NewTokenStore()
		store.Add(*adminToken, observability.RoleAdmin)
		admin.SetAuth(store)
	}

	// 启动顺序：链路 → 成员发现 → 场景 → 网关 → 运维接口。
	// 链路要先于场景：场景一开跑就可能有人跨进程迁进来，收不到就是丢人。
	// 发现要早于网关：等第一个玩家能登录时，成员视图已经贴到路由表上了。
	if msh != nil {
		app.Register(meshComponent{msh, *nodeAddr})
	}
	if disc != nil {
		app.Register(disc)
	}
	for _, sc := range scenes {
		app.Register(sc)
	}
	// 运行期接管进来的场景不在上面这份静态表里，单独收尾。
	app.Register(dynScenes{w})
	// 拓扑对账：定期和其它节点互换"这块地归谁、第几版"。
	// 排在场景之后、网关之前——它改的是路由表，而路由表在第一个玩家
	// 登录之前就该是最新的。
	if msh != nil {
		app.Register(&topoReconcile{w: w})
	}
	// 自动重分区排在拓扑对账之后：对账先把"这块地归谁"纠正成最新，
	// 调度器才能基于准确的归属判断谁超载。
	if sched != nil {
		app.Register(sched)
	}
	// 存档排在场景之后、网关之前：
	//  Start 时场景 tick 已在跑，恢复存档能安全走 mailbox；
	//  Stop 时（逆序）网关先停、存档后停，落盘时场景 tick 还在跑。
	// 这保证"关服前最后一份存档"和"恢复时能读到"两个方向都成立。
	if *saveDir != "" {
		saver = newSaveComponent(w, *saveDir, *nodeID, *saveEvery, logger)
		app.Register(saver)
	}
	if c, ok := playerStore.(persist.Closer); ok {
		app.Register(&playerStoreComponent{closer: c})
	}
	app.Register(g)
	app.Register(admin)

	logger.Info("openworld server starting",
		"instance", *instanceID,
		"zone", *zoneID,
		"node", *nodeID,
		"nodeAddr", *nodeAddr,
		"disc", *discKind,
		"discDir", *discDir,
		"scenes", len(scenes),
		"gate", *gateAddr,
		"admin", *adminAddr,
		"tick", time.Duration(*tickMs)*time.Millisecond,
		"go", runtime.Version(),
	)

	if err := app.Run(context.Background()); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// peerMux 把节点间消息分给世界层和网关层。
//
// 分派规则只有一条：迁移与会话转发地址（Peer/ClientRef）相关的归世界，
// 直接写 socket 和改会话路由的归网关。两边都不认识的记一条 debug 日志，
// 不做兜底转发——静默转发会让"协议升了一半"表现为随机丢包。
type peerMux struct {
	w *world.World
	g *gate.Gate
}

func (m *peerMux) Deliver(msgID uint16, body []byte) {
	switch msgID {
	case mesh.MsgAdmit, mesh.MsgAdmitAck, mesh.MsgCommit, mesh.MsgRelay,
		mesh.MsgReleaseID, mesh.MsgSceneCtl, mesh.MsgTopoDigest:
		m.w.Deliver(msgID, body)
	case mesh.MsgClientSend, mesh.MsgClientBatch, mesh.MsgRebind:
		m.g.Deliver(msgID, body)
	}
}

// topoReconcile 让拓扑对账参与服务的启停顺序。
//
// 它没有自己的状态，只有一条后台循环：每两秒和所有节点互换一次整份
// 拓扑摘要。放在组件表里是为了让它随进程退出——一个不结束的 goroutine
// 在关服日志里不会留下任何痕迹，下次排查"为什么关不干净"会白花时间。
type topoReconcile struct {
	w      *world.World
	cancel context.CancelFunc
}

// firstDigestWindow 是启动时等首轮摘要答复的时间窗口。
//
// 本节点可能是"刚回来"的那一个：它按配置重建了自己原来的地，而那块地
// 在这期间已经被别人接管。摘要能纠正它，但答复要走一个往返。
// 在这个窗口之前把网关放出去，第一个登录的玩家就可能落在一块
// 马上要被退休的地上，然后被踢下线。
//
// 它不保证一定收到答复（对端可能压根没起来），所以只是把竞争窗口
// 从"一个对账周期"压到"毫秒级"，不是消除。真正的解法是拓扑持久化，
// 那是 M5 存档的事。
const firstDigestWindow = 300 * time.Millisecond

func (t *topoReconcile) Name() string { return "topo-reconcile" }

func (t *topoReconcile) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	if sent := t.w.SendTopoDigest(nil); sent > 0 {
		time.Sleep(firstDigestWindow)
	}
	go t.w.Reconcile(ctx)
	return nil
}

func (t *topoReconcile) Stop(context.Context) error {
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

// dynScenes 负责停掉运行期新建的场景（重分区接管进来的那些）。
//
// 它们启动时还不存在，所以不在 core.App 的组件表里。没有这一步，
// 关服时这些场景的 tick 协程会跟着进程一起消失，日志里少了它们的收尾记录，
// 而且未来的存档钩子也不会被触发。
type dynScenes struct{ w *world.World }

func (d dynScenes) Name() string { return "dyn-scenes" }

func (d dynScenes) Start(context.Context) error { return nil }

func (d dynScenes) Stop(ctx context.Context) error {
	d.w.StopDynamic(ctx)
	return nil
}

// meshComponent 让节点间链路参与服务的启停顺序。
type meshComponent struct {
	m    *mesh.Mesh
	addr string
}

func (c meshComponent) Name() string { return "mesh" }

func (c meshComponent) Start(ctx context.Context) error { return c.m.Start(ctx, c.addr) }

func (c meshComponent) Stop(ctx context.Context) error { return c.m.Stop(ctx) }

// discoveryComponent 把注册中心的成员视图贴到世界拓扑上。
//
// 它做三件事，每收到一份视图就重做一遍（视图是幂等的整份快照，
// 不用担心重复应用）：
//
//  1. 新成员/地址变化 → 登记到 mesh（连接是懒建的，这里只更新地址）
//  2. 成员消失       → 对应地块从路由表里摘掉，玩家走到边界过不去
//  3. 成员回归       → 地块重新可达，玩家可以正常走过去
//
// 为什么不在这里直接改世界拓扑（加减场景）：场景归属是配置，
// 变更需要迁移玩家。成员变化只影响"能不能走过去"，不影响"谁该管哪块地"。
type discoveryComponent struct {
	reg  discovery.Registry
	w    *world.World
	msh  *mesh.Mesh
	self discovery.Node
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	unreg  func()

	applied atomic.Int64
	members atomic.Pointer[[]string]
}

func (c *discoveryComponent) Name() string { return "discovery" }

func (c *discoveryComponent) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.ctx, c.cancel = ctx, cancel

	unreg, err := c.reg.Register(ctx, c.self)
	if err != nil {
		cancel()
		return err
	}
	c.unreg = unreg
	c.log.Info("registered to discovery", "node", c.self.Name, "addr", c.self.Addr,
		"kind", c.reg.Info().Kind)
	go c.loop(c.reg.Watch(ctx))
	return nil
}

func (c *discoveryComponent) Stop(context.Context) error {
	// 先撤销记录再关注册中心：优雅下线时邻居应该立刻看到我消失，
	// 而不是等一个 TTL 之后才把我判死（那段时间里他们会往我这儿发迁移）。
	if c.unreg != nil {
		c.unreg()
	}
	if c.cancel != nil {
		c.cancel()
	}
	return c.reg.Close()
}

func (c *discoveryComponent) loop(ch <-chan discovery.View) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case v, ok := <-ch:
			if !ok {
				return
			}
			c.apply(v)
		}
	}
}

func (c *discoveryComponent) apply(v discovery.View) {
	alive := make(map[string]bool, len(v.Nodes)+1)
	for _, n := range v.Nodes {
		alive[n.Name] = true
		if c.msh != nil && n.Name != c.self.Name && n.Addr != "" {
			c.msh.AddPeer(n.Name, n.Addr)
		}
	}
	// 自己永远可达：注册中心短暂看不到自己（写失败了、时钟偏了）
	// 不该导致本节点把自己判死然后把本地场景当"别人的地"。
	alive[c.self.Name] = true

	var gone, back []string
	for _, name := range c.w.NodeMembers() {
		now := alive[name]
		if was := c.w.NodeAlive(name); was != now {
			c.w.SetNodeAlive(name, now)
			if now {
				back = append(back, name)
			} else {
				gone = append(gone, name)
			}
		}
	}

	names := v.Names()
	c.members.Store(&names)
	c.applied.Add(1)

	// 回归的节点手里是**配置里**的拓扑，不是世界的现状——它关机期间
	// 别人可能把它原来的地接管走了。主动给它一份摘要，让它立刻纠正，
	// 而不是等它自己走到下一个对账周期（那两秒里它会往一个已经没有
	// 这个场景的节点上发迁移）。
	if len(back) > 0 {
		c.w.SendTopoDigest(back)
	}

	switch {
	case len(gone) > 0 || len(back) > 0:
		c.log.Info("member view changed", "alive", names, "joined", back, "lost", gone,
			"scenes", c.reachableScenes())
	default:
		c.log.Debug("member view applied", "alive", names, "views", v.At.Format(time.RFC3339Nano))
	}
}

// reachableScenes 统计当前路由表里可达的地块数，只用于日志。
func (c *discoveryComponent) reachableScenes() int {
	n := 0
	for _, ch := range c.w.Chunks() {
		if ch.Alive {
			n++
		}
	}
	return n
}

// snapshot 返回 /admin/state 里的发现视图。
func (c *discoveryComponent) snapshot() map[string]any {
	var members []string
	if p := c.members.Load(); p != nil {
		members = append([]string(nil), (*p)...)
	}
	return map[string]any{
		"self":     c.self,
		"registry": c.reg.Info(),
		"members":  members,
		"applied":  c.applied.Load(),
	}
}

// newRegistry 按启动参数创建注册中心。
func newRegistry(kind, dir string, ttl time.Duration, self string,
	cfg *world.Config, log *slog.Logger) (discovery.Registry, error) {
	switch kind {
	case "file":
		if dir == "" {
			return nil, fmt.Errorf("file 发现方式需要 -disc-dir 指定共享目录")
		}
		return discovery.NewFile(dir, discovery.WithTTL(ttl), discovery.WithLogger(log))
	case "static", "":
		nodes := make([]discovery.Node, 0, 4)
		seen := false
		if cfg != nil {
			for name, addr := range cfg.Nodes {
				nodes = append(nodes, discovery.Node{Name: name, Addr: addr})
				if name == self {
					seen = true
				}
			}
		}
		if self != "" && !seen {
			nodes = append(nodes, discovery.Node{Name: self})
		}
		return discovery.NewStatic(nodes...), nil
	default:
		return nil, fmt.Errorf("未知的发现方式 %q（支持 static / file）", kind)
	}
}

// advertiseAddr 把监听地址换成可拨号地址。
//
// 监听 0.0.0.0:7003 或者 :7003 的节点，注册出去的不能也是 0.0.0.0——
// 邻居会去拨自己的 0.0.0.0，本机上"能连上"，多机部署就全哑了。
// 这里退化成 127.0.0.1，多机部署必须显式给 -node-advertise。
func advertiseAddr(bind string) string {
	if bind == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return bind
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// parsePartition 解析 "3x2" 这样的拓扑描述，非法时退化成单个场景。
func parsePartition(v string) (rows, cols int) {
	var r, c int
	if _, err := fmt.Sscanf(v, "%dx%d", &r, &c); err != nil || r <= 0 || c <= 0 {
		return 1, 1
	}
	return r, c
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}

// buildState 组装 /admin/state 的返回体。
// 这个结构会直接进入控制台和排障脚本，字段名保持稳定。
func buildState(app *core.App, g *gate.Gate, w *world.World, msh *mesh.Mesh,
	disc *discoveryComponent, sched *world.Scheduler, saver *saveComponent) map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	worldState := map[string]any{
		"sceneCount": w.Count(),
		"chunkSize":  w.ChunkSize(),
		"ids":        w.IDStat(),
		"chunks":     w.Chunks(),
		"scenes":     w.Stats(),
		"node":       w.Node(),
		"alive":      w.AliveNodes(),
		"topology":   w.TopoStat(),
	}
	if msh != nil {
		worldState["mesh"] = msh.Stat()
	}

	state := map[string]any{
		"app": map[string]any{
			"name":          app.Name(),
			"ready":         app.Ready(),
			"uptimeSeconds": int64(app.Uptime().Seconds()),
			"components":    app.Components(),
			"status":        app.Status(),
		},
		"runtime": map[string]any{
			"goVersion":     runtime.Version(),
			"goroutines":    runtime.NumGoroutine(),
			"heapAllocMB":   float64(ms.HeapAlloc) / 1024 / 1024,
			"numGC":         ms.NumGC,
			"lastGCPauseMs": float64(ms.PauseNs[(ms.NumGC+255)%256]) / 1e6,
		},
		"gate":  g.Stats(),
		"world": worldState,
	}
	if disc != nil {
		state["discovery"] = disc.snapshot()
	}
	if sched != nil {
		state["scheduler"] = sched.Stat()
	}
	if saver != nil {
		state["save"] = saver.snapshot()
	}
	return state
}

// saveFilePath 返回某节点的存档文件路径。每个节点一份，互不覆盖——
// 拓扑版本号、ID 分区、实体都是按节点分的。
func saveFilePath(dir, node string) string {
	name := "world-save.json"
	if node != "" {
		name = "world-save-" + node + ".json"
	}
	return filepath.Join(dir, name)
}

// saveComponent 是存档的生命周期组件：启动时恢复、周期落盘、关服落盘。
//
// 它不持有自己的状态机，只是给 World.SaveWorld / RestoreWorld 套一个
// core.Component 的壳，并把文件路径、周期这些部署细节收拢在这里。
// 排它不需要锁：保存是低频操作，且关服落盘与周期落盘不会并发
// （关服时周期循环已随 ctx 取消退出）。
type saveComponent struct {
	w        *world.World
	dir      string
	node     string
	interval time.Duration
	log      *slog.Logger

	cancel   context.CancelFunc
	saves    atomic.Int64
	restored atomic.Int64
	lastErr  atomic.Value // string
}

func newSaveComponent(w *world.World, dir, node string, interval time.Duration, log *slog.Logger) *saveComponent {
	return &saveComponent{w: w, dir: dir, node: node, interval: interval, log: log}
}

// savePath 返回本节点的存档文件路径。
func (c *saveComponent) savePath() string {
	return saveFilePath(c.dir, c.node)
}

func (c *saveComponent) Name() string { return "save" }

// Start 先恢复存档，再启动周期落盘。
//
// 恢复必须在场景 tick 跑起来之后（本组件注册在场景之后），
// 所以 Start 里做是安全的。恢复失败不致命——就当没有存档，
// 世界从配置冷启动。
func (c *saveComponent) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	path := c.savePath()
	if b, err := os.ReadFile(path); err == nil {
		if err := c.w.RestoreWorld(b); err != nil {
			c.log.Warn("restore world failed, cold start", "path", path, "err", err)
			c.setErr(err)
		} else {
			c.restored.Store(1)
			c.log.Info("world restored from save", "path", path)
		}
	} else if !os.IsNotExist(err) {
		c.log.Warn("read save file failed", "path", path, "err", err)
	}

	go c.loop(ctx)
	return nil
}

func (c *saveComponent) loop(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.save("periodic")
		}
	}
}

// Stop 关服前落最后一份盘。此时场景 tick 还在跑（本组件注册在场景之后，
// 逆序停止时先于场景），走 mailbox 采集是安全的。
func (c *saveComponent) Stop(context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	c.save("shutdown")
	return nil
}

// save 采集快照并原子落盘。
func (c *saveComponent) save(reason string) {
	b, err := c.w.SaveWorld()
	if err != nil {
		c.log.Warn("save world failed", "reason", reason, "err", err)
		c.setErr(err)
		return
	}
	path := c.savePath()
	if err := world.AtomicWrite(path, b); err != nil {
		c.log.Warn("write save failed", "reason", reason, "path", path, "err", err)
		c.setErr(err)
		return
	}
	c.saves.Add(1)
	c.log.Info("world saved", "reason", reason, "path", path, "bytes", len(b))
}

func (c *saveComponent) setErr(err error) {
	c.lastErr.Store(err.Error())
}

// playerStoreComponent 把需要释放连接的玩家档案后端（Redis/MySQL/Layered）
// 包成 core.Component，只为在进程退出时显式 Close 释放连接池。它不 Start
// 任何东西——连接在构造时建立，Stop 里 Close 即可。不注册这个组件也不影响
// 正确性，只是退出时少了一次显式关闭（进程退出本来就回收连接），但对
// 长跑进程的优雅退出语义更完整。用 persist.Closer 接口而非具体类型，
// 这样无论底层是 RedisStore / MySQLStore / LayeredStore 都能统一收口。
type playerStoreComponent struct {
	closer persist.Closer
}

func (c *playerStoreComponent) Name() string { return "player-store" }

func (c *playerStoreComponent) Start(context.Context) error { return nil }

func (c *playerStoreComponent) Stop(context.Context) error { return c.closer.Close() }

// snapshot 暴露存档状态给 /admin/state。
func (c *saveComponent) snapshot() map[string]any {
	lastErr := ""
	if v, ok := c.lastErr.Load().(string); ok {
		lastErr = v
	}
	return map[string]any{
		"path":      c.savePath(),
		"interval":  c.interval.String(),
		"saves":     c.saves.Load(),
		"restored":  c.restored.Load() == 1,
		"lastError": lastErr,
	}
}
