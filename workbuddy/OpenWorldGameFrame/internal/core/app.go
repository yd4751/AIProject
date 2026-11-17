// Package core 提供服务容器：组件的注册、有序启动、优雅退出。
//
// 设计原则：进程退出的顺序必须和启动相反。Gate 最后启动、最先关闭，
// 这样关闭期间不会再有新连接进来，已经在线的玩家有时间存档。
package core

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Component 是可以被容器托管的服务单元。Start 返回后组件应在后台自行运行，
// 收到 Stop 时优雅收尾。
type Component interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// App 是进程级容器。
type App struct {
	name string
	log  *slog.Logger

	comps []Component

	ready     atomic.Bool
	startedAt time.Time

	stopTimeout time.Duration

	// stopCh 让"程序内部主动退出"和"收到信号"走同一条收尾路径。
	// 控制面通过 /admin/shutdown 触发它——绝不能用 kill 代替，
	// 那样组件没有机会存档、也没有机会把倒计时通知发出去。
	stopOnce sync.Once
	stopCh   chan struct{}
	stopWhy  atomic.Value

	mu     sync.Mutex
	status map[string]string
}

// New 创建容器。
func New(name string, log *slog.Logger) *App {
	return &App{
		name:        name,
		log:         log,
		startedAt:   time.Now(),
		stopTimeout: 15 * time.Second,
		stopCh:      make(chan struct{}),
		status:      make(map[string]string),
	}
}

// Shutdown 以与 SIGTERM 完全相同的方式触发退出：先摘 ready，再逆序停止组件。
// 重复调用是安全的，只有第一次生效。
func (a *App) Shutdown(reason string) {
	a.stopOnce.Do(func() {
		a.stopWhy.Store(reason)
		a.log.Warn("shutdown requested", "reason", reason)
		close(a.stopCh)
	})
}

// ShutdownReason 返回触发退出的原因，未退出时为空。
func (a *App) ShutdownReason() string {
	if v, ok := a.stopWhy.Load().(string); ok {
		return v
	}
	return ""
}

// Register 按调用顺序登记组件，启动顺序即登记顺序。
func (a *App) Register(c Component) {
	a.comps = append(a.comps, c)
}

// MarkReady 表示服务已完全就绪，/readyz 从此返回 200。
func (a *App) MarkReady() { a.ready.Store(true) }

// Ready 返回当前是否就绪。
func (a *App) Ready() bool { return a.ready.Load() }

// Uptime 返回启动至今的时长。
func (a *App) Uptime() time.Duration { return time.Since(a.startedAt) }

// Name 返回应用名。
func (a *App) Name() string { return a.name }

// Run 阻塞运行直到收到 SIGINT/SIGTERM 或 ctx 被取消。
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	started := make([]Component, 0, len(a.comps))
	for _, c := range a.comps {
		if err := c.Start(ctx); err != nil {
			a.log.Error("component start failed", "component", c.Name(), "err", err)
			a.stopAll(started, 5*time.Second)
			return err
		}
		a.log.Info("component started", "component", c.Name())
		started = append(started, c)
	}

	a.log.Info("app started", "name", a.name, "components", len(started), "uptime_from", a.startedAt.Format(time.RFC3339))
	// 所有组件都启动成功才算就绪。运维看到的 ready 必须与服务真实能力一致。
	a.ready.Store(true)

	select {
	case <-ctx.Done():
		a.log.Info("shutdown signal received", "name", a.name, "reason", "signal")
	case <-a.stopCh:
		a.log.Info("shutdown signal received", "name", a.name, "reason", a.ShutdownReason())
	}

	// 一旦开始退出，立刻让 /readyz 返回失败，把流量摘掉。
	a.ready.Store(false)
	a.stopAll(started, a.stopTimeout)
	a.log.Info("app stopped", "name", a.name, "uptime", time.Since(a.startedAt).String())
	return nil
}

// stopAll 逆序停止，单个组件超时不影响后续组件继续收尾。
func (a *App) stopAll(started []Component, timeout time.Duration) {
	for i := len(started) - 1; i >= 0; i-- {
		c := started[i]
		sctx, cancel := context.WithTimeout(context.Background(), timeout)
		start := time.Now()
		if err := c.Stop(sctx); err != nil {
			a.log.Warn("component stop failed", "component", c.Name(), "err", err)
		} else {
			a.log.Info("component stopped", "component", c.Name(), "cost", time.Since(start).String())
		}
		cancel()
	}
}

// Components 返回已注册组件名，供状态接口展示。
func (a *App) Components() []string {
	out := make([]string, 0, len(a.comps))
	for _, c := range a.comps {
		out = append(out, c.Name())
	}
	return out
}

// SetStatus 记录一条可观测的运行时状态，会出现在 /admin/state 里。
func (a *App) SetStatus(k, v string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status[k] = v
}

// Status 返回状态快照。
func (a *App) Status() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]string, len(a.status))
	for k, v := range a.status {
		out[k] = v
	}
	return out
}
