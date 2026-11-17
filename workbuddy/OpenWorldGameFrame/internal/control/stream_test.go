package control

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer 构造一个可测 HTTP 处理器的 Server。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	reg := NewRegistry(cfg, testLogger())
	sup, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	orch := NewOrchestrator(cfg, reg, sup, testLogger())
	return NewServer(cfg, reg, orch, testLogger())
}

// TestStreamSSEContentType 验证 /api/stream 返回 SSE 内容类型并立即推第一帧。
func TestStreamSSEContentType(t *testing.T) {
	s := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	// handleStream 会阻塞在 ticker 循环，用 goroutine 跑，读到第一帧就 cancel。
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	// 等第一帧写入。
	deadline := time.After(2 * time.Second)
	for rec.Body.Len() == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("2 秒内未收到第一帧推送")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %q", ct)
	}

	// 读第一帧：`data: <json>\n\n`。
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	sc.Scan()
	line := sc.Text()
	if !strings.HasPrefix(line, "data: ") {
		t.Fatalf("SSE 首行应以 data: 开头，实际 %q", line)
	}
	payload := strings.TrimPrefix(line, "data: ")

	var ov overviewResponse
	if err := json.Unmarshal([]byte(payload), &ov); err != nil {
		t.Fatalf("SSE 数据应能解析为 overviewResponse: %v", err)
	}
	if ov.Summary.Zones != 2 {
		t.Fatalf("overview 应有 2 个区服，实际 %d", ov.Summary.Zones)
	}

	cancel()
	<-done
}

// TestStreamBuildOverviewConsistency 验证 SSE 推送和轮询响应用同一份组装逻辑。
//
// 两个通道拿到的 overview 结构必须一致，否则前端在切换数据源时会看到字段缺失。
func TestStreamBuildOverviewConsistency(t *testing.T) {
	s := newTestServer(t)
	ov := s.buildOverview()
	if ov.Summary.Zones != 2 || ov.Summary.Instances != 3 {
		t.Fatalf("buildOverview 应统计 2 区服 3 实例，实际 %d/%d",
			ov.Summary.Zones, ov.Summary.Instances)
	}
	if len(ov.Zones) != 2 || len(ov.Instances) != 3 {
		t.Fatalf("overview 应有 2 zone / 3 instance，实际 %d/%d",
			len(ov.Zones), len(ov.Instances))
	}
}
