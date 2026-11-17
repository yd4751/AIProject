package world

import (
	"testing"
	"time"

	"openworld/internal/mesh"
)

// TestConfigSlotsDefault 验证老配置（没有 slots/maxNodes）仍然按字典序分区。
//
// 这是兼容性底线：M3.3 的两个节点配置文件一个字都不改，升级后
// 分区结果必须完全一致，否则历史 ID 会指向错误的所有者。
func TestConfigSlotsDefault(t *testing.T) {
	c := &Config{ChunkSize: 512, Nodes: map[string]string{"n2": "b", "n1": "a"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.MaxNodes != 2 {
		t.Fatalf("maxNodes 应默认为节点数 2，得到 %d", c.MaxNodes)
	}
	if s, o := c.Partition("n1"); s != 2 || o != 0 {
		t.Fatalf("n1 分区应为 (2,0)，得到 (%d,%d)", s, o)
	}
	if s, o := c.Partition("n2"); s != 2 || o != 1 {
		t.Fatalf("n2 分区应为 (2,1)，得到 (%d,%d)", s, o)
	}
	if got := c.SlotTable(); len(got) != 2 || got[0] != "n1" || got[1] != "n2" {
		t.Fatalf("槽位表不对: %v", got)
	}
}

// TestConfigPartitionStableOnJoin 是 M3.4 的核心性质：
// 加一个节点之后，已有节点的 (stride, offset) 一个字节都不变。
//
// 只要这条成立，"加节点"就不会让已经发出去的 ID 改变归属；
// 一旦不成立，两个节点会在某一天开始发重复的 ID，而且是静默的。
func TestConfigPartitionStableOnJoin(t *testing.T) {
	before := &Config{
		Nodes:    map[string]string{"n1": "a", "n2": "b"},
		MaxNodes: 4,
	}
	if err := before.Validate(); err != nil {
		t.Fatal(err)
	}
	after := &Config{
		Nodes:    map[string]string{"n1": "a", "n2": "b", "n3": "c"},
		MaxNodes: 4,
	}
	if err := after.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"n1", "n2"} {
		s1, o1 := before.Partition(n)
		s2, o2 := after.Partition(n)
		if s1 != s2 || o1 != o2 {
			t.Fatalf("%s 的分区变了: (%d,%d) → (%d,%d)", n, s1, o1, s2, o2)
		}
	}
	// 新节点必须落在一个空槽位上，不能挤掉任何已有节点。
	s3, o3 := after.Partition("n3")
	if s3 != 4 {
		t.Fatalf("stride 不该变化，得到 %d", s3)
	}
	if o3 == 0 || o3 == 1 {
		t.Fatalf("n3 抢了已有节点的槽位 %d", o3)
	}
	if got := after.SlotTable(); got[o3] != "n3" {
		t.Fatalf("槽位表没登记 n3: %v", got)
	}
}

// TestIDCollisionIfStrideGrows 是一个反例，用来说明"步长取当前节点数"为什么不行。
//
// 把 stride 从 2 改成 3 之后，n1 新发的 ID 会撞上 n2 在旧步长下已经用过的 ID。
// 这里的数字是手算的，不是随机的：n2 在 stride=2 下第 103 个号是 207，
// n1 在 stride=3 下第 69 个号也是 207。
func TestIDCollisionIfStrideGrows(t *testing.T) {
	const n2Old = 103*2 + 1 // stride=2, offset=1
	const n1New = 69 * 3    // stride=3, offset=0
	if n2Old != n1New {
		t.Fatalf("反例失效了：%d != %d（改了公式记得更新这段说明）", n2Old, n1New)
	}
	// 固定步长就不会出现这种情况：槽位上限 4 时，n1 永远是 4k、n2 永远是 4k+1。
	if (69*4)%4 == (103*4+1)%4 {
		t.Fatal("固定步长下不同槽位不应该同余")
	}
}

func TestConfigSlotValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"节点数超过上限", &Config{Nodes: map[string]string{"n1": "", "n2": "", "n3": ""}, MaxNodes: 2}},
		{"槽位重复", &Config{Nodes: map[string]string{"n1": "", "n2": ""}, MaxNodes: 4,
			Slots: map[string]int{"n1": 2, "n2": 2}}},
		{"槽位越界", &Config{Nodes: map[string]string{"n1": ""}, MaxNodes: 2,
			Slots: map[string]int{"n1": 9}}},
		{"槽位表里有陌生节点", &Config{Nodes: map[string]string{"n1": ""}, MaxNodes: 2,
			Slots: map[string]int{"n1": 0, "n9": 1}}},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); err == nil {
			t.Fatalf("%s: 应该报错但通过了", tc.name)
		}
	}
}

// TestNodeAliveAffectsRouting 验证成员视图如何改变可走的地块。
//
// 关键不是"走不过去"，而是**不会发起注定失败的迁移**：
// 一个死节点如果还留在路由表里，玩家每帧都会发起一次 Prepare，
// 3 秒后超时回滚，然后下一帧继续——日志被刷满，玩家贴着边界抖动。
func TestNodeAliveAffectsRouting(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"), WithNodeSlots([]string{"n1", "n2"}))
	w.NewScene(1, 50*time.Millisecond, nil, quiet(), WithBounds(Rect{0, 0, 512, 512}))
	remote := mesh.Peer{Node: "n2", Scene: 2}
	if err := w.DeclareRemote(remote, Rect{MinX: 512, MaxX: 1024, MaxZ: 512}); err != nil {
		t.Fatal(err)
	}

	if p, ok := w.PeerAt(600, 100); !ok || p != remote {
		t.Fatalf("n2 默认可达，600,100 应路由到 %s，得到 %s %v", remote, p, ok)
	}

	w.SetNodeAlive("n2", false)
	if p, ok := w.PeerAt(600, 100); ok {
		t.Fatalf("n2 已判死，不该再路由过去（得到 %s）", p)
	}
	if chunks := w.Chunks(); len(chunks) != 2 {
		t.Fatalf("Chunk 归属不该消失，期望 2 块，得到 %d", len(chunks))
	} else {
		for _, c := range chunks {
			if c.Node == "n2" && c.Alive {
				t.Fatal("n2 的 Chunk 应标记为不可达")
			}
		}
	}
	if w.NodeAlive("n2") {
		t.Fatal("NodeAlive 应返回 false")
	}

	w.SetNodeAlive("n2", true)
	if _, ok := w.PeerAt(600, 100); !ok {
		t.Fatal("n2 回来后应该恢复路由")
	}

	// 本节点永远可达：自己把自己判死会把本地场景当"别人的地"。
	w.SetNodeAlive("n1", false)
	if p, ok := w.PeerAt(100, 100); !ok || p.Node != "n1" {
		t.Fatalf("本地地块不该受成员视图影响: %s %v", p, ok)
	}
}

// TestDeadNeighborClampsPlayer 验证玩家走到死节点的边界会被夹住，
// 且**不产生迁移尝试**。
func TestDeadNeighborClampsPlayer(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"), WithNodeSlots([]string{"n1", "n2"}))
	s := w.NewScene(1, 50*time.Millisecond, nil, quiet(), WithBounds(Rect{0, 0, 512, 512}))
	if err := w.DeclareRemote(mesh.Peer{Node: "n2", Scene: 2}, Rect{MinX: 512, MaxX: 1024, MaxZ: 512}); err != nil {
		t.Fatal(err)
	}
	w.SetNodeAlive("n2", false)

	id, _ := enterPlayer(t, s, "p1", 500, 500)
	// 手动把速度调高：测试里 tick 是手动的，elapsed 会被兜底成 0.01s，
	// 默认速度下每帧只能走几厘米，20 次都到不了边界。
	s.Entity(id).speed = 100
	for i := 0; i < 20; i++ {
		s.Move(id, 520, 0, 500) // 一直往东撞墙
		s.TickOnce()
	}

	e := s.Entity(id)
	if e == nil {
		t.Fatal("玩家不该消失")
	}
	if e.x >= 512 {
		t.Fatalf("玩家应该在边界内被夹住，实际 x=%.2f", e.x)
	}
	if st := s.Stats(); st.HandoverFails != 0 || st.HandoversOut != 0 {
		t.Fatalf("死节点不该产生迁移尝试: fails=%d out=%d", st.HandoverFails, st.HandoversOut)
	}
}
