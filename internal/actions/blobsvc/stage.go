package blobsvc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// ErrTooLarge is returned when an upload exceeds its limit.
var ErrTooLarge = errors.New("upload too large")

// maxBlockList bounds a Put Block List body.
const maxBlockList = 8 << 20

// Stager keeps the parts of uploads in progress on storage (prefix
// "uploads/") and concatenates them into the final object.
type Stager struct {
	Q     *dbgen.Queries
	Class *tier.Class
	Log   *slog.Logger
}

func partKey(owner, block string) string {
	h := sha256.Sum256([]byte(block))
	return "uploads/" + owner + "/" + hex.EncodeToString(h[:16])
}

// Put stores one part of owner's upload; a part with the same block name
// is replaced.
func (s *Stager) Put(ctx context.Context, owner, block string, r io.Reader, size int64) error {
	key := partKey(owner, block)
	if _, err := s.Class.Put(ctx, key, r, size, nil); err != nil {
		return fmt.Errorf("stage part: %w", err)
	}
	if err := s.Q.UpsertActionsUploadPart(ctx, dbgen.UpsertActionsUploadPartParams{
		Owner: owner, Block: block, Size: size, StorageKey: key}); err != nil {
		return fmt.Errorf("record part: %w", err)
	}
	return nil
}

// Parts lists the staged parts of owner in arrival order.
func (s *Stager) Parts(ctx context.Context, owner string) ([]dbgen.ActionsUploadPart, error) {
	p, err := s.Q.ListActionsUploadParts(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	return p, nil
}

// Commit concatenates parts into key, up to max bytes, then discards
// every part of owner.
func (s *Stager) Commit(ctx context.Context, owner string, parts []dbgen.ActionsUploadPart, key string, max int64) (tier.Object, error) {
	var total int64
	keys := make([]string, len(parts))
	for i, p := range parts {
		total += p.Size
		keys[i] = p.StorageKey
	}
	if total > max {
		return tier.Object{}, ErrTooLarge
	}
	cr := &concatReader{ctx: ctx, c: s.Class, keys: keys}
	obj, err := s.Class.Put(ctx, key, cr, total, nil)
	cr.close()
	if err != nil {
		return tier.Object{}, fmt.Errorf("commit upload: %w", err)
	}
	if err := s.Discard(ctx, owner); err != nil {
		s.Log.WarnContext(ctx, "discard upload parts", "owner", owner, "error", err)
	}
	return obj, nil
}

// Discard removes every staged part of owner.
func (s *Stager) Discard(ctx context.Context, owner string) error {
	parts, err := s.Parts(ctx, owner)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if err := s.Class.Delete(ctx, p.StorageKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("delete part: %w", err)
		}
	}
	if err := s.Q.DeleteActionsUploadParts(ctx, owner); err != nil {
		return fmt.Errorf("delete parts: %w", err)
	}
	return nil
}

// SweepStale discards the uploads whose parts are older than before.
func (s *Stager) SweepStale(ctx context.Context, before time.Time) error {
	for {
		owners, err := s.Q.ListStaleActionsUploadOwners(ctx, dbgen.ListStaleActionsUploadOwnersParams{
			Before: Timestamp(before), Lim: 100})
		if err != nil {
			return fmt.Errorf("list stale uploads: %w", err)
		}
		for _, o := range owners {
			if err := s.Discard(ctx, o); err != nil {
				return err
			}
		}
		if len(owners) < 100 {
			return nil
		}
	}
}

// concatReader reads the parts one after the other, opening each lazily.
type concatReader struct {
	ctx  context.Context
	c    *tier.Class
	keys []string
	cur  io.ReadCloser
}

func (r *concatReader) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			if len(r.keys) == 0 {
				return 0, io.EOF
			}
			rc, _, err := r.c.Get(r.ctx, r.keys[0], nil)
			if err != nil {
				return 0, fmt.Errorf("open part: %w", err)
			}
			r.cur, r.keys = rc, r.keys[1:]
		}
		n, err := r.cur.Read(p)
		if errors.Is(err, io.EOF) {
			r.close()
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *concatReader) close() {
	if r.cur != nil {
		_ = r.cur.Close()
		r.cur = nil
	}
}

// Upload is the target of one signed upload URL.
type Upload struct {
	// Owner names the upload's staged parts.
	Owner string
	// Key is the storage key of the committed object.
	Key string
	// Max bounds the committed object.
	Max int64
	// Done, when not nil, runs once the object is committed.
	Done func(ctx context.Context, obj tier.Object) error
}

// ServeUpload implements the subset of the Azure Blob API the toolkit
// uses on a signed URL: Put Blob (single shot), Put Block + Put Block
// List (staged), and the append blob calls of older clients (committed
// later with CommitAppended).
func (s *Stager) ServeUpload(w http.ResponseWriter, r *http.Request, u Upload) {
	ctx := r.Context()
	q := r.URL.Query()
	comp := strings.ToLower(q.Get("comp"))
	if comp == "blocklist" {
		s.serveBlockList(w, r, u)
		return
	}
	size := r.ContentLength
	if size < 0 {
		Problem(w, http.StatusLengthRequired, "Content-Length is required")
		return
	}
	if size > u.Max {
		Problem(w, http.StatusRequestEntityTooLarge, "upload exceeds the size limit")
		return
	}
	switch comp {
	case "block":
		id := q.Get("blockid")
		if id == "" || len(id) > 128 {
			Problem(w, http.StatusBadRequest, "invalid blockid")
			return
		}
		if err := s.Put(ctx, u.Owner, id, r.Body, size); err != nil {
			s.fail(w, r, err)
			return
		}
	case "appendblock":
		if err := s.Put(ctx, u.Owner, appendBlockName(), r.Body, size); err != nil {
			s.fail(w, r, err)
			return
		}
	case "":
		if strings.EqualFold(r.Header.Get("x-ms-blob-type"), "AppendBlob") {
			// Create an empty append blob: forget any earlier attempt.
			if err := s.Discard(ctx, u.Owner); err != nil {
				s.fail(w, r, err)
				return
			}
			break
		}
		obj, err := s.Class.Put(ctx, u.Key, r.Body, size, nil)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.done(ctx, u, obj); err != nil {
			s.fail(w, r, err)
			return
		}
	default:
		Problem(w, http.StatusBadRequest, "unsupported comp")
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// appendBlockName names an appended block; parts keep arrival order.
func appendBlockName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "append-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + hex.EncodeToString(b)
}

// blockList is a Put Block List body: Latest, Committed and Uncommitted
// elements in order.
type blockList struct {
	Items []struct {
		Value string `xml:",chardata"`
	} `xml:",any"`
}

func (s *Stager) serveBlockList(w http.ResponseWriter, r *http.Request, u Upload) {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBlockList+1))
	if err != nil || len(body) > maxBlockList {
		Problem(w, http.StatusBadRequest, "block list unreadable or too large")
		return
	}
	var bl blockList
	if err := xml.Unmarshal(body, &bl); err != nil {
		Problem(w, http.StatusBadRequest, "invalid block list")
		return
	}
	parts, err := s.Parts(ctx, u.Owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byName := make(map[string]dbgen.ActionsUploadPart, len(parts))
	for _, p := range parts {
		byName[p.Block] = p
	}
	ordered := make([]dbgen.ActionsUploadPart, 0, len(bl.Items))
	for _, it := range bl.Items {
		p, ok := byName[strings.TrimSpace(it.Value)]
		if !ok {
			Problem(w, http.StatusBadRequest, "block list names a block not uploaded")
			return
		}
		ordered = append(ordered, p)
	}
	obj, err := s.Commit(ctx, u.Owner, ordered, u.Key, u.Max)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.done(ctx, u, obj); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// CommitAppended commits the appended blocks of an append blob upload, in
// arrival order. It returns blob.ErrNotFound when nothing was appended.
func (s *Stager) CommitAppended(ctx context.Context, u Upload) (tier.Object, error) {
	parts, err := s.Parts(ctx, u.Owner)
	if err != nil {
		return tier.Object{}, err
	}
	if len(parts) == 0 {
		return tier.Object{}, blob.ErrNotFound
	}
	obj, err := s.Commit(ctx, u.Owner, parts, u.Key, u.Max)
	if err != nil {
		return tier.Object{}, err
	}
	return obj, s.done(ctx, u, obj)
}

func (s *Stager) done(ctx context.Context, u Upload, obj tier.Object) error {
	if u.Done == nil {
		return nil
	}
	return u.Done(ctx, obj)
}

func (s *Stager) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrTooLarge) {
		Problem(w, http.StatusRequestEntityTooLarge, "upload exceeds the size limit")
		return
	}
	s.Log.ErrorContext(r.Context(), "actions results upload", "error", err)
	Problem(w, http.StatusInternalServerError, "")
}

// ServeBlob streams a committed object; filename, when set, is offered as
// the attachment name.
func ServeBlob(w http.ResponseWriter, r *http.Request, c *tier.Class, log *slog.Logger, key, contentType, filename string) {
	rc, obj, err := c.Get(r.Context(), key, nil)
	if errors.Is(err, blob.ErrNotFound) {
		Problem(w, http.StatusNotFound, "")
		return
	}
	if err != nil {
		log.ErrorContext(r.Context(), "actions results download", "error", err)
		Problem(w, http.StatusInternalServerError, "")
		return
	}
	defer func() { _ = rc.Close() }()
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	h.Set("Cache-Control", "private, no-store")
	if filename != "" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		log.WarnContext(r.Context(), "actions results download interrupted", "error", err)
	}
}
