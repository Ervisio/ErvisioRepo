// Package auth holds the authentication primitives shared by every entry
// point: argon2id password hashes, opaque tokens, database sessions with
// their cookies, CSRF protection, brute-force backoff, the request
// Principal and the HTTP middleware that enforces them (PLAN §8.1).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MaxPasswordBytes bounds the input of the hash function.
const MaxPasswordBytes = 1024

// ErrMalformedHash is returned for a stored hash that cannot be parsed.
var ErrMalformedHash = errors.New("malformed password hash")

// Argon2Params are the argon2id cost parameters.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// Hasher hashes and verifies passwords with argon2id. Concurrent hash
// computations are bounded, since each one allocates MemoryKiB.
type Hasher struct {
	params Argon2Params
	sem    chan struct{}
	dummy  string // hash of a random password, verified for unknown users
}

// NewHasher returns a Hasher with the given parameters and concurrency limit.
func NewHasher(p Argon2Params, maxConcurrent int) (*Hasher, error) {
	if p.SaltLen == 0 {
		p.SaltLen = 16
	}
	if p.KeyLen == 0 {
		p.KeyLen = 32
	}
	if p.MemoryKiB < 8 || p.Iterations < 1 || p.Parallelism < 1 {
		return nil, fmt.Errorf("invalid argon2 parameters %+v", p)
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	h := &Hasher{params: p, sem: make(chan struct{}, maxConcurrent)}
	random := make([]byte, 32)
	_, _ = rand.Read(random)
	dummy, err := h.Hash(context.Background(), base64.RawStdEncoding.EncodeToString(random))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for password hasher: %w", ctx.Err())
	}
}

func (h *Hasher) release() { <-h.sem }

// Hash returns the PHC string of password with a fresh random salt.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", errors.New("password too long")
	}
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	p := h.params
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	return encodePHC(p, salt, key), nil
}

// Verify reports whether password matches the PHC string encoded. It also
// reports whether the hash should be recomputed with the current parameters.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (ok, rehash bool, err error) {
	p, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, false, err
	}
	if len(password) > MaxPasswordBytes {
		password = password[:MaxPasswordBytes] // still spend the same time, then fail below
		defer func() { ok, rehash = false, false }()
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLen)
	ok = subtle.ConstantTimeCompare(got, want) == 1
	rehash = ok && (p.MemoryKiB != h.params.MemoryKiB || p.Iterations != h.params.Iterations ||
		p.Parallelism != h.params.Parallelism || p.KeyLen != h.params.KeyLen ||
		p.SaltLen != h.params.SaltLen)
	return ok, rehash, nil
}

// VerifyDummy spends the same time as Verify on a real hash and always
// fails. Call it when the account does not exist, so that response times do
// not reveal which logins are registered.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) {
	_, _, _ = h.Verify(ctx, h.dummy, password)
}

func encodePHC(p Argon2Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// decodePHC parses "$argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>".
func decodePHC(s string) (Argon2Params, []byte, []byte, error) {
	var p Argon2Params
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrMalformedHash
	}
	var par uint32
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &par); err != nil || n != 3 ||
		par < 1 || par > 255 || p.Iterations < 1 || p.MemoryKiB < 8*par || p.MemoryKiB > 4<<20 || p.Iterations > 100 {
		return p, nil, nil, ErrMalformedHash
	}
	p.Parallelism = uint8(par) //nolint:gosec // bounded above
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 128 {
		return p, nil, nil, ErrMalformedHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key)) //nolint:gosec // bounded above
	return p, salt, key, nil
}
