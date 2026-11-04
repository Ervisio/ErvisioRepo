// Package repos implements repositories as records: creation, settings,
// rename and transfer with redirects, deletion and collaborators (PLAN
// §4.3, §5.2). The git data lives behind gitstore.Tier; this package only
// creates the bare repository on Create and trashes it on Delete.
package repos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Errors returned by the service; their messages are safe to show.
var (
	// ErrNotFound hides repositories and namespaces the caller cannot see.
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("you do not have permission to do this")
	ErrNameTaken = errors.New("a repository with this name already exists")
)

// ValidationError is an invalid input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// MovedError is returned for an old path of a repository the caller can
// see: the repository now lives at Owner/Name (HTTP 301).
type MovedError struct{ Owner, Name string }

func (e *MovedError) Error() string { return "repository moved to " + e.Owner + "/" + e.Name }

// MaxDescription is the longest description.
const MaxDescription = 1000

// Page limits of the list calls.
const (
	DefaultLimit = 30
	MaxLimit     = 100
)

// Repo is a repository as seen by a caller.
type Repo struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	Owner         string
	OwnerKind     string // "user" or "org"
	Name          string
	Description   string
	Visibility    authz.Visibility
	DefaultBranch string
	StorageState  string
	SizeBytes     int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Permission is the caller's effective permission (at least PermRead).
	Permission authz.Permission
}

// FullName is "owner/name".
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

// Collaborator is a direct grant on a repository.
type Collaborator struct {
	UserID      uuid.UUID
	Username    string
	DisplayName string
	Permission  authz.Permission
	Since       time.Time
}

// Service implements the repository flows.
type Service struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
	tier *gitstore.Tier
	jobs identity.Enqueuer
	log  *slog.Logger

	hooksMu     sync.RWMutex
	deleteHooks []DeleteHook
}

// DeleteHook is told about the deletion of a repository. RepoDeleting runs
// inside the transaction that deletes the row, before the delete (the rows
// that cascade with it are still there); the returned function, if any,
// runs after the commit with a context that is not cancelled. An error
// aborts the deletion.
type DeleteHook interface {
	RepoDeleting(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID) (after func(context.Context), err error)
}

// OnDelete registers a deletion hook (wiring, before serving).
func (s *Service) OnDelete(h DeleteHook) {
	s.hooksMu.Lock()
	defer s.hooksMu.Unlock()
	s.deleteHooks = append(s.deleteHooks, h)
}

// runDeleteHooks calls the deletion hooks inside the delete transaction.
func (s *Service) runDeleteHooks(ctx context.Context, q *dbgen.Queries, id uuid.UUID) ([]func(context.Context), error) {
	s.hooksMu.RLock()
	hooks := slices.Clone(s.deleteHooks)
	s.hooksMu.RUnlock()
	var after []func(context.Context)
	for _, h := range hooks {
		fn, err := h.RepoDeleting(ctx, q, id)
		if err != nil {
			return nil, fmt.Errorf("deletion hook: %w", err)
		}
		if fn != nil {
			after = append(after, fn)
		}
	}
	return after, nil
}

// Options configures a Service.
type Options struct {
	Pool *pgxpool.Pool
	// Tier creates and trashes the bare repositories; required.
	Tier *gitstore.Tier
	// Jobs enqueues the purge of deleted repositories; nil skips it (tests).
	Jobs identity.Enqueuer
	Log  *slog.Logger
}

// New builds a Service.
func New(o Options) *Service {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{pool: o.Pool, q: dbgen.New(o.Pool), tier: o.Tier, jobs: o.Jobs, log: log}
}

func repoFrom(r dbgen.Repo, owner, kind string, p authz.Permission) Repo {
	return Repo{ID: r.ID, OwnerID: r.OwnerID, Owner: owner, OwnerKind: kind, Name: r.Name, Description: r.Description,
		Visibility: authz.Visibility(r.Visibility), DefaultBranch: r.DefaultBranch, StorageState: r.StorageState,
		SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, Permission: p}
}

// Facts loads what the permission of who on repo depends on.
func Facts(ctx context.Context, q *dbgen.Queries, who auth.Principal, r dbgen.Repo, ownerKind string) (authz.RepoFacts, error) {
	if who.Grant != nil {
		// An Actions job token: its grant on one repository, anonymous
		// access elsewhere.
		f := authz.RepoFacts{Visibility: authz.Visibility(r.Visibility)}
		if r.ID == who.Grant.RepoID {
			f.Signed = true
			f.Collaborator, _ = authz.ParsePermission(who.Grant.Permission)
		}
		return f, nil
	}
	f := authz.RepoFacts{
		Signed:        who.UserID != uuid.Nil,
		InstanceAdmin: who.IsAdmin(),
		Restricted:    who.Role == auth.RoleRestricted,
		Visibility:    authz.Visibility(r.Visibility),
	}
	if !f.Signed {
		return f, nil
	}
	f.PersonalOwner = ownerKind == "user" && r.OwnerID == who.UserID
	acc, err := q.GetRepoAccess(ctx, dbgen.GetRepoAccessParams{UserID: who.UserID, RepoID: r.ID})
	if err != nil {
		return f, fmt.Errorf("get repo access: %w", err)
	}
	f.OrgRole = authz.OrgRole(acc.OrgRole)
	f.BasePermission, _ = authz.ParsePermission(acc.BasePermission)
	f.Collaborator, _ = authz.ParsePermission(acc.Collaborator)
	teams, err := q.ListUserTeamRepoPermissions(ctx, dbgen.ListUserTeamRepoPermissionsParams{RepoID: r.ID, UserID: who.UserID})
	if err != nil {
		return f, fmt.Errorf("list team permissions: %w", err)
	}
	for _, t := range teams {
		if p, err := authz.ParsePermission(t); err == nil {
			f.Teams = append(f.Teams, p)
		}
	}
	return f, nil
}

// resolve finds owner/name for the caller. Paths of repositories the
// caller cannot read are ErrNotFound; old paths are a MovedError.
func (s *Service) resolve(ctx context.Context, q *dbgen.Queries, who auth.Principal, owner, name string) (Repo, dbgen.Repo, error) {
	row, err := q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		id, rerr := q.GetRepoRedirect(ctx, dbgen.GetRepoRedirectParams{Owner: owner, Name: name})
		if errors.Is(rerr, pgx.ErrNoRows) {
			return Repo{}, dbgen.Repo{}, ErrNotFound
		}
		if rerr != nil {
			return Repo{}, dbgen.Repo{}, fmt.Errorf("get redirect: %w", rerr)
		}
		r, err := s.byID(ctx, q, who, id)
		if err != nil {
			return Repo{}, dbgen.Repo{}, err
		}
		return Repo{}, dbgen.Repo{}, &MovedError{Owner: r.Owner, Name: r.Name}
	}
	if err != nil {
		return Repo{}, dbgen.Repo{}, fmt.Errorf("get repo: %w", err)
	}
	r, err := s.view(ctx, q, who, row.Repo, row.OwnerName, row.OwnerKind)
	return r, row.Repo, err
}

func (s *Service) byID(ctx context.Context, q *dbgen.Queries, who auth.Principal, id uuid.UUID) (Repo, error) {
	row, err := q.GetRepo(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	if err != nil {
		return Repo{}, fmt.Errorf("get repo: %w", err)
	}
	return s.view(ctx, q, who, row.Repo, row.OwnerName, row.OwnerKind)
}

func (s *Service) view(ctx context.Context, q *dbgen.Queries, who auth.Principal, r dbgen.Repo, owner, kind string) (Repo, error) {
	f, err := Facts(ctx, q, who, r, kind)
	if err != nil {
		return Repo{}, err
	}
	p := authz.RepoPermission(f)
	if !authz.Allowed(p, authz.ActRead) {
		return Repo{}, ErrNotFound
	}
	return repoFrom(r, owner, kind, p), nil
}

// Get returns owner/name if the caller can read it. An old path of the
// repository is a *MovedError.
func (s *Service) Get(ctx context.Context, who auth.Principal, owner, name string) (Repo, error) {
	r, _, err := s.resolve(ctx, s.q, who, owner, name)
	return r, err
}

// GetByID returns a repository by ID if the caller can read it.
func (s *Service) GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (Repo, error) {
	return s.byID(ctx, s.q, who, id)
}

// Authorize returns owner/name if the caller may perform action on it:
// ErrNotFound when it cannot even read it, ErrForbidden otherwise. It does
// not follow redirects (a *MovedError is returned as is).
func (s *Service) Authorize(ctx context.Context, who auth.Principal, owner, name string, action authz.Action) (Repo, error) {
	r, err := s.Get(ctx, who, owner, name)
	if err != nil {
		return Repo{}, err
	}
	if !authz.Allowed(r.Permission, action) {
		return Repo{}, ErrForbidden
	}
	return r, nil
}

// namespace resolves an owner for a caller that wants to create in it.
type namespace struct {
	id   uuid.UUID
	name string
	kind string
	role authz.OrgRole
}

func (s *Service) namespace(ctx context.Context, q *dbgen.Queries, who auth.Principal, name string) (namespace, error) {
	ns, err := q.GetNamespaceByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace{}, ErrNotFound
	}
	if err != nil {
		return namespace{}, fmt.Errorf("get namespace: %w", err)
	}
	out := namespace{id: ns.ID, name: ns.Name, kind: ns.Kind}
	if ns.Kind == "org" && who.UserID != uuid.Nil {
		r, err := q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: ns.ID, UserID: who.UserID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return namespace{}, fmt.Errorf("get member role: %w", err)
		}
		out.role = authz.OrgRole(r)
	}
	return out, nil
}

// canCreate: restricted users never create; users create in their own
// namespace, organization members in the organization, instance admins
// anywhere.
func (ns namespace) canCreate(who auth.Principal) bool {
	if who.UserID == uuid.Nil || who.Role == auth.RoleRestricted {
		return false
	}
	if who.IsAdmin() {
		return true
	}
	if ns.kind == "user" {
		return ns.id == who.UserID
	}
	return ns.role.Member()
}

func (ns namespace) orgID() uuid.UUID {
	if ns.kind == "org" {
		return ns.id
	}
	return uuid.Nil
}

func event(who auth.Principal, action string, r uuid.UUID, org uuid.UUID, c identity.Client, data map[string]any) audit.Event {
	return audit.Event{Actor: who.UserID, Action: action, TargetType: "repo", TargetID: r.String(), IP: c.IP, Data: data, OrgID: org}
}

func orgOf(kind string, id uuid.UUID) uuid.UUID {
	if kind == "org" {
		return id
	}
	return uuid.Nil
}

// CreateInput is a new repository.
type CreateInput struct {
	// Owner is the user or organization name; empty means the caller.
	Owner         string
	Name          string
	Description   string
	Visibility    string // default private
	DefaultBranch string // default main
}

func checkDescription(v string) error {
	if utf8.RuneCountInString(v) > MaxDescription {
		return invalid("description is longer than %d characters", MaxDescription)
	}
	if strings.ContainsRune(v, 0) {
		return invalid("description contains invalid characters")
	}
	return nil
}

func parseVisibility(v string) (authz.Visibility, error) {
	switch vis := authz.Visibility(v); vis {
	case authz.Public, authz.Internal, authz.Private:
		return vis, nil
	}
	return "", invalid("unknown visibility %q", v)
}

// Create creates an empty repository: the record, the quota reservation,
// the bare repository in the hot tier and its empty manifest. A plain
// organization member who creates a repository becomes its admin
// collaborator.
func (s *Service) Create(ctx context.Context, who auth.Principal, in CreateInput, c identity.Client) (Repo, error) {
	if in.Owner == "" {
		in.Owner = who.Username
	}
	if err := git.ValidateRepoName(in.Name); err != nil {
		return Repo{}, invalid("invalid repository name")
	}
	if err := checkDescription(in.Description); err != nil {
		return Repo{}, err
	}
	if in.Visibility == "" {
		in.Visibility = string(authz.Private)
	}
	vis, err := parseVisibility(in.Visibility)
	if err != nil {
		return Repo{}, err
	}
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if err := git.ValidateBranchName(in.DefaultBranch); err != nil {
		return Repo{}, invalid("invalid default branch")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Repo{}, fmt.Errorf("repo id: %w", err)
	}
	var out Repo
	created := false
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		ns, err := s.namespace(ctx, q, who, in.Owner)
		if err != nil {
			return err
		}
		if !ns.canCreate(who) {
			return ErrForbidden
		}
		if err := q.InsertRepo(ctx, dbgen.InsertRepoParams{ID: id, OwnerID: ns.id, Name: in.Name, Description: in.Description,
			Visibility: string(vis), DefaultBranch: in.DefaultBranch, StorageState: gitstore.StateHot,
			CreatedBy: uuid.NullUUID{UUID: who.UserID, Valid: true}}); err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return fmt.Errorf("insert repo: %w", err)
		}
		if err := quota.Reserve(ctx, q, ns.id, quota.Delta{Repos: 1}); err != nil {
			return err
		}
		if err := q.DeleteRepoRedirect(ctx, dbgen.DeleteRepoRedirectParams{OwnerID: ns.id, Name: in.Name}); err != nil {
			return fmt.Errorf("delete redirect: %w", err)
		}
		if err := keepAdmin(ctx, q, ns, id, who); err != nil {
			return err
		}
		if err := audit.Record(ctx, q, event(who, audit.ActionRepoCreated, id, ns.orgID(), c,
			map[string]any{"owner": ns.name, "name": in.Name, "visibility": string(vis)})); err != nil {
			return err
		}
		// The bare repository is created inside the transaction so that a
		// failure leaves no record; a failed commit is compensated below.
		if err := s.tier.Init(ctx, id, in.DefaultBranch); err != nil {
			return fmt.Errorf("init repository: %w", err)
		}
		created = true
		if out, err = s.byID(ctx, q, who, id); err != nil {
			return err
		}
		return nil
	})
	if err != nil && created {
		s.discard(ctx, id)
	}
	return out, err
}

// discard removes the git data of a repository whose record is gone.
func (s *Service) discard(ctx context.Context, id uuid.UUID) {
	ctx = context.WithoutCancel(ctx)
	if err := s.tier.Trash(ctx, id); err != nil {
		s.log.ErrorContext(ctx, "trash repository", "repo", id, "error", err)
	}
	if st := s.tier.Store(); st != nil {
		if err := st.DeleteRepo(ctx, id); err != nil {
			s.log.ErrorContext(ctx, "delete remote repository", "repo", id, "error", err)
		}
	}
}

// ListOwner lists the repositories of a user or organization the caller
// can see.
func (s *Service) ListOwner(ctx context.Context, who auth.Principal, owner string, offset, limit int) ([]Repo, error) {
	ns, err := s.namespace(ctx, s.q, who, owner)
	if err != nil {
		return nil, err
	}
	all := who.IsAdmin() || (ns.kind == "user" && ns.id == who.UserID) || ns.role.Manager()
	rows, err := s.q.ListOwnerRepos(ctx, dbgen.ListOwnerReposParams{OwnerID: ns.id, AllRepos: all,
		Internal: who.UserID != uuid.Nil && who.Role != auth.RoleRestricted, Viewer: who.UserID,
		Off: pageOffset(offset), Lim: int32(pageLimit(limit))}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	out := make([]Repo, 0, len(rows))
	for _, r := range rows {
		v, err := s.view(ctx, s.q, who, r.Repo, r.OwnerName, r.OwnerKind)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ListMine lists the repositories the caller reaches by ownership,
// membership or grant (not merely by visibility).
func (s *Service) ListMine(ctx context.Context, who auth.Principal, offset, limit int) ([]Repo, error) {
	if who.UserID == uuid.Nil {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListUserAccessibleRepos(ctx, dbgen.ListUserAccessibleReposParams{UserID: who.UserID,
		Off: pageOffset(offset), Lim: int32(pageLimit(limit))}) //nolint:gosec // pageLimit clamps to MaxLimit
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	out := make([]Repo, 0, len(rows))
	for _, r := range rows {
		v, err := s.view(ctx, s.q, who, r.Repo, r.OwnerName, r.OwnerKind)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// pageOffset clamps a caller offset to the int32 range of the query.
func pageOffset(n int) int32 {
	return int32(min(max(n, 0), math.MaxInt32)) //nolint:gosec // clamped
}

func pageLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return min(n, MaxLimit)
}

// UpdateInput changes a repository; nil fields stay. Name renames it (old
// path redirects); Visibility needs admin, the rest maintain.
type UpdateInput struct {
	Name          *string
	Description   *string
	Visibility    *string
	DefaultBranch *string
}

// Update changes the settings of a repository.
func (s *Service) Update(ctx context.Context, who auth.Principal, owner, name string, in UpdateInput, c identity.Client) (Repo, error) {
	var out Repo
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		r, row, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		if !authz.Allowed(r.Permission, authz.ActSettings) {
			return ErrForbidden
		}
		changed := []string{}
		if in.Description != nil {
			if err := checkDescription(*in.Description); err != nil {
				return err
			}
			row.Description, changed = *in.Description, append(changed, "description")
		}
		if in.DefaultBranch != nil {
			if err := git.ValidateBranchName(*in.DefaultBranch); err != nil {
				return invalid("invalid default branch")
			}
			row.DefaultBranch, changed = *in.DefaultBranch, append(changed, "default_branch")
		}
		if in.Visibility != nil {
			vis, err := parseVisibility(*in.Visibility)
			if err != nil {
				return err
			}
			if !authz.Allowed(r.Permission, authz.ActDelete) {
				return ErrForbidden
			}
			row.Visibility, changed = string(vis), append(changed, "visibility")
		}
		org := orgOf(r.OwnerKind, r.OwnerID)
		if in.Name != nil && *in.Name != r.Name {
			if !authz.Allowed(r.Permission, authz.ActDelete) {
				return ErrForbidden
			}
			if err := git.ValidateRepoName(*in.Name); err != nil {
				return invalid("invalid repository name")
			}
			if err := s.move(ctx, q, r, r.OwnerID, *in.Name); err != nil {
				return err
			}
			if err := audit.Record(ctx, q, event(who, audit.ActionRepoRenamed, r.ID, org, c,
				map[string]any{"from": r.Name, "to": *in.Name})); err != nil {
				return err
			}
		}
		if len(changed) > 0 {
			if err := q.UpdateRepo(ctx, dbgen.UpdateRepoParams{ID: r.ID, Description: row.Description,
				Visibility: row.Visibility, DefaultBranch: row.DefaultBranch}); err != nil {
				return fmt.Errorf("update repo: %w", err)
			}
			if err := audit.Record(ctx, q, event(who, audit.ActionRepoUpdated, r.ID, org, c,
				map[string]any{"fields": changed})); err != nil {
				return err
			}
		}
		out, err = s.byID(ctx, q, who, r.ID)
		return err
	})
	return out, err
}

// move gives r the path newOwner/newName: it waits for running git writes
// (LockRepoWrite), redirects the old path and drops any redirect that sat
// on the new one. The caller updates quota and grants on a transfer.
func (s *Service) move(ctx context.Context, q *dbgen.Queries, r Repo, newOwner uuid.UUID, newName string) error {
	if err := q.LockRepoWrite(ctx, r.ID); err != nil {
		return fmt.Errorf("lock repo: %w", err)
	}
	var err error
	if newOwner == r.OwnerID {
		err = q.RenameRepo(ctx, dbgen.RenameRepoParams{ID: r.ID, Name: newName})
	} else {
		err = q.TransferRepo(ctx, dbgen.TransferRepoParams{ID: r.ID, OwnerID: newOwner, Name: newName})
	}
	if isUniqueViolation(err) {
		return ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("move repo: %w", err)
	}
	if err := q.DeleteRepoRedirect(ctx, dbgen.DeleteRepoRedirectParams{OwnerID: newOwner, Name: newName}); err != nil {
		return fmt.Errorf("delete redirect: %w", err)
	}
	if newOwner != r.OwnerID || !strings.EqualFold(newName, r.Name) {
		if err := q.UpsertRepoRedirect(ctx, dbgen.UpsertRepoRedirectParams{OwnerID: r.OwnerID, RepoID: r.ID, Name: r.Name}); err != nil {
			return fmt.Errorf("add redirect: %w", err)
		}
	}
	return nil
}

// TransferInput moves a repository to another namespace.
type TransferInput struct {
	NewOwner string
	NewName  string // empty keeps the name
}

// Transfer moves a repository to another user or organization. The caller
// needs admin on the repository and the right to create in the target.
// Team grants are dropped (teams belong to the old organization);
// collaborators stay. Usage moves with the repository.
func (s *Service) Transfer(ctx context.Context, who auth.Principal, owner, name string, in TransferInput, c identity.Client) (Repo, error) {
	var out Repo
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		r, _, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		if !authz.Allowed(r.Permission, authz.ActDelete) {
			return ErrForbidden
		}
		ns, err := s.namespace(ctx, q, who, in.NewOwner)
		if errors.Is(err, ErrNotFound) {
			return invalid("unknown owner %q", in.NewOwner)
		}
		if err != nil {
			return err
		}
		if !ns.canCreate(who) {
			return ErrForbidden
		}
		newName := in.NewName
		if newName == "" {
			newName = r.Name
		}
		if err := git.ValidateRepoName(newName); err != nil {
			return invalid("invalid repository name")
		}
		if ns.id == r.OwnerID {
			return invalid("the repository already belongs to %s", ns.name)
		}
		if err := s.move(ctx, q, r, ns.id, newName); err != nil {
			return err
		}
		size, err := q.GetRepoStorage(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("get repo size: %w", err)
		}
		if err := quota.Reserve(ctx, q, ns.id, quota.Delta{Repos: 1, Bytes: size.SizeBytes}); err != nil {
			return err
		}
		if err := quota.Release(ctx, q, r.OwnerID, quota.Delta{Repos: 1, Bytes: size.SizeBytes}); err != nil {
			return err
		}
		if err := q.DeleteRepoTeamGrants(ctx, r.ID); err != nil {
			return fmt.Errorf("drop team grants: %w", err)
		}
		data := map[string]any{"from": r.FullName(), "to": ns.name + "/" + newName}
		if err := audit.Record(ctx, q, event(who, audit.ActionRepoTransferred, r.ID, orgOf(r.OwnerKind, r.OwnerID), c, data)); err != nil {
			return err
		}
		if ns.kind == "org" {
			if err := audit.Record(ctx, q, event(who, audit.ActionRepoTransferred, r.ID, ns.id, c, data)); err != nil {
				return err
			}
		}
		if err := keepAdmin(ctx, q, ns, r.ID, who); err != nil {
			return err
		}
		out, err = s.byID(ctx, q, who, r.ID)
		return err
	})
	return out, err
}

// keepAdmin makes a plain organization member who creates (or transfers
// in) a repository its admin collaborator, so that the caller does not lose
// access to it.
func keepAdmin(ctx context.Context, q *dbgen.Queries, ns namespace, repo uuid.UUID, who auth.Principal) error {
	if ns.kind != "org" || ns.role != authz.OrgMember {
		return nil
	}
	if err := q.UpsertRepoCollaborator(ctx, dbgen.UpsertRepoCollaboratorParams{RepoID: repo, UserID: who.UserID,
		Permission: authz.PermAdmin.String(), AddedBy: uuid.NullUUID{UUID: who.UserID, Valid: true}}); err != nil {
		return fmt.Errorf("add admin collaborator: %w", err)
	}
	return nil
}

// Delete deletes a repository (admin). The record goes at once; the hot
// copy is moved to the trash and the remote objects are purged by a job.
func (s *Service) Delete(ctx context.Context, who auth.Principal, owner, name string, c identity.Client) error {
	var id uuid.UUID
	var after []func(context.Context)
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		r, _, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		if !authz.Allowed(r.Permission, authz.ActDelete) {
			return ErrForbidden
		}
		if err := q.LockRepoWrite(ctx, r.ID); err != nil {
			return fmt.Errorf("lock repo: %w", err)
		}
		st, err := q.GetRepoStorage(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("get repo size: %w", err)
		}
		// Release assets and issue attachments are charged to the owner too;
		// their rows go with the repository (ON DELETE CASCADE), so they are
		// summed before the delete.
		assetBytes, err := q.SumRepoReleaseAssetBytes(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("sum release asset size: %w", err)
		}
		attachmentBytes, err := q.SumRepoIssueAttachmentBytes(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("sum issue attachment size: %w", err)
		}
		if after, err = s.runDeleteHooks(ctx, q, r.ID); err != nil {
			return err
		}
		if _, err := q.DeleteRepo(ctx, r.ID); err != nil {
			return fmt.Errorf("delete repo: %w", err)
		}
		st.SizeBytes += assetBytes + attachmentBytes
		if err := quota.Release(ctx, q, r.OwnerID, quota.Delta{Repos: 1, Bytes: st.SizeBytes}); err != nil {
			return err
		}
		id = r.ID
		return audit.Record(ctx, q, event(who, audit.ActionRepoDeleted, r.ID, orgOf(r.OwnerKind, r.OwnerID), c,
			map[string]any{"name": r.FullName()}))
	})
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if err := s.tier.Trash(ctx, id); err != nil {
		s.log.ErrorContext(ctx, "trash repository", "repo", id, "error", err)
	}
	if s.jobs != nil {
		if err := s.jobs.Enqueue(ctx, PurgeArgs{RepoID: id}); err != nil {
			s.log.ErrorContext(ctx, "enqueue repository purge", "repo", id, "error", err)
		}
	}
	for _, fn := range after {
		fn(ctx)
	}
	return nil
}

// Abandon removes a repository that another service created and could not
// finish (a fork whose content copy failed): the row, its quota and its
// storage go, without a permission check or an audit entry of its own. It
// is not an API operation.
func (s *Service) Abandon(ctx context.Context, id uuid.UUID) error {
	var after []func(context.Context)
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.LockRepoWrite(ctx, id); err != nil {
			return fmt.Errorf("lock repo: %w", err)
		}
		st, err := q.GetRepoStorage(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get repo size: %w", err)
		}
		if after, err = s.runDeleteHooks(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.DeleteRepo(ctx, id); err != nil {
			return fmt.Errorf("delete repo: %w", err)
		}
		return quota.Release(ctx, q, st.OwnerID, quota.Delta{Repos: 1, Bytes: st.SizeBytes})
	})
	if err != nil {
		return err
	}
	s.discard(ctx, id)
	ctx = context.WithoutCancel(ctx)
	for _, fn := range after {
		fn(ctx)
	}
	s.log.InfoContext(ctx, "repository abandoned", "repo", id)
	return nil
}

// ListCollaborators lists the direct grants (readers of the repository).
func (s *Service) ListCollaborators(ctx context.Context, who auth.Principal, owner, name string) ([]Collaborator, error) {
	r, err := s.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListRepoCollaborators(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("list collaborators: %w", err)
	}
	out := make([]Collaborator, 0, len(rows))
	for _, c := range rows {
		p, _ := authz.ParsePermission(c.Permission)
		out = append(out, Collaborator{UserID: c.UserID, Username: c.Username, DisplayName: c.DisplayName, Permission: p, Since: c.CreatedAt.Time})
	}
	return out, nil
}

// SetCollaborator grants permission to a user (admin).
func (s *Service) SetCollaborator(ctx context.Context, who auth.Principal, owner, name, username, permission string, c identity.Client) error {
	p, err := authz.ParseGrant(permission)
	if err != nil {
		return &ValidationError{Msg: err.Error()}
	}
	return s.tx(ctx, func(q *dbgen.Queries) error {
		r, _, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		if !authz.Allowed(r.Permission, authz.ActCollaborators) {
			return ErrForbidden
		}
		u, err := q.GetUserByUsername(ctx, username)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalid("unknown user %q", username)
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		if r.OwnerKind == "user" && u.ID == r.OwnerID {
			return invalid("the owner already has full access")
		}
		if err := q.UpsertRepoCollaborator(ctx, dbgen.UpsertRepoCollaboratorParams{RepoID: r.ID, UserID: u.ID,
			Permission: p.String(), AddedBy: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil}}); err != nil {
			return fmt.Errorf("set collaborator: %w", err)
		}
		return audit.Record(ctx, q, event(who, audit.ActionRepoCollaboratorSet, r.ID, orgOf(r.OwnerKind, r.OwnerID), c,
			map[string]any{"user": u.ID.String(), "permission": p.String()}))
	})
}

// RemoveCollaborator drops a direct grant (admin, or the collaborator).
func (s *Service) RemoveCollaborator(ctx context.Context, who auth.Principal, owner, name, username string, c identity.Client) error {
	return s.tx(ctx, func(q *dbgen.Queries) error {
		r, _, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		u, err := q.GetUserByUsername(ctx, username)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		if u.ID != who.UserID && !authz.Allowed(r.Permission, authz.ActCollaborators) {
			return ErrForbidden
		}
		n, err := q.DeleteRepoCollaborator(ctx, dbgen.DeleteRepoCollaboratorParams{RepoID: r.ID, UserID: u.ID})
		if err != nil {
			return fmt.Errorf("remove collaborator: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, event(who, audit.ActionRepoCollaboratorDel, r.ID, orgOf(r.OwnerKind, r.OwnerID), c,
			map[string]any{"user": u.ID.String(), "self": u.ID == who.UserID}))
	})
}

func (s *Service) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
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

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
