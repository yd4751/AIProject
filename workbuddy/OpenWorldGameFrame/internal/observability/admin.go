package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// StateFunc 返回一份可序列化的状态快照。
type StateFunc func() any

// Admin 是运维接口服务，提供探针、状态和指标。
//
// 注意：这个服务必须绑内网地址。生产环境还要在前面加一层 token 鉴权，
// 绝不能把 /admin/* 暴露到公网。
type Admin struct {
	addr    string
	log     *slog.Logger
	srv     *http.Server
	metrics *Metrics
	state   StateFunc
	ready   func() bool

	// drainFn 是排水回调：控制面调 POST /admin/drain 时触发，
	// 由 main 接到 Gate.BeginDrain 上。参数是给玩家的倒计时长度。
	//
	// 做成回调而不是 Admin 直接持有 Gate，是为了让 observability 包
	// 保持"只观测、不驱动业务"的边界。
	drainFn func(grace time.Duration)
	stopFn  func(reason string)
	// moveFn 是重分区回调：把一块地搬到另一个节点。
	// 和 drainFn 一样是回调——observability 不该知道"世界拓扑"是什么。
	moveFn   func(scene uint32, to string) error
	draining atomic.Bool

	// audit 记录危险操作的审计日志。可能为 nil（未启用审计时）。
	audit *Audit

	// topoFn 返回拓扑热力图数据（Chunk 归属 + 负载）。为 nil 时
	// /admin/topo 返回"本进程不支持"。返回 any 是为了避免 observability
	// 依赖 world 包（world 反过来 import observability，会循环）。
	topoFn func() any
}

// NewAdmin 创建运维服务。
func NewAdmin(addr string, log *slog.Logger, metrics *Metrics, ready func() bool, state StateFunc) *Admin {
	a := &Admin{
		addr:    addr,
		log:     log,
		metrics: metrics,
		state:   state,
		ready:   ready,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealthz)
	mux.HandleFunc("/readyz", a.handleReadyz)
	mux.HandleFunc("/admin/state", a.handleState)
	mux.HandleFunc("/admin/drain", a.handleDrain)
	mux.HandleFunc("/admin/shutdown", a.handleShutdown)
	mux.HandleFunc("/admin/repartition", a.handleRepartition)
	mux.HandleFunc("/admin/audit", a.handleAudit)
	mux.HandleFunc("/admin/topo", a.handleTopo)
	mux.HandleFunc("/metrics", a.handleMetrics)

	a.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return a
}

// OnDrain 注册排水回调。必须在 Start 之前调用。
func (a *Admin) OnDrain(fn func(grace time.Duration)) { a.drainFn = fn }

// OnShutdown 注册退出回调。必须在 Start 之前调用。
func (a *Admin) OnShutdown(fn func(reason string)) { a.stopFn = fn }

// OnRepartition 注册重分区回调。必须在 Start 之前调用。
func (a *Admin) OnRepartition(fn func(scene uint32, to string) error) { a.moveFn = fn }

// SetAudit 挂上审计记录器。必须在 Start 之前调用。
// 不调用则审计关闭，/admin/audit 返回空列表。
func (a *Admin) SetAudit(au *Audit) { a.audit = au }

// SetAuth 挂上鉴权。store 非 nil 时，所有 /admin/* 写接口要求运维角色、
// 读接口要求只读角色；store 为 nil 时保持开放（开发模式）。
// 必须在 Start 之前调用。
func (a *Admin) SetAuth(store *TokenStore) {
	if store == nil {
		return
	}
	a.srv.Handler = NewRBAC(store, a.srv.Handler)
}

// OnTopo 注册拓扑热力图数据源。必须在 Start 之前调用。
// fn 返回 []ChunkInfo（world 包类型，经 any 传递避免循环依赖）。
func (a *Admin) OnTopo(fn func() any) { a.topoFn = fn }

// Name 实现 core.Component。
func (a *Admin) Name() string { return "admin" }

// Start 在后台开始监听。
func (a *Admin) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		return err
	}
	a.log.Info("admin server listening", "addr", ln.Addr().String(), "endpoints",
		[]string{"/healthz", "/readyz", "/admin/state", "/admin/audit", "/admin/drain", "/admin/shutdown", "/admin/repartition", "/metrics"})

	go func() {
		if err := a.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.log.Error("admin server exited", "err", err)
		}
	}()
	return nil
}

// Stop 优雅关闭，给正在处理的请求 5 秒。
func (a *Admin) Stop(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.srv.Shutdown(sctx)
}

// Draining 返回是否已进入排水状态。
func (a *Admin) Draining() bool { return a.draining.Load() }

func (a *Admin) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *Admin) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if a.draining.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("draining\n"))
		return
	}
	if a.ready != nil && !a.ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (a *Admin) handleState(w http.ResponseWriter, r *http.Request) {
	var payload any
	if a.state != nil {
		payload = a.state()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

// handleDrain 触发排水：摘流 + 通知 + 到期踢人。
//
// 参数 grace 是给玩家的倒计时秒数，默认 0（立即踢）。
// 生产环境应该由控制面传 300 左右，给玩家留出打完副本的时间。
func (a *Admin) handleDrain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	grace := 0
	if v := r.URL.Query().Get("grace"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			grace = n
		}
	}

	a.draining.Store(true)
	a.log.Warn("drain requested via admin api", "graceSeconds", grace)
	if a.audit != nil {
		a.audit.Record(r, "drain", "grace="+strconv.Itoa(grace)+"s", "accepted")
	}
	if a.drainFn != nil {
		a.drainFn(time.Duration(grace) * time.Second)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"draining":    true,
		"graceSecond": grace,
	})
}

// handleShutdown 让进程自己走优雅退出，而不是被 kill。
//
// 必须和 drain 配合使用：先 drain 把玩家送走，再 shutdown 让组件收尾。
// 如果直接 shutdown，网关会在 Stop 里给玩家发踢线包，但没时间让客户端
// 处理倒计时——体验上仍然是"突然掉线"。
func (a *Admin) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "admin request"
	}
	a.log.Warn("shutdown requested via admin api", "reason", reason)
	if a.audit != nil {
		a.audit.Record(r, "shutdown", "reason="+reason, "accepted")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "shutdown": true})

	// 先回包再退出，否则调用方拿到的是连接被重置而不是 202。
	if a.stopFn != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			a.stopFn(reason)
		}()
	}
}

// handleRepartition 把一块地搬到另一个节点。
//
//	POST /admin/repartition?scene=3&to=n2
//
// 返回 202 而不是 200：搬迁是异步的（要排空实体、两次握手），
// 这里只能确认"指令被接受了"。进度看 /admin/state 里对应场景的 move 字段。
func (a *Admin) handleRepartition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	sceneID, err := strconv.ParseUint(q.Get("scene"), 10, 32)
	if err != nil {
		http.Error(w, "scene 必须是场景 ID", http.StatusBadRequest)
		return
	}
	to := q.Get("to")
	if to == "" {
		http.Error(w, "缺少 to（目标节点）", http.StatusBadRequest)
		return
	}
	if a.moveFn == nil {
		http.Error(w, "本进程不支持重分区", http.StatusNotImplemented)
		return
	}
	if err := a.moveFn(uint32(sceneID), to); err != nil {
		a.log.Warn("repartition rejected", "scene", sceneID, "to", to, "err", err)
		if a.audit != nil {
			a.audit.Record(r, "repartition",
				"scene="+q.Get("scene")+" to="+to, "rejected: "+err.Error())
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.log.Warn("repartition accepted via admin api", "scene", sceneID, "to", to)
	if a.audit != nil {
		a.audit.Record(r, "repartition",
			"scene="+q.Get("scene")+" to="+to, "accepted")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "scene": sceneID, "to": to})
}

// handleAudit 返回最近的危险操作审计记录，按时间从旧到新。
//
// 支持 ?limit=N 限制条数（默认全部，最多 256 条上限内的全部）。
// 审计关闭时返回空数组而非 null，让前端不用判空。
func (a *Admin) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries := []AuditEntry{}
	if a.audit != nil {
		entries = a.audit.Entries()
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < len(entries) {
			entries = entries[len(entries)-n:]
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"audit": entries,
		"count": len(entries),
	})
}

func (a *Admin) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if a.metrics != nil {
		a.metrics.Render(w)
	}
}
