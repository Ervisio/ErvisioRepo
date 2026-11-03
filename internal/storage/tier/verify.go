package tier

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// ErrCorrupt is returned when the bytes of an object do not match its
// recorded size or sha256.
var ErrCorrupt = errors.New("storage object corrupt")

// verifyReader passes through exactly size bytes and checks their sha256.
//
// The check happens before the last byte is handed out: if the content does
// not match, the final Read returns an error instead of data, so a driver
// that streams it into a write never receives a complete object and aborts
// the write (temporary file discarded, multipart upload aborted). Any read
// error, a short stream or extra bytes fail the same way.
type verifyReader struct {
	r    io.Reader
	size int64
	want []byte // nil accepts any content of the right size
	h    hash.Hash
	n    int64
	err  error
	done bool
}

func newVerifyReader(r io.Reader, size int64, want []byte) *verifyReader {
	return &verifyReader{r: r, size: size, want: want, h: sha256.New()}
}

func (v *verifyReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	if v.done {
		return 0, io.EOF
	}
	remaining := v.size - v.n
	if remaining == 0 {
		// Zero-byte object, or the previous call stopped exactly at size.
		return 0, v.finish()
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := v.r.Read(p)
	v.h.Write(p[:n])
	v.n += int64(n)
	if v.n == v.size {
		if ferr := v.finish(); ferr != nil && ferr != io.EOF {
			return 0, ferr
		}
		return n, nil
	}
	if err == io.EOF {
		v.err = fmt.Errorf("%w: got %d of %d bytes", io.ErrUnexpectedEOF, v.n, v.size)
		return 0, v.err
	}
	if err != nil {
		v.err = err
		return 0, err
	}
	return n, nil
}

// finish checks that the source is exhausted and that the hash matches.
func (v *verifyReader) finish() error {
	var probe [1]byte
	for {
		n, err := v.r.Read(probe[:])
		if n > 0 {
			v.err = fmt.Errorf("%w: more than %d bytes", ErrCorrupt, v.size)
			return v.err
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			v.err = err
			return err
		}
	}
	if v.want != nil && !bytes.Equal(v.h.Sum(nil), v.want) {
		v.err = fmt.Errorf("%w: sha256 is %x, expected %x", ErrCorrupt, v.h.Sum(nil), v.want)
		return v.err
	}
	v.done = true
	return io.EOF
}

// Verified reports whether the whole stream was read and matched.
func (v *verifyReader) Verified() bool { return v.done }

// Sum returns the sha256 of the bytes read so far.
func (v *verifyReader) Sum() []byte { return v.h.Sum(nil) }

// ParseSHA256 decodes a lowercase or uppercase hex sha256.
func ParseSHA256(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("invalid sha256 %q", s)
	}
	return b, nil
}
