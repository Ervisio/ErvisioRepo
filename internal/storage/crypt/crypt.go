// Package crypt wraps a blob.Store with client-side encryption.
//
// Each object gets a random 256-bit data key (DEK) wrapped with AES-256-GCM by
// a key-encryption key (KEK) derived with HKDF-SHA256 from the master key. The
// body is split into chunks (64 KiB by default) each sealed with AES-256-GCM,
// so ranges can be read by decrypting only the chunks they touch.
//
// Object layout (version 1):
//
//	header (84 bytes)
//	  magic "EREN" | version u8 | alg u8 | reserved u16 | chunk size u32 | KEK id u32
//	  wrap nonce [12] | wrapped DEK [48] | nonce prefix [8]
//	chunk 0 .. chunk n-1: AES-GCM(DEK, nonce prefix || index u32, plaintext, AAD = last flag)
//
// Every chunk but the last carries exactly chunk-size bytes of plaintext; the
// last carries fewer (possibly zero), so the plaintext size follows from the
// ciphertext size and truncation or reordering fails authentication. The
// wrapped DEK is bound to the header and to the object key, so a ciphertext
// moved to another key does not decrypt.
package crypt

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

const (
	magic        = "EREN"
	version      = 1
	algAES256GCM = 1
	headerSize   = 84
	fixedSize    = 16 // magic..KEK id, authenticated with the DEK wrap
	tagSize      = 16
	keySize      = 32
	// DefaultChunkSize is the plaintext size of a chunk.
	DefaultChunkSize = 64 << 10
	maxChunkSize     = 16 << 20
	kekInfo          = "ervisio-repo storage kek v1"
)

var (
	// ErrAuth means the ciphertext was modified, truncated or does not
	// belong to this key.
	ErrAuth = errors.New("crypt: authentication failed")
	// ErrUnknownKey means the object was encrypted with a master key that is
	// not configured.
	ErrUnknownKey = errors.New("crypt: object encrypted with an unknown master key")
	// ErrFormat means the object is not in a supported format.
	ErrFormat = errors.New("crypt: unsupported object format")
)

// ParseMasterKey decodes a master key file: 32 bytes written as 64 hex
// characters or as standard base64, surrounding whitespace ignored.
func ParseMasterKey(data []byte) ([]byte, error) {
	s := string(bytes.TrimSpace(data))
	if k, err := hex.DecodeString(s); err == nil && len(k) == keySize {
		return k, nil
	}
	if k, err := base64.StdEncoding.DecodeString(s); err == nil && len(k) == keySize {
		return k, nil
	}
	return nil, fmt.Errorf("crypt: master key must be %d bytes as hex or base64", keySize)
}

type kek struct {
	id   uint32
	aead cipher.AEAD
}

func deriveKEK(master []byte) (kek, error) {
	if len(master) < keySize {
		return kek{}, fmt.Errorf("crypt: master key must be at least %d bytes", keySize)
	}
	k, err := hkdf.Key(sha256.New, master, nil, kekInfo, keySize)
	if err != nil {
		return kek{}, fmt.Errorf("crypt: derive kek: %w", err)
	}
	a, err := newGCM(k)
	if err != nil {
		return kek{}, err
	}
	sum := sha256.Sum256(append([]byte("kek id\x00"), k...))
	return kek{id: binary.BigEndian.Uint32(sum[:4]), aead: a}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	return a, nil
}

// Options configures a Store.
type Options struct {
	// Master is the current master key; new objects use it.
	Master []byte
	// Previous master keys still accepted for reading (key rotation).
	Previous [][]byte
	// ChunkSize of new objects (default DefaultChunkSize).
	ChunkSize int
}

// Store encrypts everything written to the inner store.
type Store struct {
	inner     blob.Store
	cur       kek
	keks      map[uint32]kek
	chunkSize int
}

var _ blob.Store = (*Store)(nil)

// New wraps inner. Use AsConditional to also get blob.ConditionalPutter when
// inner supports it.
func New(inner blob.Store, o Options) (*Store, error) {
	cs := o.ChunkSize
	if cs == 0 {
		cs = DefaultChunkSize
	}
	if cs < 1024 || cs > maxChunkSize {
		return nil, fmt.Errorf("crypt: chunk size %d out of range", cs)
	}
	cur, err := deriveKEK(o.Master)
	if err != nil {
		return nil, err
	}
	s := &Store{inner: inner, cur: cur, keks: map[uint32]kek{cur.id: cur}, chunkSize: cs}
	for _, m := range o.Previous {
		k, err := deriveKEK(m)
		if err != nil {
			return nil, err
		}
		s.keks[k.id] = k
	}
	return s, nil
}

// Wrap returns a blob.Store that also implements blob.ConditionalPutter when
// inner does.
func Wrap(inner blob.Store, o Options) (blob.Store, error) {
	s, err := New(inner, o)
	if err != nil {
		return nil, err
	}
	if _, ok := inner.(blob.ConditionalPutter); ok {
		return &condStore{s}, nil
	}
	return s, nil
}

type condStore struct{ *Store }

// PutConditional implements blob.ConditionalPutter. Conditions apply to the
// ETags returned by this store (those of the ciphertext).
func (c *condStore) PutConditional(ctx context.Context, key string, r io.Reader, size int64, cond blob.Condition, o blob.PutOpts) (blob.Info, error) {
	cp := c.inner.(blob.ConditionalPutter)
	return c.put(ctx, key, r, size, o, func(cr io.Reader, csize int64) (blob.Info, error) {
		return cp.PutConditional(ctx, key, cr, csize, cond, blob.PutOpts{ContentType: "application/octet-stream"})
	})
}

// Name implements blob.Store.
func (s *Store) Name() string { return s.inner.Name() }

// Unwrap implements blob.Unwrapper.
func (s *Store) Unwrap() blob.Store { return s.inner }

// Caps implements blob.Store. Presigned URLs would expose ciphertext, so they
// are not offered.
func (s *Store) Caps() blob.Caps {
	c := s.inner.Caps()
	c.Presign = false
	if _, ok := s.inner.(blob.ConditionalPutter); !ok {
		c.ConditionalPut = false
	}
	return c
}

// CiphertextSize returns the stored size of a plaintext of size n.
func CiphertextSize(n int64, chunkSize int) int64 {
	cs := int64(chunkSize)
	chunks := n/cs + 1
	return headerSize + n + chunks*tagSize
}

// plaintextSize inverts CiphertextSize.
func plaintextSize(c int64, chunkSize int) (int64, error) {
	full := int64(chunkSize) + tagSize
	body := c - headerSize
	if body < tagSize {
		return 0, fmt.Errorf("%w: object too short", ErrAuth)
	}
	chunks := body/full + 1
	last := body - (chunks-1)*full - tagSize
	if last < 0 || last >= int64(chunkSize) {
		return 0, fmt.Errorf("%w: bad ciphertext length", ErrAuth)
	}
	return body - chunks*tagSize, nil
}

// Put implements blob.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, o, func(cr io.Reader, csize int64) (blob.Info, error) {
		return s.inner.Put(ctx, key, cr, csize, blob.PutOpts{ContentType: "application/octet-stream"})
	})
}

func (s *Store) put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts, put func(io.Reader, int64) (blob.Info, error)) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	// Size and checksum refer to the plaintext; the verifier fails the
	// stream at EOF so the inner store discards the object.
	vr, err := blob.NewVerifyReader(ctx, r, size, o.SHA256)
	if err != nil {
		return blob.Info{}, err
	}
	enc, err := s.newEncrypter(key, vr)
	if err != nil {
		return blob.Info{}, err
	}
	csize := int64(-1)
	if size >= 0 {
		csize = CiphertextSize(size, s.chunkSize)
	}
	info, err := put(enc, csize)
	if err != nil {
		// Report the plaintext-side cause rather than a ciphertext size
		// mismatch it triggered.
		if enc.srcErr != nil && !errors.Is(err, enc.srcErr) {
			return blob.Info{}, fmt.Errorf("crypt: put %s: %w", key, enc.srcErr)
		}
		return blob.Info{}, err
	}
	n, err := plaintextSize(info.Size, s.chunkSize)
	if err != nil {
		return blob.Info{}, err
	}
	info.Size = n
	return info, nil
}

type encrypter struct {
	src       io.Reader
	aead      cipher.AEAD
	prefix    [8]byte
	chunkSize int
	idx       uint32
	plain     []byte
	out       []byte // pending output
	done      bool
	srcErr    error
}

func (s *Store) newEncrypter(key string, src io.Reader) (*encrypter, error) {
	var dek [keySize]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	aead, err := newGCM(dek[:])
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, fixedSize, headerSize)
	copy(hdr, magic)
	hdr[4], hdr[5] = version, algAES256GCM
	binary.BigEndian.PutUint32(hdr[8:], uint32(s.chunkSize)) //nolint:gosec // bounded by New
	binary.BigEndian.PutUint32(hdr[12:], s.cur.id)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	hdr = append(hdr, nonce[:]...)
	hdr = s.cur.aead.Seal(hdr, nonce[:], dek[:], wrapAAD(hdr[:fixedSize], key))
	e := &encrypter{src: src, aead: aead, chunkSize: s.chunkSize, plain: make([]byte, s.chunkSize)}
	if _, err := rand.Read(e.prefix[:]); err != nil {
		return nil, fmt.Errorf("crypt: %w", err)
	}
	e.out = append(hdr, e.prefix[:]...)
	return e, nil
}

func wrapAAD(fixed []byte, key string) []byte {
	return append(append([]byte{}, fixed...), key...)
}

func chunkNonce(prefix [8]byte, idx uint32) []byte {
	n := make([]byte, 12)
	copy(n, prefix[:])
	binary.BigEndian.PutUint32(n[8:], idx)
	return n
}

func chunkAAD(last bool) []byte {
	if last {
		return []byte{1}
	}
	return []byte{0}
}

func (e *encrypter) Read(p []byte) (int, error) {
	for len(e.out) == 0 {
		if e.done {
			return 0, io.EOF
		}
		n, err := io.ReadFull(e.src, e.plain)
		last := false
		switch {
		case err == nil:
		case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
			last = true
		default:
			e.srcErr = err
			return 0, err
		}
		if e.idx == ^uint32(0) {
			return 0, errors.New("crypt: object too large")
		}
		e.out = e.aead.Seal(e.out[:0], chunkNonce(e.prefix, e.idx), e.plain[:n], chunkAAD(last))
		e.idx++
		e.done = last
	}
	n := copy(p, e.out)
	e.out = e.out[n:]
	return n, nil
}

type header struct {
	chunkSize int
	aead      cipher.AEAD
	prefix    [8]byte
}

func (s *Store) parseHeader(key string, h []byte) (header, error) {
	if len(h) != headerSize || string(h[:4]) != magic {
		return header{}, fmt.Errorf("crypt: %s: %w", key, ErrFormat)
	}
	if h[4] != version || h[5] != algAES256GCM {
		return header{}, fmt.Errorf("crypt: %s: %w: version %d alg %d", key, ErrFormat, h[4], h[5])
	}
	cs := binary.BigEndian.Uint32(h[8:])
	if cs < 1024 || cs > maxChunkSize {
		return header{}, fmt.Errorf("crypt: %s: %w: chunk size %d", key, ErrAuth, cs)
	}
	k, ok := s.keks[binary.BigEndian.Uint32(h[12:])]
	if !ok {
		return header{}, fmt.Errorf("crypt: %s: %w", key, ErrUnknownKey)
	}
	dek, err := k.aead.Open(nil, h[16:28], h[28:76], wrapAAD(h[:fixedSize], key))
	if err != nil {
		return header{}, fmt.Errorf("crypt: %s: data key: %w", key, ErrAuth)
	}
	aead, err := newGCM(dek)
	if err != nil {
		return header{}, err
	}
	hd := header{chunkSize: int(cs), aead: aead}
	copy(hd.prefix[:], h[76:84])
	return hd, nil
}

// Get implements blob.Store. Ranges refer to the plaintext; only the chunks
// they overlap are fetched and decrypted.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	if rng == nil {
		rc, info, err := s.inner.Get(ctx, key, nil)
		if err != nil {
			return nil, blob.Info{}, err
		}
		h := make([]byte, headerSize)
		if _, err := io.ReadFull(rc, h); err != nil {
			_ = rc.Close()
			return nil, blob.Info{}, s.readErr(key, err)
		}
		hd, err := s.parseHeader(key, h)
		if err != nil {
			_ = rc.Close()
			return nil, blob.Info{}, err
		}
		if info.Size, err = plaintextSize(info.Size, hd.chunkSize); err != nil {
			_ = rc.Close()
			return nil, blob.Info{}, fmt.Errorf("crypt: %s: %w", key, err)
		}
		return &decrypter{rc: rc, hd: hd, key: key, buf: make([]byte, hd.chunkSize+tagSize), last: -1}, info, nil
	}

	// Header first: it also tells the ciphertext size (and thus the
	// plaintext size) and the ETag the chunk fetch must match.
	hrc, info, err := s.inner.Get(ctx, key, &blob.Range{Offset: 0, Length: headerSize})
	if err != nil {
		return nil, blob.Info{}, err
	}
	h := make([]byte, headerSize)
	_, err = io.ReadFull(hrc, h)
	_ = hrc.Close()
	if err != nil {
		return nil, blob.Info{}, s.readErr(key, err)
	}
	hd, err := s.parseHeader(key, h)
	if err != nil {
		return nil, blob.Info{}, err
	}
	psize, err := plaintextSize(info.Size, hd.chunkSize)
	if err != nil {
		return nil, blob.Info{}, fmt.Errorf("crypt: %s: %w", key, err)
	}
	etag := info.ETag
	info.Size = psize
	start, end, err := blob.ResolveRange(rng, psize)
	if err != nil {
		return nil, blob.Info{}, err
	}
	if start == end {
		return io.NopCloser(bytes.NewReader(nil)), info, nil
	}
	cs := int64(hd.chunkSize)
	full := cs + tagSize
	first, lastIdx := start/cs, (end-1)/cs
	nchunks := psize/cs + 1
	off := headerSize + first*full
	length := min(headerSize+(lastIdx+1)*full, CiphertextSize(psize, hd.chunkSize)) - off
	rc, cinfo, err := s.inner.Get(ctx, key, &blob.Range{Offset: off, Length: length})
	if err != nil {
		return nil, blob.Info{}, err
	}
	if cinfo.ETag != etag {
		_ = rc.Close()
		return nil, blob.Info{}, blob.MarkTransient(fmt.Errorf("crypt: get %s: object changed while reading", key))
	}
	d := &decrypter{rc: rc, hd: hd, key: key, buf: make([]byte, full), idx: uint32(first), last: lastIdx, //nolint:gosec // bounded by the format
		skip: start - first*cs, remain: end - start, ranged: true, nchunks: nchunks}
	return d, info, nil
}

func (s *Store) readErr(key string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("crypt: %s: %w: truncated header", key, ErrAuth)
	}
	return err
}

type decrypter struct {
	rc      io.ReadCloser
	hd      header
	key     string
	buf     []byte
	out     []byte
	idx     uint32
	done    bool
	err     error
	ranged  bool
	last    int64 // ranged: index of the last chunk to read
	nchunks int64
	skip    int64 // ranged: plaintext bytes to drop from the first chunk
	remain  int64 // ranged: plaintext bytes still to return
}

func (d *decrypter) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			return 0, io.EOF
		}
		if d.err = d.next(); d.err != nil {
			return 0, d.err
		}
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	return n, nil
}

func (d *decrypter) next() error {
	n, err := io.ReadFull(d.rc, d.buf)
	var last bool
	switch {
	case err == nil:
		last = d.ranged && int64(d.idx) == d.nchunks-1
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		last = true
		if d.ranged && int64(d.idx) != d.nchunks-1 {
			return fmt.Errorf("crypt: %s: %w: truncated", d.key, ErrAuth)
		}
	default:
		return err
	}
	if n < tagSize {
		return fmt.Errorf("crypt: %s: %w: truncated", d.key, ErrAuth)
	}
	plain, err := d.hd.aead.Open(d.buf[:0], chunkNonce(d.hd.prefix, d.idx), d.buf[:n], chunkAAD(last))
	if err != nil {
		return fmt.Errorf("crypt: %s: chunk %d: %w", d.key, d.idx, ErrAuth)
	}
	if !last && len(plain) != d.hd.chunkSize {
		return fmt.Errorf("crypt: %s: %w", d.key, ErrAuth)
	}
	d.idx++
	if d.ranged {
		if d.skip > 0 {
			plain = plain[d.skip:]
			d.skip = 0
		}
		if int64(len(plain)) > d.remain {
			plain = plain[:d.remain]
		}
		d.remain -= int64(len(plain))
		if int64(d.idx) > d.last || last {
			d.done = true
		}
	} else if last {
		d.done = true
		// Anything after the final chunk is tampering.
		var one [1]byte
		if m, _ := d.rc.Read(one[:]); m > 0 {
			return fmt.Errorf("crypt: %s: %w: trailing data", d.key, ErrAuth)
		}
	}
	d.out = plain
	return nil
}

func (d *decrypter) Close() error { return d.rc.Close() }

// Stat implements blob.Store. The size is derived from the ciphertext size
// with the default chunk size of this store's objects; objects written with
// another chunk size report it after a Get.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	info, err := s.inner.Stat(ctx, key)
	if err != nil {
		return blob.Info{}, err
	}
	if info.Size, err = plaintextSize(info.Size, s.chunkSize); err != nil {
		return blob.Info{}, fmt.Errorf("crypt: %s: %w", key, err)
	}
	return info, nil
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error { return s.inner.Delete(ctx, key) }

// List implements blob.Store. Sizes are plaintext sizes (see Stat).
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	return s.inner.List(ctx, prefix, func(i blob.Info) error {
		if n, err := plaintextSize(i.Size, s.chunkSize); err == nil {
			i.Size = n
		}
		return fn(i)
	})
}
