// Package persist 是玩家档案的持久化后端。
//
// 和 world 层的存档（M5）是两回事：M5 存的是"世界状态"（怪物、拓扑版本），
// 这里是"玩家档案"（属性、战斗数据），按玩家名索引，跨场景/跨进程/跨区
// 可迁移——玩家在 A 区下线，B 区重连，还能拿回自己的血量和等级。
//
// 刻意以接口为中心：业务层（player 包）只认 Store，换后端零改动。
// 目前的实现分两类：
//   - 无外部依赖：MemoryStore（进程内 map，单测/单机演示）、
//     FileStore（JSON 文件，多进程/跨重启）
//   - 网络后端：RedisStore（在线缓存）、MySQLStore（最终档案），
//     以及把两者叠成"缓存 + 真相"的 LayeredStore
//
// 网络后端需要释放连接池，额外实现 Closer；进程退出时由
// cmd/server 的 playerStoreComponent 统一 Close。要接别的数据库，
// 实现 Store（可选 Closer）即可，业务层零改动。
package persist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store 是玩家档案的键值存储。键是玩家名，值是 JSON 序列化的档案。
//
// 为什么值用 []byte 而不是强类型：Store 不该知道"档案里有什么字段"，
// 那是业务层（creature/combat）的事。Store 只管"按名字存取一段字节"，
// 这样换后端（Redis、数据库）时不用跟着业务字段的增删改。
type Store interface {
	// Get 返回玩家档案。不存在时 ok=false，不报错——"没存档"是常态
	// （新玩家第一次登录），不是异常。
	Get(player string) ([]byte, bool)
	// Put 写入玩家档案。
	Put(player string, data []byte) error
	// Delete 删除玩家档案（销号用）。
	Delete(player string) error
}

// Closer 表示需要释放底层资源（连接池、句柄）的存储后端。
//
// Redis/MySQL 这类网络连接型后端实现了它；进程退出时由 playerStoreComponent
// 统一 Close，释放连接。MemoryStore/FileStore 不需要释放，自然不实现这个接口。
type Closer interface {
	Close() error
}

// MemoryStore 是进程内的 map 实现，线程安全。
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

// NewMemoryStore 创建空的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: make(map[string][]byte)}
}

func (s *MemoryStore) Get(player string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.m[player]
	if !ok {
		return nil, false
	}
	// 返回副本，避免调用方改到共享切片。
	out := make([]byte, len(b))
	copy(out, b)
	return out, true
}

func (s *MemoryStore) Put(player string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	s.m[player] = cp
	return nil
}

func (s *MemoryStore) Delete(player string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, player)
	return nil
}

// FileStore 把档案落成 JSON 文件，每个玩家一个文件。
//
// 落盘格式是 { "player": ..., "data": <base64> } 的 JSON 对象。用 JSON
// 包一层（而不是裸字节）是为了让档案文件能被人眼和工具直接看懂——
// 运维排查"这个玩家的血量为什么不对"时能直接 cat 文件。
type FileStore struct {
	dir string
	mu  sync.Mutex // 保护并发写同一个文件；不同文件之间其实无冲突，但省事
}

// NewFileStore 创建文件存储。dir 必须已存在（调用方负责 mkdir）。
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir}
}

// filePath 返回玩家档案文件的路径。玩家名可能含路径分隔符，
// 这里用 base64 或 hash 做文件名会更好，但玩家名通常是 [a-zA-Z0-9_]+，
// 直接拼路径够用；真要支持任意字符串再改文件名编码。
func (s *FileStore) filePath(player string) string {
	return filepath.Join(s.dir, player+".json")
}

// fileRecord 是落盘文件的 JSON 结构。
type fileRecord struct {
	Player string `json:"player"`
	Data   []byte `json:"data"` // base64 自动编码
}

func (s *FileStore) Get(player string) ([]byte, bool) {
	b, err := os.ReadFile(s.filePath(player))
	if err != nil {
		return nil, false
	}
	var rec fileRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, false
	}
	return rec.Data, true
}

func (s *FileStore) Put(player string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 先写临时文件再 rename，和 world 存档同一个理由：进程可能任意
	// 时刻被杀，直接覆盖会留下半截文件。
	tmp := s.filePath(player) + ".tmp"
	rec := fileRecord{Player: player, Data: data}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath(player))
}

func (s *FileStore) Delete(player string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.filePath(player)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
