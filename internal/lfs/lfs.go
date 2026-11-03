// Package lfs implements Git LFS (PLAN §4.4): the batch API with the basic
// transfer adapter, the Locks API, git-lfs-authenticate and git-lfs-transfer
// over SSH, short-lived tokens, quota accounting and a conservative GC.
//
// Storage layout: objects are content-addressed and deduplicated across the
// instance in the tier class "lfs" under sha256/<oid[0:2]>/<oid[2:4]>/<oid>
// (full key lfs/sha256/ab/cd/<oid>). A row in lfs_objects(repo_id, oid)
// authorizes a repository to download an object and charges its size to the
// quota of the repository owner; every repository referencing an object is
// charged for it. A repository gains a row only by proving possession of the
// content: a proxied upload is hashed by the server even when the object is
// already stored, and a presigned upload is only handed out for objects that
// are not stored yet (the store enforces the checksum).
//
// Transfers: when the primary target supports presigned URLs and is not
// encrypted (S3), uploads go straight to it and downloads not held by the
// local cache are redirected to it; otherwise (SFTP, local, client-side
// encryption) the server proxies the bytes and verifies sha256 and size on
// the fly.
package lfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/githttp"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Policy is the storage data type of LFS objects.
const Policy = "lfs"

// Lifetimes.
const (
	// TokenTTL bounds the short-lived tokens of git-lfs-authenticate and of
	// the batch action headers.
	TokenTTL = time.Hour
	// PresignTTL bounds presigned upload and download URLs.
	PresignTTL = 15 * time.Minute
)

const (
	tokenPrefix = "erl_"
	tokenBytes  = 32
	// maxBatchObjects bounds the objects of one batch request.
	maxBatchObjects = 1000
)

// Options configure the server.
type Options struct {
	Pool    *pgxpool.Pool
	Storage *tier.Class // the "lfs" class
	// Git is the hot repository tier; the GC scans hot repositories for
	// LFS pointers through it. nil disables the reference GC.
	Git *gitstore.Tier
	// PublicURL is the external base URL of the server (hrefs).
	PublicURL string
	Log       *slog.Logger
}

// Server serves LFS over HTTP and SSH.
type Server struct {
	o    Options
	q    *dbgen.Queries
	base *url.URL
	now  func() time.Time
}

// New returns the server.
func New(o Options) (*Server, error) {
	if o.Pool == nil || o.Storage == nil {
		return nil, errors.New("lfs: pool and storage are required")
	}
	base, err := url.Parse(strings.TrimSuffix(o.PublicURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("lfs: invalid public URL %q", o.PublicURL)
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{o: o, q: dbgen.New(o.Pool), base: base, now: time.Now}, nil
}

// validOID reports whether oid is a lowercase hex SHA-256.
func validOID(oid string) bool {
	if len(oid) != 64 {
		return false
	}
	for i := 0; i < len(oid); i++ {
		c := oid[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// objectKey is the key of an object in the "lfs" class.
func objectKey(oid string) string { return "sha256/" + oid[:2] + "/" + oid[2:4] + "/" + oid }

func oidBytes(oid string) []byte {
	b, _ := hex.DecodeString(oid)
	return b
}

// href returns the external URL of an LFS endpoint of repo.
func (s *Server) href(repo repos.Repo, path string) string {
	u := *s.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + repo.Owner + "/" + repo.Name + ".git/info/lfs"
	if path != "" {
		u.Path += "/" + path
	}
	return u.String()
}

// issueToken creates a short-lived token acting for user on repo.
func (s *Server) issueToken(ctx context.Context, repoID, userID uuid.UUID, upload bool) (string, time.Time, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("lfs token: %w", err)
	}
	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(tok))
	id, err := uuid.NewV7()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("lfs token id: %w", err)
	}
	op := "download"
	if upload {
		op = "upload"
	}
	exp := s.now().Add(TokenTTL)
	if err := s.q.InsertLFSToken(ctx, dbgen.InsertLFSTokenParams{
		ID: id, TokenHash: sum[:], RepoID: repoID, UserID: userID, Operation: op,
		ExpiresAt: pgtype.Timestamptz{Time: exp, Valid: true},
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("store lfs token: %w", err)
	}
	return tok, exp, nil
}

// LookupToken implements githttp.LFS.
func (s *Server) LookupToken(ctx context.Context, tok string) (githttp.LFSToken, error) {
	if !strings.HasPrefix(tok, tokenPrefix) || len(tok) != len(tokenPrefix)+base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return githttp.LFSToken{}, githttp.ErrNoLFSToken
	}
	sum := sha256.Sum256([]byte(tok))
	row, err := s.q.GetLFSToken(ctx, sum[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return githttp.LFSToken{}, githttp.ErrNoLFSToken
	}
	if err != nil {
		return githttp.LFSToken{}, fmt.Errorf("look up lfs token: %w", err)
	}
	if !row.ExpiresAt.Time.After(s.now()) || row.SuspendedAt.Valid {
		return githttp.LFSToken{}, githttp.ErrNoLFSToken
	}
	upload := row.Operation == "upload"
	scopes := []string{auth.ScopeReadRepo}
	if upload {
		scopes = []string{auth.ScopeWriteRepo}
	}
	return githttp.LFSToken{
		Principal: auth.Principal{UserID: row.UserID, Username: row.Username, Role: row.Role,
			Method: auth.MethodToken, TokenID: row.ID, Scopes: scopes},
		RepoID: row.RepoID, Upload: upload,
	}, nil
}

// tx runs fn in a transaction.
func (s *Server) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.o.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// grant records that repo references oid and charges its size to the
// repository owner. It fails with a quota.ExceededError (recording nothing)
// when the owner is over its storage quota.
func (s *Server) grant(ctx context.Context, repo repos.Repo, oid string, size int64) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.InsertLFSObject(ctx, dbgen.InsertLFSObjectParams{RepoID: repo.ID, Oid: oid, Size: size, ChargedTo: repo.OwnerID})
		if err != nil {
			return fmt.Errorf("record lfs object: %w", err)
		}
		if n == 0 {
			return nil
		}
		return quota.Reserve(ctx, q, repo.OwnerID, quota.Delta{Bytes: size})
	})
}

// has returns the objects among oids that repo references, by oid.
func (s *Server) has(ctx context.Context, repoID uuid.UUID, oids []string) (map[string]dbgen.LfsObject, error) {
	rows, err := s.q.ListLFSObjectsByOIDs(ctx, dbgen.ListLFSObjectsByOIDsParams{RepoID: repoID, Oids: oids})
	if err != nil {
		return nil, fmt.Errorf("list lfs objects: %w", err)
	}
	out := make(map[string]dbgen.LfsObject, len(rows))
	for _, r := range rows {
		out[r.Oid] = r
	}
	return out, nil
}

// Usage returns the number and total size of the LFS objects referenced
// by a repository.
func (s *Server) Usage(ctx context.Context, repoID uuid.UUID) (objects, bytes int64, err error) {
	row, err := s.q.RepoLFSUsage(ctx, repoID)
	if err != nil {
		return 0, 0, fmt.Errorf("lfs usage: %w", err)
	}
	return row.Objects, row.SizeBytes, nil
}
