// frptun 是一个轻量、稳定、支持高负载的端口转发（内网穿透）工具。
// 通过配置中的 role 字段决定以服务端还是客户端模式运行。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"frptun/internal/admin"
	"frptun/internal/client"
	"frptun/internal/config"
	"frptun/internal/server"
	"frptun/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	configPath := flag.String("c", "config.json", "配置文件路径")
	version := flag.Bool("v", false, "打印版本信息")
	flag.Parse()

	if *version {
		fmt.Println("frptun v1.0.0")
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[frptun] 配置错误: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Role {
	case config.RoleServer:
		runServer(cfg, ctx)
	case config.RoleClient:
		runClient(cfg, ctx)
	}
}

func runServer(cfg *config.Config, ctx context.Context) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("[frps] 数据库初始化失败: %v", err)
	}
	defer st.Close()

	// 把配置里的 auth_mode 同步到数据库（数据库为准，首次写入）
	if stored := st.GetSetting("auth_mode", ""); stored == "" {
		_ = st.SetSetting("auth_mode", cfg.AuthMode)
	}

	srv := server.New(cfg, st)
	defer srv.Close()

	var adminSrv *admin.Server
	if cfg.Admin.Enabled {
		adminSrv = admin.New(cfg, st, srv)
		go func() {
			if err := adminSrv.Run(); err != nil {
				log.Printf("[admin] 管理端异常退出: %v", err)
			}
		}()
		defer adminSrv.Close()
	}

	go func() {
		<-ctx.Done()
		log.Println("[frps] 收到退出信号，正在关闭...")
		srv.Close()
		if adminSrv != nil {
			adminSrv.Close()
		}
	}()

	if err := srv.Run(); err != nil {
		log.Fatalf("[frps] 运行失败: %v", err)
	}
	log.Println("[frps] 已退出")
}

func runClient(cfg *config.Config, ctx context.Context) {
	cli := client.New(cfg)
	go func() {
		<-ctx.Done()
		log.Println("[frpc] 收到退出信号，正在关闭...")
		cli.Close()
	}()
	if err := cli.Run(); err != nil {
		log.Fatalf("[frpc] 运行失败: %v", err)
	}
	log.Println("[frpc] 已退出")
}
