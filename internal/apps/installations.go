package apps

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

func pgInt8(n int64) pgtype.Int8 { return pgtype.Int8{Int64: n, Valid: true} }

// maxSelectedRepos bounds the repositories of a selection or a token.
const maxSelectedRepos = 500

// Installation is an App installed on an account.
type Installation struct {
	ID          uuid.UUID
	NumID       int64
	AppID       uuid.UUID
	AppNumID    int64
	AppSlug     string
	AppName     string
	AppOwnerID  uuid.UUID
	BotID       uuid.UUID
	AccountID   uuid.UUID
	AccountKind string
	AccountName string
	// RepositorySelection is SelectionAll or SelectionSelected.
	RepositorySelection string
	// Permissions and Events are the accepted ones.
	Permissions authz.AppPermissions
	Events      []string
	// AppPermissions and AppEvents are what the App asks for now.
	AppPermissions authz.AppPermissions
	AppEvents      []string
	SetupURL       string
	SuspendedAt    *time.Time
	SuspendedBy    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Pending reports whether the App asks for permissions or events the
// installation has not accepted yet.
func (i Installation) Pending() bool {
	if !i.Permissions.Covers(i.AppPermissions) {
		return true
	}
	for _, e := range i.AppEvents {
		if !slices.Contains(i.Events, e) {
			return true
		}
	}
	return false
}

// Effective is the permission set the installation acts with: the
// accepted permissions still held by the App.
func (i Installation) Effective() authz.AppPermissions {
	return i.Permissions.Intersect(i.AppPermissions)
}

func installationFrom(r dbgen.GetInstallationRow) Installation {
	return Installation{ID: r.ID, NumID: r.NumID.Int64, AppID: r.AppID, AppNumID: r.AppNumID.Int64, AppSlug: r.AppSlug,
		AppName: r.AppName, AppOwnerID: r.AppOwnerID, BotID: r.BotID, AccountID: r.AccountID, AccountKind: r.AccountKind,
		AccountName: r.AccountName, RepositorySelection: r.RepositorySelection, Permissions: decodePerms(r.Permissions),
		Events: nonNil(r.Events), AppPermissions: decodePerms(r.AppPermissions), AppEvents: nonNil(r.AppEvents),
		SetupURL: r.SetupUrl, SuspendedAt: tsPtr(r.SuspendedAt), SuspendedBy: r.SuspendedBy.UUID,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

// RepoRef is a repository of an installation.
type RepoRef struct {
	ID            uuid.UUID
	Owner         string
	Name          string
	Visibility    string
	Description   string
	DefaultBranch string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// FullName is owner/name.
func (r RepoRef) FullName() string { return r.Owner + "/" + r.Name }

func (s *Service) installation(ctx context.Context, q *dbgen.Queries, id uuid.UUID) (Installation, error) {
	r, err := q.GetInstallation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Installation{}, ErrNotFound
	}
	if err != nil {
		return Installation{}, fmt.Errorf("get installation: %w", err)
	}
	return installationFrom(r), nil
}

func (s *Service) installationByNum(ctx context.Context, num int64) (Installation, error) {
	r, err := s.q.GetInstallationByNum(ctx, pgInt8(num))
	if errors.Is(err, pgx.ErrNoRows) {
		return Installation{}, ErrNotFound
	}
	if err != nil {
		return Installation{}, fmt.Errorf("get installation: %w", err)
	}
	return installationFrom(dbgen.GetInstallationRow(r)), nil
}

// Repos returns the repositories an installation covers.
func (s *Service) Repos(ctx context.Context, inst Installation) ([]RepoRef, error) {
	rows, err := s.q.ListInstallationRepos(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list installation repos: %w", err)
	}
	out := make([]RepoRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, RepoRef{ID: r.ID, Owner: r.OwnerName, Name: r.Name, Visibility: r.Visibility,
			Description: r.Description, DefaultBranch: r.DefaultBranch, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	return out, nil
}

// InstallTarget is an account p may install an App on.
type InstallTarget struct {
	ID   uuid.UUID
	Kind string
	Name string
	// InstallationNumID is set when the App is installed there already.
	InstallationNumID int64
}

// InstallTargets returns the accounts p may install the App slug on: its
// own and the organizations it owns. A private App installs only on the
// account that owns it.
func (s *Service) InstallTargets(ctx context.Context, p auth.Principal, slug string) (App, []InstallTarget, error) {
	app, err := s.installable(ctx, p, slug)
	if err != nil {
		return App{}, nil, err
	}
	cands := []InstallTarget{{ID: p.UserID, Kind: OwnerUser, Name: p.Username}}
	orgs, err := s.q.ListUserOwnedOrgs(ctx, p.UserID)
	if err != nil {
		return App{}, nil, fmt.Errorf("list owned orgs: %w", err)
	}
	for _, o := range orgs {
		cands = append(cands, InstallTarget{ID: o.ID, Kind: OwnerOrg, Name: o.Name})
	}
	out := make([]InstallTarget, 0, len(cands))
	for _, c := range cands {
		if !app.Public && c.ID != app.OwnerID {
			continue
		}
		id, err := s.q.GetInstallationByAccount(ctx, dbgen.GetInstallationByAccountParams{AppID: app.ID, AccountID: c.ID})
		switch {
		case err == nil:
			inst, ierr := s.installation(ctx, s.q, id)
			if ierr != nil {
				return App{}, nil, ierr
			}
			c.InstallationNumID = inst.NumID
		case !errors.Is(err, pgx.ErrNoRows):
			return App{}, nil, fmt.Errorf("get installation: %w", err)
		}
		out = append(out, c)
	}
	return app, out, nil
}

// installable returns an App p can see for installing.
func (s *Service) installable(ctx context.Context, p auth.Principal, slug string) (App, error) {
	if p.UserID == uuid.Nil || p.Automated() {
		return App{}, ErrForbidden
	}
	app, err := s.appBySlug(ctx, slug)
	if err != nil {
		return App{}, err
	}
	if !app.Public {
		ok, err := s.managesAccount(ctx, p, app.OwnerID, app.OwnerKind)
		if err != nil {
			return App{}, err
		}
		if !ok {
			return App{}, ErrNotFound
		}
	}
	return app, nil
}

// selectRepos resolves repository names of account accountID.
func (s *Service) selectRepos(ctx context.Context, q *dbgen.Queries, accountID uuid.UUID, names []string) ([]uuid.UUID, error) {
	if len(names) == 0 {
		return nil, inputErr("select at least one repository")
	}
	if len(names) > maxSelectedRepos {
		return nil, inputErr("at most %d repositories", maxSelectedRepos)
	}
	lower := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if i := strings.LastIndex(n, "/"); i >= 0 {
			n = n[i+1:]
		}
		if n != "" && !slices.Contains(lower, n) {
			lower = append(lower, n)
		}
	}
	rows, err := q.ReposByNames(ctx, dbgen.ReposByNamesParams{OwnerID: accountID, Names: lower})
	if err != nil {
		return nil, fmt.Errorf("repos by names: %w", err)
	}
	if len(rows) != len(lower) {
		return nil, inputErr("some repositories do not exist on this account")
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// Install installs the App slug on account (p itself or an organization
// p owns) with every repository or the named ones, accepting the App's
// current permissions. It returns the installation and, when the App has
// a setup URL, where to send the browser.
func (s *Service) Install(ctx context.Context, p auth.Principal, slug, account, selection string, repoNames []string, ip string) (Installation, string, error) {
	app, err := s.installable(ctx, p, slug)
	if err != nil {
		return Installation{}, "", err
	}
	accID, kind, accName, err := s.account(ctx, account)
	if err != nil {
		return Installation{}, "", err
	}
	ok, err := s.managesAccount(ctx, p, accID, kind)
	if err != nil {
		return Installation{}, "", err
	}
	if !ok {
		return Installation{}, "", ErrForbidden
	}
	if !app.Public && accID != app.OwnerID {
		return Installation{}, "", ErrForbidden
	}
	if selection != SelectionAll && selection != SelectionSelected {
		return Installation{}, "", inputErr("repository selection must be all or selected")
	}
	id, err := newID()
	if err != nil {
		return Installation{}, "", err
	}
	var repoIDs []uuid.UUID
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if selection == SelectionSelected {
			if repoIDs, err = s.selectRepos(ctx, q, accID, repoNames); err != nil {
				return err
			}
		}
		_, err := q.InsertInstallation(ctx, dbgen.InsertInstallationParams{ID: id, AppID: app.ID, AccountID: accID,
			RepositorySelection: selection, Permissions: encodePerms(app.Permissions), Events: app.Events,
			CreatedBy: uuid.NullUUID{UUID: p.UserID, Valid: true}})
		if isUniqueViolation(err) {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("insert installation: %w", err)
		}
		for _, r := range repoIDs {
			if err := q.InsertInstallationRepo(ctx, dbgen.InsertInstallationRepoParams{InstallationID: id, RepoID: r}); err != nil {
				return fmt.Errorf("insert installation repo: %w", err)
			}
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionInstallationCreated, TargetType: "app_installation",
			TargetID: id.String(), IP: ip, OrgID: orgIfKind(accID, kind),
			Data: map[string]any{"app": app.Slug, "account": accName, "repository_selection": selection,
				"repositories": len(repoIDs), "permissions": app.Permissions}})
	})
	if err != nil {
		return Installation{}, "", err
	}
	inst, err := s.installation(ctx, s.q, id)
	if err != nil {
		return Installation{}, "", err
	}
	s.log.InfoContext(ctx, "app installed", "app", app.Slug, "account", accName, "installation", inst.NumID, "user_id", p.UserID)
	if selection == SelectionAll {
		if all, err := s.Repos(ctx, inst); err == nil {
			for _, r := range all {
				repoIDs = append(repoIDs, r.ID)
			}
		}
	}
	s.event(ctx, inst, "installation", "created", p.UserID, repoIDs, nil, nil)
	redirect := ""
	if app.SetupURL != "" {
		redirect = withQuery(app.SetupURL, url.Values{"installation_id": {strconv.FormatInt(inst.NumID, 10)},
			"setup_action": {"install"}})
	}
	return inst, redirect, nil
}

func withQuery(base string, v url.Values) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vals := range v {
		q[k] = vals
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func orgIfKind(id uuid.UUID, kind string) uuid.UUID {
	if kind == OwnerOrg {
		return id
	}
	return uuid.Nil
}

// event delivers an installation webhook event; failures are logged, the
// change is already committed.
func (s *Service) event(ctx context.Context, inst Installation, event, action string, sender uuid.UUID, repos, added, removed []uuid.UUID) {
	if s.hooks == nil {
		return
	}
	info := webhooks.InstallationInfo{ID: inst.ID, NumID: inst.NumID, AppID: inst.AppID, AppNumID: inst.AppNumID,
		AppSlug: inst.AppSlug, AccountID: inst.AccountID, RepositorySelection: inst.RepositorySelection,
		Permissions: inst.Effective(), Events: inst.Events, CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
		SuspendedAt: inst.SuspendedAt, SuspendedBy: inst.SuspendedBy}
	if err := s.hooks.InstallationEvent(ctx, info, event, action, sender, repos, added, removed); err != nil {
		s.log.WarnContext(ctx, "app installation event not delivered", "app", inst.AppSlug, "event", event,
			"action", action, "error", err)
	}
}

// ManagedInstallations returns the installations on account ("" for p's
// own account), which p must administer.
func (s *Service) ManagedInstallations(ctx context.Context, p auth.Principal, account string) ([]Installation, error) {
	id, kind := p.UserID, OwnerUser
	if account != "" {
		var err error
		if id, kind, _, err = s.account(ctx, account); err != nil {
			return nil, err
		}
	}
	ok, err := s.managesAccount(ctx, p, id, kind)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListAccountInstallations(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	out := make([]Installation, 0, len(rows))
	for _, r := range rows {
		out = append(out, installationFrom(dbgen.GetInstallationRow(r)))
	}
	return out, nil
}

// managedInstallation returns installation num when p administers its
// account, ErrNotFound otherwise.
func (s *Service) managedInstallation(ctx context.Context, p auth.Principal, num int64) (Installation, error) {
	inst, err := s.installationByNum(ctx, num)
	if err != nil {
		return Installation{}, err
	}
	ok, err := s.managesAccount(ctx, p, inst.AccountID, inst.AccountKind)
	if err != nil {
		return Installation{}, err
	}
	if !ok {
		return Installation{}, ErrNotFound
	}
	return inst, nil
}

// GetInstallation returns an installation p administers, with its
// repositories.
func (s *Service) GetInstallation(ctx context.Context, p auth.Principal, num int64) (Installation, []RepoRef, error) {
	inst, err := s.managedInstallation(ctx, p, num)
	if err != nil {
		return Installation{}, nil, err
	}
	repos, err := s.Repos(ctx, inst)
	if err != nil {
		return Installation{}, nil, err
	}
	return inst, repos, nil
}

// SetRepositories changes the repository selection of an installation p
// administers and sends installation_repositories.
func (s *Service) SetRepositories(ctx context.Context, p auth.Principal, num int64, selection string, repoNames []string, ip string) (Installation, error) {
	inst, err := s.managedInstallation(ctx, p, num)
	if err != nil {
		return Installation{}, err
	}
	if selection != SelectionAll && selection != SelectionSelected {
		return Installation{}, inputErr("repository selection must be all or selected")
	}
	before, err := s.Repos(ctx, inst)
	if err != nil {
		return Installation{}, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		var ids []uuid.UUID
		if selection == SelectionSelected {
			if ids, err = s.selectRepos(ctx, q, inst.AccountID, repoNames); err != nil {
				return err
			}
		}
		if err := q.UpdateInstallationSelection(ctx, dbgen.UpdateInstallationSelectionParams{ID: inst.ID,
			RepositorySelection: selection}); err != nil {
			return fmt.Errorf("update installation: %w", err)
		}
		if err := q.DeleteInstallationRepos(ctx, inst.ID); err != nil {
			return fmt.Errorf("clear installation repos: %w", err)
		}
		for _, r := range ids {
			if err := q.InsertInstallationRepo(ctx, dbgen.InsertInstallationRepoParams{InstallationID: inst.ID, RepoID: r}); err != nil {
				return fmt.Errorf("insert installation repo: %w", err)
			}
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionInstallationRepos, TargetType: "app_installation",
			TargetID: inst.ID.String(), IP: ip, OrgID: orgIfKind(inst.AccountID, inst.AccountKind),
			Data: map[string]any{"app": inst.AppSlug, "repository_selection": selection, "repositories": len(ids)}})
	})
	if err != nil {
		return Installation{}, err
	}
	inst, err = s.installation(ctx, s.q, inst.ID)
	if err != nil {
		return Installation{}, err
	}
	after, err := s.Repos(ctx, inst)
	if err != nil {
		return Installation{}, err
	}
	added, removed := diffRepos(before, after)
	if len(added) > 0 || len(removed) > 0 {
		s.event(ctx, inst, "installation_repositories", repoAction(added), p.UserID, nil, added, removed)
	}
	return inst, nil
}

func repoAction(added []uuid.UUID) string {
	if len(added) > 0 {
		return "added"
	}
	return "removed"
}

func diffRepos(before, after []RepoRef) (added, removed []uuid.UUID) {
	in := func(list []RepoRef, id uuid.UUID) bool {
		return slices.ContainsFunc(list, func(r RepoRef) bool { return r.ID == id })
	}
	for _, r := range after {
		if !in(before, r.ID) {
			added = append(added, r.ID)
		}
	}
	for _, r := range before {
		if !in(after, r.ID) {
			removed = append(removed, r.ID)
		}
	}
	return added, removed
}

// SetSuspended suspends or resumes an installation p administers. A
// suspended installation gets no token and no event, and its tokens stop
// working.
func (s *Service) SetSuspended(ctx context.Context, p auth.Principal, num int64, suspend bool, ip string) (Installation, error) {
	inst, err := s.managedInstallation(ctx, p, num)
	if err != nil {
		return Installation{}, err
	}
	if (inst.SuspendedAt != nil) == suspend {
		return inst, nil
	}
	return s.setSuspended(ctx, inst, p.UserID, suspend, ip)
}

func (s *Service) setSuspended(ctx context.Context, inst Installation, by uuid.UUID, suspend bool, ip string) (Installation, error) {
	action, act := ActionInstallationResumed, "unsuspend"
	params := dbgen.SetInstallationSuspendedParams{ID: inst.ID}
	if suspend {
		action, act = ActionInstallationSuspended, "suspend"
		params.SuspendedAt = pgTime(s.now())
		params.SuspendedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	}
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.SetInstallationSuspended(ctx, params); err != nil {
			return fmt.Errorf("suspend installation: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: by, Action: action, TargetType: "app_installation",
			TargetID: inst.ID.String(), IP: ip, OrgID: orgIfKind(inst.AccountID, inst.AccountKind),
			Data: map[string]any{"app": inst.AppSlug, "account": inst.AccountName}})
	})
	if err != nil {
		return Installation{}, err
	}
	out, err := s.installation(ctx, s.q, inst.ID)
	if err != nil {
		return Installation{}, err
	}
	// The App hears about the suspension (GitHub sends it although the
	// installation is suspended).
	s.event(ctx, out, "installation", act, by, nil, nil, nil)
	return out, nil
}

// AcceptPermissions accepts the permissions and events the App asks for
// now on an installation p administers.
func (s *Service) AcceptPermissions(ctx context.Context, p auth.Principal, num int64, ip string) (Installation, error) {
	inst, err := s.managedInstallation(ctx, p, num)
	if err != nil {
		return Installation{}, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.SetInstallationGrants(ctx, dbgen.SetInstallationGrantsParams{ID: inst.ID,
			Permissions: encodePerms(inst.AppPermissions), Events: inst.AppEvents}); err != nil {
			return fmt.Errorf("accept permissions: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: p.UserID, Action: ActionInstallationAccepted, TargetType: "app_installation",
			TargetID: inst.ID.String(), IP: ip, OrgID: orgIfKind(inst.AccountID, inst.AccountKind),
			Data: map[string]any{"app": inst.AppSlug, "permissions": inst.AppPermissions, "events": inst.AppEvents}})
	})
	if err != nil {
		return Installation{}, err
	}
	out, err := s.installation(ctx, s.q, inst.ID)
	if err != nil {
		return Installation{}, err
	}
	s.event(ctx, out, "installation", "new_permissions_accepted", p.UserID, nil, nil, nil)
	return out, nil
}

// Uninstall removes an installation p administers.
func (s *Service) Uninstall(ctx context.Context, p auth.Principal, num int64, ip string) error {
	inst, err := s.managedInstallation(ctx, p, num)
	if err != nil {
		return err
	}
	return s.uninstall(ctx, inst, p.UserID, ip)
}

func (s *Service) uninstall(ctx context.Context, inst Installation, by uuid.UUID, ip string) error {
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeleteInstallation(ctx, inst.ID)
		if err != nil {
			return fmt.Errorf("delete installation: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit.Record(ctx, q, audit.Event{Actor: by, Action: ActionInstallationDeleted, TargetType: "app_installation",
			TargetID: inst.ID.String(), IP: ip, OrgID: orgIfKind(inst.AccountID, inst.AccountKind),
			Data: map[string]any{"app": inst.AppSlug, "account": inst.AccountName}})
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "app uninstalled", "app", inst.AppSlug, "account", inst.AccountName, "installation", inst.NumID)
	s.event(ctx, inst, "installation", "deleted", by, nil, nil, nil)
	return nil
}

// ListForApp returns a page of the installations of an App p manages.
func (s *Service) ListForApp(ctx context.Context, p auth.Principal, slug string) ([]Installation, error) {
	app, err := s.managed(ctx, p, slug)
	if err != nil {
		return nil, err
	}
	return s.AppInstallations(ctx, app.ID, 0, 100)
}
