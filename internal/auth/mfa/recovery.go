package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// RecoveryCodeCount is the number of codes in a set.
const RecoveryCodeCount = 10

// recoveryAlphabet is lower-case base32 without padding: 16 characters
// carry 80 bits.
const recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

var recoveryEnc = base32.NewEncoding(recoveryAlphabet).WithPadding(base32.NoPadding)

// NewRecoveryCodes returns n random codes formatted xxxx-xxxx-xxxx-xxxx.
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, n)
	for i := range out {
		var b [10]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("recovery code: %w", err)
		}
		s := recoveryEnc.EncodeToString(b[:])
		out[i] = s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
	}
	return out, nil
}

// CanonicalRecoveryCode returns the 16 code characters, or "" when code
// is not shaped like a recovery code.
func CanonicalRecoveryCode(code string) string {
	c := strings.ToLower(NormalizeCode(code))
	if len(c) != 16 {
		return ""
	}
	// Checked by hand: the base32 decoder skips '\r' and '\n'.
	for i := range len(c) {
		if !strings.ContainsRune(recoveryAlphabet, rune(c[i])) {
			return ""
		}
	}
	return c
}

// HashRecoveryCode returns the stored form of a canonical code. The codes
// carry 80 random bits, so a fast hash is enough.
func HashRecoveryCode(canonical string) []byte {
	h := sha256.Sum256([]byte("erepo-recovery:" + canonical))
	return h[:]
}
