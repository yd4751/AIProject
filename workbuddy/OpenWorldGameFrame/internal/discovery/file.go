package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// file 用"一个共享目录 + 心跳文件"当注册中心，零依赖。
//
// 每个节点每个心跳周期写一次 <dir>/<name>.node（内容 {name,addr,ts}），
// 读的一方扫描目录、丢掉 ts 超过 TTL 的记录。语义与 etcd 的
// lease + prefix watch 一一对应：
//
//	写记录   = put 带 lease
//	心跳续期 = keepalive（这里靠改写 ts）
//	过期     = lease 到期自动删除
//	扫目录   = range 带 prefix
//
// 为什么先做这个而不是直接接 etcd：语义（租约、整份视图、幂等应用）
// 才是容易写错的部分，存储介质反而是最容易替换的一层。把 File 跑通、
// 用真双进程验证过"加节点不重启"，再写一个同接口的 etcd 实现，
// 风险就被隔离在一个文件里了。
//
// 适用边界：单机多进程、或所有节点挂同一块共享卷。跨机房必须换 etcd——
// 文件系统没有一致性保证，两个节点可能同时看不到对方。
type file struct {
	dir      string
	interval time.Duration
	ttl      time.Duration
	log      *slog.Logger

	views   atomic.Int64
	changes atomic.Int64
	members atomic.Pointer[[]string]
}

// FileOption 是文件注册中心的选项。
type FileOption func(*file)

// WithInterval 设置心跳与轮询周期。
//
// 越小发现越快、IO 越多。1 秒是"进程被杀到别人知道"的上界来源：
// 最坏情况 = 心跳间隔 + TTL。
func WithInterval(d time.Duration) FileOption {
	return func(f *file) {
		if d > 0 {
			f.interval = d
		}
	}
}

// WithTTL 设置记录过期时间。
//
// 必须显著大于 interval（默认 3 倍）：等于 interval 时，一次 GC 停顿
// 或者一次调度延迟就会让自己的记录过期，成员视图开始抖，
// 而"抖"在拓扑上表现为玩家走到边界偶尔过不去。
func WithTTL(d time.Duration) FileOption {
	return func(f *file) {
		if d > 0 {
			f.ttl = d
		}
	}
}

// WithLogger 设置日志。
func WithLogger(l *slog.Logger) FileOption {
	return func(f *file) {
		if l != nil {
			f.log = l
		}
	}
}

// NewFile 创建基于共享目录的注册中心。
func NewFile(dir string, opts ...FileOption) (Registry, error) {
	if dir == "" {
		return nil, errors.New("discovery: empty dir")
	}
	f := &file{
		dir:      dir,
		interval: defaultInterval,
		ttl:      defaultTTL,
		log:      slog.Default(),
	}
	for _, o := range opts {
		o(f)
	}
	if f.ttl <= f.interval {
		f.ttl = 3 * f.interval
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("discovery: 创建目录 %s 失败: %w", dir, err)
	}
	return f, nil
}

// Kind 返回实现类型名，供测试和日志使用。
func (f *file) Kind() string { return "file" }

func (f *file) path(name string) string {
	return filepath.Join(f.dir, name+recordSuffix)
}

// Register 写入自己的心跳记录，并按 interval 续期。
func (f *file) Register(ctx context.Context, self Node) (func(), error) {
	if self.Name == "" {
		return nil, errors.New("discovery: 节点名为空")
	}
	if strings.ContainsAny(self.Name, `/\:*?"<>|`) {
		// 节点名会变成文件名，带路径分隔符就能写到目录外面去。
		return nil, fmt.Errorf("discovery: 节点名 %q 含非法字符", self.Name)
	}

	var (
		stopOnce   sync.Once
		removeOnce sync.Once
		stop       = make(chan struct{})
	)
	remove := func() { removeOnce.Do(func() { _ = os.Remove(f.path(self.Name)) }) }

	f.write(self) // 先写一次：Watch 的首个视图里立刻有自己
	go func() {
		defer remove()
		t := time.NewTicker(f.interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				f.write(self)
			}
		}
	}()

	unregister := func() {
		stopOnce.Do(func() { close(stop) })
		remove()
	}
	return unregister, nil
}

// write 原子地写一条记录：先写临时文件再 rename。
//
// 不原子写的话，读的一方会扫到"写了一半的 JSON"，解析失败后
// 把那个节点判成死亡——每秒钟都可能发生一次，表现为成员随机闪断。
func (f *file) write(n Node) {
	n.TS = time.Now().UTC()
	b, err := json.Marshal(n)
	if err != nil {
		return
	}
	p := f.path(n.Name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		f.log.Warn("discovery: 写心跳失败", "node", n.Name, "err", err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		f.log.Warn("discovery: 心跳改名失败", "node", n.Name, "err", err)
	}
}

// Watch 轮询目录，成员有变化时推一份新视图。
func (f *file) Watch(ctx context.Context) <-chan View {
	ch := make(chan View, 4)
	go func() {
		defer close(ch)
		first, _ := f.scan(time.Now())
		f.views.Add(1)
		if !f.publish(first, ch, ctx) {
			return
		}
		last := first

		t := time.NewTicker(f.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				v, err := f.scan(time.Now())
				f.views.Add(1)
				if err != nil {
					f.log.Warn("discovery: 扫描失败", "dir", f.dir, "err", err)
					continue
				}
				if v.Equal(last) {
					continue
				}
				f.changes.Add(1)
				if !f.publish(v, ch, ctx) {
					return
				}
				last = v
			}
		}
	}()
	return ch
}

// publish 推一份视图，并把成员列表记进 Info。返回 false 表示 ctx 已结束。
func (f *file) publish(v View, ch chan<- View, ctx context.Context) bool {
	names := v.Names()
	f.members.Store(&names)
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// scan 读取目录，返回 TTL 内的存活成员。
func (f *file) scan(now time.Time) (View, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return View{At: now}, err
	}
	out := make([]Node, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), recordSuffix) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			// 记录刚好被主人删掉：不是错误，下一秒它就真的不在了。
			continue
		}
		var n Node
		if err := json.Unmarshal(b, &n); err != nil || n.Name == "" {
			f.log.Warn("discovery: 跳过损坏的记录", "file", e.Name())
			continue
		}
		if now.Sub(n.TS) > f.ttl {
			// 心跳过期：主人的进程多半已经不在了。这里不删文件——
			// 删别人的文件会让"到底谁还活着"取决于谁的时钟快，
			// 而且主人重启后还能续上原来那条记录。
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return View{Nodes: out, At: now}, nil
}

// Info 返回运行状态。
func (f *file) Info() Info {
	var members []string
	if p := f.members.Load(); p != nil {
		members = append([]string(nil), (*p)...)
	}
	return Info{
		Kind:     "file",
		Detail:   f.dir,
		Interval: f.interval.String(),
		TTL:      f.ttl.String(),
		Views:    f.views.Load(),
		Changes:  f.changes.Load(),
		Members:  members,
	}
}

// Close 不做事：后台协程都挂在 Watch/Register 传入的 ctx 上，
// 由调用方取消 ctx 收尾。
func (f *file) Close() error { return nil }

const (
	recordSuffix    = ".node"
	defaultInterval = time.Second
	defaultTTL      = 3 * time.Second
)
