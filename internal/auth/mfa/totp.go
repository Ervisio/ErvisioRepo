// Package mfa implements the second factors (PLAN §8.1): TOTP (RFC 6238),
// WebAuthn credentials and passkeys, single-use recovery codes, the
// two-step login and the instance and organization 2FA policy.
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; authenticator apps expect SHA-1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters: the defaults every authenticator app supports.
const (
	TOTPPeriod     = 30 * time.Second
	TOTPDigits     = 6
	TOTPSecretSize = 20 // 160 bits, as recommended by RFC 4226
	totpSkew       = 1  // accepted steps before and after the current one
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random secret.
func NewTOTPSecret() ([]byte, error) {
	s := make([]byte, TOTPSecretSize)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("totp secret: %w", err)
	}
	return s, nil
}

// EncodeSecret returns the base32 form shown to users.
func EncodeSecret(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPStep returns the RFC 6238 time step of t.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod/time.Second) }

// HOTP returns the RFC 4226 code of secret for counter.
func HOTP(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // steps are non-negative
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", TOTPDigits, bin%1_000_000)
}

// NormalizeCode strips the spaces and dashes users type in codes.
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, code)
}

// ValidTOTPFormat reports whether code looks like a TOTP code.
func ValidTOTPFormat(code string) bool {
	if len(code) != TOTPDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// MatchTOTP checks code against the steps around now that are later than
// last (the last accepted step) and returns the matched step. Every
// candidate is compared in constant time.
func MatchTOTP(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	code = NormalizeCode(code)
	if !ValidTOTPFormat(code) {
		return 0, false
	}
	cur := TOTPStep(now)
	var (
		step  int64
		found int
	)
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		s := cur + d
		eq := subtle.ConstantTimeCompare([]byte(HOTP(secret, s)), []byte(code))
		if eq == 1 && s > last && found == 0 {
			step, found = s, 1
		}
	}
	return step, found == 1
}

// TOTPURI returns the otpauth:// URI of the secret (the QR code content).
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	v := url.Values{}
	v.Set("secret", EncodeSecret(secret))
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(TOTPDigits))
	v.Set("period", fmt.Sprint(int(TOTPPeriod/time.Second)))
	return "otpauth://totp/" + label + "?" + v.Encode()
}
