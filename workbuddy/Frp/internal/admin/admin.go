// Package admin 实现内嵌的 Web 管理端：实时连接/流量查看、授权规则与配置管理。
package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"frptun/internal/auth"
	"frptun/internal/config"
	"frptun/internal/server"
	"frptun/internal/stats"
	"frptun/internal/store"
)

//go:embed web/*
var webFS embed.FS

// Server 管理端 HTTP 服务。
type Server struct {
	cfg    *config.Config
	store  *store.Store
	srv    *server.Server
	hub    *stats.Hub
	broker *sseBroker
	http   *http.Server
}

// New 创建管理端实例。
func New(cfg *config.Config, st *store.Store, srv *server.Server) *Server {
	return &Server{cfg: cfg, store: st, srv: srv, hub: srv.Hub(), broker: newSSEBroker()}
}

// Run 启动管理端 HTTP 服务并阻塞。
func (a *Server) Run() error {
	addr := fmt.Sprintf("%s:%d", a.cfg.Admin.Addr, a.cfg.Admin.Port)

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	// 静态页面
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	// API（统一加 Basic Auth）
	mux.Handle("/api/", a.basicAuth(a.apiRouter()))

	a.http = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("[admin] 管理端已启动 http://%s (用户 %s)", addr, a.cfg.Admin.User)

	// 定期推送实时数据 + 清理过期流量
	go a.periodic()

	err = a.http.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Close 关闭管理端。
func (a *Server) Close() {
	if a.http != nil {
		_ = a.http.Close()
	}
}

func (a *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != a.cfg.Admin.User || p != a.cfg.Admin.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="frptun admin"`)
			http.Error(w, "未授权", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Server) apiRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", a.overview)
	mux.HandleFunc("/api/connections", a.connections)
	mux.HandleFunc("/api/clients", a.clients)
	mux.HandleFunc("/api/traffic", a.traffic)
	mux.HandleFunc("/api/hourly", a.hourly)
	mux.HandleFunc("/api/rules", a.rules)
	mux.HandleFunc("/api/settings", a.settings)
	mux.HandleFunc("/api/ports", a.ports)
	mux.HandleFunc("/api/events", a.events)
	return mux
}

// ---- API handlers ----

func (a *Server) overview(w http.ResponseWriter, r *http.Request) {
	totals, err := a.store.Totals()
	if err != nil {
		writeErr(w, err)
		return
	}
	clients, err := a.store.ListClients()
	if err != nil {
		writeErr(w, err)
		return
	}
	// 当前实时上下行速率：由 SSE 快照求和近似（累计字节在此用于展示累计）
	live := a.hub.Snapshot()
	var liveUp, liveDown int64
	for _, c := range live {
		liveUp += c.Up
		liveDown += c.Down
	}
	writeJSON(w, map[string]any{
		"online_clients":  len(a.srv.ConnectedClients()),
		"active_tunnels":  a.srv.ActiveTunnels(),
		"ports_count":     len(a.srv.Ports()),
		"total_clients":   len(clients),
		"total_conns":     totals.Connections,
		"bytes_up":        totals.BytesUp,
		"bytes_down":      totals.BytesDown,
		"live_bytes_up":   liveUp,
		"live_bytes_down": liveDown,
		"server_time":     time.Now().Unix(),
	})
}

func (a *Server) connections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"connections": a.hub.Snapshot()})
}

func (a *Server) clients(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListClients()
	if err != nil {
		writeErr(w, err)
		return
	}
	online := make(map[string]bool)
	for _, c := range a.srv.ConnectedClients() {
		online[c.MachineID] = true
	}
	type clientWithStatus struct {
		store.ClientRecord
		Online bool `json:"online"`
	}
	out := make([]clientWithStatus, 0, len(list))
	for _, c := range list {
		out = append(out, clientWithStatus{ClientRecord: c, Online: online[c.MachineID]})
	}
	writeJSON(w, map[string]any{"clients": out})
}

func (a *Server) traffic(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.store.ListTraffic(limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"traffic": list})
}

func (a *Server) hourly(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	list, err := a.store.HourlyTraffic(hours)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"points": list})
}

func (a *Server) rules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := a.store.ListRules()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"rules": list})

	case http.MethodPost:
		var req struct {
			Kind   string `json:"kind"`
			Value  string `json:"value"`
			Action string `json:"action"`
			Note   string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, err)
			return
		}
		id, err := a.store.AddRule(req.Kind, req.Value, req.Action, req.Note)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": id})

	case http.MethodPut:
		var req struct {
			ID      int64 `json:"id"`
			Enabled bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, err)
			return
		}
		if err := a.store.SetRuleEnabled(req.ID, req.Enabled); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err := a.store.DeleteRule(id); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	default:
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
	}
}

func (a *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"auth_mode": a.store.GetSetting("auth_mode", auth.ModeOff),
		})
	case http.MethodPost:
		var req struct {
			AuthMode string `json:"auth_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, err)
			return
		}
		switch req.AuthMode {
		case auth.ModeOff, auth.ModeWhitelist, auth.ModeBlacklist:
		default:
			http.Error(w, "无效授权模式", http.StatusBadRequest)
			return
		}
		if err := a.store.SetSetting("auth_mode", req.AuthMode); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
	}
}

// ports 实时端口管理：查询、新增、关闭。
func (a *Server) ports(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"ports": a.srv.Ports()})

	case http.MethodPost:
		var req struct {
			MachineID  string `json:"machine_id"`
			Name       string `json:"name"`
			LocalIP    string `json:"local_ip"`
			LocalPort  int    `json:"local_port"`
			RemotePort int    `json:"remote_port"`
			Note       string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, err)
			return
		}
		id, acked, err := a.srv.AddPort(req.MachineID, req.Name, req.LocalIP, req.LocalPort, req.RemotePort, req.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": id, "client_acked": acked})

	case http.MethodDelete:
		machineID := r.URL.Query().Get("machine_id")
		port, _ := strconv.Atoi(r.URL.Query().Get("remote_port"))
		if machineID == "" || port == 0 {
			http.Error(w, "缺少 machine_id 或 remote_port", http.StatusBadRequest)
			return
		}
		if err := a.srv.RemovePort(machineID, port); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	default:
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
	}
}

// events 通过 SSE 每 1 秒推送一次实时快照。
func (a *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持流式响应", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.broker.subscribe()
	defer a.broker.unsubscribe(ch)

	for {
		select {
		case data := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *Server) periodic() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	purge := time.NewTicker(6 * time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ticker.C:
			snapshot := map[string]any{
				"connections":    a.hub.Snapshot(),
				"online_clients": len(a.srv.ConnectedClients()),
				"active_tunnels": a.srv.ActiveTunnels(),
				"ports":          a.srv.Ports(),
				"server_time":    time.Now().Unix(),
			}
			data, _ := json.Marshal(snapshot)
			a.broker.publish(string(data))
		case <-purge.C:
			_ = a.store.PurgeOldTraffic(30)
		}
	}
}

// ---- SSE 广播 ----

type sseBroker struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newSSEBroker() *sseBroker { return &sseBroker{subs: make(map[chan string]struct{})} }

func (b *sseBroker) subscribe() chan string {
	ch := make(chan string, 4)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sseBroker) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *sseBroker) publish(data string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- data:
		default: // 慢客户端丢弃本帧，避免阻塞广播
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
