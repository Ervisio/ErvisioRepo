package credentials

import (
	"bytes"
	"crypto/dsa" //nolint:staticcheck // testing that DSA keys are refused
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"golang.org/x/crypto/ssh"
)

func authorized(t testing.TB, k any, comment string) string {
	t.Helper()
	pub, err := ssh.NewPublicKey(k)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		s += " " + comment
	}
	return s
}

func ed25519Key(t testing.TB) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return authorized(t, pub, "alice@laptop")
}

func TestParseSSHKey(t *testing.T) {
	ed := ed25519Key(t)
	k, err := ParseSSHKey("  " + ed + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != ssh.KeyAlgoED25519 || k.Bits != 256 || k.Comment != "alice@laptop" || strings.HasPrefix(k.Fingerprint, "SHA256:") ||
		len(k.Fingerprint) != 43 || strings.Contains(k.PublicKey, "alice") {
		t.Fatalf("key = %+v", k)
	}
	// The fingerprint ignores the comment.
	k2, err := ParseSSHKey(strings.TrimSuffix(ed, " alice@laptop"))
	if err != nil || k2.Fingerprint != k.Fingerprint || k2.PublicKey != k.PublicKey {
		t.Fatalf("no comment: %+v %v", k2, err)
	}

	ec, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := ParseSSHKey(authorized(t, &ec.PublicKey, "")); err != nil || k.Bits != 384 || k.Type != ssh.KeyAlgoECDSA384 {
		t.Fatalf("ecdsa: %+v %v", k, err)
	}
	if testing.Short() {
		t.Skip("RSA key generation")
	}
	r2048, _ := rsa.GenerateKey(rand.Reader, 2048)
	r3072, _ := rsa.GenerateKey(rand.Reader, 3072)
	if _, err := ParseSSHKey(authorized(t, &r2048.PublicKey, "")); err == nil || !strings.Contains(err.Error(), "3072") {
		t.Fatalf("rsa 2048: %v", err)
	}
	if k, err := ParseSSHKey(authorized(t, &r3072.PublicKey, "")); err != nil || k.Bits != 3072 || k.Type != ssh.KeyAlgoRSA {
		t.Fatalf("rsa 3072: %+v %v", k, err)
	}
}

func TestParseSSHKeyErrors(t *testing.T) {
	ed := ed25519Key(t)
	var params dsa.Parameters
	if err := dsa.GenerateParameters(&params, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatal(err)
	}
	dk := dsa.PrivateKey{PublicKey: dsa.PublicKey{Parameters: params}}
	if err := dsa.GenerateKey(&dk, rand.Reader); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"empty":     "  ",
		"garbage":   "not a key",
		"options":   `command="/bin/sh" ` + ed,
		"two keys":  ed + "\n" + ed,
		"too long":  ed + " " + strings.Repeat("x", MaxSSHKeyText),
		"dsa":       authorized(t, &dk.PublicKey, ""),
		"truncated": ed[:40],
		"private":   "-----BEGIN OPENSSH PRIVATE KEY-----",
	} {
		if _, err := ParseSSHKey(in); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.HasPrefix(err.Error(), ErrInvalidKey.Error()) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func newEntity(t testing.TB, name, email string, lifetime uint32) *openpgp.Entity {
	t.Helper()
	cfg := &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, KeyLifetimeSecs: lifetime}
	e, err := openpgp.NewEntity(name, "", email, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func armorPublic(t testing.TB, es ...*openpgp.Entity) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if err := e.Serialize(w); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	return buf.String()
}

func TestParseGPGKey(t *testing.T) {
	e := newEntity(t, "Alice", "Alice@Example.com", 3600*24*365)
	if err := e.AddUserId("Alice Work", "", "alice@work.example", &packet.Config{}); err != nil {
		t.Fatal(err)
	}
	k, err := ParseGPGKey(armorPublic(t, e))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.KeyID) != 16 || k.KeyID != strings.ToUpper(k.KeyID) || !strings.HasSuffix(k.Fingerprint, k.KeyID) || len(k.Fingerprint) != 40 {
		t.Fatalf("ids = %q %q", k.KeyID, k.Fingerprint)
	}
	if strings.Join(k.Emails, ",") != "alice@example.com,alice@work.example" || len(k.SubkeyIDs) != 1 {
		t.Fatalf("key = %+v", k)
	}
	if k.ExpiresAt == nil || k.ExpiresAt.Before(time.Now().Add(300*24*time.Hour)) {
		t.Fatalf("expiry = %v", k.ExpiresAt)
	}
	if !strings.Contains(k.Armored, "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatal("armored")
	}
	// The re-armored key parses to the same key.
	k2, err := ParseGPGKey(k.Armored)
	if err != nil || k2.Fingerprint != k.Fingerprint {
		t.Fatalf("round trip: %v", err)
	}
	if k, err := ParseGPGKey(armorPublic(t, newEntity(t, "Bob", "bob@example.com", 0))); err != nil || k.ExpiresAt != nil {
		t.Fatalf("no expiry: %+v %v", k, err)
	}
}

func TestParseGPGKeyErrors(t *testing.T) {
	a, b := newEntity(t, "A", "a@example.com", 0), newEntity(t, "B", "b@example.com", 0)
	var priv bytes.Buffer
	w, _ := armor.Encode(&priv, openpgp.PrivateKeyType, nil)
	_ = a.SerializePrivate(w, nil)
	_ = w.Close()
	pub := armorPublic(t, a)
	for name, in := range map[string]string{
		"empty":    "",
		"garbage":  "hello",
		"private":  priv.String(),
		"two keys": armorPublic(t, a, b),
		"corrupt":  strings.Replace(pub, pub[120:130], "AAAAAAAAAA", 1),
		"too long": pub + strings.Repeat("x", MaxGPGKeyText),
		"ssh":      ed25519Key(t),
	} {
		if _, err := ParseGPGKey(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzParseSSHKey(f *testing.F) {
	f.Add(ed25519Key(f))
	f.Add("ssh-rsa AAAA")
	f.Add(`no-pty ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl x`)
	f.Fuzz(func(t *testing.T, s string) {
		k, err := ParseSSHKey(s)
		if err != nil {
			return
		}
		again, err := ParseSSHKey(k.PublicKey)
		if err != nil || again.Fingerprint != k.Fingerprint {
			t.Fatalf("round trip of %q: %v", s, err)
		}
	})
}

func FuzzParseGPGKey(f *testing.F) {
	f.Add(armorPublic(f, newEntity(f, "A", "a@example.com", 0)))
	f.Add("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nxsBN\n-----END PGP PUBLIC KEY BLOCK-----\n")
	f.Fuzz(func(t *testing.T, s string) {
		k, err := ParseGPGKey(s)
		if err != nil {
			return
		}
		if len(k.KeyID) != 16 || len(k.Armored) > MaxGPGKeyText {
			t.Fatalf("key %+v", k)
		}
	})
}
