package world

import "sync"

// intentShardCount 是移动意图缓冲的分片数。
//
// 写入方是每个连接的读协程（Gate 侧），读取方是场景的 tick 协程。
// 用一把大锁会让上千个连接在读协程里互相排队；按 entityID 取模分片后，
// 单次写入的临界区只剩一次 map 赋值，冲突概率降到 1/16。
//
// 16 是经验值：再细分收益有限（临界区本来就只有一个赋值），
// 而每片一个 map 会有内存碎片成本。
const intentShardCount = 16

// moveIntent 是一次"状态覆盖"式的移动意图。
//
// 这里刻意没有序号、没有队列。客户端发的不是"我走过了哪几步"，
// 而是"我现在在哪"——同一个 tick 内对同一实体写多次，
// 只有最后一次有意义，直接覆盖即可。
type moveIntent struct {
	x, y, z float32
}

type intentShard struct {
	mu sync.Mutex
	m  map[uint64]moveIntent
}

// intentBuffer 是写后合并（write-behind coalescing）的移动意图缓冲。
//
// 它取代了早期"每条移动 = 一封 mailbox 消息"的做法。区别不只是少一次分配：
//
//  1. 每帧工作量有上界。有 N 个实体，一帧最多就 N 次位移计算，
//     与客户端发包频率无关。作弊客户端每秒发 1000 次移动，
//     对服务端的成本和一个正常客户端完全一样。
//  2. 不再受 mailbox 容量和单帧消息配额约束。旧设计里
//     "实体数 × 移动频率 × 帧长 > 4096" 就会开始积压输入。
//  3. 移动不再分配对象。旧的 post(&msgMove{...}) 每条消息一次堆分配。
//
// 代价是引入了锁。但临界区只有一次 map 赋值（约 20ns），
// 10 万次/秒的写入也才 2ms CPU，比丢掉的可控性好得多。
type intentBuffer struct {
	shards [intentShardCount]intentShard
}

// set 记录一次移动意图。后写覆盖先写。
func (b *intentBuffer) set(id uint64, x, y, z float32) {
	sh := &b.shards[id%intentShardCount]
	sh.mu.Lock()
	if sh.m == nil {
		// 按常见分片人数预分配。扩容要 rehash，热路径上尽量少做。
		sh.m = make(map[uint64]moveIntent, 256)
	}
	sh.m[id] = moveIntent{x: x, y: y, z: z}
	sh.mu.Unlock()
}

// drainTo 取走当前所有意图，追加到 dst 后面并返回。
//
// 必须把 map 里的内容拷出来再清空、然后放锁，不能在持锁期间执行位移逻辑：
// 位移会触发广播（Send 可能阻塞在写队列上），持锁做这件事会把所有
// 上行读协程一起拖住。
//
// clear 而不是重新 make：Go 的 clear 只把 bucket 清零、保留已分配的
// 桶数组，稳态下完全不分配。重新 make 会让每帧都有一次 map 分配，
// 20Hz × 16 分片 = 每秒 320 次无谓分配。
func (b *intentBuffer) drainTo(dst []moveEntry) []moveEntry {
	for i := range b.shards {
		sh := &b.shards[i]
		sh.mu.Lock()
		for id, in := range sh.m {
			dst = append(dst, moveEntry{id: id, x: in.x, y: in.y, z: in.z})
		}
		clear(sh.m)
		sh.mu.Unlock()
	}
	return dst
}

// moveEntry 是 drain 出来的一条待处理位移。
type moveEntry struct {
	id      uint64
	x, y, z float32
}
