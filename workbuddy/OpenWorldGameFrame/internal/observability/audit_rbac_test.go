package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuditRecord 验证审计记录的环形缓冲与排序。
func TestAuditRecord(t *testing.T) {
	a := NewAudit(4)
	req := httptest.NewRequest(http.MethodPost, "/admin/drain", nil)
	req.RemoteAddr = "10.0.0.1:12345"

	for i := 0; i < 6; i++ {
		a.Record(req, "drain", "grace=1s", "accepted")
	}
	if a.Len() != 4 {
		t.Fatalf("环形缓冲应保留 4 条，实际 %d", a.Len())
	}
	entries := a.Entries()
	if len(entries) != 4 {
		t.Fatalf("Entries 应返回 4 条，实际 %d", len(entries))
	}
	// 时间应单调不减（按旧到新排序）。
	for i := 1; i < len(entries); i++ {
		if entries[i].Time < entries[i-1].Time {
			t.Fatalf("审计记录未按时间排序: %s > %s", entries[i-1].Time, entries[i].Time)
		}
	}
	// 来源 IP 应从 RemoteAddr 剥掉端口。
	if entries[0].Actor != "10.0.0.1" {
		t.Fatalf("来源 IP 应剥掉端口，得到 10.0.0.1，实际 %q", entries[0].Actor)
	}
	// Entries 返回副本，修改不影响内部。
	entries[0].Action = "corrupted"
	if got := a.Entries()[0].Action; got == "corrupted" {
		t.Fatal("Entries 应返回副本，修改不应影响内部状态")
	}
}

// TestAuditRemoteIP 验证 X-Forwarded-For 优先于 RemoteAddr。
func TestAuditRemoteIP(t *testing.T) {
	a := NewAudit(8)
	req := httptest.NewRequest(http.MethodPost, "/admin/drain", nil)
	req.RemoteAddr = "10.0.0.9:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	a.Record(req, "drain", "", "accepted")
	if got := a.Entries()[0].Actor; got != "203.0.113.5" {
		t.Fatalf("应取 X-Forwarded-For 第一段 203.0.113.5，实际 %q", got)
	}
}

// TestAuditEntriesEmpty 验证空审计返回空切片而非 nil（JSON 序列化为 []）。
func TestAuditEntriesEmpty(t *testing.T) {
	a := NewAudit(8)
	entries := a.Entries()
	if entries == nil {
		t.Fatal("空审计 Entries 应返回空切片而非 nil")
	}
	b, _ := json.Marshal(map[string]any{"audit": entries})
	if !strings.Contains(string(b), `"audit":[]`) {
		t.Fatalf("空审计应序列化为 []，实际 %s", b)
	}
}

// TestTokenStore 验证 token → 角色映射。
func TestTokenStore(t *testing.T) {
	s := NewTokenStore()
	s.Add("read-token", RoleReadonly)
	s.Add("op-token", RoleOperator)

	cases := []struct {
		token string
		want  Role
	}{
		{"read-token", RoleReadonly},
		{"op-token", RoleOperator},
		{"unknown", RoleNone},
		{"", RoleNone},
	}
	for _, c := range cases {
		if got := s.Role(c.token); got != c.want {
			t.Fatalf("Role(%q) = %v, want %v", c.token, got, c.want)
		}
	}
}

// TestRBACRequiredRole 验证路径 → 所需角色的映射。
func TestRBACRequiredRole(t *testing.T) {
	cases := []struct {
		path string
		want Role
		need bool
	}{
		{"/healthz", RoleNone, false},
		{"/readyz", RoleNone, false},
		{"/metrics", RoleReadonly, true},
		{"/admin/state", RoleReadonly, true},
		{"/admin/audit", RoleReadonly, true},
		{"/admin/drain", RoleOperator, true},
		{"/admin/shutdown", RoleOperator, true},
		{"/admin/repartition", RoleOperator, true},
	}
	for _, c := range cases {
		got, need := requiredRole(http.MethodGet, c.path)
		if got != c.want || need != c.need {
			t.Fatalf("requiredRole(%s) = (%v, %v), want (%v, %v)",
				c.path, got, need, c.want, c.need)
		}
	}
}

// TestRBACMiddleware 验证鉴权中间件的放行/拒绝。
func TestRBACMiddleware(t *testing.T) {
	store := NewTokenStore()
	store.Add("readonly-token", RoleReadonly)
	store.Add("op-token", RoleOperator)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rbac := NewRBAC(store, next)

	do := func(method, path, token string) int {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		rbac.ServeHTTP(rec, req)
		return rec.Code
	}

	// 无 token 访问只读接口 → 拒绝。
	if got := do(http.MethodGet, "/admin/state", ""); got != http.StatusForbidden {
		t.Fatalf("无 token 访问只读接口应 403，实际 %d", got)
	}
	// 只读 token 访问只读接口 → 放行。
	if got := do(http.MethodGet, "/admin/state", "readonly-token"); got != http.StatusOK {
		t.Fatalf("只读 token 访问只读接口应 200，实际 %d", got)
	}
	// 只读 token 访问写接口 → 拒绝。
	if got := do(http.MethodPost, "/admin/drain", "readonly-token"); got != http.StatusForbidden {
		t.Fatalf("只读 token 访问写接口应 403，实际 %d", got)
	}
	// 运维 token 访问写接口 → 放行。
	if got := do(http.MethodPost, "/admin/drain", "op-token"); got != http.StatusOK {
		t.Fatalf("运维 token 访问写接口应 200，实际 %d", got)
	}
	// 探针接口无需鉴权。
	if got := do(http.MethodGet, "/healthz", ""); got != http.StatusOK {
		t.Fatalf("探针接口应免鉴权，实际 %d", got)
	}
}

// TestRBACNilStore 验证 store 为 nil 时全放行（开发模式）。
func TestRBACNilStore(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rbac := NewRBAC(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/admin/drain", nil)
	rec := httptest.NewRecorder()
	rbac.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("nil store 应全放行，实际 %d", rec.Code)
	}
}

// TestRBACTokenViaQuery 验证 ?token= 带法也能鉴权。
func TestRBACTokenViaQuery(t *testing.T) {
	store := NewTokenStore()
	store.Add("op-token", RoleOperator)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rbac := NewRBAC(store, next)
	req := httptest.NewRequest(http.MethodPost, "/admin/drain?token=op-token", nil)
	rec := httptest.NewRecorder()
	rbac.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("query token 应放行，实际 %d", rec.Code)
	}
}

// TestLoadColor 验证负载分档着色。
func TestLoadColor(t *testing.T) {
	cases := []struct {
		load int64
		want string
	}{
		{0, "#2e7d32"},
		{59, "#2e7d32"},
		{60, "#f9a825"},
		{84, "#f9a825"},
		{85, "#c62828"},
		{100, "#c62828"},
	}
	for _, c := range cases {
		if got := loadColor(c.load); got != c.want {
			t.Fatalf("loadColor(%d) = %s, want %s", c.load, got, c.want)
		}
	}
}

// TestRenderTopoPage 验证热力图页面能生成合法的 SVG。
func TestRenderTopoPage(t *testing.T) {
	cells := []topoCell{
		{Chunk: struct{ X, Z int32 }{0, 0}, SceneID: 1, Local: true, Alive: true, Entities: 10, Load: 20},
		{Chunk: struct{ X, Z int32 }{1, 0}, SceneID: 2, Local: true, Alive: true, Entities: 50, Load: 90},
		{Chunk: struct{ X, Z int32 }{0, 1}, SceneID: 3, Node: "n2", Local: false, Alive: true, Load: 0},
		{Chunk: struct{ X, Z int32 }{1, 1}, SceneID: 4, Node: "n3", Local: false, Alive: false, Load: 0},
	}
	html := renderTopoPage(cells)
	for _, want := range []string{"<svg", "</svg>", "S1", "S2", "#c62828", "#2e7d32", "#555", "stroke-dasharray"} {
		if !strings.Contains(html, want) {
			t.Fatalf("热力图页面缺少 %q，实际输出片段: %s", want, html)
		}
	}
}

// TestDecodeTopoCells 验证 any → []topoCell 的 JSON 转换。
func TestDecodeTopoCells(t *testing.T) {
	// 模拟 world.ChunkInfo 的 JSON 形状。
	src := []map[string]any{
		{"chunk": map[string]any{"X": 0, "Z": 1}, "sceneId": 7, "node": "n2", "local": false, "alive": true, "entities": 3, "load": 0},
	}
	cells, err := decodeTopoCells(src)
	if err != nil {
		t.Fatalf("decodeTopoCells 失败: %v", err)
	}
	if len(cells) != 1 {
		t.Fatalf("应解出 1 个 cell，实际 %d", len(cells))
	}
	if cells[0].SceneID != 7 || cells[0].Chunk.X != 0 || cells[0].Chunk.Z != 1 || cells[0].Node != "n2" {
		t.Fatalf("解码结果不对: %+v", cells[0])
	}
}
