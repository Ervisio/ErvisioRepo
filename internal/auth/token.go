package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// TokenBytes is the entropy of every opaque token (sessions, one-time mail
// tokens, invites, CSRF): 256 bits.
const TokenBytes = 32

// NewToken returns a random URL-safe token and the SHA-256 that is stored
// in place of it.
func NewToken() (token string, hash []byte) {
	b := make([]byte, TokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken is the stored form of a token. Tokens carry 256 bits of
// entropy, so an unsalted SHA-256 is enough.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ValidTokenFormat reports whether s looks like a token from NewToken, so
// that garbage never reaches the database.
func ValidTokenFormat(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(TokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}

// EqualTokens compares two tokens in constant time.
func EqualTokens(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
