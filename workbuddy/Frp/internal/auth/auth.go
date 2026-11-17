// Package auth 实现 IP / 机器码的黑白名单授权判定。
package auth

import (
	"net"
	"strings"

	"frptun/internal/store"
)

// 授权模式
const (
	ModeOff       = "off"       // 不做名单校验，仅 token
	ModeWhitelist = "whitelist" // 只有命中 allow 规则才放行
	ModeBlacklist = "blacklist" // 命中 deny 规则则拒绝，其余放行
)

// Checker 基于持久化的规则做授权判定。
type Checker struct {
	store *store.Store
}

func New(st *store.Store) *Checker { return &Checker{store: st} }

// Check 判定一个客户端是否被允许连接。
// 返回 (allowed bool, reason string)。
func (c *Checker) Check(mode, ip, machineID string) (bool, string) {
	if c == nil || c.store == nil || mode == "" || mode == ModeOff {
		return true, ""
	}
	rules, err := c.store.ListRules()
	if err != nil {
		// 规则读取失败时 fail-open 会绕过安全策略，这里选择拒绝并给出原因。
		return false, "读取授权规则失败: " + err.Error()
	}

	//  separating out host from ip:port
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	ip = strings.TrimSpace(ip)

	matchedAllow := false
	matchedDeny := false
	var denyRule store.Rule

	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		var hit bool
		switch r.Kind {
		case "ip":
			hit = matchIP(ip, r.Value)
		case "machine":
			hit = matchMachine(machineID, r.Value)
		}
		if !hit {
			continue
		}
		if r.Action == "deny" {
			matchedDeny = true
			denyRule = r
		} else if r.Action == "allow" {
			matchedAllow = true
		}
	}

	switch mode {
	case ModeWhitelist:
		if matchedAllow && !matchedDeny {
			return true, ""
		}
		if matchedDeny {
			return false, "命中拒绝规则: " + describe(denyRule)
		}
		return false, "未在白名单中"
	case ModeBlacklist:
		if matchedDeny {
			return false, "命中黑名单规则: " + describe(denyRule)
		}
		return true, ""
	default:
		return true, ""
	}
}

func describe(r store.Rule) string {
	return r.Kind + "=" + r.Value
}

// matchIP 支持精确 IP、CIDR 网段以及 * 通配符（如 192.168.1.*）。
func matchIP(ip, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.Contains(pattern, "*") {
		return wildcardMatch(ip, pattern)
	}
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return cidr.Contains(net.ParseIP(ip))
	}
	return net.ParseIP(pattern).Equal(net.ParseIP(ip))
}

// matchMachine 支持精确机器码与 * 通配符。
func matchMachine(id, pattern string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	return wildcardMatch(strings.ToLower(id), strings.ToLower(pattern))
}

// wildcardMatch 简单的 * / ? 通配匹配（避免引入 filepath.Match 的路径语义）。
func wildcardMatch(s, pattern string) bool {
	sRunes, pRunes := []rune(s), []rune(pattern)
	n, m := len(sRunes), len(pRunes)
	dp := make([][]bool, n+1)
	for i := range dp {
		dp[i] = make([]bool, m+1)
	}
	dp[0][0] = true
	for j := 1; j <= m; j++ {
		if pRunes[j-1] == '*' {
			dp[0][j] = dp[0][j-1]
		}
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			switch {
			case pRunes[j-1] == '*':
				dp[i][j] = dp[i-1][j] || dp[i][j-1]
			case pRunes[j-1] == '?' || pRunes[j-1] == sRunes[i-1]:
				dp[i][j] = dp[i-1][j-1]
			}
		}
	}
	return dp[n][m]
}
