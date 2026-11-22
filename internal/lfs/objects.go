package lfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Errors of the object operations shared by HTTP and SSH.
var (
	errNotFound   = errors.New("object not found")
	errBadContent = errors.New("object content does not match its oid or size")
	errForbidden  = errors.New("operation not permitted")
)

// ErrPreviewNotFound does not distinguish a missing object from one not linked to the repository.
var ErrPreviewNotFound = errors.New("the Git LFS object is not available in this repository")

// canRead reports whether the caller may download from repo.
func canRead(repo repos.Repo, who auth.Principal) bool {
	return authz.Allowed(repo.Permission, authz.ActRead) &&
		(who.Method != auth.MethodToken || who.HasScope(auth.ScopeReadRepo))
}

// canWrite reports whether the caller may upload to repo and manage locks.
func canWrite(repo repos.Repo, who auth.Principal) bool {
	return authz.Allowed(repo.Permission, authz.ActPush) &&
		(who.Method != auth.MethodToken || who.HasScope(auth.ScopeWriteRepo))
}

// canAdmin reports whether the caller may break other users' locks.
func canAdmin(repo repos.Repo, who auth.Principal) bool {
	return (authz.Allowed(repo.Permission, authz.ActSettings) || who.IsAdmin()) && canWrite(repo, who)
}

// lookup returns the row of oid in repo, or errNotFound.
func (s *Server) lookup(ctx context.Context, repo repos.Repo, oid string) (dbgen.LfsObject, error) {
	row, err := s.q.GetLFSObject(ctx, dbgen.GetLFSObjectParams{RepoID: repo.ID, Oid: oid})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get lfs object: %w", err)
	}
	return row, nil
}

// receive stores size bytes of body as oid for repo. The body is always
// hashed by the server: an object already stored is not written again but
// the caller must still prove it has the content.
func (s *Server) receive(ctx context.Context, repo repos.Repo, oid string, size int64, body io.Reader) error {
	if size < 0 {
		return fmt.Errorf("%w: negative size", errBadContent)
	}
	if row, err := s.lookup(ctx, repo, oid); err == nil {
		if row.Size != size {
			return fmt.Errorf("%w: recorded with %d bytes", errBadContent, row.Size)
		}
		// Already referenced: drain what the client sends.
		_, _ = io.Copy(io.Discard, io.LimitReader(body, size))
		return nil
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	if err := quota.Check(ctx, s.q, repo.OwnerID, quota.Delta{Bytes: size}); err != nil {
		return err
	}
	key := objectKey(oid)
	want := oidBytes(oid)
	if obj, err := s.o.Storage.Stat(ctx, key); err == nil && obj.Size == size && bytes.Equal(obj.SHA256, want) {
		if err := hashBody(body, size, want); err != nil {
			return err
		}
	} else {
		if err != nil && !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("stat lfs object: %w", err)
		}
		if _, err := s.o.Storage.Put(ctx, key, body, size, want); err != nil {
			if badContent(err) {
				return fmt.Errorf("%w: %w", errBadContent, err)
			}
			return fmt.Errorf("store lfs object: %w", err)
		}
	}
	return s.grant(ctx, repo, oid, size)
}

// hashBody reads exactly size bytes and checks their sha256.
func hashBody(body io.Reader, size int64, want []byte) error {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(body, size))
	if err != nil {
		return fmt.Errorf("read lfs object: %w", err)
	}
	if n != size {
		return fmt.Errorf("%w: got %d of %d bytes", errBadContent, n, size)
	}
	// The client must not send more than announced.
	var extra [1]byte
	if m, _ := body.Read(extra[:]); m > 0 {
		return fmt.Errorf("%w: more than %d bytes", errBadContent, size)
	}
	if !bytes.Equal(h.Sum(nil), want) {
		return fmt.Errorf("%w: sha256 mismatch", errBadContent)
	}
	return nil
}

func badContent(err error) bool {
	return errors.Is(err, blob.ErrChecksumMismatch) || errors.Is(err, blob.ErrSizeMismatch) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, tier.ErrCorrupt)
}

// open returns the content of oid (or the range rng) for repo.
func (s *Server) open(ctx context.Context, repo repos.Repo, oid string, rng *blob.Range) (io.ReadCloser, int64, error) {
	row, err := s.lookup(ctx, repo, oid)
	if err != nil {
		return nil, 0, err
	}
	rc, err := s.openObject(ctx, oid, rng)
	if err != nil {
		return nil, 0, err
	}
	return rc, row.Size, nil
}

func (s *Server) openObject(ctx context.Context, oid string, rng *blob.Range) (io.ReadCloser, error) {
	rc, _, err := s.o.Storage.Get(ctx, objectKey(oid), rng)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open lfs object: %w", err)
	}
	return rc, nil
}

// Preview authorizes a pointer before serving metadata or opening a byte range.
func (s *Server) Preview(ctx context.Context, repo repos.Repo, who auth.Principal, oid string, size int64) (func(*blob.Range) (io.ReadCloser, error), error) {
	if !canRead(repo, who) || !validOID(oid) {
		return nil, ErrPreviewNotFound
	}
	if perms, ok := who.AppPermissions(); ok && !perms.Has(authz.AppContents, authz.AppRead) {
		return nil, ErrPreviewNotFound
	}
	row, err := s.lookup(ctx, repo, oid)
	if errors.Is(err, errNotFound) || err == nil && row.Size != size {
		return nil, ErrPreviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return func(rng *blob.Range) (io.ReadCloser, error) {
		rc, err := s.openObject(ctx, oid, rng)
		if errors.Is(err, errNotFound) {
			return nil, ErrPreviewNotFound
		}
		return rc, err
	}, nil
}

// verify confirms that oid is available to repo with the given size. After
// a presigned upload it records the object, which the store accepted only
// with the announced checksum. An object that was already stored before
// (whose presigned upload the server never handed out) is not adopted: the
// client must upload it through the server to prove possession.
func (s *Server) verify(ctx context.Context, repo repos.Repo, oid string, size int64) error {
	row, err := s.lookup(ctx, repo, oid)
	if err == nil {
		if row.Size != size {
			return fmt.Errorf("%w: recorded with %d bytes", errBadContent, row.Size)
		}
		return nil
	}
	if !errors.Is(err, errNotFound) {
		return err
	}
	if !s.o.Storage.CanPresign() {
		return errNotFound
	}
	key := objectKey(oid)
	if _, err := s.o.Storage.Stat(ctx, key); err == nil {
		return errNotFound
	} else if !errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("stat lfs object: %w", err)
	}
	if _, err := s.o.Storage.Adopt(ctx, key, size, oidBytes(oid)); err != nil {
		switch {
		case errors.Is(err, blob.ErrNotFound):
			return errNotFound
		case badContent(err):
			return fmt.Errorf("%w: %w", errBadContent, err)
		}
		return fmt.Errorf("adopt lfs object: %w", err)
	}
	return s.grant(ctx, repo, oid, size)
}

// status maps an object error to an HTTP status (also used by SSH).
func status(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, errNotFound):
		return http.StatusNotFound
	case errors.Is(err, errBadContent):
		return http.StatusUnprocessableEntity
	case errors.Is(err, errForbidden):
		return http.StatusForbidden
	case quota.IsExceeded(err):
		return http.StatusInsufficientStorage
	case errors.Is(err, context.Canceled):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// message is the client-facing text of an object error.
func message(err error) string {
	switch status(err) {
	case http.StatusInternalServerError:
		return "internal error"
	case http.StatusInsufficientStorage:
		return "storage quota exceeded"
	}
	return err.Error()
}
