package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Import of the GitHub Actions configuration of a repository: variables
// (repository and environments), secret names (GitHub never returns the
// values; the missing ones are listed for the user to fill in) and the
// latest workflow runs, stored read-only in actions_external_runs. The
// scheduler and the runners never read that table.

const (
	// ghRunsPerFetch is the number of latest GitHub runs read by an import
	// or a refresh (one page).
	ghRunsPerFetch = 100
	// keepExternalRuns bounds the GitHub runs kept per repository.
	keepExternalRuns = 500
	// maxGitHubItems bounds the variables, secrets or environments read.
	maxGitHubItems = 1000
)

// GitHubSource is the GitHub sync service as Actions needs it.
type GitHubSource interface {
	RemoteOf(ctx context.Context, repoID uuid.UUID) (ghsync.Remote, error)
	Describe(ctx context.Context, repoID uuid.UUID) (full, web string, ok bool, err error)
}

// SecretName is a secret GitHub has, by environment ("" for the repository).
type SecretName struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

// GitHubImport is the state of the import of the Actions configuration.
type GitHubImport struct {
	// Available is false when the repository has no GitHub counterpart.
	Available         bool
	Remote, RemoteURL string
	ImportedAt        *time.Time
	RunsSyncedAt      *time.Time
	VariablesImported int
	VariablesSkipped  int
	RunsImported      int
	LastError         string
	// MissingSecrets are the GitHub secrets not set here yet.
	MissingSecrets []SecretName
}

type ghNamed struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ghRun struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	DisplayTitle string             `json:"display_title"`
	Path         string             `json:"path"`
	Event        string             `json:"event"`
	Status       string             `json:"status"`
	Conclusion   *string            `json:"conclusion"`
	HeadBranch   string             `json:"head_branch"`
	HeadSha      string             `json:"head_sha"`
	RunNumber    int64              `json:"run_number"`
	RunAttempt   int32              `json:"run_attempt"`
	HTMLURL      string             `json:"html_url"`
	Actor        *ghsync.GitHubUser `json:"actor"`
	Triggering   *ghsync.GitHubUser `json:"triggering_actor"`
	RunStartedAt *time.Time         `json:"run_started_at"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

// ghList reads a paged list of the GitHub API whose answer wraps the items
// in an object under key ({"total_count": n, "<key>": [...]}).
func ghList[T any](ctx context.Context, api *ghsync.API, path, key string, perPage, limit int) ([]T, error) {
	var out []T
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; len(out) < limit; page++ {
		var body map[string]json.RawMessage
		p := path + sep + "per_page=" + strconv.Itoa(perPage) + "&page=" + strconv.Itoa(page)
		if err := api.Do(ctx, http.MethodGet, p, nil, &body); err != nil {
			return nil, err
		}
		var items []T
		if raw, ok := body[key]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("github %s: %w", key, err)
			}
		}
		out = append(out, items...)
		if len(items) < perPage {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ghProblem turns a failed GitHub read into a message for the user; a
// missing permission names the one the connection needs.
func ghProblem(what, permission string, err error) string {
	var ae *ghsync.APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusForbidden, http.StatusNotFound, http.StatusUnauthorized:
			return fmt.Sprintf("cannot read the %s: the GitHub connection needs the %q read permission", what, permission)
		}
		return fmt.Sprintf("cannot read the %s: %s", what, ae.Error())
	}
	return fmt.Sprintf("cannot read the %s: GitHub did not answer", what)
}

// importer implements ghsync.Importer for the "actions" kind.
type importer struct{ s *Service }

// GitHubImporter returns the importer of the Actions configuration and
// runs of a GitHub repository. Problems reading GitHub (a missing
// permission) are recorded in the import state and do not fail the
// repository import.
func (s *Service) GitHubImporter() ghsync.Importer { return importer{s} }

func (importer) Name() string { return ghsync.ImportActions }

func (im importer) Import(ctx context.Context, ic ghsync.ImportContext) error {
	d, err := im.s.d()
	if err != nil {
		return err
	}
	repo, err := d.Repos.GetByID(ctx, system, ic.RepoID)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}
	rem := ghsync.Remote{API: ic.API, Owner: ic.RemoteOwner, Name: ic.RemoteName, BaseURL: ic.BaseURL}
	return im.s.importGitHub(ctx, repo, rem, ic.CreatedBy, false, true)
}

// ImportFromGitHub imports the Actions configuration (and, with runs, the
// latest runs) of the GitHub counterpart of a repository (maintainers).
// Existing variables are kept unless overwrite is set.
func (s *Service) ImportFromGitHub(ctx context.Context, who auth.Principal, owner, name string, overwrite, runs bool) (GitHubImport, error) {
	repo, err := s.manageRepo(ctx, who, owner, name)
	if err != nil {
		return GitHubImport{}, err
	}
	d, err := s.d()
	if err != nil {
		return GitHubImport{}, err
	}
	if d.GitHub == nil {
		return GitHubImport{}, invalid("GitHub sync is not available on this server")
	}
	rem, err := d.GitHub.RemoteOf(ctx, repo.ID)
	if errors.Is(err, ghsync.ErrNotFound) {
		return GitHubImport{}, invalid("the repository is not linked to GitHub and was not imported from GitHub")
	}
	if err != nil {
		return GitHubImport{}, invalid("the GitHub connection cannot be used: %s", errMessage(err))
	}
	if err := s.importGitHub(ctx, repo, rem, who.UserID, overwrite, runs); err != nil {
		return GitHubImport{}, err
	}
	return s.gitHubState(ctx, d, repo)
}

// errMessage is the message of a GitHub or connection error, safe to show
// (ghsync never puts a credential in one).
func errMessage(err error) string {
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

// GitHubImportState returns the state of the import of a repository
// (maintainers).
func (s *Service) GitHubImportState(ctx context.Context, who auth.Principal, owner, name string) (GitHubImport, error) {
	repo, err := s.manageRepo(ctx, who, owner, name)
	if err != nil {
		return GitHubImport{}, err
	}
	d, err := s.d()
	if err != nil {
		return GitHubImport{}, err
	}
	return s.gitHubState(ctx, d, repo)
}

func (s *Service) gitHubState(ctx context.Context, d *Deps, repo repos.Repo) (GitHubImport, error) {
	out := GitHubImport{MissingSecrets: []SecretName{}}
	if d.GitHub != nil {
		full, web, ok, err := d.GitHub.Describe(ctx, repo.ID)
		if err != nil {
			return out, err
		}
		out.Available, out.Remote, out.RemoteURL = ok, full, web
	}
	row, err := s.q.GetActionsGitHubImport(ctx, repo.ID)
	if isNoRows(err) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("get github import: %w", err)
	}
	out.ImportedAt = timePtr(row.ImportedAt.Time, row.ImportedAt.Valid)
	out.RunsSyncedAt = timePtr(row.RunsSyncedAt.Time, row.RunsSyncedAt.Valid)
	out.VariablesImported, out.VariablesSkipped = int(row.VariablesImported), int(row.VariablesSkipped)
	out.RunsImported, out.LastError = int(row.RunsImported), row.LastError
	var names []SecretName
	if err := json.Unmarshal(row.SecretNames, &names); err != nil {
		return out, fmt.Errorf("decode secret names: %w", err)
	}
	have, err := s.q.ListActionsRepoSecretKeys(ctx, nullID(repo.ID))
	if err != nil {
		return out, fmt.Errorf("list secrets: %w", err)
	}
	set := make(map[SecretName]bool, len(have))
	for _, h := range have {
		set[SecretName{Name: h.Name, Environment: h.Environment}] = true
	}
	for _, n := range names {
		if !set[n] {
			out.MissingSecrets = append(out.MissingSecrets, n)
		}
	}
	return out, nil
}

// importGitHub reads the Actions configuration and runs of rem into repo.
// GitHub failures are recorded as the last error of the import; only
// local failures are returned.
func (s *Service) importGitHub(ctx context.Context, repo repos.Repo, rem ghsync.Remote, by uuid.UUID, overwrite, runs bool) error {
	var problems []string
	existing, err := s.q.ListActionsRepoVariableKeys(ctx, nullID(repo.ID))
	if err != nil {
		return fmt.Errorf("list variables: %w", err)
	}
	have := make(map[SecretName]bool, len(existing))
	for _, e := range existing {
		have[SecretName{Name: e.Name, Environment: e.Environment}] = true
	}
	imported, skipped := 0, 0
	putVars := func(env string, vars []ghNamed) error {
		for _, v := range vars {
			n, err := normalizeName(v.Name)
			if err != nil || len(v.Value) > maxVariableSize {
				skipped++
				continue
			}
			if have[SecretName{Name: n, Environment: env}] && !overwrite {
				skipped++
				continue
			}
			kind := "repo"
			if env != "" {
				kind = "environment"
			}
			id, _ := uuid.NewV7()
			if _, err := s.q.UpsertActionsVariable(ctx, dbgen.UpsertActionsVariableParams{ID: id, Scope: kind,
				RepoID: nullID(repo.ID), Environment: env, Name: n, Value: v.Value, CreatedBy: nullID(by)}); err != nil {
				return fmt.Errorf("store variable: %w", err)
			}
			imported++
		}
		return nil
	}
	secrets := []SecretName{}
	addSecrets := func(env string, list []ghNamed) {
		for _, sc := range list {
			if n, err := normalizeName(sc.Name); err == nil {
				secrets = append(secrets, SecretName{Name: n, Environment: env})
			}
		}
	}
	base := rem.Repo()
	if vars, err := ghList[ghNamed](ctx, rem.API, base+"/actions/variables", "variables", 30, maxGitHubItems); err != nil {
		problems = append(problems, ghProblem("variables", "Variables", err))
	} else if err := putVars("", vars); err != nil {
		return err
	}
	if list, err := ghList[ghNamed](ctx, rem.API, base+"/actions/secrets", "secrets", 100, maxGitHubItems); err != nil {
		problems = append(problems, ghProblem("secret names", "Secrets", err))
	} else {
		addSecrets("", list)
	}
	envs, err := ghList[ghNamed](ctx, rem.API, base+"/environments", "environments", 100, maxGitHubItems)
	if err != nil {
		problems = append(problems, ghProblem("environments", "Environments", err))
	}
	for _, e := range envs {
		env := strings.TrimSpace(e.Name)
		if env == "" || len(env) > 255 {
			continue
		}
		if err := s.ensureEnvironment(ctx, repo.ID, env); err != nil {
			return err
		}
		ep := base + "/environments/" + url.PathEscape(env)
		if vars, err := ghList[ghNamed](ctx, rem.API, ep+"/variables", "variables", 30, maxGitHubItems); err != nil {
			problems = append(problems, ghProblem("variables of environment "+env, "Environments", err))
		} else if err := putVars(env, vars); err != nil {
			return err
		}
		if list, err := ghList[ghNamed](ctx, rem.API, ep+"/secrets", "secrets", 100, maxGitHubItems); err != nil {
			problems = append(problems, ghProblem("secret names of environment "+env, "Environments", err))
		} else {
			addSecrets(env, list)
		}
	}
	nRuns := 0
	if runs {
		n, err := s.fetchGitHubRuns(ctx, repo.ID, rem)
		var ae *ghsync.APIError
		switch {
		case errors.As(err, &ae):
			problems = append(problems, ghProblem("workflow runs", "Actions", err))
		case err != nil:
			return err
		}
		nRuns = n
	}
	names, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encode secret names: %w", err)
	}
	if err := s.q.SaveActionsGitHubImport(ctx, dbgen.SaveActionsGitHubImportParams{RepoID: repo.ID, SecretNames: names,
		VariablesImported: int32(min(imported, maxGitHubItems*2)), VariablesSkipped: int32(min(skipped, maxGitHubItems*2)), // #nosec G115 -- bounded
		RunsImported: int32(nRuns), LastError: truncate(strings.Join(problems, "; "), 2000), // #nosec G115 -- at most ghRunsPerFetch
		RunsSynced: runs && nRuns > 0}); err != nil {
		return fmt.Errorf("save github import: %w", err)
	}
	s.log.Info("github actions import", "repo_id", repo.ID, "variables", imported, "skipped", skipped,
		"secrets", len(secrets), "runs", nRuns, "problems", len(problems))
	return nil
}

// ensureEnvironment creates an environment with the default settings
// when it does not exist; an existing one is left as it is.
func (s *Service) ensureEnvironment(ctx context.Context, repoID uuid.UUID, env string) error {
	_, err := s.q.GetActionsEnvironment(ctx, dbgen.GetActionsEnvironmentParams{RepoID: repoID, Name: env})
	if err == nil {
		return nil
	}
	if !isNoRows(err) {
		return fmt.Errorf("get environment: %w", err)
	}
	id, _ := uuid.NewV7()
	if _, err := s.q.UpsertActionsEnvironment(ctx, dbgen.UpsertActionsEnvironmentParams{ID: id, RepoID: repoID, Name: env,
		Reviewers: []uuid.UUID{}}); err != nil {
		return fmt.Errorf("store environment: %w", err)
	}
	return nil
}

// fetchGitHubRuns stores the latest runs of rem as external runs and
// returns how many were read. A GitHub failure is a *ghsync.APIError.
func (s *Service) fetchGitHubRuns(ctx context.Context, repoID uuid.UUID, rem ghsync.Remote) (int, error) {
	var body struct {
		Runs []ghRun `json:"workflow_runs"`
	}
	path := rem.Repo() + "/actions/runs?per_page=" + strconv.Itoa(ghRunsPerFetch)
	if err := rem.API.Do(ctx, http.MethodGet, path, nil, &body); err != nil {
		return 0, err
	}
	for _, r := range body.Runs {
		if err := s.storeGitHubRun(ctx, repoID, r); err != nil {
			return 0, err
		}
	}
	if err := s.q.PruneActionsExternalRuns(ctx, dbgen.PruneActionsExternalRunsParams{RepoID: repoID, Keep: keepExternalRuns}); err != nil {
		return 0, fmt.Errorf("prune external runs: %w", err)
	}
	return len(body.Runs), nil
}

// externalStatuses maps the GitHub run statuses to the local ones.
var externalStatuses = map[string]string{"requested": "queued", "queued": "queued", "pending": "pending",
	"waiting": "waiting", "in_progress": "in_progress", "completed": "completed"}

func (s *Service) storeGitHubRun(ctx context.Context, repoID uuid.UUID, r ghRun) error {
	status, ok := externalStatuses[r.Status]
	if !ok {
		status = "queued"
	}
	conclusion := ""
	if r.Conclusion != nil && status == "completed" {
		conclusion = truncate(*r.Conclusion, 32)
	}
	actor := ""
	switch {
	case r.Triggering != nil:
		actor = r.Triggering.Login
	case r.Actor != nil:
		actor = r.Actor.Login
	}
	ref := ""
	if r.HeadBranch != "" {
		ref = "refs/heads/" + r.HeadBranch
	}
	wfPath, _, _ := strings.Cut(r.Path, "@")
	title := r.DisplayTitle
	if title == "" {
		title = r.Name
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	updated := r.UpdatedAt
	if updated.IsZero() {
		updated = created
	}
	started := ts(time.Time{})
	if r.RunStartedAt != nil {
		started = ts(*r.RunStartedAt)
	}
	id, _ := uuid.NewV7()
	if err := s.q.UpsertActionsExternalRun(ctx, dbgen.UpsertActionsExternalRunParams{ID: id, RepoID: repoID,
		ExternalID: r.ID, RunNumber: r.RunNumber, Attempt: max(r.RunAttempt, 1), WorkflowPath: truncate(wfPath, 1024),
		WorkflowName: truncate(r.Name, 1024), Event: truncate(r.Event, 64), Ref: truncate(ref, 1024),
		Sha: truncate(r.HeadSha, 64), Title: truncate(firstLine(title), 1024), Status: status, Conclusion: conclusion,
		ActorLogin: truncate(actor, 100), HtmlUrl: safeHTMLURL(r.HTMLURL), StartedAt: started, CreatedAt: ts(created),
		UpdatedAt: ts(updated)}); err != nil {
		return fmt.Errorf("store external run: %w", err)
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// safeHTMLURL keeps an https link only (it is rendered as a link).
func safeHTMLURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(raw) > 2048 {
		return ""
	}
	return raw
}

// RefreshGitHubRuns reads the latest GitHub runs of a repository whose
// Actions were imported from GitHub (ghsync.Service.SetAfterRun). Errors
// are logged.
func (s *Service) RefreshGitHubRuns(ctx context.Context, repoID uuid.UUID) {
	d, err := s.d()
	if err != nil || d.GitHub == nil {
		return
	}
	if _, err := s.q.GetActionsGitHubImport(ctx, repoID); err != nil {
		if !isNoRows(err) {
			s.log.Warn("github runs refresh", "repo_id", repoID, "err", err)
		}
		return
	}
	rem, err := d.GitHub.RemoteOf(ctx, repoID)
	if err != nil {
		s.log.Info("github runs refresh skipped", "repo_id", repoID, "err", err)
		return
	}
	if _, err := s.fetchGitHubRuns(ctx, repoID, rem); err != nil {
		s.log.Info("github runs refresh failed", "repo_id", repoID, "err", err)
		return
	}
	if err := s.q.SetActionsGitHubRunsSynced(ctx, repoID); err != nil {
		s.log.Warn("github runs refresh", "repo_id", repoID, "err", err)
	}
}

// externalRunFrom shows a GitHub run like a local one.
func externalRunFrom(r dbgen.ActionsExternalRun) Run {
	run := Run{ID: r.ID, RunNumber: r.RunNumber, Attempt: r.Attempt, WorkflowPath: r.WorkflowPath,
		WorkflowName: r.WorkflowName, Event: r.Event, Ref: r.Ref, Sha: r.Sha, Title: r.Title, Status: r.Status,
		Conclusion: r.Conclusion, Actor: r.ActorLogin, TriggerActor: r.ActorLogin, URL: r.HtmlUrl, External: true,
		HTMLURL: r.HtmlUrl, StartedAt: timePtr(r.StartedAt.Time, r.StartedAt.Valid), CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time}
	if r.Status == "completed" {
		run.CompletedAt = timePtr(r.UpdatedAt.Time, r.UpdatedAt.Valid)
	}
	return run
}
