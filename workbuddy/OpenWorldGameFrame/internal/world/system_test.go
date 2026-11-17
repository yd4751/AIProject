package world

import (
	"strings"
	"testing"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/protocol"
)

// TestSystemStageOrder 验证四个阶段严格按 command -> input -> logic -> replicate 执行。
//
// 阶段顺序是玩法正确性的前提（AI 写的意图必须下一帧才结算），
// 所以这里用"记录调用顺序"而不是"看最终状态"来断言——
// 最终状态往往对顺序不敏感，测不出来。
func TestSystemStageOrder(t *testing.T) {
	s := newTestScene()
	var order []string
	rec := func(name string) func(*Ctx) {
		return func(*Ctx) { order = append(order, name) }
	}

	if err := s.AddSystemFunc("c1", StageCommand, rec("command")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSystemFunc("l1", StageLogic, rec("logic")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSystemFunc("r1", StageReplicate, rec("replicate")); err != nil {
		t.Fatal(err)
	}

	s.TickOnce()

	want := []string{"command", "logic", "replicate"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("阶段顺序 = %v，期望 %v", order, want)
	}
}

// TestBuiltinMoveSystemRunsInInputStage 保证位移在玩法逻辑之前结算。
//
// 内置的 world.move 必须排在 StageInput，否则玩法系统本帧写的意图
// 会被同帧结算，行为就变成"AI 决策立即生效"，和文档承诺的
// "决策下一帧生效"不一致，回放时也对不上。
func TestBuiltinMoveSystemRunsInInputStage(t *testing.T) {
	s := newTestScene()
	input := s.systems[StageInput]
	if len(input) != 1 || input[0].Name != "world.move" {
		t.Fatalf("StageInput 应有且仅有内置的 world.move，实际 %d 个", len(input))
	}
}

func TestAddSystemRejectsDuplicateAndBadStage(t *testing.T) {
	s := newTestScene()
	noop := func(*Ctx) {}
	if err := s.AddSystemFunc("dup", StageLogic, noop); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSystemFunc("dup", StageReplicate, noop); err == nil {
		t.Fatal("重名 System 应被拒绝")
	}
	if err := s.AddSystemFunc("bad", stageCount, noop); err == nil {
		t.Fatal("非法阶段应被拒绝")
	}
	if err := s.AddSystem(&System{Name: "nofn"}); err == nil {
		t.Fatal("没有 Tick 的 System 应被拒绝")
	}
}

func TestAddSystemAfterStartFails(t *testing.T) {
	s := newTestScene()
	s.started.Store(true)
	if err := s.AddSystemFunc("late", StageLogic, func(*Ctx) {}); err == nil {
		t.Fatal("Start 之后注册 System 应被拒绝（注册表必须运行期只读）")
	}
}

// TestSystemPanicIsIsolated 验证三点：
//  1. 一个 System panic 不会中断本帧后续 System
//  2. 连续 panic 达到上限后被停用，不再刷日志、不再占用每帧时间
//  3. panic 计数能在 Stats 里看到（排障时第一个要看的指标）
func TestSystemPanicIsIsolated(t *testing.T) {
	s := newTestScene()
	ran := 0
	if err := s.AddSystemFunc("boom", StageLogic, func(*Ctx) { panic("boom") }); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSystemFunc("after", StageLogic, func(*Ctx) { ran++ }); err != nil {
		t.Fatal(err)
	}

	s.TickOnce()
	if ran != 1 {
		t.Fatalf("panic 之后的 System 仍应执行，实际执行 %d 次", ran)
	}

	for i := 0; i < maxSystemFailures; i++ {
		s.TickOnce()
	}
	if got := s.sysPanics.Load(); got != maxSystemFailures {
		t.Fatalf("panic 计数应为 %d，实际 %d", maxSystemFailures, got)
	}
	if !s.systems[StageLogic][0].disabled.Load() {
		t.Fatal("连续 panic 达到上限后应停用该 System")
	}

	// 停用之后不该再 panic，也不该再消耗帧时间。
	s.TickOnce()
	if got := s.sysPanics.Load(); got != maxSystemFailures {
		t.Fatalf("停用后不应继续 panic，计数 = %d", got)
	}
	if ran != maxSystemFailures+2 {
		t.Fatalf("其他 System 应继续运行，实际 %d 次", ran)
	}
}

// TestEntityComponentsClearedOnLeave 验证实体离场时组件被回收。
//
// 不回收的后果不是"立刻出错"，而是运行几小时后内存里堆满
// 已离场玩家的属性/Buff，并且 GC 还得扫它们。
func TestEntityComponentsClearedOnLeave(t *testing.T) {
	s := newTestScene()
	healthy := ecs.Define[int32]("test.health")

	s.OnEntityEnter(func(ctx *Ctx, e *Entity) {
		ecs.Get(ctx.Store, healthy).Add(e.ID(), 100)
	})

	s.onEnter(&msgEnter{id: 1, player: "a", client: &mockClient{}, hasPos: true})
	pool := ecs.Get(s.Store(), healthy)
	if !pool.Has(1) {
		t.Fatal("进场钩子应给实体挂上组件")
	}

	s.onLeave(&msgLeave{entityID: 1})
	if pool.Has(1) {
		t.Fatal("离场后组件应被销毁")
	}
}

// TestLeaveHookCanStillReadComponents 离场回调必须能看到组件（存档/掉落需要），
// 但拿不到实体本身——实体已经从场景表里摘掉了。
func TestLeaveHookCanStillReadComponents(t *testing.T) {
	s := newTestScene()
	hp := ecs.Define[int32]("test.hp2")
	s.OnEntityEnter(func(ctx *Ctx, e *Entity) { ecs.Get(ctx.Store, hp).Add(e.ID(), 42) })

	var seen int32 = -1
	s.OnEntityLeave(func(ctx *Ctx, e *Entity) {
		if v := ecs.Get(ctx.Store, hp).Get(e.ID()); v != nil {
			seen = *v
		}
	})

	s.onEnter(&msgEnter{id: 2, player: "b", client: &mockClient{}, hasPos: true})
	s.onLeave(&msgLeave{entityID: 2})

	if seen != 42 {
		t.Fatalf("离场回调应能读到组件值 42，实际 %d", seen)
	}
}

// TestSpawnedMobIsNotReaped 怪物没有心跳，不能被心跳超时逻辑清掉。
func TestSpawnedMobIsNotReaped(t *testing.T) {
	s := newTestScene()
	mob := s.Spawn(KindMob, "mob", 10, 10)
	mob.lastHeartbeat = time.Now().Add(-time.Hour) // 让它看起来"超时"

	s.doTick()

	if s.Entity(mob.ID()) == nil {
		t.Fatal("NPC 不应被心跳超时逻辑回收")
	}
}

// TestSpawnNotifiesNearbyPlayers 生成 NPC 时，附近玩家必须收到 EntityEnter，
// 否则客户端不知道有这只怪，血条和模型都不会出现。
func TestSpawnNotifiesNearbyPlayers(t *testing.T) {
	s := newTestScene()
	c := &mockClient{}
	// 玩家 ID 用 100：Spawn 走 nextID（从 0 开始自增），测试里手写 1 会和它撞号，
	// 撞号之后 BroadcastNear 会把玩家当成"自己"跳过。
	s.onEnter(&msgEnter{id: 100, player: "a", client: c, hasPos: true, x: 0, z: 0})
	c.reset()

	s.Spawn(KindMob, "mob", 3, 3)

	if got := c.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("附近玩家应收到 1 条 EntityEnter，实际 %d", got)
	}
}

// TestMobMoveBroadcastsOnlyToPlayers 怪物移动要发给附近玩家，
// 但不能发给另一只怪（NPC 的出口是空实现，发了也是白跑）。
func TestMobMoveBroadcastsOnlyToPlayers(t *testing.T) {
	s := newTestScene()
	c := &mockClient{}
	s.onEnter(&msgEnter{id: 100, player: "a", client: c, hasPos: true, x: 0, z: 0})
	mob := s.Spawn(KindMob, "mob", 1, 1)
	mob.lastMoveAt = time.Now().Add(-time.Second)
	c.reset()

	s.applyMove(mob.ID(), 1.5, 0, 1.5, time.Now())

	if got := c.count(protocol.MsgS2CEntityState); got != 1 {
		t.Fatalf("玩家应收到怪物移动包，实际 %d", got)
	}
	if got := s.broadcasts.Load(); got != 1 {
		t.Fatalf("广播计数应只统计真正发给玩家的包，实际 %d", got)
	}
}

// TestKickedPlayerBroadcastsLeave 修的是 M2 的一个真实缺陷：
// 心跳超时踢人时只从索引里删了实体，没通知周围玩家，
// 结果是别人眼里那个"挂机的人"一直站着不动。
func TestKickedPlayerBroadcastsLeave(t *testing.T) {
	s := newTestScene()
	watcher, idle := &mockClient{}, &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "watcher", client: watcher, hasPos: true, x: 0, z: 0})
	s.onEnter(&msgEnter{id: 2, player: "idle", client: idle, hasPos: true, x: 1, z: 1})
	watcher.reset()

	s.entities[2].lastHeartbeat = time.Now().Add(-time.Minute)
	s.doTick()

	if !idle.kicked {
		t.Fatal("超时玩家应被踢下线")
	}
	if n := watcher.count(0x000A); n != 1 {
		t.Fatalf("周围玩家应收到 1 条 EntityLeave，实际 %d", n)
	}
}

// TestStatsExposeSystemsAndComponents 保证 /admin/state 能看出"谁吃掉了帧"。
func TestStatsExposeSystemsAndComponents(t *testing.T) {
	s := newTestScene()
	comp := ecs.Define[int]("test.stat")
	s.OnEntityEnter(func(ctx *Ctx, e *Entity) { ecs.Get(ctx.Store, comp).Add(e.ID(), 1) })
	if err := s.AddSystemFunc("noop", StageLogic, func(*Ctx) {}); err != nil {
		t.Fatal(err)
	}

	s.onEnter(&msgEnter{id: 1, player: "a", client: &mockClient{}, hasPos: true})

	// 组件快照每秒刷新一次，手动把它推到"该刷新"的状态。
	s.lastStats = time.Now().Add(-2 * time.Second)
	s.doTick()

	st := s.Stats()
	if len(st.Systems) != 2 { // world.move + noop
		t.Fatalf("应有 2 个 System，实际 %d", len(st.Systems))
	}
	found := false
	for _, c := range st.Components {
		if c.Name == "test.stat" && c.Len == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("组件快照里应有 test.stat=1，实际 %+v", st.Components)
	}
}
