package storagecfg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// sealFormat is the first byte of a sealed credential: format | nonce | ciphertext.
const sealFormat byte = 1

// sealInfo is the HKDF info string of the storage credential key. It is
// distinct from the ones of the Actions secrets and the GitHub tokens, so the
// keys derived from the master key never coincide.
const sealInfo = "ervisio-repo storage credentials v1"

// ErrNoMasterKey means credentials cannot be stored or read without the
// master key (security.master_key_file).
var ErrNoMasterKey = errors.New("storage credentials need the master key (security.master_key_file)")

var errUnseal = errors.New("a stored storage credential cannot be decrypted with the master key")

// sealer encrypts storage credentials with AES-256-GCM under a key derived
// from the master key. The AAD binds a value to its target and field name, so
// a sealed value cannot be moved to another target. There is deliberately no
// clear-text fallback: without a master key every operation fails.
type sealer struct{ aead cipher.AEAD }

func newSealer(master []byte) (*sealer, error) {
	if len(master) == 0 {
		return &sealer{}, nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("storage credential key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("storage credential key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("storage credential key: %w", err)
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) ok() bool { return s != nil && s.aead != nil }

func secretAAD(target, field string) []byte { return []byte("storage|" + target + "|" + field) }

// seal returns the credential as base64 text, ready for the JSON document.
func (s *sealer) seal(plain, target, field string) (string, error) {
	if !s.ok() {
		return "", ErrNoMasterKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seal storage credential: %w", err)
	}
	out := append([]byte{sealFormat}, nonce...)
	out = s.aead.Seal(out, nonce, []byte(plain), secretAAD(target, field))
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *sealer) open(stored, target, field string) (string, error) {
	if !s.ok() {
		return "", ErrNoMasterKey
	}
	raw, err := base64.StdEncoding.DecodeString(stored)
	ns := s.aead.NonceSize()
	if err != nil || len(raw) < 1+ns+s.aead.Overhead() || raw[0] != sealFormat {
		return "", errUnseal
	}
	plain, err := s.aead.Open(nil, raw[1:1+ns], raw[1+ns:], secretAAD(target, field))
	if err != nil {
		return "", errUnseal
	}
	return string(plain), nil
}
