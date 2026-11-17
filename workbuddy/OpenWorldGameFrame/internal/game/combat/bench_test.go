package combat

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"openworld/internal/game/creature"
	"openworld/internal/world"
)

// newBenchScene 建一个挂了 creature + combat 的场景，撒 n 只怪。
//
// 这是 M7 压测的入口：单场景 5000 实体的完整玩法负载（战斗 + AI + 回血）
// 下的单帧成本，直接回答"一个 World 进程能扛多少人"。
func newCombatBenchScene(b *testing.B, mobs int) *world.Scene {
	b.Helper()
	s := world.NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := creature.Register(s); err != nil {
		b.Fatal(err)
	}
	if err := Register(s); err != nil {
		b.Fatal(err)
	}
	creature.SpawnHorde(s, mobs, 0, 0, 500)
	return s
}

// BenchmarkFullTickCombat 测完整一帧（含战斗 Buff tick + creature 回血/AI/同步）
// 在 5000 怪规模下的成本。这是"打怪场景"的稳态帧成本：Buff tick 是 250ms
// 一次（4Hz），回血/AI 是 1Hz，大多数帧这些闸门都关着，只有移动结算跑。
//
// 关键结论：稳态帧成本 ≈ 移动结算（O(活跃实体)），低频系统靠闸门摊薄。
func BenchmarkFullTickCombat(b *testing.B) {
	for _, n := range []int{1000, 5000} {
		b.Run(itoa(n), func(b *testing.B) {
			s := newCombatBenchScene(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.TickOnce()
			}
		})
	}
}

// BenchmarkDealDamage 单测一次伤害结算的完整链路成本：
// 伤害公式 → 暴击 → Buff 修正 → 护盾 → 扣血 → 广播。
//
// 5000 人规模的攻击结算，单次约几十纳秒到百纳秒，是战斗能否扛住
// 高频攻击的关键数字。
func BenchmarkDealDamage(b *testing.B) {
	s := newCombatBenchScene(b, 100)
	r := &resolver{scene: s, now: time.Now()}

	// 攻击方和目标都是怪（用默认 Combat）。
	attacker := creature.Spawn(s, "att", 0, 0, 1000)
	target := creature.Spawn(s, "tgt", 1, 0, 100000)
	sk := Skills[1] // 普攻

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.dealDamage(attacker, target, sk, 120, false)
	}
}

// BenchmarkAttackResolve 测一次完整攻击结算（含射程校验、CD 校验、AOE 判定）。
func BenchmarkAttackResolve(b *testing.B) {
	s := newCombatBenchScene(b, 100)
	r := &resolver{scene: s, now: time.Now()}

	attacker := creature.Spawn(s, "att", 0, 0, 1000)
	target := creature.Spawn(s, "tgt", 1, 0, 100000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.attack(time.Now(), attacker.ID(), target.ID(), 1)
	}
}

// itoa 是 strconv.Itoa 的小别名，避免 benchmark 文件名里引 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
