package combat

import (
	"encoding/binary"
	"time"

	"openworld/internal/ecs"
	"openworld/internal/world"
)

// 跨场景迁移 / 存档时带走的战斗数据（战斗属性 + 技能 CD + 连招 + Buff 列表）。
//
// creature 存属性（血蓝等级）和 AI，combat 存战斗（攻防 + 冷却 + 连招 + Buff）。
// 两者各自注册 codec，world 层用长度前缀拼成一个 blob，互不感知对方的
// 格式——这是"多玩法层共享一套迁移/存档机制"的分层收益。
//
// 打包格式（小端，v2）：
//
//	[0]     version = 2
//	[1:5]   Attack  int32
//	[5:9]   Defense int32
//	[9]     CritRate uint8
//	[10:14] CritMult int32
//	[14:18] Learned  uint32（技能解锁位图，低 32 位够用）
//	[18:22] LastSkill uint8 填充（占用 1 字节 + 3 保留）
//	[22:30] LastCast int64 (UnixNano)
//	[30:38] CD0     int64（8 个 CD，UnixNano，0 表示可用）
//	[38:46] CD1
//	[46:54] CD2
//	[54:62] CD3
//	[62:70] CD4
//	[70:78] CD5
//	[78:86] CD6
//	[86:94] CD7
//	[94:96] buffCount uint16
//	然后每只 Buff 16 字节：
//	  [0]    kind  uint8
//	  [1:5]  value int32
//	  [5:13] until int64 (UnixNano)
//	  [13:15] 保留
//
// 头部固定 96 字节 + buffCount × 16。
const combatPayloadVersion = 2

const (
	buffFieldSize = 16
	combatHeader  = 96 // 版本号之后到 buffCount 结束的字节数（含 buffCount）
)

// codec 实现 world.TransferCodec。
type codec struct{}

// Encode 把实体的战斗属性、CD、连招、Buff 打包。没有 Combat 组件时返回 nil。
func (codec) Encode(ctx *world.Ctx, e *world.Entity) []byte {
	c := ecs.Get(ctx.Store, CCombat).Get(e.ID())
	if c == nil {
		return nil
	}

	buffs := ecs.Get(ctx.Store, CBuff).Get(e.ID())
	var nb int
	if buffs != nil {
		nb = len(*buffs)
	}

	buf := make([]byte, combatHeader+nb*buffFieldSize)
	buf[0] = combatPayloadVersion
	binary.LittleEndian.PutUint32(buf[1:], uint32(c.Attack))
	binary.LittleEndian.PutUint32(buf[5:], uint32(c.Defense))
	buf[9] = c.CritRate
	binary.LittleEndian.PutUint32(buf[10:], uint32(c.CritMult))
	binary.LittleEndian.PutUint32(buf[14:], uint32(c.Learned))
	buf[18] = c.LastSkill
	// [19:22] 保留
	var lastNanos int64
	if !c.LastCast.IsZero() {
		lastNanos = c.LastCast.UnixNano()
	}
	binary.LittleEndian.PutUint64(buf[22:], uint64(lastNanos))
	// 8 个 CD，从偏移 30 开始，每个 8 字节。
	for i := 0; i < len(c.CDs); i++ {
		var nanos int64
		if !c.CDs[i].IsZero() {
			nanos = c.CDs[i].UnixNano()
		}
		binary.LittleEndian.PutUint64(buf[30+i*8:], uint64(nanos))
	}
	binary.LittleEndian.PutUint16(buf[94:], uint16(nb))
	off := combatHeader
	if buffs != nil {
		for _, b := range *buffs {
			buf[off] = uint8(b.Kind)
			binary.LittleEndian.PutUint32(buf[off+1:], uint32(b.Value))
			binary.LittleEndian.PutUint64(buf[off+5:], uint64(b.Until.UnixNano()))
			off += buffFieldSize
		}
	}
	return buf
}

// Decode 覆盖战斗属性、CD、连招、Buff。
func (codec) Decode(ctx *world.Ctx, e *world.Entity, data []byte) {
	if len(data) < combatHeader || data[0] != combatPayloadVersion {
		return
	}
	c := ecs.Get(ctx.Store, CCombat).Get(e.ID())
	if c == nil {
		return
	}
	c.Attack = int32(binary.LittleEndian.Uint32(data[1:]))
	c.Defense = int32(binary.LittleEndian.Uint32(data[5:]))
	c.CritRate = data[9]
	c.CritMult = int32(binary.LittleEndian.Uint32(data[10:]))
	c.Learned = uint64(binary.LittleEndian.Uint32(data[14:]))
	c.LastSkill = data[18]
	if nanos := int64(binary.LittleEndian.Uint64(data[22:])); nanos != 0 {
		c.LastCast = time.Unix(0, nanos)
	} else {
		c.LastCast = time.Time{}
	}
	for i := 0; i < len(c.CDs); i++ {
		if nanos := int64(binary.LittleEndian.Uint64(data[30+i*8:])); nanos != 0 {
			c.CDs[i] = time.Unix(0, nanos)
		} else {
			c.CDs[i] = time.Time{}
		}
	}

	nb := int(binary.LittleEndian.Uint16(data[94:]))
	if nb > 0 && len(data) >= combatHeader+nb*buffFieldSize {
		buffs := make([]Buff, 0, nb)
		off := combatHeader
		for i := 0; i < nb; i++ {
			b := Buff{
				Kind:  BuffKind(data[off]),
				Value: int32(binary.LittleEndian.Uint32(data[off+1:])),
				Until: time.Unix(0, int64(binary.LittleEndian.Uint64(data[off+5:]))),
			}
			buffs = append(buffs, b)
			off += buffFieldSize
		}
		ecs.Get(ctx.Store, CBuff).Add(e.ID(), buffs)
	}
}

// EnableTransfer 让战斗玩法支持跨场景迁移 / 存档。
func EnableTransfer(s *world.Scene) { s.AddTransferCodec(codec{}) }
