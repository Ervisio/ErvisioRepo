package webhooks

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
)

// sealFormat is the first byte of a sealed secret.
const sealFormat byte = 1

const sealInfo = "ervisio-repo webhook secrets v1"

// sealPurpose is bound into the associated data with the hook id, so that a
// sealed secret cannot be moved to another hook.
const sealPurpose = "webhook"

// sealer encrypts hook secrets at rest with AES-256-GCM under a key derived
// with HKDF-SHA256 from the master key (as internal/ghsync does). Without a
// key it refuses to seal: secrets are never stored in clear.
type sealer struct{ aead cipher.AEAD }

func newSealer(master []byte) (*sealer, error) {
	if len(master) == 0 {
		return &sealer{}, nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("webhook secret key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("webhook secret key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webhook secret key: %w", err)
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) ok() bool { return s != nil && s.aead != nil }

func aad(id uuid.UUID) []byte { return append(id[:], sealPurpose...) }

func (s *sealer) seal(plain []byte, id uuid.UUID) ([]byte, error) {
	if !s.ok() {
		return nil, ErrNoMasterKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal webhook secret: %w", err)
	}
	out := append([]byte{sealFormat}, nonce...)
	return s.aead.Seal(out, nonce, plain, aad(id)), nil
}

func (s *sealer) open(stored []byte, id uuid.UUID) ([]byte, error) {
	if !s.ok() || len(stored) < 1+s.aead.NonceSize() || stored[0] != sealFormat {
		return nil, errSealed
	}
	n := s.aead.NonceSize()
	out, err := s.aead.Open(nil, stored[1:1+n], stored[1+n:], aad(id))
	if err != nil {
		return nil, errSealed
	}
	return out, nil
}
