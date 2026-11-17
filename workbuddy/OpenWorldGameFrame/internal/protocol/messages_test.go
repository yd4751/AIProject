package protocol

import (
	"errors"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	t.Run("LoginReq", func(t *testing.T) {
		in := &LoginReq{Token: "bot-1:1700000000:abcdef"}
		var out LoginReq
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Token != in.Token {
			t.Fatalf("token mismatch: %q != %q", out.Token, in.Token)
		}
	})

	t.Run("LoginResp", func(t *testing.T) {
		in := &LoginResp{Code: CodeAuthFailed, SessionID: 42, PlayerID: "p1", Reason: "expired"}
		var out LoginResp
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if *in != out {
			t.Fatalf("mismatch: %+v != %+v", out, *in)
		}
	})

	t.Run("EnterSceneResp", func(t *testing.T) {
		in := &EnterSceneResp{Code: CodeOK, EntityID: 12345, SceneID: 1, X: 1.5, Y: 0, Z: -2.25}
		var out EnterSceneResp
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.EntityID != in.EntityID || out.X != in.X || out.Z != in.Z {
			t.Fatalf("mismatch: %+v != %+v", out, *in)
		}
	})

	t.Run("Heartbeat", func(t *testing.T) {
		req := &HeartbeatReq{Seq: 7, ClientTime: 1700000000123}
		var reqOut HeartbeatReq
		if err := reqOut.Decode(req.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if reqOut != *req {
			t.Fatalf("req mismatch: %+v != %+v", reqOut, *req)
		}

		resp := &HeartbeatResp{Seq: 7, ServerTime: 1700000000456}
		var respOut HeartbeatResp
		if err := respOut.Decode(resp.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if respOut != *resp {
			t.Fatalf("resp mismatch: %+v != %+v", respOut, *resp)
		}
	})

	t.Run("Move", func(t *testing.T) {
		in := &MoveReq{X: -10.5, Y: 2.0, Z: 33.75, ClientTime: 1}
		var out MoveReq
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out != *in {
			t.Fatalf("mismatch: %+v != %+v", out, *in)
		}
	})

	t.Run("EntityState", func(t *testing.T) {
		in := &EntityState{EntityID: ^uint64(0), X: 1, Y: 2, Z: 3}
		var out EntityState
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out != *in {
			t.Fatalf("mismatch: %+v != %+v", out, *in)
		}
	})

	t.Run("Kick", func(t *testing.T) {
		in := &Kick{Code: CodeKickHeartbeatTimeout, Reason: "heartbeat timeout"}
		var out Kick
		if err := out.Decode(in.Encode()); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out != *in {
			t.Fatalf("mismatch: %+v != %+v", out, *in)
		}
	})
}

func TestReaderRejectsShortBuffer(t *testing.T) {
	// 只给 2 字节，但 U64 需要 8 字节。
	r := NewReader([]byte{0x01, 0x02})
	if v := r.U64(); v != 0 {
		t.Fatalf("expected 0 on short buffer, got %d", v)
	}
	if !errors.Is(r.Err(), ErrShortBuffer) {
		t.Fatalf("expected ErrShortBuffer, got %v", r.Err())
	}
}

func TestReaderStringRejectsTruncatedPayload(t *testing.T) {
	// 声明长度 100，但后面只有 3 字节。
	r := NewReader([]byte{0x00, 0x64, 'a', 'b', 'c'})
	if s := r.String(); s != "" {
		t.Fatalf("expected empty string on truncated payload, got %q", s)
	}
	if !errors.Is(r.Err(), ErrShortBuffer) {
		t.Fatalf("expected ErrShortBuffer, got %v", r.Err())
	}
}

func TestName(t *testing.T) {
	if got := Name(MsgC2SLogin); got != "C2S_Login" {
		t.Fatalf("Name(MsgC2SLogin) = %q", got)
	}
	if got := Name(0xFFFF); got != "Unknown" {
		t.Fatalf("Name(unknown) = %q", got)
	}
}

func TestSkillListRoundTrip(t *testing.T) {
	in := &SkillList{
		Skills: []SkillInfo{
			{ID: 1, Name: "普通攻击", Damage: 100, Range: 3.0, CooldownMs: 0, Level: 0, Requires: 0},
			{ID: 2, Name: "重击", Damage: 250, Range: 3.0, CooldownMs: 3000, Level: 0, Requires: 0, ComboFrom: 1},
			{ID: 4, Name: "连击", Damage: 150, Range: 3.0, CooldownMs: 2000, Level: 0, Requires: 1, ComboFrom: 1, ComboScale: 150},
			{ID: 5, Name: "处决", Damage: 300, Range: 3.0, CooldownMs: 8000, AOE: false, Level: 2, Requires: 2, ComboFrom: 2, ComboScale: 200},
			{ID: 6, Name: "净化之刃", Damage: 80, Range: 3.0, CooldownMs: 5000, Dispel: true},
		},
		Learned: 1<<1 | 1<<6, // 普攻 + 净化之刃 已解锁
	}
	var out SkillList
	if err := out.Decode(in.Encode()); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Skills) != len(in.Skills) {
		t.Fatalf("skill count mismatch: %d != %d", len(out.Skills), len(in.Skills))
	}
	for i := range in.Skills {
		a, b := in.Skills[i], out.Skills[i]
		if a.ID != b.ID || a.Name != b.Name || a.Damage != b.Damage || a.Range != b.Range ||
			a.CooldownMs != b.CooldownMs || a.AOE != b.AOE || a.AOERad != b.AOERad ||
			a.Level != b.Level || a.Requires != b.Requires || a.ComboFrom != b.ComboFrom ||
			a.ComboScale != b.ComboScale || a.Dispel != b.Dispel {
			t.Fatalf("skill[%d] mismatch: %+v != %+v", i, b, a)
		}
	}
	if out.Learned != in.Learned {
		t.Fatalf("learned mismatch: %d != %d", out.Learned, in.Learned)
	}
}

func TestSkillListDecodeRejectsTruncated(t *testing.T) {
	// 声明 3 个技能但 body 在第二个技能中间截断。
	in := &SkillList{Skills: []SkillInfo{
		{ID: 1, Name: "A", Damage: 1, Range: 1, CooldownMs: 1},
		{ID: 2, Name: "B", Damage: 2, Range: 2, CooldownMs: 2},
		{ID: 3, Name: "C", Damage: 3, Range: 3, CooldownMs: 3},
	}, Learned: 1}
	body := in.Encode()
	// 砍掉最后 4 字节（Learned 的一部分）。
	var out SkillList
	if err := out.Decode(body[:len(body)-4]); err == nil {
		t.Fatalf("expected error on truncated body")
	}
}
