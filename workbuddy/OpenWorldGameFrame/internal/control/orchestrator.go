package control

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound 表示区服或实例不存在。
var ErrNotFound = errors.New("not found")

// Orchestrator 编排开服与关服。
//
// 核心约定：
//   - 开服按依赖顺序，任何一步失败都要回滚已经起来的实例（Saga）
//   - 关服按依赖逆序，每个实例走完"摘流 → 通知 → 排水 → 退出"四步
//   - 所有操作异步执行，接口只返回 taskId
type Orchestrator struct {
	reg          *Registry
	sup          *Supervisor
	log          Logger
	grace        time.Duration
	readyTimeout time.Duration

	mu       sync.Mutex
	tasks    map[string]*Task
	taskList []string          // 按创建顺序
	inflight map[string]string // "zoneID|op" -> taskID
	seq      atomic.Int64
}

// NewOrchestrator 创建编排器。
func NewOrchestrator(cfg Config, reg *Registry, sup *Supervisor, log Logger) *Orchestrator {
	grace := time.Duration(cfg.GraceSeconds) * time.Second
	if grace <= 0 {
		grace = 10 * time.Second
	}
	ready := time.Duration(cfg.ReadyTimeoutSeconds) * time.Second
	if ready <= 0 {
		ready = 20 * time.Second
	}
	return &Orchestrator{
		reg:          reg,
		sup:          sup,
		log:          log,
		grace:        grace,
		readyTimeout: ready,
		tasks:        make(map[string]*Task),
		inflight:     make(map[string]string),
	}
}

// StartZone 异步开启一个区服。
//
// 幂等：同一个区已经在开服过程中时，直接返回那个任务的 ID，
// 而不是再起一次。否则前端连点两下就会有两个协程抢同一个端口。
func (o *Orchestrator) StartZone(zoneID string) (Task, error) {
	if !o.reg.ZoneExists(zoneID) {
		return Task{}, ErrNotFound
	}
	if t, ok := o.inflightTask(zoneID, "start"); ok {
		return t, nil
	}
	insts := o.reg.ZoneInstances(zoneID)
	o.reg.SetDesired(zoneID, true)
	t := o.newTask(zoneID, "start")
	go o.runStart(t, insts)
	return t.clone(), nil
}

// StopZone 异步关闭一个区服。grace 为 0 时使用配置里的默认倒计时。
func (o *Orchestrator) StopZone(zoneID string, grace time.Duration) (Task, error) {
	if !o.reg.ZoneExists(zoneID) {
		return Task{}, ErrNotFound
	}
	if t, ok := o.inflightTask(zoneID, "stop"); ok {
		return t, nil
	}
	if grace <= 0 {
		grace = o.grace
	}
	insts := o.reg.ZoneInstances(zoneID)
	o.reg.SetDesired(zoneID, false)
	t := o.newTask(zoneID, "stop")
	go o.runStop(t, insts, grace)
	return t.clone(), nil
}

// StartInstance 单独拉起一个实例（排障用）。
func (o *Orchestrator) StartInstance(id string) (Task, error) {
	inst, ok := o.reg.Get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	if t, ok := o.inflightTask(inst.ZoneID, "start"); ok {
		return t, nil
	}
	t := o.newTask(inst.ZoneID, "start")
	go o.runStart(t, []Instance{inst})
	return t.clone(), nil
}

// StopInstance 单独关闭一个实例。
func (o *Orchestrator) StopInstance(id string, grace time.Duration) (Task, error) {
	inst, ok := o.reg.Get(id)
	if !ok {
		return Task{}, ErrNotFound
	}
	if t, ok := o.inflightTask(inst.ZoneID, "stop"); ok {
		return t, nil
	}
	if grace <= 0 {
		grace = o.grace
	}
	t := o.newTask(inst.ZoneID, "stop")
	go o.runStop(t, []Instance{inst}, grace)
	return t.clone(), nil
}

// Task 返回任务副本。
func (o *Orchestrator) Task(id string) (Task, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t, ok := o.tasks[id]
	if !ok {
		return Task{}, false
	}
	return t.clone(), true
}

// Tasks 返回最近的任务，最新的在前。
func (o *Orchestrator) Tasks(limit int) []Task {
	o.mu.Lock()
	defer o.mu.Unlock()
	if limit <= 0 || limit > len(o.taskList) {
		limit = len(o.taskList)
	}
	out := make([]Task, 0, limit)
	for i := len(o.taskList) - 1; i >= 0 && len(out) < limit; i-- {
		if t, ok := o.tasks[o.taskList[i]]; ok {
			out = append(out, t.clone())
		}
	}
	return out
}

// ---- 开服 ----

func (o *Orchestrator) runStart(t *Task, insts []Instance) {
	started := make([]Instance, 0, len(insts))

	rollback := func(cause error) {
		if len(started) == 0 {
			o.finish(t, "failed", cause.Error())
			return
		}
		o.appendStep(t, "回滚", "running", "逆序停止本次已启动的实例")
		for i := len(started) - 1; i >= 0; i-- {
			o.quickStop(started[i])
			o.appendStep(t, "回滚", "warn", started[i].ID+" 已停止")
		}
		o.finish(t, "failed", cause.Error())
	}

	for _, inst := range insts {
		o.appendStep(t, "启动 "+inst.ID, "running", inst.GateAddr+" / "+inst.AdminAddr)
		o.reg.Update(inst.ID, func(i *Instance) {
			i.State = StateStarting
			i.Since = time.Now()
			i.LastError = ""
			i.ExitCode = 0
		})

		pid, err := o.sup.Start(inst)
		if err != nil {
			o.reg.Update(inst.ID, func(i *Instance) {
				i.State = StateFailed
				i.LastError = err.Error()
				i.Since = time.Now()
			})
			o.appendStep(t, "启动 "+inst.ID, "failed", err.Error())
			rollback(fmt.Errorf("%s 启动失败: %w", inst.ID, err))
			return
		}
		o.reg.Update(inst.ID, func(i *Instance) { i.PID = pid })

		if err := o.waitReady(inst); err != nil {
			o.sup.Kill(inst.ID)
			o.reg.Update(inst.ID, func(i *Instance) {
				i.State = StateFailed
				i.LastError = err.Error()
				i.Since = time.Now()
			})
			o.appendStep(t, "启动 "+inst.ID, "failed", err.Error())
			rollback(fmt.Errorf("%s 未就绪: %w", inst.ID, err))
			return
		}

		o.reg.Update(inst.ID, func(i *Instance) {
			i.State = StateRunning
			i.Since = time.Now()
			i.LastError = ""
		})
		o.appendStep(t, "启动 "+inst.ID, "ok", fmt.Sprintf("pid %d 已就绪", pid))
		started = append(started, inst)
	}

	o.finish(t, "done", fmt.Sprintf("%d 个实例全部就绪", len(started)))
}

func (o *Orchestrator) waitReady(inst Instance) error {
	deadline := time.Now().Add(o.readyTimeout)
	for time.Now().Before(deadline) {
		if !o.sup.Managed(inst.ID) {
			return errors.New("进程在就绪前退出")
		}
		if code, err := o.reg.getStatus(inst.ProbeURL("/readyz")); err == nil && code == http.StatusOK {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 /readyz 超时（%s）", o.readyTimeout)
}

// ---- 关服 ----

func (o *Orchestrator) runStop(t *Task, insts []Instance, grace time.Duration) {
	graceSec := int(grace / time.Second)

	// 逆序关闭：先摘入口，再停后端。
	// 反过来会让玩家在"网关还在、世界已经没了"的状态下被卡死。
	for i := len(insts) - 1; i >= 0; i-- {
		inst := insts[i]
		if inst.State == StateStopped || inst.State == StatePending {
			o.appendStep(t, "跳过 "+inst.ID, "ok", "本来就没在运行")
			continue
		}

		// 第一步：摘流。让实例拒绝新登录，但从路由里摘除是控制面的事，
		// 这里通过 drain 接口一次性完成。
		o.appendStep(t, "摘流 "+inst.ID, "running", "")
		o.reg.Update(inst.ID, func(x *Instance) {
			x.State = StateDraining
			x.Since = time.Now()
		})
		drainURL := inst.ProbeURL("/admin/drain?grace=" + strconv.Itoa(graceSec))
		if err := o.reg.post(drainURL); err != nil {
			// 排水接口不可达通常意味着进程已经挂了，继续走强杀路径。
			o.appendStep(t, "摘流 "+inst.ID, "warn", "排水接口不可达: "+err.Error())
		} else {
			o.appendStep(t, "摘流 "+inst.ID, "ok", fmt.Sprintf("已停止接客，倒计时 %ds", graceSec))
		}

		// 第二步：排水。等倒计时走完，让玩家自己下线。
		// 这段时间里实例仍然在处理在线玩家的请求，不能提前 kill。
		if grace > 0 {
			wait := grace + 2*time.Second
			o.appendStep(t, "排水 "+inst.ID, "running", fmt.Sprintf("等待 %s", wait.Round(time.Second)))
			time.Sleep(wait)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
		o.appendStep(t, "排水 "+inst.ID, "ok", "在线玩家已下线")

		// 第三步：退出。优先让进程自己走收尾流程（存档、关监听），
		// 只有超时才升级为强杀。
		o.appendStep(t, "退出 "+inst.ID, "running", "")
		_ = o.reg.post(inst.ProbeURL("/admin/shutdown"))

		code := 0
		if !o.sup.Managed(inst.ID) {
			// 控制面重启后接管的实例没有本地进程句柄，杀不了也 wait 不了，
			// 只能靠探针确认它真的不再应答了。
			if o.waitGone(inst, 15*time.Second) {
				o.appendStep(t, "退出 "+inst.ID, "ok", "已停止应答（接管实例）")
			} else {
				o.appendStep(t, "退出 "+inst.ID, "warn", "仍在应答，需要人工检查")
			}
			code = -1
		} else if c, ok := o.sup.Wait(inst.ID, 15*time.Second); ok {
			code = c
			o.appendStep(t, "退出 "+inst.ID, "ok", fmt.Sprintf("exit=%d", c))
		} else if o.sup.Kill(inst.ID) {
			o.sup.Wait(inst.ID, 5*time.Second)
			o.appendStep(t, "退出 "+inst.ID, "warn", "优雅退出超时，已强制结束")
			code = -1
		} else {
			o.appendStep(t, "退出 "+inst.ID, "warn", "进程已不在托管表中")
			code = -1
		}

		o.reg.Update(inst.ID, func(x *Instance) {
			x.State = StateStopped
			x.Since = time.Now()
			x.ExitCode = code
			x.PID = 0
			x.Snapshot = nil
		})
	}

	o.finish(t, "done", "全部实例已停止")
}

// waitGone 轮询运维接口，直到实例不再应答（或超时）。
//
// 用于没有本地进程句柄的接管实例：我们发不了信号，也不知道 PID 是否还在，
// 唯一能确认"它真的走了"的证据就是端口不再应答。
func (o *Orchestrator) waitGone(inst Instance, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := o.reg.getStatus(inst.ProbeURL("/readyz")); err != nil {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// quickStop 是回滚用的快速停止：不给倒计时，直接排水 + 退出 + 超时强杀。
//
// 回滚场景下这些实例还没来得及接客（或者刚接客几秒），
// 没有玩家需要通知，等 300 秒倒计时反而会把故障恢复时间拉长。
func (o *Orchestrator) quickStop(inst Instance) {
	o.reg.Update(inst.ID, func(x *Instance) { x.State = StateDraining })
	_ = o.reg.post(inst.ProbeURL("/admin/drain?grace=0"))
	time.Sleep(300 * time.Millisecond)
	_ = o.reg.post(inst.ProbeURL("/admin/shutdown"))

	if o.sup.Managed(inst.ID) {
		if _, ok := o.sup.Wait(inst.ID, 8*time.Second); !ok {
			o.sup.Kill(inst.ID)
			o.sup.Wait(inst.ID, 3*time.Second)
		}
	} else {
		o.waitGone(inst, 8*time.Second)
	}
	o.reg.Update(inst.ID, func(x *Instance) {
		x.State = StateStopped
		x.Since = time.Now()
		x.PID = 0
		x.Snapshot = nil
	})
}

// ---- 任务管理 ----

func (o *Orchestrator) newTask(zoneID, op string) *Task {
	id := fmt.Sprintf("%s-%s-%d", op, zoneID, o.seq.Add(1))
	t := &Task{
		ID:        id,
		ZoneID:    zoneID,
		Op:        op,
		State:     "running",
		StartedAt: time.Now(),
	}
	o.mu.Lock()
	o.tasks[id] = t
	o.taskList = append(o.taskList, id)
	o.inflight[zoneID+"|"+op] = id
	// 只保留最近 200 条，控制台不需要更久的历史。
	if len(o.taskList) > 200 {
		drop := o.taskList[0]
		o.taskList = o.taskList[1:]
		delete(o.tasks, drop)
	}
	o.mu.Unlock()
	return t
}

func (o *Orchestrator) appendStep(t *Task, name, state, detail string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t.Steps = append(t.Steps, TaskStep{Name: name, State: state, Detail: detail, At: time.Now()})
	o.log.Info("编排步骤", "task", t.ID, "step", name, "state", state, "detail", detail)
}

func (o *Orchestrator) finish(t *Task, state, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t.State = state
	t.Message = msg
	t.EndedAt = time.Now()
	delete(o.inflight, t.ZoneID+"|"+t.Op)
	if state == "failed" {
		o.log.Error("编排任务失败", "task", t.ID, "msg", msg)
	} else {
		o.log.Info("编排任务完成", "task", t.ID, "msg", msg)
	}
}

func (o *Orchestrator) inflightTask(zoneID, op string) (Task, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, ok := o.inflight[zoneID+"|"+op]
	if !ok {
		return Task{}, false
	}
	t, ok := o.tasks[id]
	if !ok {
		return Task{}, false
	}
	return t.clone(), true
}

// clone 深拷贝任务，避免把内部正在被编排协程改写的结构直接交给 HTTP 序列化。
func (t *Task) clone() Task {
	out := *t
	out.Steps = make([]TaskStep, len(t.Steps))
	copy(out.Steps, t.Steps)
	return out
}
