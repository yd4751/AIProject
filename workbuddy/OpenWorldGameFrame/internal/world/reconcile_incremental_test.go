package world

import (
	"testing"
)

// TestIncrementalDigestFirstFullThenEmpty 验证增量摘要的稳态行为：
// 首次全量（所有地都没发过），之后无变化时为空。
func TestIncrementalDigestFirstFullThenEmpty(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(1, "n1", 0, 0, 512))
	setTopo(t, w, entry(2, "n1", 0, 512, 1024))
	setTopo(t, w, entry(3, "n2", 0, 1024, 1536))

	// 首次：所有地都没发过，返回全量。
	first := w.incrementalDigest()
	if len(first) != 3 {
		t.Fatalf("首次增量应是全量 3 条, got %d", len(first))
	}

	// 第二次：无变化，返回空。
	second := w.incrementalDigest()
	if len(second) != 0 {
		t.Fatalf("无变化时增量应为空, got %d 条", len(second))
	}
}

// TestIncrementalDigestSendsOnlyChanged 验证只有版本号变化的地才会被重发。
func TestIncrementalDigestSendsOnlyChanged(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(1, "n1", 0, 0, 512))
	setTopo(t, w, entry(2, "n1", 0, 512, 1024))
	setTopo(t, w, entry(3, "n2", 0, 1024, 1536))

	// 首次全量。
	w.incrementalDigest()
	// 无变化。
	if n := len(w.incrementalDigest()); n != 0 {
		t.Fatalf("无变化应空, got %d", n)
	}

	// 场景 3 的版本号变了（模拟一次重分区搬给 n1）。
	setTopo(t, w, entry(3, "n1", 1, 1024, 1536))
	changed := w.incrementalDigest()
	if len(changed) != 1 {
		t.Fatalf("只有场景 3 变化, 应只发 1 条, got %d", len(changed))
	}
	if changed[0].Scene != 3 || changed[0].Owner != "n1" || changed[0].Ver != 1 {
		t.Fatalf("变化项应是 scene3→n1 ver1, got %+v", changed[0])
	}

	// 再次调用：场景 3 的水位已推进到 1，无变化，空。
	if n := len(w.incrementalDigest()); n != 0 {
		t.Fatalf("水位推进后应空, got %d", n)
	}
}

// TestIncrementalDigestNewSceneAlwaysSent 验证新登记的地（哪怕 ver=0）也会被发。
//
// 新场景 ver 可能是 0，如果只按"ver != 上次"判断会漏发（默认水位也是 0）。
// 所以判断条件必须是"从未发过"或"版本变了"，二者取或。
func TestIncrementalDigestNewSceneAlwaysSent(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(1, "n1", 0, 0, 512))

	// 首次全量：场景 1。
	w.incrementalDigest()
	// 无变化：空。
	if n := len(w.incrementalDigest()); n != 0 {
		t.Fatalf("无变化应空, got %d", n)
	}

	// 新登记一块地，ver=0（从未发过）。
	setTopo(t, w, entry(9, "n1", 0, 2048, 2560))
	got := w.incrementalDigest()
	if len(got) != 1 || got[0].Scene != 9 {
		t.Fatalf("新登记的地（ver=0）应被发送, got %+v", got)
	}
}

// TestIncrementalDigestTombstone 验证：地被摘掉（场景数减少）后，
// 增量不会崩溃，且水位里残留的旧场景不影响新场景的发送。
func TestIncrementalDigestHandlesShrinkingTopo(t *testing.T) {
	w := NewWorld(quiet(), WithNodeID("n1"))
	setTopo(t, w, entry(1, "n1", 0, 0, 512))
	setTopo(t, w, entry(2, "n1", 0, 512, 1024))

	w.incrementalDigest() // 全量，水位记 1 和 2

	// 场景 2 的归属被摘（重分区搬走后本地不再登记它的路由）。
	// 用 relocate 到别的节点再模拟，或者直接验证 snapshot 变小不影响。
	// 这里只验证：snapshot 从 2 变 1 时增量不 panic、不误发。
	w.mu.Lock()
	w.topo.relocate(2, "n2", 1)
	delete(w.topo.peers, 2) // 模拟地完全摘除（实际上 relocate 不会删 peers，这里显式删）
	w.mu.Unlock()

	got := w.incrementalDigest()
	// 场景 1 没变，场景 2 已被摘除（不在 snapshot 里），应该返回空。
	if len(got) != 0 {
		t.Fatalf("场景 2 摘除后增量应空, got %+v", got)
	}
}
