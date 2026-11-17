// 审计日志：运维接口的危险操作留痕。
//
// 为什么不直接用 slog：slog 是给运维看运行日志的，格式随日志级别和
// 环境漂移；审计日志是给"谁在什么时候动了什么"做追溯的，格式必须稳定、
// 可查询、可导出。两者受众不同、生命周期不同（审计要保留、日志可滚动），
// 所以单独一条环形缓冲，不混进日志管道。
package observability

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// AuditEntry 是一条审计记录。
//
// 字段刻意拍平成基本类型：审计日志可能要进数据库、要跨系统传输，
// 任何结构体嵌套都会让下游的解析多一层约定。时间用 RFC3339 字符串，
// 让"人工可读"和"机器可读"共用一种表示。
type AuditEntry struct {
	// Time 是操作受理时间（RFC3339，带时区）。
	Time string `json:"time"`
	// Actor 是发起者标识。当前只有来源 IP，后续接入账号体系后
	// 这里换成"账号@来源IP"，字段名不变，下游不用改。
	Actor string `json:"actor"`
	// Action 是操作名：drain / shutdown / repartition 等。
	Action string `json:"action"`
	// Detail 是操作参数，例如 "grace=300s" 或 "scene=3 to=n1"。
	Detail string `json:"detail,omitempty"`
	// Result 是受理结果：accepted / rejected，带原因时写 "rejected: ..."。
	Result string `json:"result"`
}

// Audit 是审计日志的环形缓冲区。
//
// 环形而非无限追加：审计记录只进内存，无限增长会拖垮长驻进程。
// 容量是可调的，满了之后最旧的被覆盖——"最近 N 条"通常是追溯的
// 全部诉求，真要全量保留应该接外部存储，而不是让内存记录无限膨胀。
type Audit struct {
	mu      sync.Mutex
	entries []AuditEntry
	head    int // 下一个写入位置
	size    int // 当前有效条数
	cap     int
}

// NewAudit 创建容量为 cap 的审计缓冲区。cap <= 0 时用默认值。
func NewAudit(cap int) *Audit {
	if cap <= 0 {
		cap = 256
	}
	return &Audit{entries: make([]AuditEntry, cap), cap: cap}
}

// Record 追加一条审计记录。result 是受理结果描述。
func (a *Audit) Record(r *http.Request, action, detail, result string) {
	now := time.Now().Format(time.RFC3339)
	actor := ""
	if r != nil {
		actor = remoteIP(r)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries[a.head] = AuditEntry{
		Time:   now,
		Actor:  actor,
		Action: action,
		Detail: detail,
		Result: result,
	}
	a.head = (a.head + 1) % a.cap
	if a.size < a.cap {
		a.size++
	}
}

// Entries 返回全部记录，按时间从旧到新排序。
//
// 返回副本而非内部切片，避免调用方改到共享数据。
func (a *Audit) Entries() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, 0, a.size)
	for i := 0; i < a.size; i++ {
		idx := (a.head - a.size + i + a.cap) % a.cap
		out = append(out, a.entries[idx])
	}
	// 环形缓冲里的顺序就是时间顺序（head 是下一个写入位），无需再排序。
	// 但保留 sort 作为防御：万一未来并发写入顺序被打乱，这里还能兜住。
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// Len 返回当前有效条数。
func (a *Audit) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.size
}

// remoteIP 从请求里取来源 IP。优先 X-Forwarded-For 的第一段，
// 因为运维接口通常藏在反代后面，直接读 RemoteAddr 拿到的是反代地址。
func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For 是 "client, proxy1, proxy2"，第一段是真实客户端。
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	// RemoteAddr 是 "ip:port"，把端口剥掉。
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
