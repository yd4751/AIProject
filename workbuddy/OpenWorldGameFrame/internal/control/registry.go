package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// zone 是一个区服的内部表示。
type zone struct {
	cfg            ZoneConfig
	instances      []*Instance
	desiredRunning bool
}

// Registry 保存全部区服与实例的实时状态。
//
// 所有可变状态都在这里，并且只有一个锁。控制面前的并发量很低
// （人点按钮 + 2 秒一次的探针），不需要为它做分片锁，
// 一把锁换来的是"不可能出现两个 goroutine 各改一半"的可推理性质。
type Registry struct {
	mu       sync.RWMutex
	cfg      Config
	zoneList []*zone
	zonesBy  map[string]*zone
	instBy   map[string]*Instance

	client *http.Client
	log    Logger
}

// Logger 是控制面用到的日志接口，避免直接依赖 slog 的字段拼装细节。
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewRegistry 根据配置构建区服与实例清单。
func NewRegistry(cfg Config, log Logger) *Registry {
	r := &Registry{
		cfg:     cfg,
		zonesBy: make(map[string]*zone),
		instBy:  make(map[string]*Instance),
		client:  &http.Client{Timeout: 1500 * time.Millisecond},
		log:     log,
	}

	for _, zc := range cfg.Zones {
		z := &zone{cfg: zc}
		n := zc.Instances
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			inst := &Instance{
				ID:        fmt.Sprintf("%s-w%d", zc.ID, i+1),
				ZoneID:    zc.ID,
				ZoneName:  zc.Name,
				Role:      "world",
				Index:     i,
				GateAddr:  fmt.Sprintf(":%d", zc.PortBase+i),
				AdminAddr: fmt.Sprintf(":%d", zc.PortBase+1000+i),
				State:     StatePending,
				Since:     time.Now(),
			}
			z.instances = append(z.instances, inst)
			r.instBy[inst.ID] = inst
		}
		r.zoneList = append(r.zoneList, z)
		r.zonesBy[zc.ID] = z
	}
	return r
}

// Addr 返回实例的探测地址（补上回环主机名）。
func (i Instance) ProbeURL(path string) string {
	return "http://127.0.0.1" + i.AdminAddr + path
}

// ZoneExists 判断区服是否存在。
func (r *Registry) ZoneExists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.zonesBy[id]
	return ok
}

// Update 在锁内修改一个实例。
func (r *Registry) Update(id string, fn func(*Instance)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instBy[id]
	if !ok {
		return false
	}
	fn(inst)
	return true
}

// Get 返回实例副本。
func (r *Registry) Get(id string) (Instance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inst, ok := r.instBy[id]
	if !ok {
		return Instance{}, false
	}
	return *inst, true
}

// SetDesired 记录运维意图。
func (r *Registry) SetDesired(zoneID string, v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if z, ok := r.zonesBy[zoneID]; ok {
		z.desiredRunning = v
	}
}

// ZoneInstances 按启动顺序返回区服内的实例副本。
//
// 排序是编排的基础：开服必须按 login → data → world → gate 的顺序，
// 关服必须反过来。现在只有 world 一种角色，等 M3 拆出独立 Gate 进程后，
// 这里加上角色权重即可，编排器不用改。
func (r *Registry) ZoneInstances(zoneID string) []Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	z, ok := r.zonesBy[zoneID]
	if !ok {
		return nil
	}
	out := make([]Instance, 0, len(z.instances))
	for _, p := range z.instances {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// zoneIDs 返回全部区服 ID，顺序与配置一致。
func (r *Registry) zoneIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.zoneList))
	for _, z := range r.zoneList {
		out = append(out, z.cfg.ID)
	}
	return out
}

// Overview 返回控制台需要的完整视图：区服卡片 + 实例列表。
func (r *Registry) Overview() ([]ZoneView, []InstanceView) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	zones := make([]ZoneView, 0, len(r.zoneList))
	instances := make([]InstanceView, 0, len(r.instBy))
	now := time.Now()

	for _, z := range r.zoneList {
		zv := ZoneView{
			ID:             z.cfg.ID,
			Name:           z.cfg.Name,
			Instances:      len(z.instances),
			DesiredRunning: z.desiredRunning,
		}
		running, failed, starting, draining := 0, 0, 0, 0
		for _, inst := range z.instances {
			switch inst.State {
			case StateRunning:
				running++
			case StateFailed:
				failed++
			case StateStarting:
				starting++
			case StateDraining:
				draining++
			}
			if inst.State == StateRunning && inst.Snapshot != nil {
				zv.Online += inst.Snapshot.Online
				zv.Entities += inst.Snapshot.Entities
				if inst.Snapshot.TickLagMs > zv.MaxTickLagMs {
					zv.MaxTickLagMs = inst.Snapshot.TickLagMs
				}
			}
			instances = append(instances, buildInstanceView(inst, now))
		}
		zv.Running = running
		zv.State = aggregateState(len(z.instances), running, starting, draining, failed)
		zv.StateLabel = zv.State.Label()
		zones = append(zones, zv)
	}

	sort.Slice(instances, func(i, j int) bool { return instances[i].ID < instances[j].ID })
	return zones, instances
}

// aggregateState 把实例状态汇总成区服状态。
//
// 判断优先级是刻意排的：异常优先于一切（哪怕只有一个实例异常，
// 运营也必须立刻看到"这个区不正常"），其次才是进行中的状态。
func aggregateState(total, running, starting, draining, failed int) State {
	switch {
	case failed > 0:
		return StateFailed
	case draining > 0:
		return StateDraining
	case starting > 0:
		return StateStarting
	case running == total && total > 0:
		return StateRunning
	case running == 0:
		return StateStopped
	default:
		return StateDegraded
	}
}

func buildInstanceView(inst *Instance, now time.Time) InstanceView {
	v := InstanceView{
		Instance:   *inst,
		StateLabel: inst.State.Label(),
	}
	if inst.Since.After(time.Time{}) && inst.State.Active() {
		v.UptimeStr = now.Sub(inst.Since).Round(time.Second).String()
	}
	v.MergeRatio = inst.Snapshot.MergeRatio()
	return v
}

// RunProbe 周期性地探测所有活跃实例。
//
// 探针只负责"发现事实"，不负责"修复"：实例从 Running 掉到 Failed 后，
// 是否要自动拉起是运维策略问题，得由人决定（自动拉起会把崩溃循环藏起来）。
//
// 生产环境这里应该换成 etcd lease：进程被 OOM kill 时 HTTP 不通，
// 网络分区时 HTTP 也不通，但这两件事的处理方式完全不同。
// lease 到期是不可伪造的失败信号，HTTP 探针做不到这一点。
func (r *Registry) RunProbe(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.probeAll()
		}
	}
}

func (r *Registry) probeAll() {
	r.mu.RLock()
	targets := make([]Instance, 0, len(r.instBy))
	for _, inst := range r.instBy {
		if inst.State.Active() {
			targets = append(targets, *inst)
		}
	}
	r.mu.RUnlock()

	for _, inst := range targets {
		r.probeOne(inst)
	}
}

func (r *Registry) probeOne(inst Instance) {
	status, err := r.getStatus(inst.ProbeURL("/readyz"))
	if err != nil {
		// 只有明确在跑的实例才降级为异常。Starting / Draining 期间连不上是
		// 正常现象，交给编排器按超时处理，探针不要抢着判死刑。
		if inst.State == StateRunning {
			r.Update(inst.ID, func(i *Instance) {
				i.State = StateFailed
				i.Since = time.Now()
				i.LastError = "就绪探针失败: " + err.Error()
			})
			r.log.Warn("实例探针失败", "instance", inst.ID, "err", err)
		}
		return
	}

	snap, serr := r.getSnapshot(inst)
	now := time.Now()

	r.Update(inst.ID, func(i *Instance) {
		i.ProbeAt = now
		if serr != nil {
			return
		}
		i.Snapshot = snap
		if i.State == StateRunning && status == http.StatusOK && i.LastError != "" {
			i.LastError = ""
		}
	})
}

func (r *Registry) getStatus(url string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// adminState 是 /admin/state 里我们关心的那部分，字段必须和服务端保持一致。
type adminState struct {
	App struct {
		UptimeSeconds int64 `json:"uptimeSeconds"`
	} `json:"app"`
	Runtime struct {
		Goroutines  int     `json:"goroutines"`
		HeapAllocMB float64 `json:"heapAllocMB"`
	} `json:"runtime"`
	Gate struct {
		Online   int64 `json:"online"`
		Draining bool  `json:"draining"`
	} `json:"gate"`
	World struct {
		Scenes []struct {
			Entities    int64 `json:"entities"`
			TickLagNs   int64 `json:"tickLagNs"`
			Broadcasts  int64 `json:"broadcasts"`
			MoveIntents int64 `json:"moveIntents"`
			MoveApplied int64 `json:"moveApplied"`
		} `json:"scenes"`
	} `json:"world"`
}

func (r *Registry) getSnapshot(inst Instance) (*Snapshot, error) {
	resp, err := r.client.Get(inst.ProbeURL("/admin/state"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("state status %d", resp.StatusCode)
	}

	var st adminState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}

	snap := &Snapshot{
		UptimeSeconds: st.App.UptimeSeconds,
		Online:        st.Gate.Online,
		HeapMB:        st.Runtime.HeapAllocMB,
		Goroutines:    st.Runtime.Goroutines,
		Draining:      st.Gate.Draining,
	}
	for _, sc := range st.World.Scenes {
		snap.Entities += sc.Entities
		snap.Broadcasts += sc.Broadcasts
		snap.MoveIntents += sc.MoveIntents
		snap.MoveApplied += sc.MoveApplied
		if lag := float64(sc.TickLagNs) / 1e6; lag > snap.TickLagMs {
			snap.TickLagMs = lag
		}
	}
	return snap, nil
}

// post 向实例的运维接口发一个无体 POST。
func (r *Registry) post(url string) error {
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	return nil
}
