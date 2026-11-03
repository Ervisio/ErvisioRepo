package credentials

import (
	"bytes"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// MaxKeyText bounds the text of a submitted public key.
const (
	MaxSSHKeyText = 16 << 10
	MaxGPGKeyText = 64 << 10
)

// MinRSABits is the smallest RSA key accepted (PLAN §8.1).
const MinRSABits = 3072

// ErrInvalidKey wraps every key parsing error; its message is safe to show.
var ErrInvalidKey = errors.New("invalid key")

// SSHKey is a parsed SSH public key.
type SSHKey struct {
	Type string
	// Fingerprint is the OpenSSH SHA256 fingerprint without "SHA256:".
	Fingerprint string
	// PublicKey is the key in authorized_keys form, without comment.
	PublicKey string
	Bits      int
	Comment   string
}

var sshKeyTypes = map[string]bool{
	ssh.KeyAlgoED25519:    true,
	ssh.KeyAlgoECDSA256:   true,
	ssh.KeyAlgoECDSA384:   true,
	ssh.KeyAlgoECDSA521:   true,
	ssh.KeyAlgoSKED25519:  true,
	ssh.KeyAlgoSKECDSA256: true,
	ssh.KeyAlgoRSA:        true,
}

func invalidKey(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidKey, fmt.Sprintf(format, args...))
}

// ParseSSHKey parses one public key in authorized_keys form ("type base64
// [comment]"). Options, certificates, DSA and RSA keys shorter than
// MinRSABits are refused.
func ParseSSHKey(text string) (SSHKey, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return SSHKey{}, invalidKey("the key is empty")
	}
	if len(text) > MaxSSHKeyText {
		return SSHKey{}, invalidKey("the key is too long")
	}
	if strings.ContainsAny(text, "\r\n") {
		return SSHKey{}, invalidKey("submit a single key on one line")
	}
	pub, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(text))
	if err != nil {
		return SSHKey{}, invalidKey("not an OpenSSH public key")
	}
	if len(options) > 0 {
		return SSHKey{}, invalidKey("authorized_keys options are not allowed")
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return SSHKey{}, invalidKey("submit a single key")
	}
	typ := pub.Type()
	if !sshKeyTypes[typ] {
		return SSHKey{}, invalidKey("unsupported key type %q", typ)
	}
	bits := 0
	switch typ {
	case ssh.KeyAlgoRSA:
		ck, ok := pub.(ssh.CryptoPublicKey)
		if !ok {
			return SSHKey{}, invalidKey("unreadable RSA key")
		}
		rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return SSHKey{}, invalidKey("unreadable RSA key")
		}
		if bits = rk.N.BitLen(); bits < MinRSABits {
			return SSHKey{}, invalidKey("RSA keys must have at least %d bits", MinRSABits)
		}
	case ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519:
		bits = 256
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoSKECDSA256:
		bits = 256
	case ssh.KeyAlgoECDSA384:
		bits = 384
	case ssh.KeyAlgoECDSA521:
		bits = 521
	}
	if len(comment) > 255 {
		comment = comment[:255]
	}
	return SSHKey{
		Type:        typ,
		Fingerprint: strings.TrimPrefix(ssh.FingerprintSHA256(pub), "SHA256:"),
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
		Bits:        bits,
		Comment:     comment,
	}, nil
}
