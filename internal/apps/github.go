package apps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// The operations of the GitHub-compatible App API (/api/v3/app…,
// /installation…): the caller is an App authenticated with its JWT, an
// installation token or a user token, checked by ghapi.

// AppByID returns an App by id.
func (s *Service) AppByID(ctx context.Context, id uuid.UUID) (App, error) {
	app, err := s.appByID(ctx, s.q, id)
	if err != nil {
		return App{}, err
	}
	n, err := s.q.CountAppInstallations(ctx, app.ID)
	if err != nil {
		return App{}, fmt.Errorf("count installations: %w", err)
	}
	app.Installations = int(n)
	return app, nil
}

// PublicApp returns the App slug as GET /apps/{slug} shows it: public
// Apps to everyone, private ones to the App itself and its managers.
func (s *Service) PublicApp(ctx context.Context, who auth.Principal, slug string) (App, error) {
	app, err := s.appBySlug(ctx, slug)
	if err != nil {
		return App{}, err
	}
	if !app.Public {
		ok := who.App != nil && who.App.ID == app.ID
		if !ok {
			if ok, err = s.manages(ctx, who, app); err != nil {
				return App{}, err
			}
		}
		if !ok {
			return App{}, ErrNotFound
		}
	}
	return app, nil
}

// AppInstallations returns a page of the installations of an App.
func (s *Service) AppInstallations(ctx context.Context, appID uuid.UUID, offset, limit int) ([]Installation, error) {
	rows, err := s.q.ListAppInstallations(ctx, dbgen.ListAppInstallationsParams{AppID: appID,
		Limit: int32(min(max(limit, 1), 100)), Offset: int32(min(max(offset, 0), 1<<30))}) //nolint:gosec // bounded
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	out := make([]Installation, 0, len(rows))
	for _, r := range rows {
		out = append(out, installationFrom(dbgen.GetInstallationRow(r)))
	}
	return out, nil
}

// InstallationOfApp returns installation num of the App appID.
func (s *Service) InstallationOfApp(ctx context.Context, appID uuid.UUID, num int64) (Installation, error) {
	inst, err := s.installationByNum(ctx, num)
	if err != nil {
		return Installation{}, err
	}
	if inst.AppID != appID {
		return Installation{}, ErrNotFound
	}
	return inst, nil
}

// UninstallAsApp lets an App remove one of its installations.
func (s *Service) UninstallAsApp(ctx context.Context, appID uuid.UUID, num int64, ip string) error {
	inst, err := s.InstallationOfApp(ctx, appID, num)
	if err != nil {
		return err
	}
	return s.uninstall(ctx, inst, inst.BotID, ip)
}

// SuspendAsApp lets an App suspend or resume one of its installations.
func (s *Service) SuspendAsApp(ctx context.Context, appID uuid.UUID, num int64, suspend bool, ip string) error {
	inst, err := s.InstallationOfApp(ctx, appID, num)
	if err != nil {
		return err
	}
	if (inst.SuspendedAt != nil) == suspend {
		return nil
	}
	_, err = s.setSuspended(ctx, inst, inst.BotID, suspend, ip)
	return err
}

// InstallationForRepo returns the installation of the App appID that
// covers repository repoID.
func (s *Service) InstallationForRepo(ctx context.Context, appID, repoID uuid.UUID) (Installation, error) {
	id, err := s.q.InstallationForRepo(ctx, dbgen.InstallationForRepoParams{RepoID: repoID, AppID: appID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Installation{}, ErrNotFound
	}
	if err != nil {
		return Installation{}, fmt.Errorf("installation for repo: %w", err)
	}
	return s.installation(ctx, s.q, id)
}

// InstallationForAccount returns the installation of the App appID on the
// user or organization account.
func (s *Service) InstallationForAccount(ctx context.Context, appID uuid.UUID, account, kind string) (Installation, error) {
	accID, k, _, err := s.account(ctx, account)
	if err != nil {
		return Installation{}, err
	}
	if kind != "" && k != kind {
		return Installation{}, ErrNotFound
	}
	id, err := s.q.GetInstallationByAccount(ctx, dbgen.GetInstallationByAccountParams{AppID: appID, AccountID: accID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Installation{}, ErrNotFound
	}
	if err != nil {
		return Installation{}, fmt.Errorf("get installation: %w", err)
	}
	return s.installation(ctx, s.q, id)
}

// TokenRequest narrows a new installation access token. Nil fields keep
// the installation's repositories and permissions.
type TokenRequest struct {
	RepoNames   []string
	RepoIDs     []uuid.UUID
	Permissions authz.AppPermissions
}

// AccessToken is a new installation access token.
type AccessToken struct {
	Token               string
	ExpiresAt           time.Time
	Permissions         authz.AppPermissions
	RepositorySelection string
	// Repos is set when the token is limited to some repositories.
	Repos []RepoRef
}

// CreateAccessToken issues an installation access token of one hour for
// installation num of the App appID, possibly narrowed to some of its
// repositories and permissions (never widened).
func (s *Service) CreateAccessToken(ctx context.Context, appID uuid.UUID, num int64, req TokenRequest, ip string) (AccessToken, error) {
	inst, err := s.InstallationOfApp(ctx, appID, num)
	if err != nil {
		return AccessToken{}, err
	}
	if inst.SuspendedAt != nil {
		return AccessToken{}, ErrSuspended
	}
	perms := inst.Effective()
	if req.Permissions != nil {
		want, err := req.Permissions.Normalize()
		if err != nil {
			return AccessToken{}, inputErr("%s", err.Error())
		}
		if !perms.Covers(want) {
			return AccessToken{}, inputErr("The permissions requested are not granted to this installation.")
		}
		perms = want
	}
	out := AccessToken{Permissions: perms, RepositorySelection: inst.RepositorySelection}
	var repoIDs []uuid.UUID
	if req.RepoNames != nil || req.RepoIDs != nil {
		covered, err := s.Repos(ctx, inst)
		if err != nil {
			return AccessToken{}, err
		}
		if len(req.RepoNames)+len(req.RepoIDs) > maxSelectedRepos {
			return AccessToken{}, inputErr("at most %d repositories", maxSelectedRepos)
		}
		for _, n := range req.RepoNames {
			n = strings.TrimSpace(n)
			if i := strings.LastIndex(n, "/"); i >= 0 {
				n = n[i+1:]
			}
			i := slices.IndexFunc(covered, func(r RepoRef) bool { return strings.EqualFold(r.Name, n) })
			if i < 0 {
				return AccessToken{}, inputErr("There is at least one repository that does not exist or is not accessible to the parent installation.")
			}
			if !slices.Contains(repoIDs, covered[i].ID) {
				repoIDs = append(repoIDs, covered[i].ID)
				out.Repos = append(out.Repos, covered[i])
			}
		}
		for _, id := range req.RepoIDs {
			i := slices.IndexFunc(covered, func(r RepoRef) bool { return r.ID == id })
			if i < 0 {
				return AccessToken{}, inputErr("There is at least one repository that does not exist or is not accessible to the parent installation.")
			}
			if !slices.Contains(repoIDs, id) {
				repoIDs = append(repoIDs, id)
				out.Repos = append(out.Repos, covered[i])
			}
		}
		if len(repoIDs) == 0 {
			return AccessToken{}, inputErr("No repositories were given.")
		}
		out.RepositorySelection = SelectionSelected
	}
	id, err := newID()
	if err != nil {
		return AccessToken{}, err
	}
	token, hash := auth.NewInstallationToken()
	out.Token, out.ExpiresAt = token, s.now().Add(TokenTTL).UTC().Truncate(time.Second)
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		if _, err := q.DeleteExpiredInstallationTokens(ctx, pgTime(s.now())); err != nil {
			return fmt.Errorf("delete expired tokens: %w", err)
		}
		if err := q.InsertInstallationToken(ctx, dbgen.InsertInstallationTokenParams{ID: id, InstallationID: inst.ID,
			TokenHash: hash, Permissions: encodePerms(perms), RepoIds: repoIDs, ExpiresAt: pgTime(out.ExpiresAt)}); err != nil {
			return fmt.Errorf("insert installation token: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: inst.BotID, Action: ActionInstallationToken, TargetType: "app_installation",
			TargetID: inst.ID.String(), IP: ip, OrgID: orgIfKind(inst.AccountID, inst.AccountKind),
			Data: map[string]any{"app": inst.AppSlug, "token_id": id.String(), "permissions": perms,
				"repositories": len(repoIDs)}})
	})
	if err != nil {
		return AccessToken{}, err
	}
	s.log.InfoContext(ctx, "installation token issued", "app", inst.AppSlug, "installation", inst.NumID, "token_id", id)
	return out, nil
}

// LookupInstallationToken implements auth.AppTokenLookup: the caller of
// an installation access token, acting as the App's bot.
func (s *Service) LookupInstallationToken(ctx context.Context, token string) (auth.Principal, error) {
	row, err := s.q.GetInstallationToken(ctx, auth.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, auth.ErrNoToken
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("get installation token: %w", err)
	}
	if row.SuspendedAt.Valid || !row.ExpiresAt.Time.After(s.now()) {
		return auth.Principal{}, auth.ErrNoToken
	}
	// Permissions the installation or the App dropped since the token was
	// issued stop working at once.
	perms := decodePerms(row.TokenPermissions).Intersect(decodePerms(row.InstallationPermissions)).
		Intersect(decodePerms(row.AppPermissions))
	g := &auth.InstallationGrant{ID: row.InstallationID, NumID: row.InstallationNumID.Int64, AppID: row.AppID,
		AppNumID: row.AppNumID.Int64, AppSlug: row.AppSlug, BotID: row.BotID, AccountID: row.AccountID,
		Permissions: perms, TokenID: row.ID}
	switch {
	case row.RepoIds != nil:
		g.RepoIDs = row.RepoIds
		if row.RepositorySelection == SelectionSelected {
			// Repositories removed from the installation since.
			cur, err := s.q.ListInstallationRepoIDs(ctx, row.InstallationID)
			if err != nil {
				return auth.Principal{}, fmt.Errorf("list installation repos: %w", err)
			}
			g.RepoIDs = slices.DeleteFunc(slices.Clone(g.RepoIDs), func(id uuid.UUID) bool { return !slices.Contains(cur, id) })
		}
	case row.RepositorySelection == SelectionAll:
		g.AllRepos = true
	default:
		if g.RepoIDs, err = s.q.ListInstallationRepoIDs(ctx, row.InstallationID); err != nil {
			return auth.Principal{}, fmt.Errorf("list installation repos: %w", err)
		}
	}
	var scopes []string
	switch {
	case perms.Has(authz.AppContents, authz.AppWrite):
		scopes = []string{auth.ScopeReadRepo, auth.ScopeWriteRepo}
	case perms.Has(authz.AppContents, authz.AppRead):
		scopes = []string{auth.ScopeReadRepo}
	}
	return auth.Principal{Username: row.BotLogin, Role: auth.RoleUser, Method: auth.MethodToken, TokenID: row.ID,
		Scopes: scopes, Installation: g}, nil
}

// RevokeToken revokes the installation token of the caller.
func (s *Service) RevokeToken(ctx context.Context, g *auth.InstallationGrant) error {
	if g == nil {
		return ErrForbidden
	}
	if _, err := s.q.DeleteInstallationToken(ctx, g.TokenID); err != nil {
		return fmt.Errorf("delete installation token: %w", err)
	}
	return nil
}

// GrantRepos returns the repositories an installation token reaches.
func (s *Service) GrantRepos(ctx context.Context, g *auth.InstallationGrant) (Installation, []RepoRef, error) {
	if g == nil {
		return Installation{}, nil, ErrForbidden
	}
	inst, err := s.installation(ctx, s.q, g.ID)
	if err != nil {
		return Installation{}, nil, err
	}
	all, err := s.Repos(ctx, inst)
	if err != nil {
		return Installation{}, nil, err
	}
	if g.AllRepos {
		return inst, all, nil
	}
	out := slices.DeleteFunc(all, func(r RepoRef) bool { return !slices.Contains(g.RepoIDs, r.ID) })
	return inst, out, nil
}

// UserInstallations returns the installations a user token reaches: on
// the user's account and on its organizations; for a user-to-server
// token, those of its App only.
func (s *Service) UserInstallations(ctx context.Context, who auth.Principal) ([]Installation, error) {
	if who.UserID == uuid.Nil {
		return nil, ErrForbidden
	}
	p := dbgen.ListUserInstallationsParams{UserID: who.UserID}
	if who.UserApp != nil {
		p.AppID = uuid.NullUUID{UUID: who.UserApp.AppID, Valid: true}
	}
	rows, err := s.q.ListUserInstallations(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list user installations: %w", err)
	}
	out := make([]Installation, 0, len(rows))
	for _, r := range rows {
		out = append(out, installationFrom(dbgen.GetInstallationRow(r)))
	}
	return out, nil
}

// UserInstallation returns installation num if the user token reaches it.
func (s *Service) UserInstallation(ctx context.Context, who auth.Principal, num int64) (Installation, error) {
	list, err := s.UserInstallations(ctx, who)
	if err != nil {
		return Installation{}, err
	}
	i := slices.IndexFunc(list, func(in Installation) bool { return in.NumID == num })
	if i < 0 {
		return Installation{}, ErrNotFound
	}
	return list[i], nil
}

// InstallationForRepoName returns the installation of the App appID that
// covers repository owner/name, whatever its visibility (the App itself
// asks, with its JWT).
func (s *Service) InstallationForRepoName(ctx context.Context, appID uuid.UUID, owner, name string) (Installation, error) {
	accID, _, _, err := s.account(ctx, owner)
	if err != nil {
		return Installation{}, err
	}
	rows, err := s.q.ReposByNames(ctx, dbgen.ReposByNamesParams{OwnerID: accID, Names: []string{strings.ToLower(name)}})
	if err != nil {
		return Installation{}, fmt.Errorf("repo by name: %w", err)
	}
	if len(rows) == 0 {
		return Installation{}, ErrNotFound
	}
	return s.InstallationForRepo(ctx, appID, rows[0].ID)
}
