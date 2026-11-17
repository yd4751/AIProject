// Package authtoken 实现一个无状态的登录令牌。
//
// 格式：playerID:unixTs:hex(HMAC-SHA256(secret, playerID:unixTs))
//
// M1 阶段由 Gate 直接验签，不依赖独立的登录服。等 Login 服务拆出去之后，
// 签发逻辑挪到 Login，验签逻辑留在这里，两边共用同一份实现。
package authtoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Sign 生成令牌。ts 使用 Unix 秒。
func Sign(secret, playerID string, ts int64) string {
	payload := playerID + ":" + strconv.FormatInt(ts, 10)
	return payload + ":" + mac(secret, payload)
}

// Verify 验签并检查是否过期。ttl <= 0 表示不检查时间。
func Verify(secret, token string, ttl time.Duration, now time.Time) (string, bool) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return "", false
	}
	playerID, rawTs, sig := parts[0], parts[1], parts[2]
	if playerID == "" {
		return "", false
	}
	ts, err := strconv.ParseInt(rawTs, 10, 64)
	if err != nil {
		return "", false
	}

	expected := mac(secret, playerID+":"+rawTs)
	// 用固定时间比较，避免时序侧信道。
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return "", false
	}

	if ttl > 0 {
		issued := time.Unix(ts, 0)
		if now.Sub(issued) > ttl || issued.Sub(now) > time.Minute {
			return "", false
		}
	}
	return playerID, true
}

func mac(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}
