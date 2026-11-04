package actions

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

// sealFormat is the first byte of a sealed secret:
// format | nonceK | wrapped DEK | nonceD | ciphertext.
const sealFormat byte = 1

const sealInfo = "ervisio-repo actions secrets v1"

// ErrNoMasterKey means secrets cannot be stored or read without
// security.master_key_file.
var ErrNoMasterKey = errors.New("actions secrets need security.master_key_file")

var errSealed = errors.New("actions secret cannot be decrypted")

// sealer envelope-encrypts secrets: every secret has its own random data
// key (DEK), encrypted with AES-256-GCM under a key (KEK) derived with
// HKDF-SHA256 from the master key. Both layers bind the secret scope and
// name as associated data, so a sealed value cannot be moved.
type sealer struct{ kek cipher.AEAD }

func newSealer(master []byte) (*sealer, error) {
	if len(master) == 0 {
		return &sealer{}, nil
	}
	key, err := hkdf.Key(sha256.New, master, nil, sealInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("actions secret key: %w", err)
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, fmt.Errorf("actions secret key: %w", err)
	}
	return &sealer{kek: aead}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *sealer) ok() bool { return s != nil && s.kek != nil }

// secretAAD binds a sealed value to its place.
func secretAAD(scope string, scopeID uuid.UUID, env, name string) []byte {
	return []byte(scope + "|" + scopeID.String() + "|" + env + "|" + name)
}

func (s *sealer) seal(plain, aad []byte) ([]byte, error) {
	if !s.ok() {
		return nil, ErrNoMasterKey
	}
	dek := make([]byte, 32)
	nonceK := make([]byte, s.kek.NonceSize())
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("seal secret: %w", err)
	}
	if _, err := rand.Read(nonceK); err != nil {
		return nil, fmt.Errorf("seal secret: %w", err)
	}
	inner, err := newGCM(dek)
	if err != nil {
		return nil, fmt.Errorf("seal secret: %w", err)
	}
	nonceD := make([]byte, inner.NonceSize())
	if _, err := rand.Read(nonceD); err != nil {
		return nil, fmt.Errorf("seal secret: %w", err)
	}
	out := append([]byte{sealFormat}, nonceK...)
	out = s.kek.Seal(out, nonceK, dek, aad)
	out = append(out, nonceD...)
	return inner.Seal(out, nonceD, plain, aad), nil
}

func (s *sealer) open(stored, aad []byte) ([]byte, error) {
	if !s.ok() {
		return nil, ErrNoMasterKey
	}
	nk := s.kek.NonceSize()
	wrapped := 32 + s.kek.Overhead()
	if len(stored) < 1+nk+wrapped+12 || stored[0] != sealFormat {
		return nil, errSealed
	}
	p := stored[1:]
	dek, err := s.kek.Open(nil, p[:nk], p[nk:nk+wrapped], aad)
	if err != nil {
		return nil, errSealed
	}
	inner, err := newGCM(dek)
	if err != nil {
		return nil, errSealed
	}
	p = p[nk+wrapped:]
	nd := inner.NonceSize()
	if len(p) < nd {
		return nil, errSealed
	}
	out, err := inner.Open(nil, p[:nd], p[nd:], aad)
	if err != nil {
		return nil, errSealed
	}
	return out, nil
}
