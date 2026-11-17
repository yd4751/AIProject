package persist

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

// mysqlDSN 从环境变量读测试用的 MySQL DSN，默认连本机 3306 的 openworld 库。
// 没装 MySQL 时 NewMySQLStore 会失败 → 测试自动 skip。若默认库不存在，
// 会尝试建库后再连一次，让本机有 MySQL 的环境能直接跑通集成测试。
func mysqlDSN(t *testing.T) string {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root@tcp(127.0.0.1:3306)/openworld?parseTime=true"
	}
	s, err := NewMySQLStore(dsn)
	if err != nil {
		if createErr := ensureMySQLDB(dsn); createErr == nil {
			s, err = NewMySQLStore(dsn)
		}
	}
	if err != nil {
		t.Skipf("跳过 MySQL 集成测试（连不上 %s）: %v", dsn, err)
	}
	s.Close()
	return dsn
}

// ensureMySQLDB 连到 server（不带库名）建库，供 mysqlDSN 在默认库不存在时自举。
func ensureMySQLDB(dsn string) error {
	server, dbName := splitDSN(dsn)
	if dbName == "" {
		return fmt.Errorf("dsn 不含库名")
	}
	db, err := sql.Open("mysql", server)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS " + dbName); err != nil {
		return err
	}
	return nil
}

// splitDSN 把 "user:pass@tcp(h:p)/db?params" 拆成 "user:pass@tcp(h:p)/" 和 "db"。
func splitDSN(dsn string) (server, db string) {
	i := strings.Index(dsn, "/")
	if i < 0 {
		return dsn, ""
	}
	server = dsn[:i+1]
	rest := dsn[i+1:]
	if j := strings.IndexByte(rest, '?'); j >= 0 {
		rest = rest[:j]
	}
	return server, rest
}

// TestMySQLStoreRoundTrip 验证 MySQL 后端的读写删。连不上 MySQL 自动 skip。
func TestMySQLStoreRoundTrip(t *testing.T) {
	dsn := mysqlDSN(t)
	s, err := NewMySQLStore(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	_ = s.Delete("alice")
	defer s.Delete("alice")

	if _, ok := s.Get("alice"); ok {
		t.Fatal("新键不该有数据")
	}
	if err := s.Put("alice", []byte(`{"hp":100}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	b, ok := s.Get("alice")
	if !ok || string(b) != `{"hp":100}` {
		t.Fatalf("get 应返回写入值, got %q ok=%v", b, ok)
	}
	if err := s.Delete("alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.Get("alice"); ok {
		t.Fatal("删除后不该再读到")
	}
}

// TestMySQLStoreUpsert 验证主键冲突时覆盖（ON DUPLICATE KEY UPDATE）。
// 玩家反复落库必须幂等：后写覆盖先写，不报错、不重复。
func TestMySQLStoreUpsert(t *testing.T) {
	dsn := mysqlDSN(t)
	s, err := NewMySQLStore(dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	_ = s.Delete("bob")
	defer s.Delete("bob")

	if err := s.Put("bob", []byte(`{"hp":100}`)); err != nil {
		t.Fatalf("put 1: %v", err)
	}
	if err := s.Put("bob", []byte(`{"hp":777}`)); err != nil {
		t.Fatalf("put 2: %v", err)
	}
	b, ok := s.Get("bob")
	if !ok || string(b) != `{"hp":777}` {
		t.Fatalf("upsert 后应是最后一次写入, got %q ok=%v", b, ok)
	}
}

// TestMySQLStoreConnectFail 验证连不上时 NewMySQLStore 返回错误。
func TestMySQLStoreConnectFail(t *testing.T) {
	// 用一个几乎不可能监听的端口，期望立即连接失败（connection refused）。
	_, err := NewMySQLStore("root@tcp(127.0.0.1:1)/nope?parseTime=true")
	if err == nil {
		t.Fatal("连不上 MySQL 应返回错误")
	}
}
