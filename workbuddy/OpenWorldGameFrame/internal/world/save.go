// 存档与恢复（M5）。
//
// 存档要回答两个问题：
//
//  1. 实体在哪、是什么、带着什么玩法数据（属性、Buff、AI 状态）
//  2. 拓扑归属现在是第几版（哪块地归谁）
//
// 第一个问题复用跨场景迁移已经建立好的 TransferCodec：它本来就是
// "实体玩法数据 <-> 字节" 的编解码器，存档和迁移读的是同一份数据，
// 不存在两套格式漂移。第二个问题是 M3.6 对账埋下的伏笔——对账只解决
// "运行期谁是最新"，但节点全挂重启之后，版本号从 0 重新数，两个节点
// 各自按配置重建了同一块地，谁都不知道别人已经把版本号推到了多少。
// 把归属 + 版本号落盘，重启时从盘里恢复，才能让"先关后开"的节点
// 不输在起跑线上。
//
// 单写者约束：采集和恢复都要遍历实体表、组件存储、ID 分配器，而这三样
// 都属于 tick 协程。所以它们都走 mailbox 投一条指令，由 tick 协程执行，
// 调用方阻塞等待结果（带超时）。这与 Enter/Leave 完全一致，不引入任何
// 新的并发路径——代价只是"保存要等一帧"，对低频的保存操作完全可接受。
package world

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SaveFormatVersion 是存档文件的格式版本号。
//
// 升级格式时加一，读旧档时据此走兼容分支。它独立于玩法层 codec 的
// 版本（那个版本藏在 blob 里），因为两者变更的触发方不同：玩法层加
// 一个组件是玩法的事，world 层改实体/拓扑结构是框架的事。
const SaveFormatVersion = 1

// saveOpTimeout 是走 mailbox 采集/恢复快照的等待上限。
//
// 正常一个 tick（50ms）内必回，取 2 秒是留足重负载下的余量。
// 场景已退休/停止时 mailbox 无人消费，靠它兜底避免调用方死锁。
const saveOpTimeout = 2 * time.Second

// savedEntity 是存档里的一个实体。
//
// 只存"重建一个实体必需的东西"，不存 client（连接是运行时概念，
// 重启后由玩家重新登录重建）、不存 cell（AOI 索引在 insert 时重建）、
// 不存 lastHeartbeat/lastMoveAt（重启后重置为本帧，见 land 的注释）。
type savedEntity struct {
	ID     uint64  `json:"id"`
	Kind   uint8   `json:"kind"`
	Player string  `json:"player,omitempty"`
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Z      float32 `json:"z"`
	Speed  float32 `json:"speed"`
	// Blob 是玩法层编出来的字节（属性/Buff/CD）。base64 编码存储。
	Blob []byte `json:"blob,omitempty"`
}

// savedScene 是存档里的一个场景（本进程承载的那些）。
type savedScene struct {
	ID       uint32        `json:"id"`
	Entities []savedEntity `json:"entities"`
	// Seq 是 ID 分配器的序号。恢复时把它回填，避免重启后从 1 重新发号
	// 撞上还在冷却/在途的旧 ID。它不是"必须"，缺了也只是多花一点
	// ID 空间，但补上更干净。
	Seq uint64 `json:"seq,omitempty"`
}

// savedTopo 是存档里的拓扑归属：每块地归谁、第几版。
type savedTopo struct {
	Scene uint32     `json:"scene"`
	Owner string     `json:"owner"`
	Ver   uint64     `json:"ver"`
	Rect  [4]float32 `json:"rect"`
}

// SaveFile 是整份存档的根结构。
type SaveFile struct {
	Format  int          `json:"format"`
	SavedAt time.Time    `json:"savedAt"`
	Node    string       `json:"node"`
	Topo    []savedTopo  `json:"topo,omitempty"`
	Scenes  []savedScene `json:"scenes,omitempty"`
}

// msgSave 让 tick 协程执行一次快照采集。结果通过 result 通道回传。
type msgSave struct {
	result chan savedScene
}

// msgRestore 让 tick 协程执行一次恢复。结果（恢复的实体数）通过 result 回传。
type msgRestore struct {
	scene  savedScene
	result chan int
}

// snapshot 采集当前场景的实体快照。只在 tick 协程内调用（由 msgSave 分发）。
func (s *Scene) snapshot() savedScene {
	sc := savedScene{ID: s.id}
	sc.Seq = s.ids.seqSnapshot()

	// 实体表只在 tick 协程里可读，这里已经是 tick 协程。
	ids := make([]uint64, 0, len(s.entities))
	for id := range s.entities {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		e := s.entities[id]
		se := savedEntity{
			ID:     e.id,
			Kind:   uint8(e.kind),
			Player: e.player,
			X:      e.x, Y: e.y, Z: e.z,
			Speed: e.speed,
		}
		if len(s.codecs) > 0 {
			se.Blob = s.encodeBlob(e)
		}
		sc.Entities = append(sc.Entities, se)
	}
	return sc
}

// restore 把存档实体重建进场景。只在 tick 协程内调用（由 msgRestore 分发）。
//
// 复用 land 的实体重建路径：insert 进实体表/AOI，进场钩子挂默认组件，
// codec 覆盖成存档值。玩家实体不恢复——连接是运行时的，存档里没有它，
// 玩家重新登录时由 Gate 走 EnterAt 重建；否则会造出一批"没有连接、
// 收不到包、也踢不掉"的僵尸玩家。
func (s *Scene) restore(sc savedScene) int {
	n := 0
	for _, se := range sc.Entities {
		if Kind(se.Kind) == KindPlayer {
			continue
		}
		if _, dup := s.entities[se.ID]; dup {
			continue
		}
		e := &Entity{
			id:     se.ID,
			kind:   Kind(se.Kind),
			player: se.Player,
			x:      se.X, y: se.Y, z: se.Z,
			speed:  se.Speed,
			client: npcClient,
		}
		if e.speed <= 0 {
			e.speed = DefaultSpeed
		}
		e.lastHeartbeat = s.clock()
		e.lastMoveAt = s.clock()
		s.insert(e)
		s.fireEnter(e)
		if len(se.Blob) > 0 {
			s.decodeBlob(e, se.Blob)
		}
		n++
	}
	return n
}

// seqSnapshot 返回 ID 分配器当前序号（供存档）。
func (a *idAlloc) seqSnapshot() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seq
}

// setSeq 恢复 ID 分配器序号。取 max，避免回退。
func (a *idAlloc) setSeq(v uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v > a.seq {
		a.seq = v
	}
}

// SaveWorld 采集本进程的完整快照并序列化为 JSON 字节。
//
// 运行期安全：每个场景的快照通过 mailbox 交给 tick 协程采集，拓扑
// 归属通过 World 的锁读取，二者都无数据竞争。正在搬迁（Moving）或已
// 退休（retired）的场景跳过——它们的实体正在搬走或已经没了。
func (w *World) SaveWorld() ([]byte, error) {
	w.mu.RLock()
	scenes := make([]*Scene, 0, len(w.scenes))
	for _, s := range w.scenes {
		scenes = append(scenes, s)
	}
	w.mu.RUnlock()
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].ID() < scenes[j].ID() })

	f := SaveFile{
		Format:  SaveFormatVersion,
		SavedAt: time.Now(),
		Node:    w.node,
	}

	w.mu.RLock()
	for _, e := range w.topo.Snapshot() {
		f.Topo = append(f.Topo, savedTopo{
			Scene: e.Scene, Owner: e.Owner, Ver: e.Ver, Rect: e.Rect,
		})
	}
	w.mu.RUnlock()

	for _, s := range scenes {
		if s.retired || s.Moving() {
			continue
		}
		sc := s.snapshotViaMailbox()
		f.Scenes = append(f.Scenes, sc)
	}
	return json.MarshalIndent(f, "", "  ")
}

// snapshotViaMailbox 让场景的 tick 协程采集快照，等待结果返回。
//
// 场景未 Start 时没有 tick 协程在跑，直接读 entities/store 是安全的
// （单写者约束只在 tick 协程启动后生效），所以直接采集，不等 mailbox。
func (s *Scene) snapshotViaMailbox() savedScene {
	if !s.started.Load() {
		return s.snapshot()
	}
	res := make(chan savedScene, 1)
	select {
	case s.mailbox <- &msgSave{result: res}:
	case <-time.After(saveOpTimeout):
		return savedScene{ID: s.id}
	}
	select {
	case sc := <-res:
		return sc
	case <-time.After(saveOpTimeout):
		return savedScene{ID: s.id}
	}
}

// restoreViaMailbox 让场景 tick 协程重建存档实体，返回恢复的实体数。
//
// 场景未 Start 时直接重建（无并发，安全）。
func (s *Scene) restoreViaMailbox(sc savedScene) int {
	if !s.started.Load() {
		return s.restore(sc)
	}
	res := make(chan int, 1)
	select {
	case s.mailbox <- &msgRestore{scene: sc, result: res}:
	case <-time.After(saveOpTimeout):
		return 0
	}
	select {
	case n := <-res:
		return n
	case <-time.After(saveOpTimeout):
		return 0
	}
}

// RestoreWorld 从 JSON 字节恢复拓扑版本号和本地场景实体。
//
// 归属**不改**——归属以配置为准（配置是期望，见 config.go 的注释）。
// 只恢复版本号，让对账从盘里的版本继续，而不是从 0 重新数。实体只
// 恢复本进程承载场景里的非玩家实体，玩家靠重新登录进入。
func (w *World) RestoreWorld(data []byte) error {
	var f SaveFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("world: 解析存档失败: %w", err)
	}
	if f.Format != SaveFormatVersion {
		return fmt.Errorf("world: 存档格式版本 %d 不受支持（当前 %d）", f.Format, SaveFormatVersion)
	}

	// 先恢复拓扑版本号。只回填版本，不动归属——归属以配置为准。
	w.mu.Lock()
	for _, t := range f.Topo {
		if _, exists := w.topo.peers[t.Scene]; exists {
			w.topo.setVersion(t.Scene, t.Ver)
		}
	}
	w.mu.Unlock()

	// 恢复场景实体。
	restored := 0
	for _, sc := range f.Scenes {
		s, ok := w.Scene(sc.ID)
		if !ok {
			continue
		}
		s.ids.setSeq(sc.Seq)
		restored += s.restoreViaMailbox(sc)
	}
	if restored > 0 && w.log != nil {
		w.log.Info("world restored", "node", w.node, "entities", restored,
			"scenes", len(f.Scenes), "topo", len(f.Topo))
	}
	return nil
}

// AtomicWrite 把字节原子地写到 path：先写临时文件再 rename。
//
// 原子写的理由和 control/persist.go 一致：进程可能在任意时刻被杀，
// 直接覆盖会留下半截文件，下次启动解析失败、整份存档作废。
func AtomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
