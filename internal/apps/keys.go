package apps

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// rsaBits is the size of the generated private keys.
const rsaBits = 2048

// newClientID returns a client id shaped like the ones of GitHub Apps
// ("Iv23li" and 14 alphanumeric characters).
func newClientID() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var b [14]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return "Iv23li" + string(out)
}

// fingerprint is the SHA-256 of the PKIX public key, as GitHub shows it.
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}

// CreateKey generates a private key of an App p manages. Only the public
// key is stored: the PEM (PKCS#1, like GitHub's) is returned once.
func (s *Service) CreateKey(ctx context.Context, p auth.Principal, slug, ip string) (Key, string, error) {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return Key{}, "", err
	}
	priv, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return Key{}, "", fmt.Errorf("generate key: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return Key{}, "", fmt.Errorf("marshal public key: %w", err)
	}
	id, err := newID()
	if err != nil {
		return Key{}, "", err
	}
	key := Key{ID: id, Fingerprint: fingerprint(der), CreatedBy: p.Username, CreatedAt: s.now()}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.LockApp(ctx, app.ID); err != nil {
			return fmt.Errorf("lock app: %w", err)
		}
		if n, err := q.CountAppKeys(ctx, app.ID); err != nil {
			return fmt.Errorf("count app keys: %w", err)
		} else if n >= MaxKeys {
			return ErrTooMany
		}
		if err := q.InsertAppKey(ctx, dbgen.InsertAppKeyParams{ID: id, AppID: app.ID, PublicKey: der,
			Fingerprint: key.Fingerprint, CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}}); err != nil {
			return fmt.Errorf("insert app key: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionAppKeyCreated, TargetType: "app",
			TargetID: app.ID.String(), IP: ip, OrgID: orgOf(app),
			Data: map[string]any{"slug": app.Slug, "key_id": id.String(), "fingerprint": key.Fingerprint}})
	})
	if err != nil {
		return Key{}, "", err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	s.log.InfoContext(ctx, "app key created", "slug", app.Slug, "key_id", id, "user_id", p.UserID)
	return key, string(block), nil
}

// DeleteKey revokes a private key of an App p manages.
func (s *Service) DeleteKey(ctx context.Context, p auth.Principal, slug string, keyID uuid.UUID, ip string) error {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteAppKey(ctx, dbgen.DeleteAppKeyParams{AppID: app.ID, KeyID: keyID})
		if err != nil {
			return fmt.Errorf("delete app key: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionAppKeyRevoked, TargetType: "app",
			TargetID: app.ID.String(), IP: ip, OrgID: orgOf(app),
			Data: map[string]any{"slug": app.Slug, "key_id": keyID.String()}})
	})
}

// LooksLikeJWT reports whether a bearer credential is a JWT rather than
// an access token.
func LooksLikeJWT(s string) bool {
	return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2 && len(s) < 8192
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Iss json.RawMessage `json:"iss"`
	Iat *json.Number    `json:"iat"`
	Exp *json.Number    `json:"exp"`
}

func numericDate(n *json.Number) (time.Time, bool) {
	if n == nil {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

// AuthenticateJWT verifies an App JWT: RS256 signed by one of the App's
// keys, iss the App id (or its client id), iat and exp present, exp at
// most ten minutes ahead, with 60 seconds of clock drift. It returns the
// App principal (MethodApp).
func (s *Service) AuthenticateJWT(ctx context.Context, token string) (auth.Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return auth.Principal{}, ErrBadJWT
	}
	dec := base64.RawURLEncoding
	hb, err1 := dec.DecodeString(parts[0])
	cb, err2 := dec.DecodeString(parts[1])
	sig, err3 := dec.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return auth.Principal{}, ErrBadJWT
	}
	var h jwtHeader
	if json.Unmarshal(hb, &h) != nil || h.Alg != "RS256" {
		return auth.Principal{}, ErrBadJWT
	}
	var c jwtClaims
	d := json.NewDecoder(strings.NewReader(string(cb)))
	d.UseNumber()
	if d.Decode(&c) != nil {
		return auth.Principal{}, ErrBadJWT
	}
	now := s.now()
	iat, ok1 := numericDate(c.Iat)
	exp, ok2 := numericDate(c.Exp)
	if !ok1 || !ok2 || iat.After(now.Add(jwtSkew)) || !exp.After(now.Add(-jwtSkew)) ||
		exp.After(now.Add(jwtMaxTTL+jwtSkew)) || !exp.After(iat) {
		return auth.Principal{}, ErrBadJWT
	}
	app, err := s.appByIssuer(ctx, c.Iss)
	if err != nil {
		return auth.Principal{}, err
	}
	keys, err := s.q.ListAppKeys(ctx, app.ID)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("list app keys: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	for _, k := range keys {
		pub, err := x509.ParsePKIXPublicKey(k.PublicKey)
		if err != nil {
			continue
		}
		rk, ok := pub.(*rsa.PublicKey)
		if !ok {
			continue
		}
		if rsa.VerifyPKCS1v15(rk, crypto.SHA256, digest[:], sig) == nil {
			if err := s.q.TouchAppKey(ctx, k.ID); err != nil {
				return auth.Principal{}, fmt.Errorf("touch app key: %w", err)
			}
			return auth.Principal{Username: app.BotLogin, Role: auth.RoleUser, Method: auth.MethodApp,
				App: &auth.AppIdentity{ID: app.ID, NumID: app.NumID, Slug: app.Slug}}, nil
		}
	}
	return auth.Principal{}, ErrBadJWT
}

// appByIssuer resolves the iss claim: the numeric App id (a number or a
// string) or the client id.
func (s *Service) appByIssuer(ctx context.Context, raw json.RawMessage) (App, error) {
	var iss string
	var num json.Number
	if json.Unmarshal(raw, &iss) != nil {
		if json.Unmarshal(raw, &num) != nil {
			return App{}, ErrBadJWT
		}
		iss = num.String()
	}
	if iss == "" || len(iss) > 100 {
		return App{}, ErrBadJWT
	}
	var r dbgen.GetAppByIDRow
	var err error
	if n, perr := strconv.ParseInt(iss, 10, 64); perr == nil {
		var row dbgen.GetAppByNumIDRow
		row, err = s.q.GetAppByNumID(ctx, pgInt8(n))
		r = dbgen.GetAppByIDRow(row)
	} else {
		var row dbgen.GetAppByClientIDRow
		row, err = s.q.GetAppByClientID(ctx, iss)
		r = dbgen.GetAppByIDRow(row)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrBadJWT
	}
	if err != nil {
		return App{}, fmt.Errorf("get app: %w", err)
	}
	return appFrom(r), nil
}
