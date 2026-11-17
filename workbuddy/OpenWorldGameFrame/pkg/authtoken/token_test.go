package authtoken

import (
	"testing"
	"time"
)

const testSecret = "test-secret"

func TestSignThenVerify(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token := Sign(testSecret, "player-1", now.Unix())

	pid, ok := Verify(testSecret, token, 5*time.Minute, now.Add(time.Minute))
	if !ok {
		t.Fatalf("token should be valid: %s", token)
	}
	if pid != "player-1" {
		t.Fatalf("player id mismatch: %q", pid)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token := Sign(testSecret, "player-1", now.Unix())

	if _, ok := Verify("another-secret", token, 5*time.Minute, now); ok {
		t.Fatal("token signed with different secret must be rejected")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token := Sign(testSecret, "player-1", now.Unix())

	if _, ok := Verify(testSecret, token, time.Minute, now.Add(2*time.Minute)); ok {
		t.Fatal("expired token must be rejected")
	}
}

func TestVerifyRejectsTamperedPlayerID(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token := Sign(testSecret, "player-1", now.Unix())

	// 把 player-1 改成 player-2，签名不再匹配。
	tampered := "player-2" + token[len("player-1"):]
	if _, ok := Verify(testSecret, tampered, 5*time.Minute, now); ok {
		t.Fatal("tampered token must be rejected")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cases := []string{
		"",
		"onlyonepart",
		"two:parts",
		"a:b:c:d",
		":1700000000:deadbeef",
		"player-1:notanumber:deadbeef",
	}
	for _, c := range cases {
		if _, ok := Verify(testSecret, c, 5*time.Minute, now); ok {
			t.Fatalf("malformed token %q must be rejected", c)
		}
	}
}

func TestVerifyWithNoTTLChecksOnlySignature(t *testing.T) {
	old := time.Unix(1600000000, 0)
	token := Sign(testSecret, "player-1", old.Unix())

	if _, ok := Verify(testSecret, token, 0, time.Now()); !ok {
		t.Fatal("ttl=0 should skip expiry check")
	}
}
