// cmd/control 是控制平面进程。
//
// 它和数据平面分开部署：World 进程挂了的时候，正是最需要看状态、
// 最需要把它拉起来的时刻。如果控制台和 World 同生共死，那它在最关键
// 的时刻恰好不可用。
//
// 控制面自己退出时不会杀掉托管的实例——这是刻意的。控制面重启不应该
// 等于全区停服。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"openworld/internal/control"
)

func main() {
	var (
		addr     = flag.String("addr", ":9000", "控制面监听地址（控制台 + API）")
		cfgPath  = flag.String("config", "configs/zones.json", "区服配置文件")
		logLevel = flag.String("log-level", "info", "日志级别：debug/info/warn/error")
		probeSec = flag.Int("probe-seconds", 2, "实例健康探测间隔（秒）")
	)
	flag.Parse()

	logger := newLogger(*logLevel)

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		logger.Error("加载配置失败", "path", *cfgPath, "err", err)
		os.Exit(1)
	}

	reg := control.NewRegistry(cfg, logger)
	sup, err := control.NewSupervisor(cfg)
	if err != nil {
		logger.Error("初始化进程管理器失败", "err", err)
		os.Exit(1)
	}

	// 进程退出的真实原因只能从这里拿到。HTTP 探针只知道"连不上"，
	// 分不清是崩溃、被 OOM 杀、还是正常退出。
	sup.SetExitHook(func(id string, code int) {
		reg.Update(id, func(i *control.Instance) {
			if i.State == control.StateDraining {
				return // 关服流程正在处理，不要抢着判异常
			}
			if i.State.Active() {
				i.State = control.StateFailed
				i.LastError = fmt.Sprintf("进程自行退出，exit=%d", code)
				i.ExitCode = code
				i.Since = time.Now()
				i.Snapshot = nil
			}
		})
		logger.Warn("托管进程退出", "instance", id, "exit", code)
	})

	orch := control.NewOrchestrator(cfg, reg, sup, logger)
	api := control.NewServer(cfg, reg, orch, logger)

	// 接管上次运行时还活着的实例。控制面是单点，它重启时底下的 World
	// 不应该跟着重来一遍——那等于每次都全区停服。
	if adopted, lost := reg.Load(); adopted > 0 || lost > 0 {
		logger.Info("已恢复上次运行状态", "adopted", adopted, "lost", lost)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go reg.RunProbe(ctx, time.Duration(*probeSec)*time.Second)
	go reg.RunAutoSave(ctx, 2*time.Second)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("控制面 HTTP 服务退出", "err", err)
			cancel()
		}
	}()

	logger.Info("控制面已启动", "addr", *addr, "console", consoleURL(*addr),
		"zones", len(cfg.Zones), "binary", cfg.Binary)

	<-ctx.Done()
	logger.Info("控制面退出中（托管实例保持运行）")

	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
}

// consoleURL 把监听地址转成能直接点开的控制台地址。
// 监听在 :9000 或 0.0.0.0:9000 时本机访问要补 127.0.0.1，否则日志里那串链接点不动。
func consoleURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func loadConfig(path string) (control.Config, error) {
	var cfg control.Config
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 %s: %w", path, err)
	}
	if cfg.Binary == "" {
		return cfg, fmt.Errorf("配置缺少 binary 字段")
	}
	if len(cfg.Zones) == 0 {
		return cfg, fmt.Errorf("配置里没有任何区服")
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "."
	}
	if cfg.LogDir == "" {
		cfg.LogDir = "logs"
	}
	return cfg, nil
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
