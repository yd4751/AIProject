// Package config 负责加载与校验 frptun 配置。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// 角色常量
const (
	RoleServer = "server"
	RoleClient = "client"
)

// Config 是服务端与客户端共用的配置结构，通过 Role 字段决定运行模式。
type Config struct {
	Role string `json:"role"`

	// 服务端监听地址
	BindAddr string `json:"bind_addr"`
	BindPort int    `json:"bind_port"`

	// 客户端连接的服务端地址
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`

	Token string `json:"token"`

	// 管理端（仅服务端生效）
	Admin struct {
		Enabled  bool   `json:"enabled"`
		Addr     string `json:"addr"`     // 监听地址，如 0.0.0.0
		Port     int    `json:"port"`     // 管理端口
		User     string `json:"user"`     // Basic Auth 用户名
		Password string `json:"password"` // Basic Auth 密码
	} `json:"admin"`

	// 数据库与授权（仅服务端生效）
	DBPath   string `json:"db_path"`   // SQLite 文件路径
	AuthMode string `json:"auth_mode"` // off | whitelist | blacklist

	HeartbeatTimeout     int `json:"heartbeat_timeout"`  // 秒，服务端判定客户端存活
	HeartbeatInterval    int `json:"heartbeat_interval"` // 秒，客户端发送心跳
	ReconnectInterval    int `json:"reconnect_interval"` // 秒，重连退避基数
	MaxReconnectInterval int `json:"max_reconnect_interval"`
	MaxConnections       int `json:"max_connections"` // 服务端最大并发隧道数
	ConnectTimeout       int `json:"connect_timeout"` // 秒，建连超时

	Services []Service `json:"services"`
}

// Service 映射一条远端端口到本地地址。
type Service struct {
	Name       string `json:"name"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
}

// Load 从文件加载配置，填充默认值并校验。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s 失败: %w", path, err)
	}
	c := &Config{}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("解析配置 %s 失败: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.BindAddr == "" {
		c.BindAddr = "0.0.0.0"
	}
	if c.BindPort == 0 {
		c.BindPort = 7000
	}
	if c.ServerPort == 0 {
		c.ServerPort = 7000
	}
	if c.HeartbeatTimeout == 0 {
		c.HeartbeatTimeout = 30
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 10
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = 1
	}
	if c.MaxReconnectInterval == 0 {
		c.MaxReconnectInterval = 30
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 10000
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 10
	}
	if c.DBPath == "" {
		c.DBPath = "frptun.db"
	}
	if c.AuthMode == "" {
		c.AuthMode = "off"
	}
	if c.Admin.Addr == "" {
		c.Admin.Addr = "0.0.0.0"
	}
	if c.Admin.Port == 0 {
		c.Admin.Port = 7500
	}
	if c.Admin.User == "" {
		c.Admin.User = "admin"
	}
	for i := range c.Services {
		if c.Services[i].LocalIP == "" {
			c.Services[i].LocalIP = "127.0.0.1"
		}
		if c.Services[i].Name == "" {
			c.Services[i].Name = fmt.Sprintf("svc-%d", c.Services[i].RemotePort)
		}
	}
}

func (c *Config) validate() error {
	switch c.Role {
	case RoleServer, RoleClient:
	default:
		return fmt.Errorf("role 必须是 %q 或 %q，当前为 %q", RoleServer, RoleClient, c.Role)
	}
	if c.Token == "" {
		return fmt.Errorf("token 不能为空，请使用强随机字符串")
	}
	if err := validPort(c.BindPort); err != nil {
		return fmt.Errorf("bind_port: %w", err)
	}
	if err := validPort(c.ServerPort); err != nil {
		return fmt.Errorf("server_port: %w", err)
	}
	if c.HeartbeatInterval >= c.HeartbeatTimeout {
		return fmt.Errorf("heartbeat_interval 必须小于 heartbeat_timeout")
	}
	if c.MaxConnections <= 0 {
		return fmt.Errorf("max_connections 必须为正数")
	}
	if c.Role == RoleClient {
		if c.ServerAddr == "" {
			return fmt.Errorf("客户端模式必须配置 server_addr")
		}
		if len(c.Services) == 0 {
			return fmt.Errorf("客户端模式至少配置一条 services")
		}
	}
	seenPort := make(map[int]bool)
	seenName := make(map[string]bool)
	for i, s := range c.Services {
		if err := validPort(s.LocalPort); err != nil {
			return fmt.Errorf("services[%d].local_port: %w", i, err)
		}
		if err := validPort(s.RemotePort); err != nil {
			return fmt.Errorf("services[%d].remote_port: %w", i, err)
		}
		if seenPort[s.RemotePort] {
			return fmt.Errorf("services[%d].remote_port %d 重复", i, s.RemotePort)
		}
		if seenName[s.Name] {
			return fmt.Errorf("services[%d].name %q 重复", i, s.Name)
		}
		seenPort[s.RemotePort] = true
		seenName[s.Name] = true
	}
	return nil
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("端口 %d 超出范围 1-65535", p)
	}
	return nil
}
