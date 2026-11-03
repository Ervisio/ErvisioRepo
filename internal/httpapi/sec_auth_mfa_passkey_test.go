package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

var b64u = base64.RawURLEncoding

// softKey is a software passkey for https://git.example.com.
type softKey struct {
	t      *testing.T
	key    *ecdsa.PrivateKey
	id     []byte
	handle []byte
	count  uint32
}

func newSoftKey(t *testing.T) *softKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{t: t, key: k, id: id}
}

const softOrigin = "https://git.example.com"

func (k *softKey) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte("git.example.com"))
	flags := byte(0x01 | 0x04) // user present, user verified
	if attested {
		flags |= 0x40
	}
	k.count++
	out := append(rp[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.count)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(k.id)))
		out = append(out, k.id...)
		pub, err := k.key.PublicKey.Bytes() // 0x04 || X || Y
		if err != nil {
			k.t.Fatal(err)
		}
		cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: pub[1:33], -3: pub[33:]})
		if err != nil {
			k.t.Fatal(err)
		}
		out = append(out, cose...)
	}
	return out
}

func clientData(typ string, options map[string]any) []byte {
	pk, _ := options["publicKey"].(map[string]any)
	ch, _ := pk["challenge"].(string)
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": ch, "origin": softOrigin})
	return raw
}

// create answers navigator.credentials.create.
func (k *softKey) create(options map[string]any) map[string]any {
	pk, _ := options["publicKey"].(map[string]any)
	user, _ := pk["user"].(map[string]any)
	h, err := b64u.DecodeString(user["id"].(string))
	if err != nil {
		k.t.Fatal(err)
	}
	k.handle = h
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(true)})
	if err != nil {
		k.t.Fatal(err)
	}
	return map[string]any{"id": b64u.EncodeToString(k.id), "rawId": b64u.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64u.EncodeToString(clientData("webauthn.create", options)),
			"attestationObject": b64u.EncodeToString(att)}}
}

// get answers navigator.credentials.get.
func (k *softKey) get(options map[string]any) map[string]any {
	cd := clientData("webauthn.get", options)
	ad := k.authData(false)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	return map[string]any{"id": b64u.EncodeToString(k.id), "rawId": b64u.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64u.EncodeToString(cd), "authenticatorData": b64u.EncodeToString(ad),
			"signature": b64u.EncodeToString(sig), "userHandle": b64u.EncodeToString(k.handle)}}
}

type passkeyChallenge struct {
	Challenge string
	Options   map[string]any
}

func TestSecAuthMFAPasskey(t *testing.T) {
	e := mfaRouter(t, nil)
	admin := withAdmin(t, e.h)
	key := newSoftKey(t)

	// Registration needs the password.
	assertProblem(t, admin.do(http.MethodPost, "/api/v1/user/security/passkeys/options", map[string]string{"password": "wrong password!"}), http.StatusForbidden)
	rec := admin.do(http.MethodPost, "/api/v1/user/security/passkeys/options", map[string]string{"password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("options: %d %s", rec.Code, rec.Body)
	}
	reg := decodeJSON[passkeyChallenge](t, rec.Body.Bytes())
	cred := key.create(reg.Options)
	rec = admin.do(http.MethodPost, "/api/v1/user/security/passkeys", map[string]any{"challenge": reg.Challenge, "name": "laptop", "credential": cred})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	pk := decodeJSON[struct{ ID, Name string }](t, rec.Body.Bytes())
	// The registration challenge is single use.
	assertProblem(t, admin.do(http.MethodPost, "/api/v1/user/security/passkeys", map[string]any{"challenge": reg.Challenge, "name": "again", "credential": cred}), http.StatusBadRequest)

	// Passwordless login.
	b := newBrowser(t, e.h, "198.51.100.60").prime()
	rec = b.do(http.MethodPost, "/api/v1/auth/mfa/passkey/options", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("passkey options: %d %s", rec.Code, rec.Body)
	}
	lc := decodeJSON[passkeyChallenge](t, rec.Body.Bytes())
	assertion := key.get(lc.Options)
	if rec := b.do(http.MethodPost, "/api/v1/auth/mfa/passkey", map[string]any{"challenge": lc.Challenge, "credential": assertion}); rec.Code != http.StatusOK || b.session() == "" {
		t.Fatalf("passkey login: %d %s", rec.Code, rec.Body)
	}
	// The same assertion cannot be replayed.
	c := newBrowser(t, e.h, "198.51.100.61").prime()
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/passkey", map[string]any{"challenge": lc.Challenge, "credential": assertion}), http.StatusUnauthorized)
	// Another key cannot answer a challenge.
	rec = c.do(http.MethodPost, "/api/v1/auth/mfa/passkey/options", nil)
	lc = decodeJSON[passkeyChallenge](t, rec.Body.Bytes())
	other := newSoftKey(t)
	other.id, other.handle = key.id, key.handle
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/passkey", map[string]any{"challenge": lc.Challenge, "credential": other.get(lc.Options)}), http.StatusUnauthorized)

	// As a second factor after the password.
	d := pendingLogin(t, e.h, "198.51.100.62")
	rec = d.do(http.MethodPost, "/api/v1/auth/mfa/webauthn/options", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("webauthn options: %d %s", rec.Code, rec.Body)
	}
	opts := decodeJSON[struct{ Options map[string]any }](t, rec.Body.Bytes()).Options
	if rec := d.do(http.MethodPost, "/api/v1/auth/mfa/webauthn", map[string]any{"credential": key.get(opts)}); rec.Code != http.StatusOK || d.session() == "" {
		t.Fatalf("webauthn second factor: %d %s", rec.Code, rec.Body)
	}

	// Deleting needs the password; then the password alone signs in again.
	assertProblem(t, admin.do(http.MethodDelete, "/api/v1/user/security/passkeys/"+pk.ID, map[string]string{}), http.StatusForbidden)
	if rec := admin.do(http.MethodDelete, "/api/v1/user/security/passkeys/"+pk.ID, map[string]string{"password": testPassword}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := newBrowser(t, e.h, "198.51.100.63").prime().login("boss", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("login after delete: %d", rec.Code)
	}
}
