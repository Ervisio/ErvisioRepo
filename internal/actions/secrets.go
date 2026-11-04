package actions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

const (
	maxSecretSize   = 48 << 10
	maxVariableSize = 64 << 10
)

var secretNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,254}$`)

// SecretHook is the Phase 9 hook for external secret stores: it may add
// or replace the secrets of a job after the stored ones are resolved.
type SecretHook interface {
	JobSecrets(ctx context.Context, repo repos.Repo, environment string, secrets map[string]string) error
}

// Scope is where secrets, variables and runners live. It is built by
// InstanceScope, OwnerScope or RepoScope, which check the caller may
// manage it.
type Scope struct {
	kind    string
	ownerID uuid.UUID
	repoID  uuid.UUID
	env     string
	// label is the Runner.scope form: "instance", "org:<name>",
	// "repo:<owner>/<name>".
	label string
}

// Kind is "instance", "org", "repo" or "environment".
func (s Scope) Kind() string { return s.kind }

// String is the scope as shown to runners and users.
func (s Scope) String() string { return s.label }

func (s Scope) scopeID() uuid.NullUUID {
	id := s.repoID
	if id == uuid.Nil {
		id = s.ownerID
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

// InstanceScope is the whole instance (site admins only).
func (s *Service) InstanceScope(who auth.Principal) (Scope, error) {
	if !who.IsAdmin() || who.Grant != nil {
		return Scope{}, ErrForbidden
	}
	return Scope{kind: "instance", label: "instance"}, nil
}

// OwnerScope is an organization (its owners and admins) or a user (that
// user) and every repository it owns.
func (s *Service) OwnerScope(ctx context.Context, who auth.Principal, owner string) (Scope, error) {
	if who.Grant != nil || who.UserID == uuid.Nil {
		return Scope{}, ErrForbidden
	}
	ns, err := s.q.GetNamespaceByName(ctx, owner)
	if isNoRows(err) {
		return Scope{}, ErrNotFound
	}
	if err != nil {
		return Scope{}, fmt.Errorf("get owner: %w", err)
	}
	sc := Scope{kind: "org", ownerID: ns.ID, label: "org:" + ns.Name}
	if who.IsAdmin() {
		return sc, nil
	}
	if ns.Kind == "user" {
		if ns.ID != who.UserID {
			return Scope{}, ErrForbidden
		}
		return sc, nil
	}
	role, err := s.q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: ns.ID, UserID: who.UserID})
	if isNoRows(err) {
		return Scope{}, ErrForbidden
	}
	if err != nil {
		return Scope{}, fmt.Errorf("org role: %w", err)
	}
	r, _ := authz.ParseOrgRole(role)
	if !r.Manager() {
		return Scope{}, ErrForbidden
	}
	return sc, nil
}

// RepoScope is one repository (maintainers and admins), or one of its
// environments when environment is not empty.
func (s *Service) RepoScope(ctx context.Context, who auth.Principal, owner, name, environment string) (Scope, error) {
	repo, err := s.manageRepo(ctx, who, owner, name)
	if err != nil {
		return Scope{}, err
	}
	sc := Scope{kind: "repo", repoID: repo.ID, label: "repo:" + repo.FullName()}
	if environment != "" {
		if len(environment) > 255 {
			return Scope{}, invalid("environment name is too long")
		}
		sc.kind, sc.env = "environment", environment
	}
	return sc, nil
}

// manageRepo resolves a repository the caller may configure.
func (s *Service) manageRepo(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error) {
	d, err := s.d()
	if err != nil {
		return repos.Repo{}, err
	}
	if who.Grant != nil {
		return repos.Repo{}, ErrForbidden
	}
	repo, err := d.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if !authz.Allowed(repo.Permission, authz.ActSettings) {
		return repos.Repo{}, ErrForbidden
	}
	return repo, nil
}

func (s Scope) params() (owner, repo uuid.NullUUID) {
	switch s.kind {
	case "org":
		owner = nullID(s.ownerID)
	case "repo", "environment":
		repo = nullID(s.repoID)
	}
	return owner, repo
}

// Secret is a stored secret; its value is never returned.
type Secret struct {
	Name        string
	Scope       string
	Environment string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func normalizeName(name string) (string, error) {
	n := strings.ToUpper(strings.TrimSpace(name))
	if !secretNameRe.MatchString(n) {
		return "", invalid("name %q must contain only letters, digits and underscores and not start with a digit", name)
	}
	if strings.HasPrefix(n, "GITHUB_") || strings.HasPrefix(n, "ERVISIO_") || n == "ACTIONS_RUNTIME_TOKEN" {
		return "", invalid("name %q uses a reserved prefix", name)
	}
	return n, nil
}

// ListSecrets lists the secrets of a scope.
func (s *Service) ListSecrets(ctx context.Context, sc Scope) ([]Secret, error) {
	rows, err := s.q.ListActionsSecretNames(ctx, dbgen.ListActionsSecretNamesParams{Scope: sc.kind, ScopeID: sc.scopeID(), Environment: sc.env})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	out := make([]Secret, 0, len(rows))
	for _, r := range rows {
		out = append(out, Secret{Name: r.Name, Scope: sc.label, Environment: r.Environment, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	return out, nil
}

// SetSecret creates or replaces a secret.
func (s *Service) SetSecret(ctx context.Context, who auth.Principal, sc Scope, name, value string) (Secret, error) {
	if !s.seal.ok() {
		return Secret{}, ErrNoMasterKey
	}
	n, err := normalizeName(name)
	if err != nil {
		return Secret{}, err
	}
	if len(value) > maxSecretSize {
		return Secret{}, invalid("a secret is at most %d KiB", maxSecretSize>>10)
	}
	sealed, err := s.seal.seal([]byte(value), secretAAD(sc.kind, sc.scopeID().UUID, sc.env, n))
	if err != nil {
		return Secret{}, err
	}
	owner, repo := sc.params()
	id, _ := uuid.NewV7()
	r, err := s.q.UpsertActionsSecret(ctx, dbgen.UpsertActionsSecretParams{ID: id, Scope: sc.kind, OwnerID: owner, RepoID: repo,
		Environment: sc.env, Name: n, Sealed: sealed, CreatedBy: nullID(who.UserID)})
	if err != nil {
		return Secret{}, fmt.Errorf("store secret: %w", err)
	}
	s.log.Info("actions secret stored", "scope", sc.label, "environment", sc.env, "name", n)
	return Secret{Name: r.Name, Scope: sc.label, Environment: r.Environment, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}, nil
}

// DeleteSecret removes a secret.
func (s *Service) DeleteSecret(ctx context.Context, sc Scope, name string) error {
	n, err := s.q.DeleteActionsSecret(ctx, dbgen.DeleteActionsSecretParams{Scope: sc.kind, ScopeID: sc.scopeID(),
		Environment: sc.env, Name: strings.ToUpper(strings.TrimSpace(name))})
	if err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Variable is a configuration variable (vars context).
type Variable struct {
	Name        string
	Value       string
	Scope       string
	Environment string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func variableFrom(sc Scope, v dbgen.ActionsVariable) Variable {
	return Variable{Name: v.Name, Value: v.Value, Scope: sc.label, Environment: v.Environment, CreatedAt: v.CreatedAt.Time, UpdatedAt: v.UpdatedAt.Time}
}

// ListVariables lists the variables of a scope.
func (s *Service) ListVariables(ctx context.Context, sc Scope) ([]Variable, error) {
	rows, err := s.q.ListActionsVariables(ctx, dbgen.ListActionsVariablesParams{Scope: sc.kind, ScopeID: sc.scopeID(), Environment: sc.env})
	if err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	out := make([]Variable, 0, len(rows))
	for _, r := range rows {
		out = append(out, variableFrom(sc, r))
	}
	return out, nil
}

// SetVariable creates or replaces a variable.
func (s *Service) SetVariable(ctx context.Context, who auth.Principal, sc Scope, name, value string) (Variable, error) {
	n, err := normalizeName(name)
	if err != nil {
		return Variable{}, err
	}
	if len(value) > maxVariableSize {
		return Variable{}, invalid("a variable is at most %d KiB", maxVariableSize>>10)
	}
	owner, repo := sc.params()
	id, _ := uuid.NewV7()
	r, err := s.q.UpsertActionsVariable(ctx, dbgen.UpsertActionsVariableParams{ID: id, Scope: sc.kind, OwnerID: owner, RepoID: repo,
		Environment: sc.env, Name: n, Value: value, CreatedBy: nullID(who.UserID)})
	if err != nil {
		return Variable{}, fmt.Errorf("store variable: %w", err)
	}
	return variableFrom(sc, r), nil
}

// DeleteVariable removes a variable.
func (s *Service) DeleteVariable(ctx context.Context, sc Scope, name string) error {
	n, err := s.q.DeleteActionsVariable(ctx, dbgen.DeleteActionsVariableParams{Scope: sc.kind, ScopeID: sc.scopeID(),
		Environment: sc.env, Name: strings.ToUpper(strings.TrimSpace(name))})
	if err != nil {
		return fmt.Errorf("delete variable: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

var scopeRank = map[string]int{"instance": 0, "org": 1, "repo": 2, "environment": 3}

// variables resolves the vars context of a job: environment > repository
// > owner > instance.
func (s *Service) variables(ctx context.Context, repo repos.Repo, env string) (map[string]string, error) {
	rows, err := s.q.ListActionsVariablesForJob(ctx, dbgen.ListActionsVariablesForJobParams{OwnerID: nullID(repo.OwnerID),
		RepoID: nullID(repo.ID), Environment: env})
	if err != nil {
		return nil, fmt.Errorf("variables: %w", err)
	}
	slices.SortStableFunc(rows, func(a, b dbgen.ActionsVariable) int { return scopeRank[a.Scope] - scopeRank[b.Scope] })
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.Value
	}
	return out, nil
}

// secrets resolves the stored secrets of a job with the same precedence
// as variables. Fork pull request runs get none.
func (s *Service) secrets(ctx context.Context, run dbgen.ActionsRun, repo repos.Repo, env string) (map[string]string, error) {
	out := map[string]string{}
	if run.Fork {
		return out, nil
	}
	if s.seal.ok() {
		rows, err := s.q.ListActionsSecretsForJob(ctx, dbgen.ListActionsSecretsForJobParams{OwnerID: nullID(repo.OwnerID),
			RepoID: nullID(repo.ID), Environment: env})
		if err != nil {
			return nil, fmt.Errorf("secrets: %w", err)
		}
		slices.SortStableFunc(rows, func(a, b dbgen.ActionsSecret) int { return scopeRank[a.Scope] - scopeRank[b.Scope] })
		for _, r := range rows {
			v, err := s.seal.open(r.Sealed, secretAAD(r.Scope, r.ScopeID.UUID, r.Environment, r.Name))
			if err != nil {
				s.log.Warn("actions secret cannot be decrypted", "scope", r.Scope, "name", r.Name, "err", err)
				continue
			}
			out[r.Name] = string(v)
		}
	}
	if d := s.deps.Load(); d != nil && d.Secrets != nil {
		if err := d.Secrets.JobSecrets(ctx, repo, env, out); err != nil {
			return nil, fmt.Errorf("secret hook: %w", err)
		}
	}
	return out, nil
}

// Environment is a deployment environment of a repository.
type Environment struct {
	Name            string
	RequireApproval bool
	Reviewers       []uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func environmentFrom(e dbgen.ActionsEnvironment) Environment {
	return Environment{Name: e.Name, RequireApproval: e.RequireApproval, Reviewers: e.Reviewers,
		CreatedAt: e.CreatedAt.Time, UpdatedAt: e.UpdatedAt.Time}
}

// ListEnvironments lists the environments of a repository (readers).
func (s *Service) ListEnvironments(ctx context.Context, who auth.Principal, owner, name string) ([]Environment, error) {
	d, err := s.d()
	if err != nil {
		return nil, err
	}
	repo, err := d.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListActionsEnvironments(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	out := make([]Environment, 0, len(rows))
	for _, r := range rows {
		out = append(out, environmentFrom(r))
	}
	return out, nil
}

// EnvironmentInput configures an environment.
type EnvironmentInput struct {
	RequireApproval bool
	// Reviewers are usernames; empty means the repository maintainers.
	Reviewers []string
}

// PutEnvironment creates or updates an environment.
func (s *Service) PutEnvironment(ctx context.Context, who auth.Principal, owner, name, env string, in EnvironmentInput) (Environment, error) {
	repo, err := s.manageRepo(ctx, who, owner, name)
	if err != nil {
		return Environment{}, err
	}
	env = strings.TrimSpace(env)
	if env == "" || len(env) > 255 {
		return Environment{}, invalid("environment name must be 1 to 255 characters")
	}
	reviewers := make([]uuid.UUID, 0, len(in.Reviewers))
	for _, u := range in.Reviewers {
		row, err := s.q.GetUserByUsername(ctx, u)
		if isNoRows(err) {
			return Environment{}, invalid("user %q does not exist", u)
		}
		if err != nil {
			return Environment{}, fmt.Errorf("reviewer: %w", err)
		}
		if !slices.Contains(reviewers, row.ID) {
			reviewers = append(reviewers, row.ID)
		}
	}
	id, _ := uuid.NewV7()
	e, err := s.q.UpsertActionsEnvironment(ctx, dbgen.UpsertActionsEnvironmentParams{ID: id, RepoID: repo.ID, Name: env,
		RequireApproval: in.RequireApproval, Reviewers: reviewers})
	if err != nil {
		return Environment{}, fmt.Errorf("store environment: %w", err)
	}
	return environmentFrom(e), nil
}

// DeleteEnvironment removes an environment (its secrets and variables
// stay until removed).
func (s *Service) DeleteEnvironment(ctx context.Context, who auth.Principal, owner, name, env string) error {
	repo, err := s.manageRepo(ctx, who, owner, name)
	if err != nil {
		return err
	}
	n, err := s.q.DeleteActionsEnvironment(ctx, dbgen.DeleteActionsEnvironmentParams{RepoID: repo.ID, Name: env})
	if err != nil {
		return fmt.Errorf("delete environment: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// canApprove reports whether who may approve a deployment to env.
func canApprove(who auth.Principal, repo repos.Repo, env dbgen.ActionsEnvironment) bool {
	if who.Grant != nil || who.UserID == uuid.Nil {
		return false
	}
	if who.IsAdmin() || authz.Allowed(repo.Permission, authz.ActCollaborators) {
		return true
	}
	if len(env.Reviewers) == 0 {
		return authz.Allowed(repo.Permission, authz.ActSettings)
	}
	return slices.Contains(env.Reviewers, who.UserID)
}

var _ = errors.New
