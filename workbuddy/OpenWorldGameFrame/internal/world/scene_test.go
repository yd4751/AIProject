package world

import (
	"io"
	"log/slog"
	"math"
	"openworld/internal/aoi"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"openworld/internal/protocol"
)

type recvMsg struct {
	id   uint16
	body []byte
}

// mockClient 记录场景发来的所有消息，用于断言广播行为。
type mockClient struct {
	mu     sync.Mutex
	msgs   []recvMsg
	kicked bool
	reason string
}

func (m *mockClient) Send(id uint16, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := make([]byte, len(body))
	copy(b, body)
	m.msgs = append(m.msgs, recvMsg{id: id, body: b})
	return nil
}

func (m *mockClient) Kick(code uint8, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kicked = true
	m.reason = reason
	return nil
}

func (m *mockClient) count(id uint16) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.msgs {
		if msg.id == id {
			n++
		}
	}
	return n
}

func (m *mockClient) last(id uint16) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if m.msgs[i].id == id {
			return m.msgs[i].body
		}
	}
	return nil
}

func (m *mockClient) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = nil
}

func newTestScene() *Scene {
	return NewScene(1, 50*time.Millisecond, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEnterSendsSceneRespAndBroadcasts(t *testing.T) {
	s := newTestScene()
	a, b := &mockClient{}, &mockClient{}

	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})
	if got := a.count(protocol.MsgS2CEnterScene); got != 1 {
		t.Fatalf("first enter should send 1 EnterSceneResp, got %d", got)
	}

	s.onEnter(&msgEnter{id: 2, player: "b", client: b, hasPos: true, x: 1, z: 1})

	// 双方都要看见对方。
	if got := b.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("b should receive 1 EntityEnter, got %d", got)
	}
	if got := a.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("a should receive 1 EntityEnter, got %d", got)
	}

	var seen protocol.EntityEnter
	if err := seen.Decode(a.last(protocol.MsgS2CEntityEnter)); err != nil {
		t.Fatalf("decode EntityEnter: %v", err)
	}
	if seen.EntityID != 2 {
		t.Fatalf("a should see entity 2, got %d", seen.EntityID)
	}
	if s.entityCount.Load() != 2 {
		t.Fatalf("entity count should be 2, got %d", s.entityCount.Load())
	}
}

func TestMoveBroadcastsToNeighborsOnly(t *testing.T) {
	s := newTestScene()
	a, b := &mockClient{}, &mockClient{}

	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})
	s.onEnter(&msgEnter{id: 2, player: "b", client: b, hasPos: true, x: 1, z: 1})
	a.reset()
	b.reset()

	// 拉开时间，保证位移在允许范围内（默认 6m/s，100ms 约 1.4m 额度）。
	s.entities[1].lastMoveAt = time.Now().Add(-100 * time.Millisecond)
	s.applyMove(1, 0.5, 0, 0.5, time.Now())

	if got := b.count(protocol.MsgS2CEntityState); got != 1 {
		t.Fatalf("b should receive 1 EntityState, got %d", got)
	}
	// 自己不该收到自己的移动广播。
	if got := a.count(protocol.MsgS2CEntityState); got != 0 {
		t.Fatalf("a should not receive its own EntityState, got %d", got)
	}

	var st protocol.EntityState
	if err := st.Decode(b.last(protocol.MsgS2CEntityState)); err != nil {
		t.Fatalf("decode EntityState: %v", err)
	}
	if st.EntityID != 1 {
		t.Fatalf("state should be for entity 1, got %d", st.EntityID)
	}
	if math.Abs(float64(st.X)-0.5) > 0.01 {
		t.Fatalf("x should be ~0.5, got %v", st.X)
	}
}

func TestMoveIsClampedWhenTooFast(t *testing.T) {
	s := newTestScene()
	a := &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})

	e := s.entities[1]
	e.lastMoveAt = time.Now().Add(-100 * time.Millisecond)

	// 瞬移 500 米，服务端应把它按 6m/s * 100ms * 1.5 + 0.5m 的额度钳制。
	s.applyMove(1, 500, 0, 0, time.Now())

	if e.x > 1.5 {
		t.Fatalf("move should be clamped near 1.4m, got x=%v", e.x)
	}
	if s.rejectedMoves.Load() != 1 {
		t.Fatalf("rejectedMoves should be 1, got %d", s.rejectedMoves.Load())
	}
}

func TestLeaveBroadcastsAndRemovesEntity(t *testing.T) {
	s := newTestScene()
	a, b := &mockClient{}, &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})
	s.onEnter(&msgEnter{id: 2, player: "b", client: b, hasPos: true, x: 1, z: 1})
	b.reset()

	s.onLeave(&msgLeave{entityID: 1})

	if got := b.count(protocol.MsgS2CEntityLeave); got != 1 {
		t.Fatalf("b should receive 1 EntityLeave, got %d", got)
	}
	var leave protocol.EntityLeave
	if err := leave.Decode(b.last(protocol.MsgS2CEntityLeave)); err != nil {
		t.Fatalf("decode EntityLeave: %v", err)
	}
	if leave.EntityID != 1 {
		t.Fatalf("leave should be for entity 1, got %d", leave.EntityID)
	}
	if s.entityCount.Load() != 1 {
		t.Fatalf("entity count should be 1 after leave, got %d", s.entityCount.Load())
	}
	if _, ok := s.entities[1]; ok {
		t.Fatal("entity 1 should be removed from entities")
	}
	if s.grid.Stats().Cells > 1 {
		t.Fatalf("cell count should not retain empty cells, got %d", s.grid.Stats().Cells)
	}
}

func TestIdleEntityKickedOnTick(t *testing.T) {
	s := newTestScene()
	a := &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})

	// 把它伪装成 1 分钟没发心跳。
	s.entities[1].lastHeartbeat = time.Now().Add(-time.Minute)
	s.doTick()

	if !a.kicked {
		t.Fatal("idle entity should be kicked")
	}
	if s.entityCount.Load() != 0 {
		t.Fatalf("entity count should be 0 after kick, got %d", s.entityCount.Load())
	}
}

func TestDuplicateLeaveIsIgnored(t *testing.T) {
	s := newTestScene()
	a := &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})

	s.onLeave(&msgLeave{entityID: 1})
	s.onLeave(&msgLeave{entityID: 1}) // 重复投递不应把计数打成负数

	if s.entityCount.Load() != 0 {
		t.Fatalf("entity count should be 0, got %d", s.entityCount.Load())
	}
}

// TestCrossCellViewDiff 验证跨格时的视野差集：该进的人进、该走的人走、不多发也不漏发。
//
// 默认配置是 16m 格 + 3×3，视野 48m 见方（以实体所在格为中心）。
// 这里用 60m 的初始距离把两人放在视野外，再逐步靠近/远离。
func TestCrossCellViewDiff(t *testing.T) {
	s := newTestScene()
	a, b := &mockClient{}, &mockClient{}

	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})
	s.onEnter(&msgEnter{id: 2, player: "b", client: b, hasPos: true, x: 60, z: 0})

	if got := a.count(protocol.MsgS2CEntityEnter); got != 0 {
		t.Fatalf("相距 60m 超出视野，不该看见对方，实际收到 %d 条 EntityEnter", got)
	}

	move := func(x float32) {
		t.Helper()
		// 把上次移动时间往前推，绕开超速钳制（否则瞬移会被裁成 0.6m）。
		// 这不是作弊，只是让测试能一步走到目标点。
		s.entities[1].lastMoveAt = time.Now().Add(-10 * time.Second)
		s.applyMove(1, x, 0, 0, time.Now())
	}

	// 靠近到 40m，进入对方视野。
	a.reset()
	b.reset()
	move(40)

	if got := a.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("进入视野后 a 应收到 1 条 EntityEnter，实际 %d", got)
	}
	if got := b.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("进入视野后 b 应收到 1 条 EntityEnter（双向通知），实际 %d", got)
	}
	var enter protocol.EntityEnter
	if err := enter.Decode(a.last(protocol.MsgS2CEntityEnter)); err != nil {
		t.Fatalf("decode EntityEnter: %v", err)
	}
	if enter.EntityID != 2 {
		t.Fatalf("a 收到的 Enter 应指向 b(2)，实际 %d", enter.EntityID)
	}

	// 同格内移动（40 → 44，都在第 2 格）不应该产生任何视野事件。
	a.reset()
	b.reset()
	move(44)
	if n := a.count(protocol.MsgS2CEntityEnter) + a.count(protocol.MsgS2CEntityLeave); n != 0 {
		t.Fatalf("同格移动不该产生 Enter/Leave，实际 %d 条", n)
	}

	// 走远到 100m，离开对方视野。
	a.reset()
	b.reset()
	move(100)
	if got := a.count(protocol.MsgS2CEntityLeave); got != 1 {
		t.Fatalf("离开视野后 a 应收到 1 条 EntityLeave，实际 %d", got)
	}
	if got := b.count(protocol.MsgS2CEntityLeave); got != 1 {
		t.Fatalf("离开视野后 b 应收到 1 条 EntityLeave，实际 %d", got)
	}

	// 再走回来，事件必须重新出现，且不能重复累计。
	a.reset()
	b.reset()
	move(40)
	if got := a.count(protocol.MsgS2CEntityEnter); got != 1 {
		t.Fatalf("重新进入视野应再次收到 1 条 EntityEnter，实际 %d", got)
	}
}

// TestGridStaysInSyncWithEntityCell 防止索引归属和实体记录的位置漂移。
// 一旦两者不一致，广播会发给错误的格子，表现为"人在眼前但看不见"。
func TestGridStaysInSyncWithEntityCell(t *testing.T) {
	s := newTestScene()
	a := &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})

	points := []struct{ x, z float32 }{
		{0, 0}, {17, 0}, {-33, 21}, {200, -200}, {0, 0},
	}
	for _, p := range points {
		s.entities[1].lastMoveAt = time.Now().Add(-10 * time.Second)
		s.applyMove(1, p.x, 0, p.z, time.Now())

		e := s.entities[1]
		want := s.grid.CellOf(aoi.Pos{X: e.x, Z: e.z})
		if e.cell != want {
			t.Fatalf("位置 (%.1f,%.1f)：实体记录格 %v，但索引应在 %v", e.x, e.z, e.cell, want)
		}
		if st := s.grid.Stats(); st.Entities != 1 {
			t.Fatalf("索引内实体数 = %d，期望 1（移动时漏删旧格）", st.Entities)
		}
	}
}

// TestMoveIntentsCoalescedPerTick 验证同帧内的多次移动只结算一次。
//
// 这既是性能保证也是语义保证：客户端在一个 tick 内发 5 次"我在这"，
// 服务端只需要知道最后那个位置，中间过程既不该广播，也不该消耗广播预算。
func TestMoveIntentsCoalescedPerTick(t *testing.T) {
	s := newTestScene()
	a, b := &mockClient{}, &mockClient{}
	s.onEnter(&msgEnter{id: 1, player: "a", client: a, hasPos: true, x: 0, z: 0})
	s.onEnter(&msgEnter{id: 2, player: "b", client: b, hasPos: true, x: 1, z: 1})
	b.reset()

	for i := 1; i <= 5; i++ {
		s.Move(1, float32(i)*0.1, 0, 0)
	}
	// 把上次移动时间往前推，绕开超速钳制。
	s.entities[1].lastMoveAt = time.Now().Add(-time.Second)
	s.doTick()

	if got := s.entities[1].x; math.Abs(float64(got)-0.5) > 0.001 {
		t.Fatalf("应只结算最后一次意图 x=0.5，实际 x=%v", got)
	}
	if got := b.count(protocol.MsgS2CEntityState); got != 1 {
		t.Fatalf("同帧 5 次移动应只广播 1 次，实际 %d 次", got)
	}
	if got := s.moveIntents.Load(); got != 5 {
		t.Fatalf("意图计数应为 5，实际 %d", got)
	}
	if got := s.moveApplied.Load(); got != 1 {
		t.Fatalf("位移计数应为 1，实际 %d", got)
	}
}

// TestMoveIntentForUnknownEntityIgnored 保证迟到的移动不会引发 panic，也不会残留。
// 客户端在 Leave 之后继续发包是常态（网络乱序），这里必须安静地丢掉。
func TestMoveIntentForUnknownEntityIgnored(t *testing.T) {
	s := newTestScene()
	s.Move(9999, 1, 0, 1)
	s.doTick()

	if got := s.moveApplied.Load(); got != 0 {
		t.Fatalf("未知实体的意图不该被结算，实际 %d", got)
	}
	if n := len(s.intents.drainTo(nil)); n != 0 {
		t.Fatalf("意图缓冲应已清空，实际残留 %d 条", n)
	}
}

// TestIntentBufferConcurrent 并发写入 + 并发 drain，验证两件事：
//  1. 不会 panic（分片锁的临界区没有共享 map 访问）
//  2. 一条意图不会被结算两次（clear 与遍历在同一把锁内）
//
// 注意：真正的数据竞争检测需要 -race，本机没装 gcc 跑不了，
// 这个用例只能覆盖逻辑不变量。
func TestIntentBufferConcurrent(t *testing.T) {
	var buf intentBuffer
	const writers, perWriter = 8, 500

	var drained atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			drained.Add(int64(len(buf.drainTo(nil))))
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id := uint64(w*perWriter + i)
				buf.set(id, float32(i), 0, float32(w))
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	<-done
	drained.Add(int64(len(buf.drainTo(nil))))

	total := int64(writers * perWriter)
	if got := drained.Load(); got > total {
		t.Fatalf("结算次数 %d 超过写入次数 %d：同一条意图被算了两次", got, total)
	}
	if got := drained.Load(); got == 0 {
		t.Fatal("并发 drain 没取到任何意图")
	}
}
