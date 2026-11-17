package persist

import (
	"encoding/json"
	"time"
)

// PlayerRecord 是玩家档案：跨场景/跨区重连时要恢复的状态。
//
// 字段刻意用 JSON 友好类型（int32/string），因为档案要落盘、要跨系统
// 传输，可读性优先于极致紧凑。这和 world 存档的二进制 blob 是两种
// 取舍：world 存档是高频、本进程内部，二进制更快；玩家档案是低频、
// 要跨区，JSON 更易排查和兼容。
type PlayerRecord struct {
	// Player 是玩家名（主键）。冗余存一份，落盘后文件自描述。
	Player string `json:"player"`
	// SavedAt 是最近一次落库时间。
	SavedAt time.Time `json:"savedAt"`

	// 基础属性（血蓝等级经验）。
	HP    int32 `json:"hp"`
	MaxHP int32 `json:"maxHp"`
	MP    int32 `json:"mp"`
	MaxMP int32 `json:"maxMp"`
	Level uint8 `json:"level"`
	XP    int32 `json:"xp"`

	// 战斗属性（攻防、暴击、技能解锁）。
	Attack   int32  `json:"attack"`
	Defense  int32  `json:"defense"`
	CritRate uint8  `json:"critRate"`
	CritMult int32  `json:"critMult"`
	Learned  uint64 `json:"learned"` // 技能解锁位图
}

// Marshal 序列化为 JSON 字节。
func (r *PlayerRecord) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalPlayerRecord 从 JSON 字节还原档案。
func UnmarshalPlayerRecord(b []byte) (*PlayerRecord, error) {
	var r PlayerRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
