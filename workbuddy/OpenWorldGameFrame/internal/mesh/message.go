// Package mesh 是节点间传输层：把"另一个进程里的场景"变成可投递的地址。
//
// 它只做搬运，不懂实体和玩法：
//
//	Peer        场景的全局地址（节点 + 场景号）
//	ClientRef   玩家连接的全局地址（节点 + 会话号）
//	Mesh        这个节点的收发端点，内含到各对等节点的 TCP 长连接
//
// 为什么不用 gRPC：节点间只有六七种小消息，都是"投出去就不管"的语义
// （握手、转发、回写 socket），没有需要同步返回的调用。为它引入 IDL、
// 代码生成和一套 HTTP/2 栈，换来的只是更长的编译时间和更多的依赖，
// 而真正需要的"非阻塞发送 + 队列满就丢弃"恰恰是 RPC 框架默认不提供的。
//
// 节点间链路是**单向**的：A 要发给 B 就自己拨一条到 B 的连接。
// 双向各一条连接，比共享一条再做多路复用简单，也避免了
// "谁先拨号"的协商和半关闭状态。
package mesh

import (
	"math"
	"strconv"
)

// Peer 是一个场景的全局地址。
//
// Node 为空表示"本节点"，这样单进程部署不需要配节点名。
type Peer struct {
	Node  string
	Scene uint32
}

// Local 判断地址是否指向本节点。
func (p Peer) Local(self string) bool { return p.Node == "" || p.Node == self }

func (p Peer) String() string {
	if p.Node == "" {
		return "scene:" + strconv.FormatUint(uint64(p.Scene), 10)
	}
	return p.Node + "/scene:" + strconv.FormatUint(uint64(p.Scene), 10)
}

// ClientRef 是一个玩家连接的全局地址。
//
// Session 用"连接 ID"而不是实体 ID：实体会在节点之间搬家，连接不会。
// 迁移时把这个引用原样带过去，落地节点就有办法把消息写回玩家的 socket。
type ClientRef struct {
	Node    string
	Session uint64
}

func (c ClientRef) String() string {
	return c.Node + "/session:" + strconv.FormatUint(c.Session, 10)
}

// 节点间消息号。和客户端协议（0x0001~0x000D）分在两个号段，
// 这样抓包或者看日志时一眼能分辨是谁的消息。
const (
	// MsgAdmit 是 Prepare：源场景问目标场景"这个人能过来吗"。
	MsgAdmit uint16 = 0x1010
	// MsgAdmitAck 是 Prepare 的结果。
	MsgAdmitAck uint16 = 0x1011
	// MsgCommit 把实体状态交给目标场景。
	MsgCommit uint16 = 0x1012
	// MsgRelay 是接入节点把上行消息转给实体所在的节点。
	MsgRelay uint16 = 0x1020
	// MsgClientSend 是实体所在节点把下行消息写回玩家的 socket。
	MsgClientSend uint16 = 0x1021
	// MsgClientBatch 是把多条 MsgClientSend 合并成一条的下行批量消息。
	//
	// 跨节点玩家的每个广播包都是一条 MsgClientSend，单独组帧 + 过一次
	// 队列 + 抢一次锁。实体数量上千时，这条链路的开销线性放大。合并成
	// 一条 MsgClientBatch 一次发，把 N 次组帧/锁竞争压成 1 次。
	//
	// 它和 MsgClientSend 的语义完全一致（都是 ClientRef+MsgID+Body 的
	// 序列），只是打包传输——接收端拆开逐条处理，客户端无感知。
	MsgClientBatch uint16 = 0x1024
	// MsgRebind 是实体换了节点，通知接入节点改路由。
	MsgRebind uint16 = 0x1022
	// MsgReleaseID 让 ID 的所属节点回收一个在别处死掉的实体 ID。
	MsgReleaseID uint16 = 0x1023
	// MsgSceneCtl 是场景级控制消息：重分区的两阶段提交。
	//
	// 和上面几条的区别是它**不针对某个实体**，而是回答"这块地归谁"。
	// 单开一个消息号而不是塞进 Admit：两者的生命周期完全不同——
	// 迁移是每个玩家一次、以毫秒计；重分区是整个场景一次、以秒计，
	// 而且失败要回滚的是拓扑，不是某个人的位置。
	MsgSceneCtl uint16 = 0x1030
	// MsgTopoDigest 是拓扑对账：把本方认为的"每块地归谁、第几版"整份发过去。
	//
	// 为什么需要它：SceneOpMove 是广播，广播会丢（队列满丢当前帧、链路断、
	// 那一刻对端正好没起来）。漏收的节点会一直保留旧路由，而它自己也
	// 无从知道自己是旧的——拓扑没有"我该是什么样"的参照物。
	//
	// 摘要就是那个参照物：定期互换整份视图，谁新谁赢，差的那一方补上。
	// 它不是"额外的一层保证"，而是把"广播必须送达"这个做不到的前提
	// 换成了"最终一致"这个做得到的性质。
	MsgTopoDigest uint16 = 0x1031
)

// TopoEntry 是一块地的归属声明。
//
// 带 Rect：对端完全不知道这块地时（配置漂移、或者它是一块重分区
// 接管来的地），只有归属没有边界就没法建路由——那等于还是走不过去。
type TopoEntry struct {
	Scene uint32
	// Owner 是节点名。空串表示"归属未定"（配置没写 node 的单进程场景）。
	Owner string
	// Ver 是这块地的归属版本号，单调递增。
	//
	// 只在**归属变化**时加一，不是每次广播加一：版本号回答的是
	// "这块地换过几次主人"，不是"我发过几条消息"。
	Ver  uint64
	Rect [4]float32
}

// TopoDigest 是一份完整的拓扑视图。
type TopoDigest struct {
	From    string
	Round   uint64
	Entries []TopoEntry
}

// SceneOp 是场景控制消息的动作。
//
// 一次成功的重分区走 Prepare → Ready → (搬实体) → Activate → Activated → Move。
// 前两步只是"问一句"，第三步之后才动数据——和实体迁移的三阶段是同一个
// 思路：**能在不承担后果的时候失败，就不要拖到承担之后**。
type SceneOp uint8

const (
	// SceneOpPrepare 让目标节点为场景建一个 standby 实例：能收 Commit，
	// 但还不在路由表里。此时源节点仍持有实体，回滚成本为零。
	SceneOpPrepare SceneOp = iota + 1
	// SceneOpReady 是 Prepare 的结果。
	SceneOpReady
	// SceneOpActivate 让目标把 standby 接进路由。
	SceneOpActivate
	// SceneOpActivated 表示目标已经把场景接进路由（此后它自己认为地是它的）。
	SceneOpActivated
	// SceneOpMove 广播"这个场景的归属已经改成 To"，收到者改自己的路由表。
	SceneOpMove
	// SceneOpAbort 撤销一次重分区：目标销毁 standby，源恢复接客。
	SceneOpAbort
)

// SceneCtl 是一条场景控制消息。
//
// 只带场景级参数，不带实体：实体仍然一个一个走 Commit，复用三阶段迁移的
// 落地路径。为重分区另写一条"整场景打包"的通道看起来更省消息，但那意味着
// 两套落地逻辑（钩子、组件解码、视野重建），很快就会分叉。
type SceneCtl struct {
	Op    SceneOp
	Scene uint32
	// From 是发起者节点，To 是目标节点。都是节点名而不是 Peer：
	// 重分区的语义是"这块地换节点"，场景号不变。
	From string
	To   string
	Rect [4]float32
	// Capacity 是接管方的迁入上限，0 表示不限。
	Capacity int32
	// Entities 是源节点预计搬过来的实体数，目标据此提前判断放不放得下。
	Entities int32
	// Token 区分"哪一次重分区"，防止上一轮的迟到 Ready 把新一轮带到沟里。
	Token uint64
	// Ver 是这次变更之后的归属版本号。
	//
	// 由发起方在 Prepare 时定下、全程不变（Activate 复用它）。目标节点
	// 用它记账，别的节点用它判断"这条 Move 是不是过期了"。没有版本号的话，
	// 一条迟到的旧 Move 会把刚切换好的归属改回去——而两边都认为自己是
	// 最新的，谁也不会纠正。
	Ver    uint64
	OK     bool
	Reason string
}

// RelayKind 区分转发的是哪类上行消息。
//
// 为什么不直接透传客户端的原始字节：处理上行的是实体所在的场景，
// 它需要的是"谁、要做什么"，不是客户端的消息号。由接入节点解一次、
// 节点间传语义，比让每个场景都会解析全部客户端协议要好。
type RelayKind uint8

const (
	RelayMove RelayKind = iota + 1
	RelayHeartbeat
	RelayLeave
	// RelayAttack 是攻击指令：Entity 是攻击者，Target 是受击者，Skill 是技能 ID。
	RelayAttack
	// RelaySkills 是技能目录查询：Entity 是查询者。只借用了 Relay 的
	// 实体路由字段，Target/Skill 不用。
	RelaySkills
)

// Admit 是迁移的第一个阶段。
//
// 带上 Client 而不是等到 Commit 才带：目的节点要在"答应"之前确认
// 自己能找到这个玩家的会话。等到 Commit 才发现找不到，实体已经从源
// 场景摘除了——那是唯一会让玩家永久消失的路径，值得多加这两个字段。
type Admit struct {
	Token  uint64
	From   Peer // 源场景，回复要投给它
	To     Peer
	Size   int32
	Client ClientRef
}

// AdmitAck 是 Prepare 的结果。
//
// To 是源场景地址：token 只在源场景内唯一，接收方靠 To 找到该投给谁。
type AdmitAck struct {
	Token  uint64
	To     Peer
	OK     bool
	Reason string
}

// Commit 把实体状态交给目标场景。
type Commit struct {
	Token uint64
	To    Peer
	P     Payload
}

// Payload 是迁移时搬走的实体状态。
//
// 只带重建实体必需的东西。客户端用 ClientRef 表示，不带任何接口值——
// 跨进程传不了接口，也传不了指向源进程的引用。
type Payload struct {
	ID     uint64
	Kind   uint8
	Player string
	Client ClientRef
	X      float32
	Y      float32
	Z      float32
	Speed  float32
	// NoClient 表示这个实体没有连接（NPC/怪物）。
	//
	// 不能靠 Client 是不是零值来判断：零值也可能是一个合法但还没编号的
	// 会话，而这两种情况的处理完全相反——NPC 补一个空实现就能活，
	// 玩家找不到连接就必须丢掉，否则会在世界里游荡到心跳超时。
	NoClient bool
	Blob     []byte
}

// Relay 是一条需要实体所在场景处理的上行消息。
type Relay struct {
	To     Peer
	Entity uint64
	Kind   RelayKind
	X      float32
	Y      float32
	Z      float32
	// Target 是攻击目标实体 ID，Skill 是技能 ID。仅 RelayAttack 用。
	Target uint64
	Skill  uint8
}

// ClientSend 让接入节点把一条下行消息写进玩家的连接。
type ClientSend struct {
	Client ClientRef
	MsgID  uint16
	Body   []byte
}

// ClientBatch 是把多条 ClientSend 合并成一条的下行批量消息。
type ClientBatch struct {
	Items []ClientSend
}

// Rebind 告诉接入节点：这个会话的实体换到 To 这个场景了。
type Rebind struct {
	Client ClientRef
	To     Peer
	Entity uint64
	X      float32
	Y      float32
	Z      float32
}

// --- 编解码 ---
//
// 手写而不是用 gob/json：节点间消息每一条都在迁移或转发的热路径上，
// 而且格式一旦定了就不该随结构体字段改名而变化。手写的成本是几十行
// append，换来的是"改了字段必须同步改这里"这个强制约束——json 会
// 静默地用零值填上漏掉的字段，那才是真的难查。

type writer struct{ b []byte }

func (w *writer) u8(v uint8) { w.b = append(w.b, v) }
func (w *writer) u16(v uint16) {
	w.b = append(w.b, byte(v>>8), byte(v))
}
func (w *writer) u32(v uint32) {
	w.b = append(w.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
func (w *writer) u64(v uint64) {
	w.b = append(w.b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) str(s string) {
	w.u16(uint16(len(s)))
	w.b = append(w.b, s...)
}
func (w *writer) bytes(b []byte) {
	w.u32(uint32(len(b)))
	w.b = append(w.b, b...)
}
func (w *writer) peer(p Peer) {
	w.str(p.Node)
	w.u32(p.Scene)
}
func (w *writer) client(c ClientRef) {
	w.str(c.Node)
	w.u64(c.Session)
}

type reader struct {
	b   []byte
	bad bool
}

func (r *reader) u8() uint8 {
	if len(r.b) < 1 {
		r.bad = true
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}
func (r *reader) u16() uint16 {
	if len(r.b) < 2 {
		r.bad = true
		return 0
	}
	v := uint16(r.b[0])<<8 | uint16(r.b[1])
	r.b = r.b[2:]
	return v
}
func (r *reader) u32() uint32 {
	if len(r.b) < 4 {
		r.bad = true
		return 0
	}
	v := uint32(r.b[0])<<24 | uint32(r.b[1])<<16 | uint32(r.b[2])<<8 | uint32(r.b[3])
	r.b = r.b[4:]
	return v
}
func (r *reader) u64() uint64 {
	if len(r.b) < 8 {
		r.bad = true
		return 0
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v = v<<8 | uint64(r.b[i])
	}
	r.b = r.b[8:]
	return v
}
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) str() string {
	n := int(r.u16())
	if r.bad || len(r.b) < n {
		r.bad = true
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}
func (r *reader) bytes() []byte {
	n := int(r.u32())
	// 长度上限防的是恶意/错乱的节点让本进程分配一块巨大内存。
	if r.bad || n < 0 || n > 1<<20 || len(r.b) < n {
		r.bad = true
		return nil
	}
	b := make([]byte, n)
	copy(b, r.b[:n])
	r.b = r.b[n:]
	return b
}
func (r *reader) peer() Peer { return Peer{Node: r.str(), Scene: r.u32()} }
func (r *reader) client() ClientRef {
	return ClientRef{Node: r.str(), Session: r.u64()}
}

// EncodeAdmit / DecodeAdmit ...
func EncodeAdmit(m *Admit) []byte {
	w := &writer{b: make([]byte, 0, 32+len(m.From.Node)+len(m.To.Node)+len(m.Client.Node))}
	w.u64(m.Token)
	w.peer(m.From)
	w.peer(m.To)
	w.u32(uint32(m.Size))
	w.client(m.Client)
	return w.b
}

func DecodeAdmit(b []byte) (*Admit, bool) {
	r := &reader{b: b}
	m := &Admit{Token: r.u64(), From: r.peer(), To: r.peer(), Size: int32(r.u32()), Client: r.client()}
	return m, !r.bad
}

func EncodeAdmitAck(m *AdmitAck) []byte {
	w := &writer{b: make([]byte, 0, 24+len(m.To.Node)+len(m.Reason))}
	w.u64(m.Token)
	w.peer(m.To)
	if m.OK {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.str(m.Reason)
	return w.b
}

func DecodeAdmitAck(b []byte) (*AdmitAck, bool) {
	r := &reader{b: b}
	m := &AdmitAck{Token: r.u64(), To: r.peer(), OK: r.u8() == 1, Reason: r.str()}
	return m, !r.bad
}

func EncodeCommit(m *Commit) []byte {
	w := &writer{b: make([]byte, 0, 64+len(m.To.Node)+len(m.P.Player)+len(m.P.Blob))}
	w.u64(m.Token)
	w.peer(m.To)
	w.u64(m.P.ID)
	w.u8(m.P.Kind)
	w.str(m.P.Player)
	w.client(m.P.Client)
	w.f32(m.P.X)
	w.f32(m.P.Y)
	w.f32(m.P.Z)
	w.f32(m.P.Speed)
	if m.P.NoClient {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.bytes(m.P.Blob)
	return w.b
}

func DecodeCommit(b []byte) (*Commit, bool) {
	r := &reader{b: b}
	m := &Commit{Token: r.u64(), To: r.peer()}
	m.P = Payload{
		ID:       r.u64(),
		Kind:     r.u8(),
		Player:   r.str(),
		Client:   r.client(),
		X:        r.f32(),
		Y:        r.f32(),
		Z:        r.f32(),
		Speed:    r.f32(),
		NoClient: r.u8() == 1,
		Blob:     r.bytes(),
	}
	return m, !r.bad
}

func EncodeRelay(m *Relay) []byte {
	w := &writer{b: make([]byte, 0, 48+len(m.To.Node))}
	w.peer(m.To)
	w.u64(m.Entity)
	w.u8(uint8(m.Kind))
	w.f32(m.X)
	w.f32(m.Y)
	w.f32(m.Z)
	w.u64(m.Target)
	w.u8(m.Skill)
	return w.b
}

func DecodeRelay(b []byte) (*Relay, bool) {
	r := &reader{b: b}
	m := &Relay{
		To:     r.peer(),
		Entity: r.u64(),
		Kind:   RelayKind(r.u8()),
		X:      r.f32(),
		Y:      r.f32(),
		Z:      r.f32(),
		Target: r.u64(),
		Skill:  r.u8(),
	}
	return m, !r.bad
}

func EncodeClientSend(m *ClientSend) []byte {
	w := &writer{b: make([]byte, 0, 16+len(m.Client.Node)+len(m.Body))}
	w.client(m.Client)
	w.u16(m.MsgID)
	w.bytes(m.Body)
	return w.b
}

func DecodeClientSend(b []byte) (*ClientSend, bool) {
	r := &reader{b: b}
	m := &ClientSend{Client: r.client(), MsgID: r.u16(), Body: r.bytes()}
	return m, !r.bad
}

// EncodeClientBatch / DecodeClientBatch 是下行批量消息的编解码。
//
// 条目数用 u16：一条下行消息 body 通常几十字节，65535 条已经是几 MB，
// 远超一个 tick 里跨节点下行的量。真到那一步说明该按 tick 批量刷，
// 而不是把单批继续加大。
func EncodeClientBatch(m *ClientBatch) []byte {
	w := &writer{b: make([]byte, 0, 2+len(m.Items)*24)}
	w.u16(uint16(len(m.Items)))
	for _, it := range m.Items {
		w.client(it.Client)
		w.u16(it.MsgID)
		w.bytes(it.Body)
	}
	return w.b
}

func DecodeClientBatch(b []byte) (*ClientBatch, bool) {
	r := &reader{b: b}
	n := int(r.u16())
	if n < 0 || n > 65535 || r.bad {
		return nil, false
	}
	m := &ClientBatch{Items: make([]ClientSend, 0, n)}
	for i := 0; i < n && !r.bad; i++ {
		m.Items = append(m.Items, ClientSend{Client: r.client(), MsgID: r.u16(), Body: r.bytes()})
	}
	return m, !r.bad
}

func EncodeRebind(m *Rebind) []byte {
	w := &writer{b: make([]byte, 0, 40+len(m.Client.Node)+len(m.To.Node))}
	w.client(m.Client)
	w.peer(m.To)
	w.u64(m.Entity)
	w.f32(m.X)
	w.f32(m.Y)
	w.f32(m.Z)
	return w.b
}

func DecodeRebind(b []byte) (*Rebind, bool) {
	r := &reader{b: b}
	m := &Rebind{Client: r.client(), To: r.peer(), Entity: r.u64(), X: r.f32(), Y: r.f32(), Z: r.f32()}
	return m, !r.bad
}

// ReleaseID 表示"这个 ID 的实体已经死了，你可以回收了"。
//
// 只发 ID 不发理由：回收方只需要知道能收，不需要知道死在哪。
type ReleaseID struct{ ID uint64 }

func EncodeReleaseID(m *ReleaseID) []byte {
	w := &writer{b: make([]byte, 0, 8)}
	w.u64(m.ID)
	return w.b
}

func DecodeReleaseID(b []byte) (*ReleaseID, bool) {
	r := &reader{b: b}
	m := &ReleaseID{ID: r.u64()}
	return m, !r.bad
}

func EncodeSceneCtl(m *SceneCtl) []byte {
	w := &writer{b: make([]byte, 0, 48+len(m.From)+len(m.To)+len(m.Reason))}
	w.u8(uint8(m.Op))
	w.u32(m.Scene)
	w.str(m.From)
	w.str(m.To)
	w.f32(m.Rect[0])
	w.f32(m.Rect[1])
	w.f32(m.Rect[2])
	w.f32(m.Rect[3])
	w.u32(uint32(m.Capacity))
	w.u32(uint32(m.Entities))
	w.u64(m.Token)
	w.u64(m.Ver)
	if m.OK {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.str(m.Reason)
	return w.b
}

func DecodeSceneCtl(b []byte) (*SceneCtl, bool) {
	r := &reader{b: b}
	m := &SceneCtl{
		Op:       SceneOp(r.u8()),
		Scene:    r.u32(),
		From:     r.str(),
		To:       r.str(),
		Rect:     [4]float32{r.f32(), r.f32(), r.f32(), r.f32()},
		Capacity: int32(r.u32()),
		Entities: int32(r.u32()),
		Token:    r.u64(),
		Ver:      r.u64(),
		OK:       r.u8() == 1,
		Reason:   r.str(),
	}
	return m, !r.bad
}

// EncodeTopoDigest / DecodeTopoDigest 是拓扑对账的编解码。
//
// 条目数用 u16：一块地 30 字节左右，65535 条目已经是 2MB——远超
// 一条节点链路该承载的量。超了就说明拓扑已经大到该换协议了
// （增量对账而不是整份），而不是把上限调大。
func EncodeTopoDigest(m *TopoDigest) []byte {
	w := &writer{b: make([]byte, 0, 16+len(m.From)+len(m.Entries)*32)}
	w.str(m.From)
	w.u64(m.Round)
	w.u16(uint16(len(m.Entries)))
	for _, e := range m.Entries {
		w.u32(e.Scene)
		w.str(e.Owner)
		w.u64(e.Ver)
		w.f32(e.Rect[0])
		w.f32(e.Rect[1])
		w.f32(e.Rect[2])
		w.f32(e.Rect[3])
	}
	return w.b
}

func DecodeTopoDigest(b []byte) (*TopoDigest, bool) {
	r := &reader{b: b}
	m := &TopoDigest{From: r.str(), Round: r.u64()}
	n := int(r.u16())
	// 每条目至少 4+2+8+16 = 30 字节：先按剩余长度夹一下，
	// 否则一个畸形长度会让下面 make 出一块巨大的切片。
	if max := len(r.b) / 30; n > max {
		return nil, false
	}
	m.Entries = make([]TopoEntry, 0, n)
	for i := 0; i < n && !r.bad; i++ {
		m.Entries = append(m.Entries, TopoEntry{
			Scene: r.u32(),
			Owner: r.str(),
			Ver:   r.u64(),
			Rect:  [4]float32{r.f32(), r.f32(), r.f32(), r.f32()},
		})
	}
	return m, !r.bad
}
