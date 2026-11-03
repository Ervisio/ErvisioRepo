package mfa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Formats of the stored TOTP secret (its first byte).
const (
	secretPlain  byte = 0
	secretSealed byte = 1
)

const sealInfo = "ervisio-repo mfa totp v1"

// Sealer encrypts TOTP secrets at rest with AES-256-GCM under a key derived
// with HKDF-SHA256 from the master key. A Sealer without a key stores them
// in clear (format byte 0) and still opens sealed ones as an error.
type Sealer struct{ aead cipher.AEAD }

// NewSealer derives the TOTP key from master; a nil master gives a Sealer
// that stores secrets in clear.
func NewSealer(master []byte) (*Sealer, error) {
	if master == nil {
		return &Sealer{}, nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("mfa key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mfa key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("mfa key: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// Encrypted reports whether secrets are sealed.
func (s *Sealer) Encrypted() bool { return s.aead != nil }

// Seal returns the stored form of secret; aad binds it to its account.
func (s *Sealer) Seal(secret, aad []byte) ([]byte, error) {
	if s.aead == nil {
		return append([]byte{secretPlain}, secret...), nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	out := append([]byte{secretSealed}, nonce...)
	return s.aead.Seal(out, nonce, secret, aad), nil
}

// ErrSealed means a sealed secret cannot be opened (no or wrong key).
var ErrSealed = errors.New("mfa: cannot open the TOTP secret (master key missing or changed)")

// Open returns the secret of a stored form.
func (s *Sealer) Open(stored, aad []byte) ([]byte, error) {
	if len(stored) < 1 {
		return nil, errors.New("mfa: empty TOTP secret")
	}
	switch stored[0] {
	case secretPlain:
		return stored[1:], nil
	case secretSealed:
		if s.aead == nil || len(stored) < 1+s.aead.NonceSize() {
			return nil, ErrSealed
		}
		n := s.aead.NonceSize()
		out, err := s.aead.Open(nil, stored[1:1+n], stored[1+n:], aad)
		if err != nil {
			return nil, ErrSealed
		}
		return out, nil
	}
	return nil, fmt.Errorf("mfa: unknown TOTP secret format %d", stored[0])
}
