package ghsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// sealFormat is the first byte of a sealed credential.
const sealFormat byte = 1

const sealInfo = "ervisio-repo github credentials v1"

// Purposes bound into the associated data of a sealed value, so that the
// webhook secret of a connection cannot be swapped with its credential.
const (
	purposeSecret  = "secret"
	purposeWebhook = "webhook"
)

// errNoKey means connections cannot be stored: there is no master key.
var errNoKey = errors.New("GitHub connections need security.master_key_file")

// errSealed means a stored credential cannot be opened.
var errSealed = errors.New("cannot open the GitHub credential (master key missing or changed)")

// sealer encrypts credentials at rest with AES-256-GCM under a key derived
// with HKDF-SHA256 from the master key. Unlike the TOTP sealer it never
// stores anything in clear: without a key it refuses to seal.
type sealer struct{ aead cipher.AEAD }

func newSealer(master []byte) (*sealer, error) {
	if len(master) == 0 {
		return &sealer{}, nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("github credential key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("github credential key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("github credential key: %w", err)
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) ok() bool { return s != nil && s.aead != nil }

func aad(id uuid.UUID, purpose string) []byte { return append(id[:], purpose...) }

func (s *sealer) seal(plain []byte, id uuid.UUID, purpose string) ([]byte, error) {
	if !s.ok() {
		return nil, errNoKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	out := append([]byte{sealFormat}, nonce...)
	return s.aead.Seal(out, nonce, plain, aad(id, purpose)), nil
}

func (s *sealer) open(stored []byte, id uuid.UUID, purpose string) ([]byte, error) {
	if !s.ok() || len(stored) < 1 || stored[0] != sealFormat || len(stored) < 1+s.aead.NonceSize() {
		return nil, errSealed
	}
	n := s.aead.NonceSize()
	out, err := s.aead.Open(nil, stored[1:1+n], stored[1+n:], aad(id, purpose))
	if err != nil {
		return nil, errSealed
	}
	return out, nil
}
