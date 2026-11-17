// Package discovery 回答一个问题：这个世界的节点现在有谁活着。
//
// M3.3 之前成员是启动参数里的静态表：configs/world-2node.json 每个节点
// 各持一份、内容必须完全一致。这带来两个后果：
//
//	加节点   要改所有节点的文件，再逐个重启（少改一个就出现"半个世界"）
//	节点挂掉 其它节点不知道，继续往它那儿发迁移请求，直到 3 秒超时回滚
//
// 这里把"成员是谁"从启动参数里拆出来，变成一个带租约的外部视图：
//
//	Register  声明自己活着，并按心跳续约
//	Watch     订阅成员变更，首个元素是当前视图
//
// 为什么必须是租约而不是"注册 + 注销"：进程被 kill、机器掉电、容器被杀时
// 根本来不及注销。只有"一段时间没心跳就判定死亡"能覆盖这些情况。
// 这也是 etcd lease、Kubernetes node heartbeat 的做法——不是巧合，
// 是分布式成员管理唯一能收敛的模型。
//
// 刻意不做的事：Registry 不负责"节点该承载哪些场景"。那是世界拓扑，
// 属于配置而不是心跳——场景归属变更是要迁移玩家的大动作，不能由
// 一次网络抖动触发。
package discovery

import (
	"context"
	"sort"
	"time"
)

// Node 是注册中心里的一个节点记录。
type Node struct {
	Name string `json:"name"`
	// Addr 是节点间链路（mesh）的可拨号地址，不是监听地址。
	// 绑定 0.0.0.0:7003 的节点必须注册成 10.0.0.5:7003，
	// 否则邻居会去拨自己的 0.0.0.0。
	Addr string `json:"addr"`
	// TS 是最后一次心跳时间，由写记录的一方填。
	// 读的一方用它判断"这条记录是不是已经过期"。
	TS time.Time `json:"ts"`
}

// View 是某一时刻的成员快照。
//
// 用值而不是增量事件：成员数是个位数，整份快照比"加/删/改"三种事件
// 好处理得多——幂等、可重放、不会因为漏掉一条事件而永久错乱。
type View struct {
	Nodes []Node
	At    time.Time
}

// Names 返回按名字排序的成员列表。
func (v View) Names() []string {
	out := make([]string, 0, len(v.Nodes))
	for _, n := range v.Nodes {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out
}

// Alive 判断某个节点是否在视图里。
func (v View) Alive(name string) bool {
	for _, n := range v.Nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

// Addr 返回某个节点的链路地址。
func (v View) Addr(name string) (string, bool) {
	for _, n := range v.Nodes {
		if n.Name == name {
			return n.Addr, true
		}
	}
	return "", false
}

// Equal 判断两个视图是否等价（忽略心跳时间）。
//
// 只比名字和地址：心跳时间每次都会变，拿它做比较会让 Watch
// 每个心跳都推一次"变更"，调用方以为成员在抖。
func (v View) Equal(o View) bool {
	if len(v.Nodes) != len(o.Nodes) {
		return false
	}
	a, b := append([]Node(nil), v.Nodes...), append([]Node(nil), o.Nodes...)
	byName := func(s []Node) {
		sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	}
	byName(a)
	byName(b)
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Addr != b[i].Addr {
			return false
		}
	}
	return true
}

// Info 是注册中心的运行状态，进 /admin/state。
type Info struct {
	Kind     string   `json:"kind"`
	Detail   string   `json:"detail,omitempty"`
	Interval string   `json:"interval,omitempty"`
	TTL      string   `json:"ttl,omitempty"`
	Views    int64    `json:"views"`
	Changes  int64    `json:"changes"`
	Members  []string `json:"members"`
}

// Registry 是成员登记与变更订阅的抽象。
//
// 实现方只需保证两件事：Register 之后自己出现在视图里、停止心跳之后
// 在有限时间内从视图里消失。至于是用 etcd 的 lease、一个目录里的文件、
// 还是 k8s 的 Endpoints，调用方不关心。
type Registry interface {
	// Register 声明本节点存活，返回的函数用于优雅下线（立刻撤销记录）。
	// 撤销函数必须可重复调用。
	Register(ctx context.Context, self Node) (func(), error)

	// Watch 返回成员视图的变更流，首个元素是当前视图。
	// ctx 结束后通道关闭。
	Watch(ctx context.Context) <-chan View

	// Info 返回运行状态快照。
	Info() Info

	// Close 停止所有后台活动。
	Close() error
}

// static 是固定成员表，等价于 M3.3 的行为：不会有人加入，也不会有人离开。
//
// 单进程部署、单元测试、以及"先跑起来再说"的场景都用它。
// 它的存在让调用方不必为"没配服务发现"写一条分支。
type static struct {
	view View
}

// NewStatic 创建固定成员表。
func NewStatic(nodes ...Node) Registry {
	cp := append([]Node(nil), nodes...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	return &static{view: View{Nodes: cp, At: time.Now()}}
}

// Register 对固定成员表是空操作：成员在构造时就定死了。
func (s *static) Register(context.Context, Node) (func(), error) {
	return func() {}, nil
}

// Watch 推一次当前视图然后挂住，直到 ctx 结束。
func (s *static) Watch(ctx context.Context) <-chan View {
	ch := make(chan View, 1)
	ch <- s.view
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

// Info 返回固定成员表的描述。
func (s *static) Info() Info {
	return Info{Kind: "static", Members: s.view.Names()}
}

func (s *static) Close() error { return nil }
