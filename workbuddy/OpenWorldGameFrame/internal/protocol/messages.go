package protocol

// LoginReq 客户端上行登录请求。
type LoginReq struct {
	Token string
}

func (m *LoginReq) Encode() []byte {
	w := NewWriter(16 + len(m.Token))
	w.String(m.Token)
	return w.Bytes()
}

func (m *LoginReq) Decode(b []byte) error {
	r := NewReader(b)
	m.Token = r.String()
	return r.Err()
}

// LoginResp 服务端返回登录结果。Code 非 0 时 Reason 说明原因。
type LoginResp struct {
	Code      uint8
	SessionID uint64
	PlayerID  string
	Reason    string
}

func (m *LoginResp) Encode() []byte {
	w := NewWriter(32 + len(m.PlayerID) + len(m.Reason))
	w.U8(m.Code)
	w.U64(m.SessionID)
	w.String(m.PlayerID)
	w.String(m.Reason)
	return w.Bytes()
}

func (m *LoginResp) Decode(b []byte) error {
	r := NewReader(b)
	m.Code = r.U8()
	m.SessionID = r.U64()
	m.PlayerID = r.String()
	m.Reason = r.String()
	return r.Err()
}

// HeartbeatReq 心跳，带客户端序号和本地时间用于算 RTT。
type HeartbeatReq struct {
	Seq        uint64
	ClientTime int64
}

func (m *HeartbeatReq) Encode() []byte {
	w := NewWriter(16)
	w.U64(m.Seq)
	w.I64(m.ClientTime)
	return w.Bytes()
}

func (m *HeartbeatReq) Decode(b []byte) error {
	r := NewReader(b)
	m.Seq = r.U64()
	m.ClientTime = r.I64()
	return r.Err()
}

// HeartbeatResp 服务端回显序号，并带上自己的时间。
type HeartbeatResp struct {
	Seq        uint64
	ServerTime int64
}

func (m *HeartbeatResp) Encode() []byte {
	w := NewWriter(16)
	w.U64(m.Seq)
	w.I64(m.ServerTime)
	return w.Bytes()
}

func (m *HeartbeatResp) Decode(b []byte) error {
	r := NewReader(b)
	m.Seq = r.U64()
	m.ServerTime = r.I64()
	return r.Err()
}

// EnterSceneReq 请求进入默认场景。M1 不带参数。
type EnterSceneReq struct{}

func (m *EnterSceneReq) Encode() []byte { return nil }

func (m *EnterSceneReq) Decode(b []byte) error { return nil }

// EnterSceneResp 返回分配到的实体 ID 和出生点。
type EnterSceneResp struct {
	Code     uint8
	EntityID uint64
	SceneID  uint32
	X        float32
	Y        float32
	Z        float32
	Reason   string
}

func (m *EnterSceneResp) Encode() []byte {
	w := NewWriter(32 + len(m.Reason))
	w.U8(m.Code)
	w.U64(m.EntityID)
	w.U32(m.SceneID)
	w.F32(m.X)
	w.F32(m.Y)
	w.F32(m.Z)
	w.String(m.Reason)
	return w.Bytes()
}

func (m *EnterSceneResp) Decode(b []byte) error {
	r := NewReader(b)
	m.Code = r.U8()
	m.EntityID = r.U64()
	m.SceneID = r.U32()
	m.X = r.F32()
	m.Y = r.F32()
	m.Z = r.F32()
	m.Reason = r.String()
	return r.Err()
}

// MoveReq 客户端上报目标位置。服务端只把它当"意图"，最终位置由服务端算。
type MoveReq struct {
	X          float32
	Y          float32
	Z          float32
	ClientTime int64
}

func (m *MoveReq) Encode() []byte {
	w := NewWriter(20)
	w.F32(m.X)
	w.F32(m.Y)
	w.F32(m.Z)
	w.I64(m.ClientTime)
	return w.Bytes()
}

func (m *MoveReq) Decode(b []byte) error {
	r := NewReader(b)
	m.X = r.F32()
	m.Y = r.F32()
	m.Z = r.F32()
	m.ClientTime = r.I64()
	return r.Err()
}

// EntityState 是某个实体的位置快照，服务端广播给视野内的其他玩家。
type EntityState struct {
	EntityID uint64
	X        float32
	Y        float32
	Z        float32
}

func (m *EntityState) Encode() []byte {
	w := NewWriter(20)
	w.U64(m.EntityID)
	w.F32(m.X)
	w.F32(m.Y)
	w.F32(m.Z)
	return w.Bytes()
}

func (m *EntityState) Decode(b []byte) error {
	r := NewReader(b)
	m.EntityID = r.U64()
	m.X = r.F32()
	m.Y = r.F32()
	m.Z = r.F32()
	return r.Err()
}

// EntityEnter 通知客户端有实体进入视野。
type EntityEnter struct {
	EntityID uint64
	X        float32
	Y        float32
	Z        float32
}

func (m *EntityEnter) Encode() []byte {
	w := NewWriter(20)
	w.U64(m.EntityID)
	w.F32(m.X)
	w.F32(m.Y)
	w.F32(m.Z)
	return w.Bytes()
}

func (m *EntityEnter) Decode(b []byte) error {
	r := NewReader(b)
	m.EntityID = r.U64()
	m.X = r.F32()
	m.Y = r.F32()
	m.Z = r.F32()
	return r.Err()
}

// EntityAttr 是实体的属性快照。
//
// 属性不走"每个 tick 广播"：血条这类数据变化慢、且只有视野内的玩家关心，
// 服务端按 1Hz 或"变化时"下发即可。包体 17 字节，比位置包大的部分
// 主要来自 HP/MaxHP 各 4 字节，客户端能用它直接画血条百分比，
// 不需要额外拉一次全量属性表。
type EntityAttr struct {
	EntityID uint64
	HP       int32
	MaxHP    int32
	Level    uint8
}

func (m *EntityAttr) Encode() []byte {
	w := NewWriter(17)
	w.U64(m.EntityID)
	w.I32(m.HP)
	w.I32(m.MaxHP)
	w.U8(m.Level)
	return w.Bytes()
}

func (m *EntityAttr) Decode(b []byte) error {
	r := NewReader(b)
	m.EntityID = r.U64()
	m.HP = r.I32()
	m.MaxHP = r.I32()
	m.Level = r.U8()
	return r.Err()
}

// EntityLeave 通知客户端有实体离开视野。
type EntityLeave struct {
	EntityID uint64
}

func (m *EntityLeave) Encode() []byte {
	w := NewWriter(8)
	w.U64(m.EntityID)
	return w.Bytes()
}

func (m *EntityLeave) Decode(b []byte) error {
	r := NewReader(b)
	m.EntityID = r.U64()
	return r.Err()
}

// Kick 服务端主动断开连接时下发的通知。
type Kick struct {
	Code   uint8
	Reason string
}

func (m *Kick) Encode() []byte {
	w := NewWriter(16 + len(m.Reason))
	w.U8(m.Code)
	w.String(m.Reason)
	return w.Bytes()
}

func (m *Kick) Decode(b []byte) error {
	r := NewReader(b)
	m.Code = r.U8()
	m.Reason = r.String()
	return r.Err()
}

// AttackReq 客户端上行攻击指令。
//
// 只带目标实体和技能 ID，不带伤害数值：伤害由服务端按攻击方的属性、
// 技能系数、Buff 和防御方减伤统一结算。客户端永远只是"表达意图"，
// 不能自己报一个伤害数字——否则等于把"我能打多少血"交给客户端决定。
type AttackReq struct {
	TargetID uint64
	SkillID  uint8
}

func (m *AttackReq) Encode() []byte {
	w := NewWriter(9)
	w.U64(m.TargetID)
	w.U8(m.SkillID)
	return w.Bytes()
}

func (m *AttackReq) Decode(b []byte) error {
	r := NewReader(b)
	m.TargetID = r.U64()
	m.SkillID = r.U8()
	return r.Err()
}

// CombatHit 服务端下发一次伤害结算结果。
//
// 下发对象是"视野内所有玩家"（含攻击者自己），这样每个客户端都能
// 看到别人打的伤害飘字和血条变化，而不需要服务端维护"谁关心谁"的订阅。
// 包体不含攻击者/受击者坐标：客户端已经有这两者的位置快照。
type CombatHit struct {
	AttackerID uint64
	TargetID   uint64
	SkillID    uint8
	Damage     int32
	TargetHP   int32 // 结算后目标剩余 HP；<=0 表示被击杀
	Killed     bool
	Crit       bool // 本次伤害是否暴击（客户端据此放大飘字）
}

func (m *CombatHit) Encode() []byte {
	w := NewWriter(27)
	w.U64(m.AttackerID)
	w.U64(m.TargetID)
	w.U8(m.SkillID)
	w.I32(m.Damage)
	w.I32(m.TargetHP)
	if m.Killed {
		w.U8(1)
	} else {
		w.U8(0)
	}
	if m.Crit {
		w.U8(1)
	} else {
		w.U8(0)
	}
	return w.Bytes()
}

func (m *CombatHit) Decode(b []byte) error {
	r := NewReader(b)
	m.AttackerID = r.U64()
	m.TargetID = r.U64()
	m.SkillID = r.U8()
	m.Damage = r.I32()
	m.TargetHP = r.I32()
	m.Killed = r.U8() != 0
	m.Crit = r.U8() != 0
	return r.Err()
}

// Notice 是服务端的系统公告，用于关服倒计时这类"要告诉玩家但不断线"的场景。
//
// 和 Kick 分开是有意的：Kick 意味着"你必须下线"，Notice 意味着
// "还有 Seconds 秒，你可以先把当前的事做完"。混用会让客户端没法区分
// "网络抖动导致的踢线"和"服务器要维护了"。
type Notice struct {
	Title   string
	Text    string
	Seconds uint32
}

func (m *Notice) Encode() []byte {
	w := NewWriter(16 + len(m.Title) + len(m.Text))
	w.String(m.Title)
	w.String(m.Text)
	w.U32(m.Seconds)
	return w.Bytes()
}

func (m *Notice) Decode(b []byte) error {
	r := NewReader(b)
	m.Title = r.String()
	m.Text = r.String()
	m.Seconds = r.U32()
	return r.Err()
}

// SkillInfo 是技能目录里的一个技能静态描述。客户端据此画技能树。
//
// 字段与 internal/game/combat.Skill 一一对应：ID 是协议里的 SkillID，
// Name 是展示名，Damage/Range/CooldownMs 是结算参数，Level/Requires 是
// 技能树的解锁条件，ComboFrom/ComboScale 是连招链，AOE/AOERad/Dispel 是效果。
type SkillInfo struct {
	ID         uint8
	Name       string
	Damage     int32
	Range      float32
	CooldownMs uint32
	AOE        bool
	AOERad     float32
	Level      uint8 // 解锁所需等级门槛
	Requires   uint8 // 前置技能 ID，0 表示无
	ComboFrom  uint8 // 连招起点技能 ID，0 表示不参与连招
	ComboScale int32 // 连招伤害加成（百分比，150 = ×1.5）
	Dispel     bool  // 命中后驱散目标所有 Buff
}

// SkillList 服务端下发的技能目录 + 玩家已解锁位图。
//
// 目录是进程级共享的（不随玩家变化），玩家只关心 Learned 位图。
// 客户端用 Learned + 自身等级 + 目录计算"可用 / 已学 / 锁定"三态。
type SkillList struct {
	Skills  []SkillInfo
	Learned uint64 // bit i 表示技能 i 已解锁
}

// Encode 把技能目录编码成字节流（大端，逐字段对齐 GDScript 解码端）。
func (m *SkillList) Encode() []byte {
	n := len(m.Skills)
	w := NewWriter(16 + n*48 + 8)
	w.U8(uint8(n))
	for _, s := range m.Skills {
		w.U8(s.ID)
		w.String(s.Name)
		w.I32(s.Damage)
		w.F32(s.Range)
		w.U32(s.CooldownMs)
		if s.AOE {
			w.U8(1)
		} else {
			w.U8(0)
		}
		w.F32(s.AOERad)
		w.U8(s.Level)
		w.U8(s.Requires)
		w.U8(s.ComboFrom)
		w.I32(s.ComboScale)
		if s.Dispel {
			w.U8(1)
		} else {
			w.U8(0)
		}
	}
	w.U64(m.Learned)
	return w.Bytes()
}

// Decode 解码技能目录。目录为空但 Learned 非 0 是合法的（理论不会出现）。
func (m *SkillList) Decode(b []byte) error {
	r := NewReader(b)
	n := int(r.U8())
	if n > 64 {
		n = 64 // 防御：技能数量异常时截断，避免分配爆炸
	}
	m.Skills = make([]SkillInfo, 0, n)
	for i := 0; i < n; i++ {
		var s SkillInfo
		s.ID = r.U8()
		s.Name = r.String()
		s.Damage = r.I32()
		s.Range = r.F32()
		s.CooldownMs = r.U32()
		s.AOE = r.U8() != 0
		s.AOERad = r.F32()
		s.Level = r.U8()
		s.Requires = r.U8()
		s.ComboFrom = r.U8()
		s.ComboScale = r.I32()
		s.Dispel = r.U8() != 0
		m.Skills = append(m.Skills, s)
	}
	m.Learned = r.U64()
	return r.Err()
}
