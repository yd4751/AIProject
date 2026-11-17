package control

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

//go:embed web/index.html
var consoleHTML []byte

// Server 暴露控制面的 HTTP 接口和内置控制台。
//
// 接口一律返回 JSON，错误也用 JSON（前端不用区分两种解析路径）。
// 所有开关服接口都是 202 + taskId，绝不同步等待——一次关服要等玩家
// 300 秒倒计时，任何同步接口都必然超时。
type Server struct {
	cfg  Config
	reg  *Registry
	orch *Orchestrator
	log  Logger
}

// NewServer 创建 HTTP 服务。
func NewServer(cfg Config, reg *Registry, orch *Orchestrator, log Logger) *Server {
	return &Server{cfg: cfg, reg: reg, orch: orch, log: log}
}

// Handler 返回路由表。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleConsole)
	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)

	mux.HandleFunc("POST /api/zones/{id}/start", s.handleZoneStart)
	mux.HandleFunc("POST /api/zones/{id}/stop", s.handleZoneStop)
	mux.HandleFunc("POST /api/instances/{id}/start", s.handleInstanceStart)
	mux.HandleFunc("POST /api/instances/{id}/stop", s.handleInstanceStop)

	return mux
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(consoleHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"serverTime": time.Now(),
	})
}

// overviewResponse 是控制台一次轮询拿到的全部数据。
//
// 把区服、实例、任务放在同一个响应里，是为了让前端一次请求画完整个页面，
// 避免"区服卡片已经更新、实例列表还是上一秒"的割裂感。
type overviewResponse struct {
	ServerTime string         `json:"serverTime"`
	Zones      []ZoneView     `json:"zones"`
	Instances  []InstanceView `json:"instances"`
	Tasks      []Task         `json:"tasks"`
	Summary    summary        `json:"summary"`
}

type summary struct {
	Zones      int     `json:"zones"`
	Instances  int     `json:"instances"`
	Running    int     `json:"running"`
	Failed     int     `json:"failed"`
	Online     int64   `json:"online"`
	Entities   int64   `json:"entities"`
	MaxTickLag float64 `json:"maxTickLagMs"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.buildOverview())
}

// buildOverview 组装控制台一次刷新需要的全部数据。
//
// 从 handleOverview 里抽出来，是为了让 SSE 推送和一次性的轮询响应
// 共用同一份组装逻辑——两种通道拿到的数据结构必须一致，否则前端
// 在"首次轮询"和"后续推送"之间会看到字段缺失。
func (s *Server) buildOverview() overviewResponse {
	zones, instances := s.reg.Overview()
	sum := summary{Zones: len(zones), Instances: len(instances)}
	for _, z := range zones {
		sum.Running += z.Running
		sum.Online += z.Online
		sum.Entities += z.Entities
		if z.MaxTickLagMs > sum.MaxTickLag {
			sum.MaxTickLag = z.MaxTickLagMs
		}
		if z.State == StateFailed {
			sum.Failed++
		}
	}

	return overviewResponse{
		ServerTime: time.Now().Format(time.RFC3339),
		Zones:      zones,
		Instances:  instances,
		Tasks:      s.orch.Tasks(20),
		Summary:    sum,
	}
}

// handleStream 是控制台的 SSE 增量推送端点。
//
// 用 SSE（Server-Sent Events）而不是 WebSocket，理由有二：
//  1. 控制台的实时需求是"服务端单向推状态"，没有"客户端往服务端发消息"
//     的需求（写操作走 POST 接口）。SSE 的语义正好匹配，WebSocket 的双向
//     是多余的。
//  2. SSE 是纯 HTTP 长连接，标准库 `http.Flusher` 就能实现，零外部依赖。
//     项目 go.mod 是零 require 的硬约束，手写 WebSocket 帧编解码会引入
//     一大块易出错的代码。
//
// 推送频率固定 1.5s，和之前前端轮询一致。真正的收益不在"更快"（状态
// 变化本来就是秒级），而在"多客户端共享一条推送流" + "EventSource 的
// 自动重连比 setInterval 更健壮"。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "流式响应不支持")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用反向代理缓冲，否则事件会被攒批

	// 立即推第一帧，避免客户端连接后空白等待一个周期。
	s.writeEvent(w, fl, s.buildOverview())

	ticker := time.NewTicker(streamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return // 客户端断开，结束流
		case <-ticker.C:
			s.writeEvent(w, fl, s.buildOverview())
		}
	}
}

// streamInterval 是 SSE 推送间隔。和之前前端轮询一致，状态变化是秒级的，
// 1.5s 足够实时，再快只是空转 CPU。
const streamInterval = 1500 * time.Millisecond

// writeEvent 写一条 SSE 事件。数据是完整的 overview JSON，前端每次收到
// 都全量重绘（和轮询时行为一致，避免做增量 diff 的复杂度）。
func (s *Server) writeEvent(w http.ResponseWriter, fl http.Flusher, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	// SSE 格式：`data: <json>\n\n`，空行是事件边界。
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	fl.Flush()
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.orch.Tasks(50)})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, ok := s.orch.Task(id)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleZoneStart(w http.ResponseWriter, r *http.Request) {
	t, err := s.orch.StartZone(r.PathValue("id"))
	if err != nil {
		s.respondErr(w, err)
		return
	}
	s.log.Info("开服任务已受理", "zone", t.ZoneID, "task", t.ID)
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleZoneStop(w http.ResponseWriter, r *http.Request) {
	grace := parseGrace(r)
	t, err := s.orch.StopZone(r.PathValue("id"), grace)
	if err != nil {
		s.respondErr(w, err)
		return
	}
	s.log.Info("关服任务已受理", "zone", t.ZoneID, "task", t.ID, "grace", grace.String())
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleInstanceStart(w http.ResponseWriter, r *http.Request) {
	t, err := s.orch.StartInstance(r.PathValue("id"))
	if err != nil {
		s.respondErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleInstanceStop(w http.ResponseWriter, r *http.Request) {
	grace := parseGrace(r)
	t, err := s.orch.StopInstance(r.PathValue("id"), grace)
	if err != nil {
		s.respondErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

// parseGrace 解析 ?grace=秒数，0 表示使用配置默认值。
func parseGrace(r *http.Request) time.Duration {
	v := r.URL.Query().Get("grace")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func (s *Server) respondErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "区服或实例不存在")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
