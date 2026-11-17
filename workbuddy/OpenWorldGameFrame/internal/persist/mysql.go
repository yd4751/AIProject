package persist

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// MySQLStore 把玩家档案存到 MySQL，实现 Store 接口。
//
// 和 RedisStore 一样，它只决定"数据放哪"——这里放 MySQL 这张表，
// 业务层（player 包）完全无感知，只认 Store 接口。区别是定位不同：
// Redis 是"在线缓存"（扛高频读、多节点共享），MySQL 是"最终档案"
// （权威、持久、崩溃后也在）。两者都配时由 LayeredStore 叠成三层蓝图。
//
// 表结构在构造时自建（CREATE TABLE IF NOT EXISTS），无需单独的迁移脚本；
// 单测和部署都能直接跑。data 用 MEDIUMBLOB——档案是 JSON 文本，但 BLOB
// 不会被 MySQL 做字符集转换，跨系统读写最稳。
type MySQLStore struct {
	db *sql.DB
}

// mysqlSchema 是玩家档案表。player 是主键（玩家名），data 是 JSON 字节，
// updated_at 是最近一次落库时间（毫秒），用于排查"这份档案多久没更新"。
const mysqlSchema = `CREATE TABLE IF NOT EXISTS player_records (
	player     VARCHAR(191) NOT NULL,
	data       MEDIUMBLOB   NOT NULL,
	updated_at BIGINT       NOT NULL,
	PRIMARY KEY (player)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// NewMySQLStore 创建 MySQL 后端。dsn 格式和 go-sql-driver 一致，
// 例如 "user:pass@tcp(127.0.0.1:3306)/openworld?parseTime=true"。
// 构造时 Ping 一次（5 秒超时）并自建表，连不上或建表失败直接报错——
// 持久化后端必须在启动时就可用，不能等运行期才发现连不上。
func NewMySQLStore(dsn string) (*MySQLStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, mysqlSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("mysql create table: %w", err)
	}
	return &MySQLStore{db: db}, nil
}

// Get 读档案。没有这一行或任何错误都返回 (nil, false)——玩家档案是
// 尽力而为的兜底，读不到就用默认属性，不能因为 MySQL 抖动就拒绝玩家进场。
func (s *MySQLStore) Get(player string) ([]byte, bool) {
	var data []byte
	err := s.db.QueryRowContext(context.Background(),
		"SELECT data FROM player_records WHERE player = ?", player).Scan(&data)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put 写档案。主键冲突时覆盖（INSERT ... ON DUPLICATE KEY UPDATE），
// 重连落库天然幂等。
func (s *MySQLStore) Put(player string, data []byte) error {
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO player_records (player, data, updated_at) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE data = VALUES(data), updated_at = VALUES(updated_at)`,
		player, data, time.Now().UnixMilli())
	return err
}

// Delete 删档案（销号）。没有这一行不算错。
func (s *MySQLStore) Delete(player string) error {
	_, err := s.db.ExecContext(context.Background(),
		"DELETE FROM player_records WHERE player = ?", player)
	return err
}

// Close 释放连接池。server 优雅退出时调用。
func (s *MySQLStore) Close() error { return s.db.Close() }
