package mfa

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"
)

var rfcSecret = []byte("12345678901234567890")

// RFC 4226 appendix D.
func TestHOTPVectors(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := HOTP(rfcSecret, int64(i)); got != w {
			t.Errorf("HOTP(%d) = %s, want %s", i, got, w)
		}
	}
}

// RFC 6238 appendix B (SHA-1), last six digits of the eight-digit codes.
func TestTOTPVectors(t *testing.T) {
	cases := []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}}
	for _, c := range cases {
		now := time.Unix(c.unix, 0)
		if got := HOTP(rfcSecret, TOTPStep(now)); got != c.code {
			t.Errorf("TOTP(%d) = %s, want %s", c.unix, got, c.code)
		}
		step, ok := MatchTOTP(rfcSecret, c.code, now, 0)
		if !ok || step != TOTPStep(now) {
			t.Errorf("MatchTOTP(%d) = %d, %v", c.unix, step, ok)
		}
	}
}

func TestMatchTOTPWindowAndReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cur := TOTPStep(now)
	for _, d := range []int64{-1, 0, 1} {
		if s, ok := MatchTOTP(rfcSecret, HOTP(rfcSecret, cur+d), now, 0); !ok || s != cur+d {
			t.Errorf("skew %d rejected", d)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := MatchTOTP(rfcSecret, HOTP(rfcSecret, cur+d), now, 0); ok {
			t.Errorf("skew %d accepted", d)
		}
	}
	code := HOTP(rfcSecret, cur)
	if _, ok := MatchTOTP(rfcSecret, code, now, cur); ok {
		t.Fatal("code of the last accepted step accepted again")
	}
	if _, ok := MatchTOTP(rfcSecret, HOTP(rfcSecret, cur-1), now, cur); ok {
		t.Fatal("older step accepted after a newer one")
	}
	if _, ok := MatchTOTP(rfcSecret, code[:3]+" "+code[3:], now, 0); !ok {
		t.Fatal("spaced code rejected")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "١٢٣٤٥٦"} {
		if _, ok := MatchTOTP(rfcSecret, bad, now, 0); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTOTPURI(t *testing.T) {
	s, err := NewTOTPSecret()
	if err != nil || len(s) != TOTPSecretSize {
		t.Fatal(err)
	}
	u, err := url.Parse(TOTPURI("Ervisio Repo", "alice", s))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || !strings.Contains(u.Path, "alice") {
		t.Fatalf("uri %s", u)
	}
	if u.Query().Get("secret") != EncodeSecret(s) || u.Query().Get("digits") != "6" {
		t.Fatalf("query %v", u.Query())
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		k := CanonicalRecoveryCode(c)
		if len(k) != 16 || seen[k] {
			t.Fatalf("code %q canonical %q", c, k)
		}
		seen[k] = true
		if CanonicalRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) != k {
			t.Fatalf("variant of %q not canonicalized", c)
		}
		if !bytes.Equal(HashRecoveryCode(k), HashRecoveryCode(k)) {
			t.Fatal("hash not deterministic")
		}
	}
	for _, bad := range []string{"", "abcd", "abcd-efgh-ijkl-mno1", "abcd-efgh-ijkl-mnopq"} {
		if CanonicalRecoveryCode(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSealer(t *testing.T) {
	plain, _ := NewSealer(nil)
	key := bytes.Repeat([]byte{7}, 32)
	sealed, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdefghij")
	aad := []byte("user-1")
	st, err := sealed.Seal(secret, aad)
	if err != nil || bytes.Contains(st, secret) {
		t.Fatalf("seal: %v", err)
	}
	if got, err := sealed.Open(st, aad); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open: %v", err)
	}
	if _, err := sealed.Open(st, []byte("user-2")); err == nil {
		t.Fatal("secret opened for another account")
	}
	if _, err := plain.Open(st, aad); err == nil {
		t.Fatal("sealed secret opened without key")
	}
	other, _ := NewSealer(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(st, aad); err == nil {
		t.Fatal("sealed secret opened with another key")
	}
	pt, _ := plain.Seal(secret, aad)
	if got, err := sealed.Open(pt, aad); err != nil || !bytes.Equal(got, secret) {
		t.Fatal("clear secret not readable after enabling the key")
	}
	if _, err := sealed.Open([]byte{9, 1}, aad); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func FuzzMatchTOTP(f *testing.F) {
	for _, s := range []string{"287082", "287 082", "", "abcdef", "28708２"} {
		f.Add(s, int64(59), int64(0))
	}
	f.Fuzz(func(t *testing.T, code string, unix, last int64) {
		if unix < 0 || unix > 1<<40 {
			return
		}
		now := time.Unix(unix, 0)
		step, ok := MatchTOTP(rfcSecret, code, now, last)
		if !ok {
			return
		}
		if step <= last || step < TOTPStep(now)-1 || step > TOTPStep(now)+1 {
			t.Fatalf("step %d out of window (now %d, last %d)", step, TOTPStep(now), last)
		}
		if HOTP(rfcSecret, step) != NormalizeCode(code) {
			t.Fatalf("code %q accepted for step %d", code, step)
		}
	})
}

func FuzzCanonicalRecoveryCode(f *testing.F) {
	for _, s := range []string{"abcd-efgh-ijkl-mnop", "ABCD EFGH IJKL MNOP", "", "\x00", "abcd-efgh-ijkl-mno1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c := CanonicalRecoveryCode(s)
		if c == "" {
			return
		}
		if len(c) != 16 || CanonicalRecoveryCode(c) != c {
			t.Fatalf("canonical %q of %q not stable", c, s)
		}
		for _, r := range c {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", r) {
				t.Fatalf("canonical %q has %q", c, r)
			}
		}
	})
}
