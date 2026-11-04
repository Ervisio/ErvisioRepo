package blobsvc

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BlobPrefix is where the signed blob URLs are served:
// BlobPrefix + "<kind>/<op>/<id>?exp=<unix>&sig=<mac>".
const BlobPrefix = "/api/actions/results/blob/"

// Blob URL operations.
const (
	OpUpload   = "upload"
	OpDownload = "download"
)

var signerInfo = []byte("ervisio actions results url v1")

// Signer issues and checks the signed blob URLs handed to the toolkit. A
// URL grants one operation on one object until it expires; the toolkit
// sends it without the runtime token.
type Signer struct {
	key  []byte
	base string
	now  func() time.Time
}

// NewSigner derives the signing key from the master key; without one the
// key is random and URLs do not survive a restart.
func NewSigner(master []byte, publicURL string) (*Signer, error) {
	var key []byte
	if len(master) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("results url key: %w", err)
		}
	} else {
		k, err := hkdf.Key(sha256.New, master, nil, string(signerInfo), 32)
		if err != nil {
			return nil, fmt.Errorf("results url key: %w", err)
		}
		key = k
	}
	return &Signer{key: key, base: strings.TrimRight(publicURL, "/"), now: time.Now}, nil
}

func (s *Signer) mac(kind, op string, id int64, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "%s\n%s\n%d\n%d", kind, op, id, exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// URL returns the absolute signed URL of op on object id of kind.
func (s *Signer) URL(kind, op string, id int64, ttl time.Duration) string {
	exp := s.now().Add(ttl).Unix()
	return fmt.Sprintf("%s%s%s/%s/%d?exp=%d&sig=%s", s.base, BlobPrefix, kind, op, id, exp, s.mac(kind, op, id, exp))
}

// Valid reports whether r carries a valid, unexpired signature for op on
// object id of kind.
func (s *Signer) Valid(r *http.Request, kind, op string, id int64) bool {
	q := r.URL.Query()
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil || s.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(q.Get("sig")), []byte(s.mac(kind, op, id, exp)))
}
