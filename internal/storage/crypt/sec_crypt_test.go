package crypt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
)

// rawPut stores ciphertext produced by enc for key, applying mutate to it.
func setup(t *testing.T, n int) (enc blob.Store, raw blob.Store, ct []byte, data []byte) {
	t.Helper()
	raw = newLocal(t)
	enc = wrap(t, raw, Options{Master: testKey(1), ChunkSize: 1024})
	data = blobtest.Data(7, n)
	ctx := context.Background()
	if _, err := enc.Put(ctx, "obj", bytes.NewReader(data), int64(n), blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	rc, _, err := raw.Get(ctx, "obj", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ = io.ReadAll(rc)
	_ = rc.Close()
	return enc, raw, ct, data
}

func readAll(s blob.Store, key string, rng *blob.Range) ([]byte, error) {
	rc, _, err := s.Get(context.Background(), key, rng)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func putRaw(t *testing.T, raw blob.Store, key string, b []byte) {
	t.Helper()
	if _, err := raw.Put(context.Background(), key, bytes.NewReader(b), int64(len(b)), blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
}

func TestSecCiphertextHidesPlaintext(t *testing.T) {
	_, _, ct, data := setup(t, 4096)
	if bytes.Contains(ct, data[:64]) {
		t.Fatal("plaintext visible in ciphertext")
	}
}

func TestSecTamperedByte(t *testing.T) {
	enc, raw, ct, _ := setup(t, 3000)
	// Every byte of the object is authenticated: the header (including the
	// reserved field), the wrapped key, the nonce prefix and each chunk.
	for _, i := range []int{4, 6, 9, 13, 20, 40, 80, headerSize + 5, headerSize + 1024 + tagSize + 3, len(ct) - 1} {
		bad := bytes.Clone(ct)
		bad[i] ^= 0x01
		putRaw(t, raw, "obj", bad)
		if _, err := readAll(enc, "obj", nil); err == nil {
			t.Errorf("flip at %d accepted", i)
		}
		if _, err := readAll(enc, "obj", &blob.Range{Offset: 0, Length: -1}); err == nil {
			t.Errorf("flip at %d accepted by a range read", i)
		}
	}
}

func TestSecTruncation(t *testing.T) {
	enc, raw, ct, _ := setup(t, 2048) // two full chunks + empty final chunk
	for _, cut := range []int{1, tagSize, tagSize + 1, 1024 + tagSize, len(ct) - headerSize} {
		putRaw(t, raw, "obj", ct[:len(ct)-cut])
		if _, err := readAll(enc, "obj", nil); !errors.Is(err, ErrAuth) {
			t.Errorf("truncated by %d: %v", cut, err)
		}
	}
	putRaw(t, raw, "obj", append(bytes.Clone(ct), 0))
	if _, err := readAll(enc, "obj", nil); !errors.Is(err, ErrAuth) {
		t.Errorf("trailing byte: %v", err)
	}
}

func TestSecChunkReorder(t *testing.T) {
	enc, raw, ct, _ := setup(t, 3000)
	full := 1024 + tagSize
	bad := bytes.Clone(ct)
	c0 := bytes.Clone(bad[headerSize : headerSize+full])
	copy(bad[headerSize:], bad[headerSize+full:headerSize+2*full])
	copy(bad[headerSize+full:], c0)
	putRaw(t, raw, "obj", bad)
	if _, err := readAll(enc, "obj", nil); !errors.Is(err, ErrAuth) {
		t.Fatalf("swapped chunks: %v", err)
	}
}

func TestSecMovedToAnotherKey(t *testing.T) {
	enc, raw, ct, _ := setup(t, 100)
	putRaw(t, raw, "other", ct)
	if _, err := readAll(enc, "other", nil); !errors.Is(err, ErrAuth) {
		t.Fatalf("ciphertext replayed under another key: %v", err)
	}
}

func TestSecWrongMasterKey(t *testing.T) {
	_, raw, _, _ := setup(t, 100)
	other := wrap(t, raw, Options{Master: testKey(9)})
	if _, err := readAll(other, "obj", nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("wrong master key: %v", err)
	}
}

func TestSecNotEncrypted(t *testing.T) {
	enc, raw, _, _ := setup(t, 1)
	putRaw(t, raw, "plain", []byte("this object was never encrypted, it is plain text....................................."))
	if _, err := readAll(enc, "plain", nil); !errors.Is(err, ErrFormat) {
		t.Fatalf("plain object: %v", err)
	}
}
