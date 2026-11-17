package persist

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore 把玩家档案存到 Redis，实现 Store 接口。
//
// 和 MemoryStore/FileStore 的唯一区别是"数据放哪"：这里放 Redis，
// 多进程/多节点共享同一份档案，玩家在 A 节点下线、B 节点重连也能
// 拿回血量和等级。业务层（player 包）完全无感知——它只认 Store 接口。
//
// 键设计：<prefix><player>，prefix 默认 "player:"。加前缀是为了和
// Redis 里其他数据（会话、排行榜）隔离开，避免键冲突。
type RedisStore struct {
	cli    *redis.Client
	prefix string
	ttl    time.Duration // 0 表示永不过期
}

// NewRedisStore 创建 Redis 后端。addr 是单地址（host:port），也支持
// 传入逗号分隔的多个地址走 Cluster（见 redis.ParseURL / UniversalClient，
// 这里刻意只接单机，够用且简单）。构造时 Ping 一次，连不上直接报错——
// 持久化后端必须在启动时就可用，不能等运行期才发现连不上。
func NewRedisStore(addr, password string, db int, prefix string) (*RedisStore, error) {
	cli := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := cli.Ping(ctx).Err(); err != nil {
		cli.Close()
		return nil, err
	}
	return &RedisStore{cli: cli, prefix: prefix}, nil
}

// SetTTL 给档案设置过期时间，0 表示永不过期。掉线的玩家档案默认
// 长期保留（跨区迁移要能读到）；真要清理僵尸档案再调这个方法。
func (s *RedisStore) SetTTL(ttl time.Duration) { s.ttl = ttl }

// key 返回某个玩家的 Redis 键。
func (s *RedisStore) key(player string) string { return s.prefix + player }

// Get 读档案。键不存在返回 (nil, false)，和 MemoryStore/FileStore 语义一致。
func (s *RedisStore) Get(player string) ([]byte, bool) {
	b, err := s.cli.Get(context.Background(), s.key(player)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false
		}
		// 其他错误（超时、断连）也当"没读到"处理：玩家档案是尽力而为的
		// 兜底，读不到就用默认属性，不能因为 Redis 抖动就拒绝玩家进场。
		return nil, false
	}
	return b, true
}

// Put 写档案。可选 TTL，写失败返回错误（由上层决定是否重试/告警）。
func (s *RedisStore) Put(player string, data []byte) error {
	return s.cli.Set(context.Background(), s.key(player), data, s.ttl).Err()
}

// Delete 删档案（销号）。键不存在不算错。
func (s *RedisStore) Delete(player string) error {
	err := s.cli.Del(context.Background(), s.key(player)).Err()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Close 释放连接。server 优雅退出时调用。
func (s *RedisStore) Close() error { return s.cli.Close() }
