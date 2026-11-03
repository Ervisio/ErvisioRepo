package credentials

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// GPGKey is a parsed OpenPGP public key.
type GPGKey struct {
	// KeyID is the 16-digit upper-case hex id of the primary key.
	KeyID       string
	Fingerprint string
	SubkeyIDs   []string
	// Emails are the lower-cased addresses of the identities, sorted.
	Emails []string
	// Armored is the key re-armored without private material.
	Armored   string
	ExpiresAt *time.Time
}

func keyIDHex(id uint64) string {
	return strings.ToUpper(hex.EncodeToString(binary.BigEndian.AppendUint64(nil, id)))
}

// ParseGPGKey parses one armored OpenPGP public key. Private keys are
// refused so that they are never stored.
func ParseGPGKey(text string) (GPGKey, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return GPGKey{}, invalidKey("the key is empty")
	}
	if len(text) > MaxGPGKeyText {
		return GPGKey{}, invalidKey("the key is too long")
	}
	block, err := armor.Decode(strings.NewReader(text))
	if err != nil {
		return GPGKey{}, invalidKey("not an armored OpenPGP key")
	}
	if block.Type != openpgp.PublicKeyType {
		return GPGKey{}, invalidKey("submit a public key (BEGIN PGP PUBLIC KEY BLOCK)")
	}
	list, err := openpgp.ReadKeyRing(block.Body)
	if err != nil {
		return GPGKey{}, invalidKey("unreadable OpenPGP key")
	}
	if len(list) != 1 {
		return GPGKey{}, invalidKey("submit exactly one key")
	}
	e := list[0]
	if e.PrivateKey != nil {
		return GPGKey{}, invalidKey("submit a public key")
	}
	pk := e.PrimaryKey
	out := GPGKey{KeyID: keyIDHex(pk.KeyId), Fingerprint: strings.ToUpper(hex.EncodeToString(pk.Fingerprint))}
	for _, sk := range e.Subkeys {
		out.SubkeyIDs = append(out.SubkeyIDs, keyIDHex(sk.PublicKey.KeyId))
	}
	for _, id := range e.Identities {
		if id.UserId == nil || id.UserId.Email == "" {
			continue
		}
		a, err := mail.ParseAddress(id.UserId.Email)
		if err != nil {
			continue
		}
		addr := strings.ToLower(a.Address)
		if !slices.Contains(out.Emails, addr) {
			out.Emails = append(out.Emails, addr)
		}
	}
	slices.Sort(out.Emails)
	if out.SubkeyIDs == nil {
		out.SubkeyIDs = []string{}
	}
	if out.Emails == nil {
		out.Emails = []string{}
	}
	if s, _ := e.PrimarySelfSignature(); s != nil && s.KeyLifetimeSecs != nil && *s.KeyLifetimeSecs > 0 {
		exp := pk.CreationTime.Add(time.Duration(*s.KeyLifetimeSecs) * time.Second)
		out.ExpiresAt = &exp
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return GPGKey{}, invalidKey("unreadable OpenPGP key")
	}
	if err := e.Serialize(w); err != nil {
		return GPGKey{}, invalidKey("unreadable OpenPGP key")
	}
	if err := w.Close(); err != nil {
		return GPGKey{}, invalidKey("unreadable OpenPGP key")
	}
	out.Armored = buf.String()
	if len(out.Armored) > MaxGPGKeyText {
		return GPGKey{}, invalidKey("the key is too long")
	}
	return out, nil
}
