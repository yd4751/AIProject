// RBAC：运维接口的 token 鉴权和角色分级。
//
// 设计取舍：不是完整的用户/角色/权限三张表，而是"token 换角色"的最小闭环。
// 运维接口的鉴权诉求很窄——"谁能看"和"谁能改"——用三个角色就能覆盖，
// 引入账号体系反而让单节点部署的运维成本翻倍。这里把扩展点留在 TokenStore
// 接口上：要接 LDAP/SSO 时换一个实现，中间件不用动。
//
// 默认安全姿态：没配 token 时（开发模式）所有接口开放。一旦配了 token，
// 读接口要求只读角色，写接口要求运维角色，审计接口要求只读角色。
// 这个"不配即开放"是刻意的——本地开发不该被鉴权卡住，但生产必须配。
package observability

import (
	"net/http"
	"strings"
	"sync"
)

// Role 是接口访问级别。
type Role int

const (
	// RoleNone 表示未认证。
	RoleNone Role = iota
	// RoleReadonly 只能读（/admin/state、/admin/audit、/metrics、探针）。
	RoleReadonly
	// RoleOperator 在只读之上，还能改（/admin/drain、/admin/shutdown、/admin/repartition）。
	RoleOperator
	// RoleAdmin 最高级别，预留（当前等同 Operator）。
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleReadonly:
		return "readonly"
	case RoleOperator:
		return "operator"
	case RoleAdmin:
		return "admin"
	default:
		return "none"
	}
}

// TokenStore 是 token → 角色 的映射。线程安全。
//
// 用一个简单的 map 加锁实现；token 数量极少（运维就几个人），
// 没必要上更重的结构。subtle.ConstantTimeCompare 做常量时间比较，
// 避免时序侧信道。
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]Role
}

// NewTokenStore 创建空 token 表。
func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[string]Role)}
}

// Add 登记一个 token 及其角色。覆盖已存在的同名 token。
func (s *TokenStore) Add(token string, role Role) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = role
}

// Role 返回 token 对应的角色，未知 token 返回 RoleNone。
func (s *TokenStore) Role(token string) Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.tokens[token]
	if !ok {
		return RoleNone
	}
	return r
}

// hasRole 判断角色是否至少达到某个级别。
func hasRole(got, required Role) bool {
	// Role 值按权限递增定义，RoleAdmin > RoleOperator > RoleReadonly > RoleNone。
	return got >= required && got != RoleNone
}

// RBAC 是鉴权中间件。包裹 http.Handler，按路径 + 方法判定所需角色。
//
// 判定规则硬编码在 allowed 表里：这是"运维接口"而非"通用框架"，
// 把规则写死比做成可配置的 DSL 更不易写错——配置驱动的权限表一旦
// 拼错一条路径，就是"本该锁死的接口裸奔"。
type RBAC struct {
	store *TokenStore
	next  http.Handler
}

// NewRBAC 创建鉴权中间件。store 为 nil 时所有请求放行（开发模式）。
func NewRBAC(store *TokenStore, next http.Handler) *RBAC {
	return &RBAC{store: store, next: next}
}

// requiredRole 返回路径 + 方法所需的角色。返回 has=false 表示无需鉴权
// （探针接口必须永远可达，否则负载均衡器会把它判死）。
func requiredRole(method, path string) (Role, bool) {
	// 探针和指标：健康检查必须匿名可达，否则 K8s/反代会反复重启它。
	if path == "/healthz" || path == "/readyz" {
		return RoleNone, false
	}
	if path == "/metrics" {
		return RoleReadonly, true
	}
	if strings.HasPrefix(path, "/admin/") {
		switch path {
		case "/admin/state", "/admin/audit":
			return RoleReadonly, true
		case "/admin/drain", "/admin/shutdown", "/admin/repartition":
			return RoleOperator, true
		}
		// 未知 /admin/* 路径：默认要求只读（宁可严一点）。
		return RoleReadonly, true
	}
	// 非 admin、非探针路径：默认不鉴权。
	return RoleNone, false
}

// ServeHTTP 实现 http.Handler。
func (r *RBAC) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	role, need := requiredRole(req.Method, req.URL.Path)
	if need && r.store != nil {
		got := r.roleOf(req)
		if !hasRole(got, role) {
			http.Error(w, "forbidden: insufficient role", http.StatusForbidden)
			return
		}
	}
	r.next.ServeHTTP(w, req)
}

// roleOf 从请求里提取 token 并映射角色。
//
// 支持两种带法：Authorization: Bearer <token>（标准）和
// ?token=<token>（方便 curl 和浏览器直接点）。两者等价，
// 优先级上 Header 优先——URL 里的 token 会进访问日志和代理日志，
// 不如 Header 干净，但运维脚本里 URL 方式实在太好用，两个都留。
func (r *RBAC) roleOf(req *http.Request) Role {
	if auth := req.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return r.store.Role(strings.TrimSpace(auth[len("Bearer "):]))
		}
		return r.store.Role(strings.TrimSpace(auth))
	}
	if token := req.URL.Query().Get("token"); token != "" {
		return r.store.Role(token)
	}
	return RoleNone
}
