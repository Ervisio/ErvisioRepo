package actions

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const boxInfo = "ervisio-repo actions secrets public key v1"

// ErrSealedSecret means an encrypted secret value cannot be opened: wrong
// key id, or not a sealed box for the public key.
var ErrSealedSecret = errors.New("the encrypted value cannot be decrypted with this key")

// boxKeys is the Curve25519 key pair GitHub clients seal secret values
// for (libsodium crypto_box_seal), derived from the master key.
type boxKeys struct {
	id        string
	pub, priv [32]byte
}

func newBoxKeys(master []byte) (*boxKeys, error) {
	if len(master) == 0 {
		return nil, nil //nolint:nilnil // no master key: no key pair
	}
	priv, err := hkdf.Key(sha256.New, master, nil, boxInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("actions secrets public key: %w", err)
	}
	k := &boxKeys{}
	copy(k.priv[:], priv)
	pub, err := curve25519.X25519(k.priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("actions secrets public key: %w", err)
	}
	copy(k.pub[:], pub)
	sum := sha256.Sum256(k.pub[:])
	// GitHub key ids are decimal strings.
	k.id = strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])>>1, 10)
	return k, nil
}

// SecretsPublicKey returns the key id and the public key that clients
// seal secret values for, as GitHub's actions/secrets/public-key.
func (s *Service) SecretsPublicKey() (string, []byte, error) {
	if s.box == nil {
		return "", nil, ErrNoMasterKey
	}
	return s.box.id, s.box.pub[:], nil
}

// OpenSealedSecret decrypts a secret value sealed for SecretsPublicKey.
func (s *Service) OpenSealedSecret(keyID string, sealed []byte) (string, error) {
	if s.box == nil {
		return "", ErrNoMasterKey
	}
	if keyID != s.box.id {
		return "", ErrSealedSecret
	}
	plain, ok := box.OpenAnonymous(nil, sealed, &s.box.pub, &s.box.priv)
	if !ok || !utf8.Valid(plain) {
		return "", ErrSealedSecret
	}
	return string(plain), nil
}
