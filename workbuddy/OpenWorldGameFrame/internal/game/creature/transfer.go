package creature

import (
	"encoding/binary"
	"math"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

// 跨场景迁移 / 存档时带走的玩法数据（属性 + AI 状态）。
//
// 属性是玩家的核心状态，Brain 是怪物的 AI 状态。存档要能把一只怪
// 原样复活（走到一半的巡逻、下一次思考的时间点），所以 Brain 也要带。
// 换成一个真的会迁的 AI（比如跨区巡逻的 NPC），就在这里再加一段，
// 并把版本号加一。
//
// 打包格式（小端）：
//
//	[0]     version = 3
//	[1:5]   HP      int32
//	[5:9]   MaxHP   int32
//	[9:13]  MP      int32
//	[13:17] MaxMP   int32
//	[17]    Level   uint8
//	[18:22] XP      int32
//	[22]    hasBrain uint8   (1 表示后面跟着 Brain 段)
//	[23:24] State   uint8    (Brain.State)
//	[24:28] HomeX   float32
//	[28:32] HomeZ   float32
//	[32:36] TargetX float32
//	[36:40] TargetZ float32
//	[40:48] NextThinkNanos int64 (相对 Unix 纳秒；0 表示立刻思考)
//
// 版本 3 相对版本 2 新增 XP 字段（4 字节），Brain 段整体后移 4 字节。
// 旧档（v2）解码时按 18 字节属性 + 无 XP 处理，Brain 段位置需要按
// 版本号区分偏移。
const attrPayloadVersion = 3

// codec 实现 world.TransferCodec。
type codec struct{}

// Encode 把实体的属性和 AI 状态打包。实体没有属性组件时返回 nil。
func (codec) Encode(ctx *world.Ctx, e *world.Entity) []byte {
	a := ecs.Get(ctx.Store, CAttr).Get(e.ID())
	if a == nil {
		return nil
	}

	hasBrain := false
	var b *Brain
	if !e.IsPlayer() {
		if bb := ecs.Get(ctx.Store, CBrain).Get(e.ID()); bb != nil {
			hasBrain = true
			b = bb
		}
	}

	size := 22
	if hasBrain {
		size = 48
	}
	buf := make([]byte, size)
	buf[0] = attrPayloadVersion
	binary.LittleEndian.PutUint32(buf[1:], uint32(a.HP))
	binary.LittleEndian.PutUint32(buf[5:], uint32(a.MaxHP))
	binary.LittleEndian.PutUint32(buf[9:], uint32(a.MP))
	binary.LittleEndian.PutUint32(buf[13:], uint32(a.MaxMP))
	buf[17] = a.Level
	binary.LittleEndian.PutUint32(buf[18:], uint32(a.XP))

	if hasBrain {
		buf[22] = 1
		buf[23] = uint8(b.State)
		binary.LittleEndian.PutUint32(buf[24:], math.Float32bits(b.HomeX))
		binary.LittleEndian.PutUint32(buf[28:], math.Float32bits(b.HomeZ))
		binary.LittleEndian.PutUint32(buf[32:], math.Float32bits(b.TargetX))
		binary.LittleEndian.PutUint32(buf[36:], math.Float32bits(b.TargetZ))
		var nanos int64
		if !b.NextThink.IsZero() {
			nanos = b.NextThink.UnixNano()
		}
		binary.LittleEndian.PutUint64(buf[40:], uint64(nanos))
	}
	return buf
}

// Decode 覆盖属性和 AI 状态。进场钩子已经挂了一份默认值，这里直接改。
//
// 必须把 Dirty 置上：新视野里的玩家还没见过这个实体，不标脏就不会触发
// 属性同步。旧档（v2）没有 XP 字段、Brain 段比 v3 前移 4 字节，
// 这里按版本号区分偏移，保证旧档还能正确还原。
func (codec) Decode(ctx *world.Ctx, e *world.Entity, data []byte) {
	a := ecs.Get(ctx.Store, CAttr).Get(e.ID())
	if a == nil {
		return
	}
	switch {
	case len(data) >= 22 && data[0] == 3:
		a.HP = int32(binary.LittleEndian.Uint32(data[1:]))
		a.MaxHP = int32(binary.LittleEndian.Uint32(data[5:]))
		a.MP = int32(binary.LittleEndian.Uint32(data[9:]))
		a.MaxMP = int32(binary.LittleEndian.Uint32(data[13:]))
		a.Level = data[17]
		a.XP = int32(binary.LittleEndian.Uint32(data[18:]))
		a.Dirty = true
		if len(data) >= 48 && data[22] == 1 {
			decodeBrain(ctx, e, data, 23)
		}
	case len(data) >= 18 && data[0] == 2:
		// v2：18 字节属性，无 XP，Brain 段从 19 开始。
		a.HP = int32(binary.LittleEndian.Uint32(data[1:]))
		a.MaxHP = int32(binary.LittleEndian.Uint32(data[5:]))
		a.MP = int32(binary.LittleEndian.Uint32(data[9:]))
		a.MaxMP = int32(binary.LittleEndian.Uint32(data[13:]))
		a.Level = data[17]
		a.Dirty = true
		if len(data) >= 47 && data[18] == 1 {
			decodeBrain(ctx, e, data, 19)
		}
	default:
		// 版本不认就保留默认属性。宁可让实体满血复活，
		// 也不要用错位的字节把血量改成随机数。
		return
	}
}

// decodeBrain 从 data 的 off 偏移开始还原 Brain 段。
func decodeBrain(ctx *world.Ctx, e *world.Entity, data []byte, off int) {
	b := ecs.Get(ctx.Store, CBrain).Get(e.ID())
	if b == nil {
		return
	}
	b.State = State(data[off])
	b.HomeX = math.Float32frombits(binary.LittleEndian.Uint32(data[off+1:]))
	b.HomeZ = math.Float32frombits(binary.LittleEndian.Uint32(data[off+5:]))
	b.TargetX = math.Float32frombits(binary.LittleEndian.Uint32(data[off+9:]))
	b.TargetZ = math.Float32frombits(binary.LittleEndian.Uint32(data[off+13:]))
	if nanos := int64(binary.LittleEndian.Uint64(data[off+17:])); nanos != 0 {
		b.NextThink = time.Unix(0, nanos)
	} else {
		b.NextThink = time.Now()
	}
	b.seed = uint32(e.ID())*2654435761 + 12345
}

// EnableTransfer 让这个玩法包支持跨场景迁移 / 存档。
//
// 单独一个函数而不是塞进 Register：迁移是可选能力，
// 单场景部署时没必要为它挂一个 codec（也就少一条永远不会执行的路径）。
func EnableTransfer(s *world.Scene) { s.AddTransferCodec(codec{}) }
