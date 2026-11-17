package control

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() Config {
	return Config{
		Binary: "server",
		LogDir: ".",
		Zones: []ZoneConfig{
			{ID: "s1", Name: "一区", Instances: 2, PortBase: 7101},
			{ID: "s2", Name: "二区", Instances: 1, PortBase: 7201},
		},
	}
}

// TestStateTransitions 锁定状态机里几条关键规则。
//
// 最重要的是"Running 不能直接跳到 Starting"：跳过去意味着旧进程还没退、
// 新进程已经去抢同一个端口，最后表现为控制台显示正常但玩家连不上。
func TestStateTransitions(t *testing.T) {
	cases := []struct {
		from, to State
		want     bool
	}{
		{StatePending, StateStarting, true},
		{StateStarting, StateRunning, true},
		{StateRunning, StateDraining, true},
		{StateDraining, StateStopped, true},
		{StateFailed, StateStarting, true}, // 异常不是终态，允许人工重启
		{StateRunning, StateStarting, false},
		{StateStopped, StateRunning, false},
		{StatePending, StateRunning, false},
		{StateDegraded, StateRunning, false},
	}
	for _, c := range cases {
		if got := c.from.CanTransition(c.to); got != c.want {
			t.Errorf("%s -> %s = %v, 期望 %v", c.from, c.to, got, c.want)
		}
	}
}

// TestAggregateStatePriority 验证区服聚合的判断优先级：
// 异常优先于一切，其次才是"正在做的事"。
func TestAggregateStatePriority(t *testing.T) {
	cases := []struct {
		name                               string
		total, running, starting, draining int
		failed                             int
		want                               State
	}{
		{"两个都在跑", 2, 2, 0, 0, 0, StateRunning},
		{"一个异常", 2, 1, 0, 0, 1, StateFailed},
		{"正在开服", 2, 1, 1, 0, 0, StateStarting},
		{"正在排水", 2, 1, 0, 1, 0, StateDraining},
		{"全部停止", 2, 0, 0, 0, 0, StateStopped},
		{"部分可用", 3, 2, 0, 0, 0, StateDegraded},
		// 异常和排水同时存在时，异常优先：运营必须先看到坏消息。
		{"异常优先于排水", 2, 0, 0, 1, 1, StateFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aggregateState(c.total, c.running, c.starting, c.draining, c.failed); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

// TestOverviewAggregatesRunningInstances 验证区服视图只统计运行中实例的指标。
//
// 排水中的实例还有在线玩家，但它已经不是"容量"了；统计进去会让运营
// 误判这个区还能接人。
func TestOverviewAggregatesRunningInstances(t *testing.T) {
	reg := NewRegistry(testConfig(), testLogger())

	reg.Update("s1-w1", func(i *Instance) {
		i.State = StateRunning
		i.Snapshot = &Snapshot{Online: 100, Entities: 100, TickLagMs: 2}
	})
	reg.Update("s1-w2", func(i *Instance) {
		i.State = StateRunning
		i.Snapshot = &Snapshot{Online: 50, Entities: 60, TickLagMs: 7}
	})
	reg.Update("s2-w1", func(i *Instance) {
		i.State = StateDraining
		i.Snapshot = &Snapshot{Online: 999, Entities: 999, TickLagMs: 99}
	})

	zones, instances := reg.Overview()
	if len(instances) != 3 {
		t.Fatalf("实例数 = %d, 期望 3", len(instances))
	}

	var s1, s2 ZoneView
	for _, z := range zones {
		switch z.ID {
		case "s1":
			s1 = z
		case "s2":
			s2 = z
		}
	}

	if s1.State != StateRunning || s1.Running != 2 || s1.Online != 150 || s1.Entities != 160 {
		t.Fatalf("s1 聚合错误: %+v", s1)
	}
	if s1.MaxTickLagMs != 7 {
		t.Fatalf("s1 最大滞后应为 7，实际 %v", s1.MaxTickLagMs)
	}
	if s2.State != StateDraining || s2.Online != 0 {
		t.Fatalf("排水中的区服不该把在线数算进容量: %+v", s2)
	}
}

// TestMergeRatio 验证移动合并率的计算边界。
func TestMergeRatio(t *testing.T) {
	if got := (*Snapshot)(nil).MergeRatio(); got != 0 {
		t.Fatalf("nil 快照应为 0，实际 %v", got)
	}
	if got := (&Snapshot{MoveIntents: 100, MoveApplied: 100}).MergeRatio(); got != 0 {
		t.Fatalf("无合并应为 0，实际 %v", got)
	}
	// 应用次数超过意图次数（重启后计数重置等异常）不能算出负数。
	if got := (&Snapshot{MoveIntents: 10, MoveApplied: 20}).MergeRatio(); got != 0 {
		t.Fatalf("异常计数应钳到 0，实际 %v", got)
	}
	if got := (&Snapshot{MoveIntents: 100, MoveApplied: 40}).MergeRatio(); got < 0.59 || got > 0.61 {
		t.Fatalf("合并率应约 0.6，实际 %v", got)
	}
}

// TestPersistRoundTripWithBOM 验证状态文件能跨重启读回，并且容忍 UTF-8 BOM。
//
// BOM 这条不是洁癖：Windows 上人工用编辑器改过状态文件就会带上 BOM，
// 而 encoding/json 遇到 BOM 直接报错。如果因此静默跳过接管，
// 现象就是"控制面重启后所有实例变成未启动"，非常难查。
func TestPersistRoundTripWithBOM(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	cfg.LogDir = dir

	reg := NewRegistry(cfg, testLogger())
	reg.Update("s1-w1", func(i *Instance) {
		i.State = StateRunning
		i.PID = 4321
	})
	reg.SetDesired("s1", true)

	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, "control-state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读状态文件: %v", err)
	}
	// 故意加上 BOM 再写回去。
	bomed := append([]byte{0xEF, 0xBB, 0xBF}, raw...)
	if err := os.WriteFile(path, bomed, 0o644); err != nil {
		t.Fatalf("写回 BOM: %v", err)
	}

	// 新控制面启动：实例已不在（测试环境没有真进程），应记为 lost 而不是忽略文件。
	fresh := NewRegistry(cfg, testLogger())
	_, lost := fresh.Load()
	if lost != 1 {
		t.Fatalf("应识别出 1 个失联实例，实际 %d（BOM 可能没被处理）", lost)
	}

	inst, _ := fresh.Get("s1-w1")
	if inst.State != StateFailed {
		t.Fatalf("失联实例应为 Failed，实际 %s", inst.State)
	}
	zones, _ := fresh.Overview()
	if len(zones) != 2 || !zones[0].DesiredRunning {
		t.Fatalf("运维意图没有恢复: %+v", zones)
	}
}

// TestInstancesAllocatedPerConfig 验证实例命名与端口分配约定。
// 约定一旦漂移，控制台按钮指向的实例和实际监听端口就会对不上。
func TestInstancesAllocatedPerConfig(t *testing.T) {
	reg := NewRegistry(testConfig(), testLogger())

	insts := reg.ZoneInstances("s1")
	if len(insts) != 2 {
		t.Fatalf("s1 应有 2 个实例，实际 %d", len(insts))
	}
	if insts[0].ID != "s1-w1" || insts[0].GateAddr != ":7101" || insts[0].AdminAddr != ":8101" {
		t.Fatalf("s1-w1 端口分配错误: %+v", insts[0])
	}
	if insts[1].ID != "s1-w2" || insts[1].GateAddr != ":7102" || insts[1].AdminAddr != ":8102" {
		t.Fatalf("s1-w2 端口分配错误: %+v", insts[1])
	}
	if _, ok := reg.Get("nope"); ok {
		t.Fatal("不存在的实例不该返回 ok")
	}
}
