package repos_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

var client = identity.Client{IP: "198.51.100.7", UserAgent: "test"}

type fixture struct {
	*identitytest.Env
	svc  *repos.Service
	tier *gitstore.Tier
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, nil)
	dir := t.TempDir()
	ls, err := local.New("hot", filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Storage{
		Targets: []config.Target{{Name: "hot", Kind: "local", Path: filepath.Join(dir, "blobs")}},
		Policy:  map[string]config.Policy{"git": {Primary: "hot", Durability: "sync"}},
	}
	tr, err := tier.New(tier.Options{Config: cfg, Stores: map[string]blob.Store{"hot": ls}, Queries: env.Queries})
	if err != nil {
		t.Fatal(err)
	}
	st, err := gitstore.NewStore(tr)
	if err != nil {
		t.Fatal(err)
	}
	pc := git.NewPool(git.PoolOptions{})
	t.Cleanup(pc.Close)
	gt, err := gitstore.NewTier(gitstore.Options{Pool: env.Pool, HotPath: filepath.Join(dir, "blobs", ".hot"), Store: st, CatFile: pc})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{Env: env, tier: gt}
	f.svc = repos.New(repos.Options{Pool: env.Pool, Tier: gt, Jobs: env.Jobs})
	return f
}

func (f *fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) user(t *testing.T, name, role string) auth.Principal {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	f.exec(t, `INSERT INTO namespaces (id, kind, name) VALUES ($1, 'user', $2)`, id, name)
	f.exec(t, `INSERT INTO users (id, role) VALUES ($1, $2)`, id, role)
	return auth.Principal{UserID: id, Username: name, Role: role, Method: auth.MethodSession}
}

type member struct {
	p    auth.Principal
	role string
}

// org creates an organization with members.
func (f *fixture) org(t *testing.T, name string, members ...member) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	f.exec(t, `INSERT INTO namespaces (id, kind, name) VALUES ($1, 'org', $2)`, id, name)
	f.exec(t, `INSERT INTO orgs (id) VALUES ($1)`, id)
	for _, m := range members {
		f.exec(t, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`, id, m.p.UserID, m.role)
	}
	return id
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func invalid(t *testing.T, err error) {
	t.Helper()
	var v *repos.ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("got %v, want a validation error", err)
	}
}

func usage(t *testing.T, f *fixture, ns uuid.UUID) quota.Status {
	t.Helper()
	s, err := quota.Get(context.Background(), f.Queries, ns)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateGet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice", auth.RoleUser)
	bob := f.user(t, "bob", auth.RoleUser)

	r, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "Hello", Description: "hi"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if r.FullName() != "alice/Hello" || r.Visibility != authz.Private || r.DefaultBranch != "main" ||
		r.Permission != authz.PermAdmin || r.StorageState != gitstore.StateHot || r.OwnerKind != "user" {
		t.Fatalf("created %+v", r)
	}
	if fi, err := os.Stat(filepath.Join(f.tier.HotPath(r.ID), "HEAD")); err != nil || fi.IsDir() {
		t.Fatalf("bare repository missing: %v", err)
	}
	if _, err := f.tier.Store().LoadManifest(ctx, r.ID); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if u := usage(t, f, alice.UserID); u.Repos != 1 {
		t.Fatalf("usage %+v", u)
	}

	got, err := f.svc.Get(ctx, alice, "ALICE", "hello")
	if err != nil || got.ID != r.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	// A private repository does not exist for others.
	_, err = f.svc.Get(ctx, bob, "alice", "Hello")
	is(t, err, repos.ErrNotFound)
	_, err = f.svc.Get(ctx, auth.Principal{}, "alice", "Hello")
	is(t, err, repos.ErrNotFound)

	_, err = f.svc.Create(ctx, alice, repos.CreateInput{Name: "hello"}, client)
	is(t, err, repos.ErrNameTaken)
	for _, in := range []repos.CreateInput{
		{Name: "x.git"}, {Name: ".hidden"}, {Name: "a/b"}, {Name: ""},
		{Name: "ok", Visibility: "secret"}, {Name: "ok", DefaultBranch: "bad..name"},
	} {
		_, err := f.svc.Create(ctx, alice, in, client)
		invalid(t, err)
	}
	// Nobody creates in another user's namespace.
	_, err = f.svc.Create(ctx, bob, repos.CreateInput{Owner: "alice", Name: "evil"}, client)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Create(ctx, bob, repos.CreateInput{Owner: "nobody", Name: "x"}, client)
	is(t, err, repos.ErrNotFound)
	if u := usage(t, f, alice.UserID); u.Repos != 1 {
		t.Fatalf("failed creates changed the usage: %+v", u)
	}

	pub, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "pub", Visibility: "public"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Get(ctx, auth.Principal{}, "alice", "pub"); err != nil || got.Permission != authz.PermRead {
		t.Fatalf("anonymous get public: %+v %v", got, err)
	}
	_, err = f.svc.Authorize(ctx, bob, "alice", "pub", authz.ActPush)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Authorize(ctx, bob, "alice", "Hello", authz.ActPush)
	is(t, err, repos.ErrNotFound)
	if _, err := f.svc.GetByID(ctx, bob, pub.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRestrictedAndQuota(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rest := f.user(t, "rest", auth.RoleRestricted)
	_, err := f.svc.Create(ctx, rest, repos.CreateInput{Name: "x"}, client)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Create(ctx, auth.Principal{}, repos.CreateInput{Owner: "rest", Name: "x"}, client)
	is(t, err, repos.ErrForbidden)

	alice := f.user(t, "alice", auth.RoleUser)
	if err := f.Queries.SetQuotaOverride(ctx, dbgen.SetQuotaOverrideParams{NamespaceID: alice.UserID, MaxRepos: pgtype.Int4{Int32: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "one"}, client); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Create(ctx, alice, repos.CreateInput{Name: "two"}, client)
	if !quota.IsExceeded(err) {
		t.Fatalf("got %v, want quota exceeded", err)
	}
	// An instance admin creates anywhere.
	admin := f.user(t, "root", auth.RoleAdmin)
	if err := f.Queries.SetQuotaOverride(ctx, dbgen.SetQuotaOverrideParams{NamespaceID: alice.UserID}); err != nil {
		t.Fatal(err)
	}
	if r, err := f.svc.Create(ctx, admin, repos.CreateInput{Owner: "alice", Name: "gift"}, client); err != nil || r.OwnerID != alice.UserID {
		t.Fatalf("admin create: %+v %v", r, err)
	}
}

func TestOrgRepos(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.user(t, "owner", auth.RoleUser)
	mem := f.user(t, "member", auth.RoleUser)
	other := f.user(t, "other", auth.RoleUser)
	f.org(t, "acme", member{owner, "owner"}, member{mem, "member"})

	_, err := f.svc.Create(ctx, other, repos.CreateInput{Owner: "acme", Name: "x"}, client)
	is(t, err, repos.ErrForbidden)
	r, err := f.svc.Create(ctx, mem, repos.CreateInput{Owner: "acme", Name: "tool", Visibility: "internal"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if r.Permission != authz.PermAdmin || r.OwnerKind != "org" {
		t.Fatalf("member keeps admin: %+v", r)
	}
	cs, err := f.svc.ListCollaborators(ctx, owner, "acme", "tool")
	if err != nil || len(cs) != 1 || cs[0].UserID != mem.UserID || cs[0].Permission != authz.PermAdmin {
		t.Fatalf("collaborators %+v %v", cs, err)
	}
	if _, err := f.svc.Create(ctx, owner, repos.CreateInput{Owner: "acme", Name: "secret"}, client); err != nil {
		t.Fatal(err)
	}

	// Internal is visible to signed-in users, private only to managers and grantees.
	names := func(who auth.Principal) []string {
		t.Helper()
		rs, err := f.svc.ListOwner(ctx, who, "acme", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rs {
			out = append(out, r.Name)
		}
		return out
	}
	if got := names(owner); len(got) != 2 {
		t.Fatalf("owner sees %v", got)
	}
	if got := names(other); len(got) != 1 || got[0] != "tool" {
		t.Fatalf("other sees %v", got)
	}
	if got := names(auth.Principal{}); len(got) != 0 {
		t.Fatalf("anonymous sees %v", got)
	}
	mine, err := f.svc.ListMine(ctx, mem, 0, 0)
	// The org base permission (read by default) reaches private repositories.
	if err != nil || len(mine) != 2 {
		t.Fatalf("mine %+v %v", mine, err)
	}
	if mine, err := f.svc.ListMine(ctx, other, 0, 0); err != nil || len(mine) != 0 {
		t.Fatalf("other mine %+v %v", mine, err)
	}

	// Orgs with repositories cannot be deleted (checked in orgs); the FK
	// also refuses it.
	if _, err := f.Pool.Exec(ctx, `DELETE FROM namespaces WHERE name = 'acme'`); err == nil {
		t.Fatal("namespace with repositories deleted")
	}
}

func ptr[T any](v T) *T { return &v }

func TestRenameRedirect(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice", auth.RoleUser)
	bob := f.user(t, "bob", auth.RoleUser)
	r, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "old", Visibility: "public"}, client)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "hidden"}, client)
	if err != nil {
		t.Fatal(err)
	}

	up, err := f.svc.Update(ctx, alice, "alice", "old", repos.UpdateInput{Name: ptr("new"), Description: ptr("d")}, client)
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != "new" || up.ID != r.ID {
		t.Fatalf("renamed %+v", up)
	}
	_, err = f.svc.Get(ctx, bob, "alice", "old")
	var mv *repos.MovedError
	if !errors.As(err, &mv) || mv.Owner != "alice" || mv.Name != "new" {
		t.Fatalf("got %v, want moved", err)
	}
	// The redirect of a private repository is not revealed.
	if _, err := f.svc.Update(ctx, alice, "alice", "hidden", repos.UpdateInput{Name: ptr("hidden2")}, client); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Get(ctx, bob, "alice", "hidden")
	is(t, err, repos.ErrNotFound)
	_, err = f.svc.Get(ctx, alice, "alice", "hidden")
	if !errors.As(err, &mv) || mv.Name != "hidden2" {
		t.Fatalf("got %v, want moved", err)
	}
	// A new repository at the old path replaces the redirect.
	again, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "old"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.Get(ctx, alice, "alice", "old"); err != nil || got.ID != again.ID {
		t.Fatalf("old path: %+v %v", got, err)
	}
	_, err = f.svc.Update(ctx, alice, "alice", "new", repos.UpdateInput{Name: ptr("OLD")}, client)
	is(t, err, repos.ErrNameTaken)
	// Case-only rename keeps no redirect.
	if _, err := f.svc.Update(ctx, alice, "alice", "new", repos.UpdateInput{Name: ptr("New")}, client); err != nil {
		t.Fatal(err)
	}
	_ = priv

	// Settings need maintain, visibility and rename need admin.
	if err := f.svc.SetCollaborator(ctx, alice, "alice", "New", "bob", "maintain", client); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(ctx, bob, "alice", "New", repos.UpdateInput{Description: ptr("by bob"), DefaultBranch: ptr("dev")}, client); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Update(ctx, bob, "alice", "New", repos.UpdateInput{Visibility: ptr("private")}, client)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Update(ctx, bob, "alice", "New", repos.UpdateInput{Name: ptr("bobs")}, client)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Update(ctx, alice, "alice", "New", repos.UpdateInput{DefaultBranch: ptr("-x")}, client)
	invalid(t, err)
}

func TestTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice", auth.RoleUser)
	mem := f.user(t, "member", auth.RoleUser)
	f.org(t, "acme", member{alice, "member"})
	f.org(t, "other", member{mem, "owner"})

	r, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "proj"}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Transfer(ctx, alice, "alice", "proj", repos.TransferInput{NewOwner: "other"}, client)
	is(t, err, repos.ErrForbidden)
	_, err = f.svc.Transfer(ctx, alice, "alice", "proj", repos.TransferInput{NewOwner: "alice"}, client)
	invalid(t, err)
	_, err = f.svc.Transfer(ctx, alice, "alice", "proj", repos.TransferInput{NewOwner: "ghost"}, client)
	invalid(t, err)

	moved, err := f.svc.Transfer(ctx, alice, "alice", "proj", repos.TransferInput{NewOwner: "acme", NewName: "project"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if moved.FullName() != "acme/project" || moved.ID != r.ID || moved.Permission != authz.PermAdmin {
		t.Fatalf("moved %+v", moved)
	}
	_, err = f.svc.Get(ctx, alice, "alice", "proj")
	var mv *repos.MovedError
	if !errors.As(err, &mv) || mv.Owner != "acme" || mv.Name != "project" {
		t.Fatalf("got %v, want moved", err)
	}
	ns, err := f.Queries.GetNamespaceByName(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if u := usage(t, f, alice.UserID); u.Repos != 0 {
		t.Fatalf("source usage %+v", u)
	}
	if u := usage(t, f, ns.ID); u.Repos != 1 {
		t.Fatalf("target usage %+v", u)
	}
}

func TestDeleteAndCollaborators(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice", auth.RoleUser)
	bob := f.user(t, "bob", auth.RoleUser)
	r, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "proj"}, client)
	if err != nil {
		t.Fatal(err)
	}
	invalid(t, f.svc.SetCollaborator(ctx, alice, "alice", "proj", "bob", "owner", client))
	invalid(t, f.svc.SetCollaborator(ctx, alice, "alice", "proj", "ghost", "read", client))
	invalid(t, f.svc.SetCollaborator(ctx, alice, "alice", "proj", "alice", "read", client))
	if err := f.svc.SetCollaborator(ctx, alice, "alice", "proj", "bob", "write", client); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Get(ctx, bob, "alice", "proj")
	if err != nil || got.Permission != authz.PermWrite {
		t.Fatalf("bob: %+v %v", got, err)
	}
	is(t, f.svc.SetCollaborator(ctx, bob, "alice", "proj", "bob", "admin", client), repos.ErrForbidden)
	is(t, f.svc.Delete(ctx, bob, "alice", "proj", client), repos.ErrForbidden)
	// A collaborator can leave.
	if err := f.svc.RemoveCollaborator(ctx, bob, "alice", "proj", "bob", client); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Get(ctx, bob, "alice", "proj")
	is(t, err, repos.ErrNotFound)
	is(t, f.svc.RemoveCollaborator(ctx, alice, "alice", "proj", "bob", client), repos.ErrNotFound)

	f.Jobs.Take()
	if err := f.svc.Delete(ctx, alice, "alice", "proj", client); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Get(ctx, alice, "alice", "proj")
	is(t, err, repos.ErrNotFound)
	if _, err := os.Stat(f.tier.HotPath(r.ID)); !os.IsNotExist(err) {
		t.Fatalf("hot copy still there: %v", err)
	}
	jobs := f.Jobs.Take()
	if len(jobs) != 1 || jobs[0].(repos.PurgeArgs).RepoID != r.ID {
		t.Fatalf("jobs %+v", jobs)
	}
	if u := usage(t, f, alice.UserID); u.Repos != 0 {
		t.Fatalf("usage %+v", u)
	}
	// The purge worker removes the remote copy.
	if err := f.tier.Store().DeleteRepo(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.tier.Store().LoadManifest(ctx, r.ID)
	is(t, err, gitstore.ErrNoManifest)
	// The name is free again.
	if _, err := f.svc.Create(ctx, alice, repos.CreateInput{Name: "proj"}, client); err != nil {
		t.Fatal(err)
	}
}
