package creature

import (
	"testing"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

// TestTransferRoundTripV3 验证 v3 属性（含 XP）往返编解码。
func TestTransferRoundTripV3(t *testing.T) {
	s := newScene(t)
	id := s.EnterAt("p1", &recClient{}, 0, 0)
	s.TickOnce() // 处理进场钩子，挂上默认属性

	attr := ecs.Get(s.Store(), CAttr)
	a := attr.Get(id)
	if a == nil {
		t.Fatal("进场后玩家应带属性组件")
	}
	a.HP = 800
	a.MaxHP = 1500
	a.Level = 7
	a.XP = 123
	attr.Add(id, *a)

	enc := codec{}
	ctx := &world.Ctx{Store: s.Store()}
	blob := enc.Encode(ctx, s.Entity(id))
	if blob == nil {
		t.Fatal("Encode 应返回非空 blob")
	}
	if len(blob) < 22 {
		t.Fatalf("v3 玩家属性至少 22 字节，实际 %d", len(blob))
	}

	// 覆盖成默认，再 Decode 回来。
	attr.Add(id, Attributes{HP: 1, MaxHP: 1, Level: 1})
	enc.Decode(ctx, s.Entity(id), blob)

	got := attr.Get(id)
	if got.HP != 800 || got.MaxHP != 1500 || got.Level != 7 || got.XP != 123 {
		t.Fatalf("属性未还原: hp=%d maxhp=%d level=%d xp=%d",
			got.HP, got.MaxHP, got.Level, got.XP)
	}
	if !got.Dirty {
		t.Fatal("Decode 后应标记 Dirty，保证属性同步")
	}
}

// TestTransferRoundTripV3WithBrain 验证 v3 带 Brain 段的编解码偏移正确。
func TestTransferRoundTripV3WithBrain(t *testing.T) {
	s := newScene(t)
	e := Spawn(s, "mob", 5, 5, 200)

	attr := ecs.Get(s.Store(), CAttr)
	a := attr.Get(e.ID())
	a.Level = 3
	a.XP = 50
	attr.Add(e.ID(), *a)

	brain := ecs.Get(s.Store(), CBrain)
	b := brain.Get(e.ID())
	b.State = StateWalk
	b.HomeX, b.HomeZ = 5, 5
	b.TargetX, b.TargetZ = 7, 8
	brain.Add(e.ID(), *b)

	enc := codec{}
	ctx := &world.Ctx{Store: s.Store()}
	blob := enc.Encode(ctx, s.Entity(e.ID()))
	if len(blob) != 48 {
		t.Fatalf("v3 带 Brain 应 48 字节，实际 %d", len(blob))
	}

	// 覆盖再还原。
	attr.Add(e.ID(), Attributes{Level: 1})
	brain.Add(e.ID(), Brain{})
	enc.Decode(ctx, s.Entity(e.ID()), blob)

	got := attr.Get(e.ID())
	if got.Level != 3 || got.XP != 50 {
		t.Fatalf("属性未还原: level=%d xp=%d", got.Level, got.XP)
	}
	gb := brain.Get(e.ID())
	if gb.State != StateWalk || gb.HomeX != 5 || gb.TargetX != 7 || gb.TargetZ != 8 {
		t.Fatalf("Brain 未还原: %+v", gb)
	}
}
