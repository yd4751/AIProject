// Package store 基于 SQLite 提供授权规则、客户端信息与流量历史的持久化。
// 使用 modernc.org/sqlite 纯 Go 驱动，无 CGO，便于跨平台交叉编译。
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store 数据库封装。
type Store struct {
	db *sql.DB
}

// Rule 一条授权规则（IP 或机器码，允许/拒绝）。
type Rule struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`   // ip | machine
	Value     string `json:"value"`  // 支持 * 通配符与 CIDR
	Action    string `json:"action"` // allow | deny
	Note      string `json:"note"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

// ClientRecord 客户端的持久化信息。
type ClientRecord struct {
	MachineID string `json:"machine_id"`
	IP        string `json:"ip"`
	Hostname  string `json:"hostname"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	BytesUp   int64  `json:"bytes_up"`
	BytesDown int64  `json:"bytes_down"`
}

// PortMapping 运行时新增的端口映射（持久化，重启后自动恢复）。
type PortMapping struct {
	ID         int64  `json:"id"`
	MachineID  string `json:"machine_id"` // 归属客户端机器码
	Name       string `json:"name"`       // 服务名
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
	Note       string `json:"note"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
}

// TrafficRecord 一条已结束隧道的流量记录。
type TrafficRecord struct {
	ID          int64  `json:"id"`
	ConnID      uint64 `json:"conn_id"`
	MachineID   string `json:"machine_id"`
	Service     string `json:"service"`
	RemoteIP    string `json:"remote_ip"`
	BytesUp     int64  `json:"bytes_up"`
	BytesDown   int64  `json:"bytes_down"`
	DurationSec int64  `json:"duration_sec"`
	EndedAt     int64  `json:"ended_at"`
}

// Open 打开（或创建）SQLite 数据库并初始化表结构。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 写串行，单连接最稳
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS auth_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			value TEXT NOT NULL,
			action TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS clients (
			machine_id TEXT PRIMARY KEY,
			ip TEXT NOT NULL DEFAULT '',
			hostname TEXT NOT NULL DEFAULT '',
			first_seen INTEGER NOT NULL,
			last_seen INTEGER NOT NULL,
			bytes_up INTEGER NOT NULL DEFAULT 0,
			bytes_down INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS traffic (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id INTEGER NOT NULL,
			machine_id TEXT NOT NULL DEFAULT '',
			service TEXT NOT NULL DEFAULT '',
			remote_ip TEXT NOT NULL DEFAULT '',
			bytes_up INTEGER NOT NULL DEFAULT 0,
			bytes_down INTEGER NOT NULL DEFAULT 0,
			duration_sec INTEGER NOT NULL DEFAULT 0,
			ended_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_ended ON traffic(ended_at)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS port_mappings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			machine_id TEXT NOT NULL,
			name TEXT NOT NULL,
			local_ip TEXT NOT NULL,
			local_port INTEGER NOT NULL,
			remote_port INTEGER NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			UNIQUE(machine_id, remote_port)
		)`,
		`CREATE TABLE IF NOT EXISTS disabled_ports (
			machine_id TEXT NOT NULL,
			remote_port INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (machine_id, remote_port)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("初始化表失败: %w", err)
		}
	}
	return nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// ---- 授权规则 ----

func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT id,kind,value,action,note,enabled,created_at FROM auth_rules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Kind, &r.Value, &r.Action, &r.Note, &enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddRule(kind, value, action, note string) (int64, error) {
	if kind != "ip" && kind != "machine" {
		return 0, fmt.Errorf("规则类型必须是 ip 或 machine")
	}
	if action != "allow" && action != "deny" {
		return 0, fmt.Errorf("动作必须是 allow 或 deny")
	}
	res, err := s.db.Exec(
		`INSERT INTO auth_rules(kind,value,action,note,enabled,created_at) VALUES(?,?,?,?,1,?)`,
		kind, value, action, note, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM auth_rules WHERE id=?`, id)
	return err
}

func (s *Store) SetRuleEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE auth_rules SET enabled=? WHERE id=?`, v, id)
	return err
}

// ---- 客户端 ----

func (s *Store) UpsertClient(machineID, ip, hostname string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO clients(machine_id,ip,hostname,first_seen,last_seen)
		VALUES(?,?,?,?,?)
		ON CONFLICT(machine_id) DO UPDATE SET ip=excluded.ip, hostname=excluded.hostname, last_seen=excluded.last_seen`,
		machineID, ip, hostname, now, now)
	return err
}

func (s *Store) AddClientTraffic(machineID string, up, down int64) error {
	_, err := s.db.Exec(`UPDATE clients SET bytes_up=bytes_up+?, bytes_down=bytes_down+?, last_seen=? WHERE machine_id=?`,
		up, down, time.Now().Unix(), machineID)
	return err
}

func (s *Store) ListClients() ([]ClientRecord, error) {
	rows, err := s.db.Query(`SELECT machine_id,ip,hostname,first_seen,last_seen,bytes_up,bytes_down FROM clients ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClientRecord
	for rows.Next() {
		var c ClientRecord
		if err := rows.Scan(&c.MachineID, &c.IP, &c.Hostname, &c.FirstSeen, &c.LastSeen, &c.BytesUp, &c.BytesDown); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- 端口映射 ----

func (s *Store) ListPortMappings(machineID string) ([]PortMapping, error) {
	query := `SELECT id,machine_id,name,local_ip,local_port,remote_port,note,enabled,created_at FROM port_mappings`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id=?`
		args = append(args, machineID)
	}
	query += ` ORDER BY id DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortMapping
	for rows.Next() {
		var m PortMapping
		var enabled int
		if err := rows.Scan(&m.ID, &m.MachineID, &m.Name, &m.LocalIP, &m.LocalPort,
			&m.RemotePort, &m.Note, &enabled, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Enabled = enabled != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddPortMapping(m MachineIDAndPort) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO port_mappings(machine_id,name,local_ip,local_port,remote_port,note,enabled,created_at)
		VALUES(?,?,?,?,?,?,1,?)
		ON CONFLICT(machine_id,remote_port) DO UPDATE SET
			name=excluded.name, local_ip=excluded.local_ip, local_port=excluded.local_port,
			note=excluded.note, enabled=1`,
		m.MachineID, m.Name, m.LocalIP, m.LocalPort, m.RemotePort, m.Note, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MachineIDAndPort 用于新增映射的参数分组。
type MachineIDAndPort struct {
	MachineID  string
	Name       string
	LocalIP    string
	LocalPort  int
	RemotePort int
	Note       string
}

func (s *Store) DeletePortMapping(id int64) error {
	_, err := s.db.Exec(`DELETE FROM port_mappings WHERE id=?`, id)
	return err
}

func (s *Store) SetPortMappingEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE port_mappings SET enabled=? WHERE id=?`, v, id)
	return err
}

// RemotePortOwner 返回占用远端端口的 machine_id。
func (s *Store) RemotePortOwner(remotePort int) (string, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT machine_id FROM port_mappings WHERE remote_port=? AND enabled=1 LIMIT 1`, remotePort).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// ---- 禁用端口（静态配置被实时关闭后，重连不再恢复）----

func (s *Store) AddDisabledPort(machineID string, remotePort int) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO disabled_ports(machine_id,remote_port,created_at) VALUES(?,?,?)`,
		machineID, remotePort, time.Now().Unix())
	return err
}

func (s *Store) RemoveDisabledPort(machineID string, remotePort int) error {
	_, err := s.db.Exec(`DELETE FROM disabled_ports WHERE machine_id=? AND remote_port=?`, machineID, remotePort)
	return err
}

func (s *Store) ListDisabledPorts(machineID string) (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT remote_port FROM disabled_ports WHERE machine_id=?`, machineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]bool)
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, err
		}
		out[port] = true
	}
	return out, rows.Err()
}

// ---- 流量历史 ----

func (s *Store) AddTraffic(r TrafficRecord) error {
	if r.EndedAt == 0 {
		r.EndedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO traffic(conn_id,machine_id,service,remote_ip,bytes_up,bytes_down,duration_sec,ended_at)
		VALUES(?,?,?,?,?,?,?,?)`, r.ConnID, r.MachineID, r.Service, r.RemoteIP, r.BytesUp, r.BytesDown, r.DurationSec, r.EndedAt)
	return err
}

func (s *Store) ListTraffic(limit int) ([]TrafficRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,conn_id,machine_id,service,remote_ip,bytes_up,bytes_down,duration_sec,ended_at
		FROM traffic ORDER BY ended_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficRecord
	for rows.Next() {
		var r TrafficRecord
		if err := rows.Scan(&r.ID, &r.ConnID, &r.MachineID, &r.Service, &r.RemoteIP,
			&r.BytesUp, &r.BytesDown, &r.DurationSec, &r.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HourlyTraffic 按小时聚合最近 N 小时的流量，用于趋势图。
type HourlyPoint struct {
	Hour      int64 `json:"hour"`
	BytesUp   int64 `json:"bytes_up"`
	BytesDown int64 `json:"bytes_down"`
}

func (s *Store) HourlyTraffic(hours int) ([]HourlyPoint, error) {
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	rows, err := s.db.Query(`SELECT ended_at/3600*3600 AS h, SUM(bytes_up), SUM(bytes_down)
		FROM traffic WHERE ended_at>=? GROUP BY h ORDER BY h`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlyPoint
	for rows.Next() {
		var p HourlyPoint
		if err := rows.Scan(&p.Hour, &p.BytesUp, &p.BytesDown); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Totals 累计流量统计。
type Totals struct {
	Connections int64 `json:"connections"`
	BytesUp     int64 `json:"bytes_up"`
	BytesDown   int64 `json:"bytes_down"`
}

func (s *Store) Totals() (Totals, error) {
	var t Totals
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(bytes_up),0), COALESCE(SUM(bytes_down),0) FROM traffic`).Scan(
		&t.Connections, &t.BytesUp, &t.BytesDown)
	return t, err
}

// ---- 设置 ----

func (s *Store) GetSetting(key, fallback string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return fallback
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// PurgeOldTraffic 清理超过 keepDays 天的流量明细，防止数据库无限增长。
func (s *Store) PurgeOldTraffic(keepDays int) error {
	if keepDays <= 0 {
		keepDays = 30
	}
	cutoff := time.Now().Add(-time.Duration(keepDays) * 24 * time.Hour).Unix()
	_, err := s.db.Exec(`DELETE FROM traffic WHERE ended_at < ?`, cutoff)
	return err
}
