package issues

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Attachment storage: the "attachments" data type, "lfs" as fallback.
const (
	PolicyAttachments = "attachments"
	PolicyLFS         = "lfs"

	// DefaultMaxAttachmentSize bounds one attachment by default (25 MiB).
	DefaultMaxAttachmentSize int64 = 25 << 20
)

// Attachment is a file uploaded for an issue or comment body.
type Attachment struct {
	ID          uuid.UUID
	Name        string
	ContentType string
	Size        int64
	UploaderID  uuid.UUID
	CreatedAt   time.Time
}

// AttachmentInput is an upload. Size must be known.
type AttachmentInput struct {
	Name        string
	ContentType string
	Size        int64
	Body        io.Reader
}

func attachmentFrom(a dbgen.IssueAttachment) Attachment {
	return Attachment{ID: a.ID, Name: a.Name, ContentType: a.ContentType, Size: a.Size, UploaderID: a.UploaderID.UUID,
		CreatedAt: a.CreatedAt.Time}
}

func (s *Service) class() (*tier.Class, error) {
	if s.o.Storage == nil {
		return nil, ErrNoStorage
	}
	for _, p := range []string{PolicyAttachments, PolicyLFS} {
		if c, err := s.o.Storage.Class(p); err == nil {
			return c, nil
		}
	}
	return nil, ErrNoStorage
}

func checkFileName(n string) error {
	if n == "" || n == "." || n == ".." {
		return invalid("file name is required")
	}
	if err := checkText("file name", n, 255); err != nil {
		return err
	}
	for _, c := range n {
		if c == '/' || c == '\\' || c < 0x20 || c == 0x7f {
			return invalid("file name must not contain slashes or control characters")
		}
	}
	return nil
}

// UploadAttachment stores a file for the issues of owner/name (any signed-in
// reader). The bytes are charged to the quota of the repository owner.
func (s *Service) UploadAttachment(ctx context.Context, who auth.Principal, owner, name string, in AttachmentInput) (Attachment, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Attachment{}, err
	}
	if err := checkFileName(in.Name); err != nil {
		return Attachment{}, err
	}
	if in.Size < 0 {
		return Attachment{}, invalid("the file size must be known")
	}
	if in.Size > s.o.MaxAttachmentSize {
		return Attachment{}, invalid("file is larger than the limit of %d bytes", s.o.MaxAttachmentSize)
	}
	cl, err := s.class()
	if err != nil {
		return Attachment{}, err
	}
	ctype := in.ContentType
	if ctype == "" {
		ctype = mime.TypeByExtension(path.Ext(in.Name))
	}
	if mt, _, err := mime.ParseMediaType(ctype); err != nil || len(ctype) > 255 {
		ctype = "application/octet-stream"
	} else {
		ctype = mt
	}
	id, err := newID()
	if err != nil {
		return Attachment{}, err
	}
	if err := quota.Reserve(ctx, s.q, r.OwnerID, quota.Delta{Bytes: in.Size}); err != nil {
		return Attachment{}, err
	}
	refund := func() {
		if err := quota.Release(context.WithoutCancel(ctx), s.q, r.OwnerID, quota.Delta{Bytes: in.Size}); err != nil {
			s.log.ErrorContext(ctx, "release attachment quota", "repo", r.ID, "error", err)
		}
	}
	key := fmt.Sprintf("attachments/%s/%s", r.ID, id)
	obj, err := cl.Put(ctx, key, in.Body, in.Size, nil)
	if err != nil {
		refund()
		return Attachment{}, fmt.Errorf("store attachment: %w", err)
	}
	row, err := s.q.InsertIssueAttachment(ctx, dbgen.InsertIssueAttachmentParams{ID: id, RepoID: r.ID, UploaderID: nullID(who.UserID),
		Name: in.Name, ContentType: ctype, Size: obj.Size, Sha256: obj.SHA256, StoragePolicy: cl.Name(), StorageKey: key})
	if err != nil {
		refund()
		if delErr := cl.Delete(context.WithoutCancel(ctx), key); delErr != nil {
			s.log.WarnContext(ctx, "delete unreferenced attachment object", "key", key, "error", delErr)
		}
		return Attachment{}, fmt.Errorf("insert attachment: %w", err)
	}
	return attachmentFrom(row), nil
}

// GetAttachment returns the metadata of an attachment for a reader of
// owner/name.
func (s *Service) GetAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (Attachment, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return Attachment{}, err
	}
	row, err := s.q.GetIssueAttachment(ctx, dbgen.GetIssueAttachmentParams{ID: id, RepoID: r.ID})
	if err != nil {
		return Attachment{}, notFound(err, ErrAttachmentGone, "get attachment")
	}
	return attachmentFrom(row), nil
}

// OpenAttachment opens the bytes of an attachment (rng selects a part) for
// a reader of owner/name. The caller closes the reader.
func (s *Service) OpenAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, rng *blob.Range) (io.ReadCloser, Attachment, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, Attachment{}, err
	}
	row, err := s.q.GetIssueAttachment(ctx, dbgen.GetIssueAttachmentParams{ID: id, RepoID: r.ID})
	if err != nil {
		return nil, Attachment{}, notFound(err, ErrAttachmentGone, "get attachment")
	}
	if s.o.Storage == nil {
		return nil, Attachment{}, ErrNoStorage
	}
	cl, err := s.o.Storage.Class(row.StoragePolicy)
	if err != nil {
		return nil, Attachment{}, fmt.Errorf("attachment storage: %w", err)
	}
	rc, _, err := cl.Get(ctx, row.StorageKey, rng)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return nil, Attachment{}, fmt.Errorf("attachment %s: object missing: %w", row.ID, err)
		}
		return nil, Attachment{}, fmt.Errorf("open attachment: %w", err)
	}
	return rc, attachmentFrom(row), nil
}

// DeleteAttachment removes an attachment (its uploader or triage), frees
// its bytes and refunds the quota.
func (s *Service) DeleteAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return err
	}
	var a dbgen.IssueAttachment
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		cur, err := q.GetIssueAttachment(ctx, dbgen.GetIssueAttachmentParams{ID: id, RepoID: r.ID})
		if err != nil {
			return notFound(err, ErrAttachmentGone, "get attachment")
		}
		if (!cur.UploaderID.Valid || cur.UploaderID.UUID != who.UserID) && !canTriage(r) {
			return ErrForbidden
		}
		if a, err = q.DeleteIssueAttachment(ctx, dbgen.DeleteIssueAttachmentParams{ID: id, RepoID: r.ID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAttachmentGone
			}
			return fmt.Errorf("delete attachment: %w", err)
		}
		if err := quota.Release(ctx, q, r.OwnerID, quota.Delta{Bytes: a.Size}); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, ActionAttachmentDeleted, r, "issue_attachment", id, c,
			map[string]any{"name": a.Name, "size": a.Size}))
	})
	if err != nil {
		return err
	}
	cl, err := s.o.Storage.Class(a.StoragePolicy)
	if err == nil {
		err = cl.Delete(context.WithoutCancel(ctx), a.StorageKey)
	}
	if err != nil {
		s.log.WarnContext(ctx, "delete attachment object", "attachment", a.ID, "policy", a.StoragePolicy, "error", err)
	}
	return nil
}
