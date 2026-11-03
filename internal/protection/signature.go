package protection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"net/mail"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
)

// signedCommit is a raw commit split into its signature and the payload
// the signature covers (the commit without the gpgsig header).
type signedCommit struct {
	signature      string
	payload        []byte
	committerEmail string
}

// splitCommit parses a raw commit object.
func splitCommit(raw []byte) signedCommit {
	var c signedCommit
	var payload, sig bytes.Buffer
	header, body, _ := bytes.Cut(raw, []byte("\n\n"))
	inSig := false
	for line := range bytes.SplitSeq(header, []byte("\n")) {
		switch {
		case inSig && len(line) > 0 && line[0] == ' ':
			sig.Write(line[1:])
			sig.WriteByte('\n')
			continue
		case bytes.HasPrefix(line, []byte("gpgsig ")) || bytes.HasPrefix(line, []byte("gpgsig-sha256 ")):
			// A SHA-256 repository signs with gpgsig-sha256; a commit
			// carrying both is verified on the first one.
			if sig.Len() == 0 {
				_, v, _ := bytes.Cut(line, []byte(" "))
				sig.Write(v)
				sig.WriteByte('\n')
				inSig = true
			}
			continue
		}
		inSig = false
		if v, ok := bytes.CutPrefix(line, []byte("committer ")); ok {
			if i, j := bytes.IndexByte(v, '<'), bytes.IndexByte(v, '>'); i >= 0 && j > i {
				c.committerEmail = string(v[i+1 : j])
			}
		}
		payload.Write(line)
		payload.WriteByte('\n')
	}
	payload.WriteByte('\n')
	payload.Write(body)
	c.payload = payload.Bytes()
	c.signature = sig.String()
	return c
}

// verifyCommit reports why the raw commit has no verified signature, or ""
// when it is signed by a GPG or SSH key of the account owning the
// committer email (a verified address).
func (e *Engine) verifyCommit(ctx context.Context, raw []byte) (string, error) {
	c := splitCommit(raw)
	if c.signature == "" {
		return "the commit is not signed", nil
	}
	if _, err := mail.ParseAddress(c.committerEmail); err != nil {
		return "the committer email is invalid", nil
	}
	userID, err := e.q.GetProtectionUserByVerifiedEmail(ctx, c.committerEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return "the committer email is not a verified address of any account", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup committer: %w", err)
	}
	switch {
	case strings.HasPrefix(c.signature, "-----BEGIN PGP SIGNATURE-----"):
		return e.verifyGPG(ctx, userID, c)
	case strings.HasPrefix(c.signature, "-----BEGIN SSH SIGNATURE-----"):
		return e.verifySSH(ctx, userID, c)
	}
	return "the signature format is not supported (GPG and SSH are)", nil
}

func (e *Engine) verifyGPG(ctx context.Context, userID uuid.UUID, c signedCommit) (string, error) {
	keys, err := e.q.ListProtectionGPGKeys(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("list gpg keys: %w", err)
	}
	var ring openpgp.EntityList
	for _, k := range keys {
		el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(k.Armored))
		if err != nil {
			continue // stored keys were validated on upload
		}
		ring = append(ring, el...)
	}
	if len(ring) == 0 {
		return "the committer has no GPG key", nil
	}
	signer, err := openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(c.payload), strings.NewReader(c.signature), nil)
	if err != nil || signer == nil {
		return "the GPG signature does not verify with a key of the committer", nil
	}
	return "", nil
}

// sshSig is the SSHSIG envelope (openssh PROTOCOL.sshsig).
type sshSig struct {
	Version       uint32
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

func (e *Engine) verifySSH(ctx context.Context, userID uuid.UUID, c signedCommit) (string, error) {
	pub, ok := verifySSHSignature(c.signature, c.payload)
	if !ok {
		return "the SSH signature is invalid", nil
	}
	keys, err := e.q.ListProtectionSSHKeys(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("list ssh keys: %w", err)
	}
	fp := strings.TrimPrefix(ssh.FingerprintSHA256(pub), "SHA256:")
	for _, k := range keys {
		if strings.TrimPrefix(k.Fingerprint, "SHA256:") == fp {
			return "", nil
		}
	}
	return "the SSH signing key is not a key of the committer", nil
}

// verifySSHSignature checks an armored SSHSIG signature of payload in the
// "git" namespace and returns the signing key.
func verifySSHSignature(armored string, payload []byte) (ssh.PublicKey, bool) {
	const begin, end = "-----BEGIN SSH SIGNATURE-----", "-----END SSH SIGNATURE-----"
	s := strings.TrimSpace(armored)
	if !strings.HasPrefix(s, begin) || !strings.HasSuffix(s, end) {
		return nil, false
	}
	b64 := strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimPrefix(s, begin), end)), "")
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(blob) < 6 || string(blob[:6]) != "SSHSIG" {
		return nil, false
	}
	var env sshSig
	if err := ssh.Unmarshal(blob[6:], &env); err != nil || env.Version != 1 || env.Namespace != "git" {
		return nil, false
	}
	pub, err := ssh.ParsePublicKey(env.PublicKey)
	if err != nil {
		return nil, false
	}
	if _, isCert := pub.(*ssh.Certificate); isCert {
		return nil, false
	}
	var h hash.Hash
	switch env.HashAlgorithm {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return nil, false
	}
	h.Write(payload)
	signed := append([]byte("SSHSIG"), ssh.Marshal(struct {
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Hash          []byte
	}{env.Namespace, env.Reserved, env.HashAlgorithm, h.Sum(nil)})...)
	var sig ssh.Signature
	if err := ssh.Unmarshal(env.Signature, &sig); err != nil {
		return nil, false
	}
	if pub.Verify(signed, &sig) != nil {
		return nil, false
	}
	return pub, true
}
