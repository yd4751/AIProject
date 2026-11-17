package mesh

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestMessageRoundTrip(t *testing.T) {
	admit := &Admit{Token: 7, From: Peer{Node: "node-a", Scene: 3}, Size: 128}
	got, ok := DecodeAdmit(EncodeAdmit(admit))
	if !ok || got.Token != 7 || got.From.Node != "node-a" || got.From.Scene != 3 || got.Size != 128 {
		t.Fatalf("admit roundtrip mismatch: %+v ok=%v", got, ok)
	}

	ack := &AdmitAck{Token: 9, To: Peer{Node: "", Scene: 1}, OK: false, Reason: "draining"}
	gotAck, ok := DecodeAdmitAck(EncodeAdmitAck(ack))
	if !ok || gotAck.Token != 9 || gotAck.OK || gotAck.Reason != "draining" || gotAck.To.Scene != 1 {
		t.Fatalf("ack roundtrip mismatch: %+v ok=%v", gotAck, ok)
	}

	commit := &Commit{
		Token: 11,
		To:    Peer{Node: "node-b", Scene: 2},
		P: Payload{
			ID: 4096, Kind: 1, Player: "玩家-001",
			Client: ClientRef{Node: "node-a", Session: 42},
			X:      512.5, Y: 0, Z: -3.25, Speed: 6,
			Blob: []byte{0x01, 0x02, 0x03},
		},
	}
	gotC, ok := DecodeCommit(EncodeCommit(commit))
	if !ok {
		t.Fatal("commit decode failed")
	}
	if gotC.P.ID != 4096 || gotC.P.Player != "玩家-001" ||
		gotC.P.Client.Node != "node-a" || gotC.P.Client.Session != 42 ||
		gotC.P.X != 512.5 || gotC.P.Z != -3.25 || gotC.P.Speed != 6 ||
		!bytes.Equal(gotC.P.Blob, []byte{0x01, 0x02, 0x03}) || gotC.To.Scene != 2 {
		t.Fatalf("commit roundtrip mismatch: %+v", gotC)
	}

	relay := &Relay{To: Peer{Node: "node-b", Scene: 1}, Entity: 900, Kind: RelayMove, X: 1.5, Y: 0, Z: 2.5}
	gotR, ok := DecodeRelay(EncodeRelay(relay))
	if !ok || gotR.Entity != 900 || gotR.Kind != RelayMove || gotR.Z != 2.5 {
		t.Fatalf("relay roundtrip mismatch: %+v ok=%v", gotR, ok)
	}

	cs := &ClientSend{Client: ClientRef{Node: "node-a", Session: 5}, MsgID: 0x0008, Body: []byte{9, 9}}
	gotCS, ok := DecodeClientSend(EncodeClientSend(cs))
	if !ok || gotCS.MsgID != 0x0008 || gotCS.Client.Session != 5 || !bytes.Equal(gotCS.Body, []byte{9, 9}) {
		t.Fatalf("clientsend roundtrip mismatch: %+v ok=%v", gotCS, ok)
	}

	rb := &Rebind{Client: ClientRef{Node: "node-a", Session: 5}, To: Peer{Node: "node-b", Scene: 4}, Entity: 77, X: 1024, Z: 8}
	gotRB, ok := DecodeRebind(EncodeRebind(rb))
	if !ok || gotRB.Entity != 77 || gotRB.To.Scene != 4 || gotRB.X != 1024 {
		t.Fatalf("rebind roundtrip mismatch: %+v ok=%v", gotRB, ok)
	}

	// 重分区：浮点边界和"有没有连接"这两个字段最容易在编解码里丢，
	// 前者丢了会让接管方算错 Chunk，后者丢了会把 NPC 当成掉线的玩家删掉。
	sc := &SceneCtl{
		Op: SceneOpPrepare, Scene: 3, From: "n1", To: "n2",
		Rect: [4]float32{1024, 0, 1536, 512}, Capacity: 2000, Entities: 137,
		Token: 0xDEADBEEF, Ver: 12, OK: true, Reason: "ok",
	}
	gotSC, ok := DecodeSceneCtl(EncodeSceneCtl(sc))
	if !ok || gotSC.Op != sc.Op || gotSC.Scene != 3 || gotSC.From != "n1" || gotSC.To != "n2" ||
		gotSC.Rect != sc.Rect || gotSC.Capacity != 2000 || gotSC.Entities != 137 ||
		gotSC.Token != sc.Token || gotSC.Ver != 12 || !gotSC.OK || gotSC.Reason != "ok" {
		t.Fatalf("scene ctl roundtrip mismatch: %+v ok=%v", gotSC, ok)
	}

	// 拓扑摘要：条目数、中文节点名、浮点边界都不能丢。
	dg := &TopoDigest{
		From: "节点一", Round: 42,
		Entries: []TopoEntry{
			{Scene: 1, Owner: "n1", Ver: 0, Rect: [4]float32{0, 0, 512, 512}},
			{Scene: 3, Owner: "n2", Ver: 7, Rect: [4]float32{1024, 0, 1536, 512}},
		},
	}
	gotDG, ok := DecodeTopoDigest(EncodeTopoDigest(dg))
	if !ok || gotDG.From != "节点一" || gotDG.Round != 42 || len(gotDG.Entries) != 2 {
		t.Fatalf("digest roundtrip mismatch: %+v ok=%v", gotDG, ok)
	}
	if e := gotDG.Entries[1]; e.Scene != 3 || e.Owner != "n2" || e.Ver != 7 || e.Rect[2] != 1536 {
		t.Fatalf("digest entry mismatch: %+v", e)
	}

	// 空摘要也是一条合法消息：它表示"我一块地都没有"，不是错误。
	empty, ok := DecodeTopoDigest(EncodeTopoDigest(&TopoDigest{From: "n1"}))
	if !ok || len(empty.Entries) != 0 {
		t.Fatalf("empty digest should decode: %+v ok=%v", empty, ok)
	}

	nc := &Commit{Token: 2, To: Peer{Scene: 1}, P: Payload{ID: 5, Kind: 2, NoClient: true}}
	gotNC, ok := DecodeCommit(EncodeCommit(nc))
	if !ok || !gotNC.P.NoClient {
		t.Fatalf("npc commit lost the no-client flag: %+v ok=%v", gotNC, ok)
	}
}

func TestDecodeRejectsTruncated(t *testing.T) {
	full := EncodeCommit(&Commit{Token: 1, To: Peer{Node: "n", Scene: 1}, P: Payload{ID: 1, Blob: []byte{1, 2, 3, 4}}})
	for _, n := range []int{0, 1, 4, len(full) - 2} {
		if _, ok := DecodeCommit(full[:n]); ok {
			t.Fatalf("truncated body of %d bytes was accepted", n)
		}
	}
	// blob 长度字段被改大时必须拒绝，否则接收方会按攻击者给的数字分配内存。
	bad := append([]byte(nil), full...)
	bad[len(bad)-6] = 0x7F
	if _, ok := DecodeCommit(bad); ok {
		t.Fatal("oversized blob length was accepted")
	}
}

// recorder 记录收到的消息，用来断言链路真的把字节送过去了。
type recorder struct {
	mu   sync.Mutex
	msgs []struct {
		id   uint16
		body []byte
	}
	ch chan uint16
}

func (r *recorder) Deliver(msgID uint16, body []byte) {
	r.mu.Lock()
	r.msgs = append(r.msgs, struct {
		id   uint16
		body []byte
	}{msgID, append([]byte(nil), body...)})
	r.mu.Unlock()
	select {
	case r.ch <- msgID:
	default:
	}
}

func (r *recorder) last() (uint16, []byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return 0, nil, false
	}
	m := r.msgs[len(r.msgs)-1]
	return m.id, m.body, true
}

func TestMeshDeliversBetweenNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := New("node-a", quietLogger())
	b := New("node-b", quietLogger())
	rb := &recorder{ch: make(chan uint16, 4)}
	b.SetHandler(rb)

	if err := b.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start b: %v", err)
	}
	defer b.Stop(context.Background())
	if err := a.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start a: %v", err)
	}
	defer a.Stop(context.Background())

	a.AddPeer("node-b", b.Addr())

	commit := &Commit{Token: 3, To: Peer{Node: "node-b", Scene: 1}, P: Payload{ID: 555, Player: "p1"}}
	if err := a.Send("node-b", MsgCommit, EncodeCommit(commit)); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case id := <-rb.ch:
		if id != MsgCommit {
			t.Fatalf("got msgID 0x%04X, want 0x%04X", id, MsgCommit)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("message never arrived")
	}

	_, body, ok := rb.last()
	if !ok {
		t.Fatal("no message recorded")
	}
	got, ok := DecodeCommit(body)
	if !ok || got.P.ID != 555 || got.P.Player != "p1" {
		t.Fatalf("payload corrupted in transit: %+v", got)
	}

	// 统计要对得上：这条连接发了一次、收了一次。
	if st := a.Stat(); st.Sent != 1 {
		t.Fatalf("sender sent=%d, want 1", st.Sent)
	}
	if st := b.Stat(); st.Recv != 1 {
		t.Fatalf("receiver recv=%d, want 1", st.Recv)
	}
}

func TestMeshUnknownPeer(t *testing.T) {
	ctx := context.Background()
	a := New("node-a", quietLogger())
	if err := a.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer a.Stop(ctx)

	err := a.Send("node-z", MsgAdmit, EncodeAdmit(&Admit{Token: 1}))
	if !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("want ErrUnknownPeer, got %v", err)
	}
}

func TestMeshSendBeforeStartFails(t *testing.T) {
	a := New("node-a", quietLogger())
	a.AddPeer("node-b", "127.0.0.1:1")
	if err := a.Send("node-b", MsgAdmit, nil); err == nil {
		t.Fatal("send on a mesh that was never started should fail")
	}
}

func TestMeshSelfSendIsNoop(t *testing.T) {
	a := New("node-a", quietLogger())
	// 本节点自己不该走 mesh：真实路径是 mailbox。这里返回 nil 而不是报错，
	// 是为了让调用方不需要在每次投递前判断"是不是自己"。
	if err := a.Send("node-a", MsgAdmit, nil); err != nil {
		t.Fatalf("self send should be a no-op, got %v", err)
	}
}

// TestClientBatchRoundTrip 验证批量消息的编解码无损。
func TestClientBatchRoundTrip(t *testing.T) {
	in := &ClientBatch{Items: []ClientSend{
		{Client: ClientRef{Node: "node-a", Session: 1}, MsgID: 0x0008, Body: []byte{1, 2, 3}},
		{Client: ClientRef{Node: "node-a", Session: 2}, MsgID: 0x0009, Body: []byte{}},
		{Client: ClientRef{Node: "node-a", Session: 3}, MsgID: 0x000B, Body: []byte{9}},
	}}
	got, ok := DecodeClientBatch(EncodeClientBatch(in))
	if !ok || len(got.Items) != 3 {
		t.Fatalf("batch roundtrip mismatch: %+v ok=%v", got, ok)
	}
	if got.Items[1].Client.Session != 2 || got.Items[1].MsgID != 0x0009 || len(got.Items[1].Body) != 0 {
		t.Fatalf("item[1] mismatch: %+v", got.Items[1])
	}
	if !bytes.Equal(got.Items[0].Body, []byte{1, 2, 3}) {
		t.Fatalf("item[0] body mismatch: %v", got.Items[0].Body)
	}

	// 空批也是合法的（不该发生，但解码不应 panic）。
	empty, ok := DecodeClientBatch(EncodeClientBatch(&ClientBatch{}))
	if !ok || len(empty.Items) != 0 {
		t.Fatalf("empty batch should decode: %+v ok=%v", empty, ok)
	}
}

// TestDecodeClientBatchRejectsTruncated 验证畸形批量被拒绝。
func TestDecodeClientBatchRejectsTruncated(t *testing.T) {
	full := EncodeClientBatch(&ClientBatch{Items: []ClientSend{
		{Client: ClientRef{Node: "n", Session: 1}, MsgID: 0x0008, Body: []byte{1, 2, 3, 4}},
	}})
	for _, n := range []int{0, 1, len(full) - 1} {
		if _, ok := DecodeClientBatch(full[:n]); ok {
			t.Fatalf("truncated batch of %d bytes was accepted", n)
		}
	}
}

// TestIsControlMsg 验证优先级分类：数据走低优先，其余走高优先。
func TestIsControlMsg(t *testing.T) {
	low := map[uint16]bool{MsgRelay: false, MsgClientSend: false, MsgClientBatch: false}
	high := map[uint16]bool{
		MsgAdmit: true, MsgAdmitAck: true, MsgCommit: true,
		MsgRebind: true, MsgReleaseID: true, MsgSceneCtl: true, MsgTopoDigest: true,
	}
	for id, want := range low {
		if got := isControlMsg(id); got != want {
			t.Fatalf("isControlMsg(0x%04X)=%v, want %v", id, got, want)
		}
	}
	for id, want := range high {
		if got := isControlMsg(id); got != want {
			t.Fatalf("isControlMsg(0x%04X)=%v, want %v", id, got, want)
		}
	}
}

// TestMeshBatchesClientSend 验证跨节点下行被合并成 MsgClientBatch 送达。
func TestMeshBatchesClientSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := New("node-a", quietLogger())
	b := New("node-b", quietLogger())
	rb := &recorder{ch: make(chan uint16, 16)}
	b.SetHandler(rb)

	if err := b.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start b: %v", err)
	}
	defer b.Stop(context.Background())
	if err := a.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatalf("start a: %v", err)
	}
	defer a.Stop(context.Background())

	a.AddPeer("node-b", b.Addr())

	// 发 5 条下行消息，应该被合并成（至少）一条 MsgClientBatch。
	for i := 0; i < 5; i++ {
		cs := &ClientSend{Client: ClientRef{Node: "node-b", Session: uint64(i + 1)},
			MsgID: 0x0008, Body: []byte{byte(i)}}
		if err := a.Send("node-b", MsgClientSend, EncodeClientSend(cs)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	// 等 flushLoop 把批发出并送到 b。
	deadline := time.Now().Add(3 * time.Second)
	var got *ClientBatch
	for time.Now().Before(deadline) {
		rb.mu.Lock()
		for _, m := range rb.msgs {
			if m.id == MsgClientBatch {
				batch, ok := DecodeClientBatch(m.body)
				if ok && len(batch.Items) == 5 {
					got = batch
				}
			}
		}
		rb.mu.Unlock()
		if got != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("batched client send never arrived as a single MsgClientBatch")
	}
	for i := range got.Items {
		if got.Items[i].Client.Session != uint64(i+1) || got.Items[i].Body[0] != byte(i) {
			t.Fatalf("item %d corrupted: %+v", i, got.Items[i])
		}
	}

	// 统计：5 条消息被合并，至少 1 次 flush。
	st := a.Stat()
	if st.Batched != 5 {
		t.Fatalf("batched=%d, want 5", st.Batched)
	}
	if st.BatchedFlush < 1 {
		t.Fatalf("batchedFlush=%d, want >=1", st.BatchedFlush)
	}
}
