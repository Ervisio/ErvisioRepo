package ghapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/statuses"
)

// Commit statuses and check runs and suites, in the GitHub shape. Without
// the statuses service the read endpoints answer empty results (clients
// waiting on CI see "no checks") and the write endpoints answer 501.

// checksErr maps a statuses service error to its GitHub answer. An unknown
// commit is 422 on a write (the body or path named it) and 404 on a read.
func (c *call) checksErr(err error) error {
	var ve *statuses.ValidationError
	switch {
	case errors.As(err, &ve):
		return errf(http.StatusUnprocessableEntity, "Validation Failed: %s", ve.Msg)
	case errors.Is(err, statuses.ErrCommitNotFound):
		if c.r.Method != http.MethodGet {
			return errf(http.StatusUnprocessableEntity, "No commit found for SHA: %s", chi.URLParam(c.r, "ref"))
		}
		return errNotFound
	case errors.Is(err, statuses.ErrCheckRunNotFound), errors.Is(err, statuses.ErrCheckSuiteNotFound):
		return errNotFound
	}
	return err
}

type ghStatus struct {
	URL         string  `json:"url"`
	AvatarURL   *string `json:"avatar_url"`
	ID          int64   `json:"id"`
	NodeID      string  `json:"node_id"`
	State       string  `json:"state"`
	Description *string `json:"description"`
	TargetURL   *string `json:"target_url"`
	Context     string  `json:"context"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Creator     *ghUser `json:"creator"`
}

func (c *call) statusObj(s statuses.Status) ghStatus {
	at := ts(s.CreatedAt)
	return ghStatus{URL: c.repoAPI() + "/statuses/" + s.SHA, ID: c.ids.of(s.ID), NodeID: nodeID("StatusContext", s.ID),
		State: s.State, Description: strp(s.Description), TargetURL: strp(s.TargetURL), Context: s.Context,
		CreatedAt: at, UpdatedAt: at, Creator: c.userByName(s.Creator)}
}

type ghApp struct {
	ID          int64          `json:"id"`
	Slug        string         `json:"slug"`
	NodeID      string         `json:"node_id"`
	Owner       *ghUser        `json:"owner"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ExternalURL string         `json:"external_url"`
	HTMLURL     string         `json:"html_url"`
	Permissions map[string]any `json:"permissions"`
	Events      []string       `json:"events"`
	CreatedAt   *string        `json:"created_at"`
	UpdatedAt   *string        `json:"updated_at"`
}

// appObj is the reporter of a check suite as a GitHub App. ErvisioRepo has
// no apps: the reporter name doubles as slug and its user, when it names
// one, as owner and id.
func (c *call) appObj(name string) ghApp {
	owner := c.userByName(name)
	var id int64
	if owner != nil {
		id = owner.ID
	}
	return ghApp{ID: id, Slug: name, NodeID: nodeIDString("App", name), Owner: owner, Name: name,
		ExternalURL: c.s.base, HTMLURL: c.s.base, Permissions: map[string]any{}, Events: []string{}}
}

type ghCheckOutput struct {
	Title            *string `json:"title"`
	Summary          *string `json:"summary"`
	Text             *string `json:"text"`
	AnnotationsCount int     `json:"annotations_count"`
	AnnotationsURL   string  `json:"annotations_url"`
}

type ghCheckRun struct {
	ID           int64         `json:"id"`
	HeadSHA      string        `json:"head_sha"`
	NodeID       string        `json:"node_id"`
	ExternalID   string        `json:"external_id"`
	URL          string        `json:"url"`
	HTMLURL      string        `json:"html_url"`
	DetailsURL   string        `json:"details_url"`
	Status       string        `json:"status"`
	Conclusion   *string       `json:"conclusion"`
	StartedAt    *string       `json:"started_at"`
	CompletedAt  *string       `json:"completed_at"`
	Output       ghCheckOutput `json:"output"`
	Name         string        `json:"name"`
	CheckSuite   ghSuiteRef    `json:"check_suite"`
	App          ghApp         `json:"app"`
	PullRequests []any         `json:"pull_requests"`
}

type ghSuiteRef struct {
	ID int64 `json:"id"`
}

func (c *call) checkRunObj(r statuses.CheckRun) ghCheckRun {
	id := c.ids.of(r.ID)
	self := c.repoAPI() + "/check-runs/" + strconv.FormatInt(id, 10)
	return ghCheckRun{ID: id, HeadSHA: r.HeadSHA, NodeID: nodeID("CheckRun", r.ID), ExternalID: r.ExternalID,
		URL: self, HTMLURL: c.repoHTML() + "/commit/" + r.HeadSHA, DetailsURL: r.DetailsURL, Status: r.Status,
		Conclusion: strp(r.Conclusion), StartedAt: tsp(r.StartedAt), CompletedAt: tsp(r.CompletedAt),
		Output: ghCheckOutput{Title: strp(r.Title), Summary: strp(r.Summary), Text: strp(r.Text),
			AnnotationsCount: r.Annotations, AnnotationsURL: self + "/annotations"},
		Name: r.Name, CheckSuite: ghSuiteRef{ID: c.ids.of(r.SuiteID)}, App: c.appObj(r.App), PullRequests: []any{}}
}

type ghCheckSuite struct {
	ID                   int64   `json:"id"`
	NodeID               string  `json:"node_id"`
	HeadBranch           *string `json:"head_branch"`
	HeadSHA              string  `json:"head_sha"`
	Status               string  `json:"status"`
	Conclusion           *string `json:"conclusion"`
	URL                  string  `json:"url"`
	Before               *string `json:"before"`
	After                string  `json:"after"`
	PullRequests         []any   `json:"pull_requests"`
	App                  ghApp   `json:"app"`
	Repository           *ghRepo `json:"repository,omitempty"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	LatestCheckRunsCount int     `json:"latest_check_runs_count"`
	CheckRunsURL         string  `json:"check_runs_url"`
	Rerequestable        bool    `json:"rerequestable"`
	RunsRerequestable    bool    `json:"runs_rerequestable"`
}

func (c *call) checkSuiteObj(s statuses.CheckSuite, repo *ghRepo) ghCheckSuite {
	id := c.ids.of(s.ID)
	self := c.repoAPI() + "/check-suites/" + strconv.FormatInt(id, 10)
	return ghCheckSuite{ID: id, NodeID: nodeID("CheckSuite", s.ID), HeadBranch: strp(s.HeadBranch), HeadSHA: s.HeadSHA,
		Status: s.Status, Conclusion: strp(s.Conclusion), URL: self, After: s.HeadSHA, PullRequests: []any{},
		App: c.appObj(s.App), Repository: repo, CreatedAt: ts(s.CreatedAt), UpdatedAt: ts(s.UpdatedAt),
		LatestCheckRunsCount: s.Runs, CheckRunsURL: self + "/check-runs"}
}

type ghAnnotation struct {
	Path        string  `json:"path"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	StartColumn *int    `json:"start_column"`
	EndColumn   *int    `json:"end_column"`
	Level       string  `json:"annotation_level"`
	Title       *string `json:"title"`
	Message     string  `json:"message"`
	RawDetails  *string `json:"raw_details"`
	BlobHref    string  `json:"blob_href"`
}

// ghCheckOutputIn is the output of a check run request body.
type ghCheckOutputIn struct {
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Text        string         `json:"text"`
	Annotations []ghAnnotation `json:"annotations"`
}

func (o *ghCheckOutputIn) toService() *statuses.Output {
	if o == nil {
		return nil
	}
	out := &statuses.Output{Title: o.Title, Summary: o.Summary, Text: o.Text}
	for _, a := range o.Annotations {
		out.Annotations = append(out.Annotations, statuses.Annotation{Path: a.Path, StartLine: a.StartLine,
			EndLine: a.EndLine, StartColumn: a.StartColumn, EndColumn: a.EndColumn, Level: a.Level,
			Message: a.Message, Title: deref(a.Title), RawDetails: deref(a.RawDetails)})
	}
	return out
}

// ---- commit statuses ----

func (c *call) combinedStatus() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	ref := chi.URLParam(c.r, "ref")
	self := c.repoAPI() + "/commits/" + esc(ref) + "/status"
	if c.s.d.Statuses == nil {
		res, err := c.s.d.Browse.Resolve(c.ctx, browseRepo(r), ref)
		if err != nil {
			return err
		}
		return c.ok(http.StatusOK, map[string]any{
			"state": "pending", "statuses": []any{}, "sha": res.Commit, "total_count": 0,
			"repository": c.repoObj(r), "commit_url": c.repoAPI() + "/commits/" + res.Commit, "url": self,
		})
	}
	cs, err := c.s.d.Statuses.CombinedStatus(c.ctx, c.who, c.owner, c.repo, ref)
	if err != nil {
		return c.checksErr(err)
	}
	p := c.pager()
	page, _ := slice(p, cs.Statuses)
	list := make([]ghStatus, 0, len(page))
	for _, st := range page {
		list = append(list, c.statusObj(st))
	}
	return c.ok(http.StatusOK, map[string]any{
		"state": cs.State, "statuses": list, "sha": cs.SHA, "total_count": len(cs.Statuses),
		"repository": c.repoObj(r), "commit_url": c.repoAPI() + "/commits/" + cs.SHA, "url": self,
	})
}

func (c *call) listStatuses() error {
	if c.s.d.Statuses == nil {
		if _, err := c.loadRepo(); err != nil {
			return err
		}
		return c.ok(http.StatusOK, []any{})
	}
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, _, err := c.s.d.Statuses.ListStatuses(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "ref"), p.offset(), p.limit())
	if err != nil {
		return c.checksErr(err)
	}
	page, more := cut(p, list)
	out := make([]ghStatus, 0, len(page))
	for _, st := range page {
		out = append(out, c.statusObj(st))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) createStatus() error {
	if err := c.write(); err != nil {
		return err
	}
	if c.s.d.Statuses == nil {
		return errf(http.StatusNotImplemented, "Commit statuses are not supported by this server")
	}
	var body struct {
		State       string `json:"state"`
		TargetURL   string `json:"target_url"`
		Description string `json:"description"`
		Context     string `json:"context"`
	}
	if err := c.decode(&body); err != nil {
		return err
	}
	st, err := c.s.d.Statuses.CreateStatus(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "ref"), statuses.StatusInput{
		State: body.State, Context: body.Context, Description: body.Description, TargetURL: body.TargetURL})
	if err != nil {
		return c.checksErr(err)
	}
	return c.ok(http.StatusCreated, c.statusObj(st))
}

// ---- check runs ----

// checkRunFilter reads the check_name, status and filter query parameters.
func (c *call) checkRunFilter(p pager) statuses.CheckRunFilter {
	return statuses.CheckRunFilter{Name: c.query("check_name"), Status: c.query("status"),
		All: c.query("filter") == "all", Offset: p.offset(), Limit: p.per}
}

func (c *call) checkRunList(p pager, list []statuses.CheckRun, total int) error {
	out := make([]ghCheckRun, 0, len(list))
	for _, run := range list {
		out = append(out, c.checkRunObj(run))
	}
	c.link(p, p.offset()+len(list) < total)
	return c.ok(http.StatusOK, map[string]any{"total_count": total, "check_runs": out})
}

func (c *call) listCheckRuns() error {
	if c.s.d.Statuses == nil {
		if _, err := c.loadRepo(); err != nil {
			return err
		}
		return c.ok(http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
	}
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, total, _, err := c.s.d.Statuses.ListCheckRuns(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "ref"), c.checkRunFilter(p))
	if err != nil {
		return c.checksErr(err)
	}
	return c.checkRunList(p, list, total)
}

func (c *call) listSuiteCheckRuns() error {
	if c.s.d.Statuses == nil {
		return errNotFound
	}
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	p := c.pager()
	list, total, err := c.s.d.Statuses.ListSuiteCheckRuns(c.ctx, c.who, c.owner, c.repo, id, c.checkRunFilter(p))
	if err != nil {
		return c.checksErr(err)
	}
	return c.checkRunList(p, list, total)
}

// checkRunBody is the body of a check run create or update. Pointers tell
// the omitted fields of an update apart.
type checkRunBody struct {
	Name        *string          `json:"name"`
	HeadSHA     string           `json:"head_sha"`
	DetailsURL  *string          `json:"details_url"`
	ExternalID  *string          `json:"external_id"`
	Status      *string          `json:"status"`
	StartedAt   *time.Time       `json:"started_at"`
	Conclusion  *string          `json:"conclusion"`
	CompletedAt *time.Time       `json:"completed_at"`
	Output      *ghCheckOutputIn `json:"output"`
}

func (c *call) createCheckRun() error {
	if err := c.write(); err != nil {
		return err
	}
	if c.s.d.Statuses == nil {
		return errf(http.StatusNotImplemented, "Checks are not supported by this server")
	}
	var b checkRunBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.HeadSHA == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: head_sha is required")
	}
	run, err := c.s.d.Statuses.CreateCheckRun(c.ctx, c.who, c.owner, c.repo, statuses.CheckRunInput{
		Name: deref(b.Name), HeadSHA: b.HeadSHA, Status: deref(b.Status), Conclusion: deref(b.Conclusion),
		ExternalID: deref(b.ExternalID), DetailsURL: deref(b.DetailsURL), StartedAt: b.StartedAt,
		CompletedAt: b.CompletedAt, Output: b.Output.toService()})
	if err != nil {
		if errors.Is(err, statuses.ErrCommitNotFound) {
			return errf(http.StatusUnprocessableEntity, "No commit found for SHA: %s", b.HeadSHA)
		}
		return c.checksErr(err)
	}
	return c.ok(http.StatusCreated, c.checkRunObj(run))
}

func (c *call) getCheckRun() error {
	if c.s.d.Statuses == nil {
		return errNotFound
	}
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	run, err := c.s.d.Statuses.GetCheckRun(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return c.checksErr(err)
	}
	return c.ok(http.StatusOK, c.checkRunObj(run))
}

func (c *call) updateCheckRun() error {
	if err := c.write(); err != nil {
		return err
	}
	if c.s.d.Statuses == nil {
		return errf(http.StatusNotImplemented, "Checks are not supported by this server")
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	var b checkRunBody
	if err := c.decode(&b); err != nil {
		return err
	}
	run, err := c.s.d.Statuses.UpdateCheckRun(c.ctx, c.who, c.owner, c.repo, id, statuses.CheckRunPatch{
		Name: b.Name, Status: b.Status, Conclusion: b.Conclusion, ExternalID: b.ExternalID,
		DetailsURL: b.DetailsURL, StartedAt: b.StartedAt, CompletedAt: b.CompletedAt, Output: b.Output.toService()})
	if err != nil {
		return c.checksErr(err)
	}
	return c.ok(http.StatusOK, c.checkRunObj(run))
}

func (c *call) listCheckRunAnnotations() error {
	if c.s.d.Statuses == nil {
		return errNotFound
	}
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Statuses.ListCheckRunAnnotations(c.ctx, c.who, c.owner, c.repo, id, p.offset(), p.limit())
	if err != nil {
		return c.checksErr(err)
	}
	page, more := cut(p, list)
	var sha string
	if len(page) > 0 {
		// blob_href points at the file at the commit of the run.
		run, err := c.s.d.Statuses.GetCheckRun(c.ctx, c.who, c.owner, c.repo, id)
		if err != nil {
			return c.checksErr(err)
		}
		sha = run.HeadSHA
	}
	out := make([]ghAnnotation, 0, len(page))
	for _, a := range page {
		out = append(out, ghAnnotation{Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine,
			StartColumn: a.StartColumn, EndColumn: a.EndColumn, Level: a.Level, Title: strp(a.Title),
			Message: a.Message, RawDetails: strp(a.RawDetails), BlobHref: c.repoHTML() + "/blob/" + sha + "/" + escPath(a.Path)})
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

// ---- check suites ----

func (c *call) listCheckSuites() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	if c.s.d.Statuses == nil {
		return c.ok(http.StatusOK, map[string]any{"total_count": 0, "check_suites": []any{}})
	}
	p := c.pager()
	list, total, _, err := c.s.d.Statuses.ListCheckSuites(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "ref"),
		c.query("app_slug"), p.offset(), p.per)
	if err != nil {
		return c.checksErr(err)
	}
	repo := c.repoObj(r)
	out := make([]ghCheckSuite, 0, len(list))
	for _, cs := range list {
		out = append(out, c.checkSuiteObj(cs, repo))
	}
	c.link(p, p.offset()+len(list) < total)
	return c.ok(http.StatusOK, map[string]any{"total_count": total, "check_suites": out})
}

func (c *call) getCheckSuite() error {
	if c.s.d.Statuses == nil {
		return errNotFound
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	cs, err := c.s.d.Statuses.GetCheckSuite(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return c.checksErr(err)
	}
	return c.ok(http.StatusOK, c.checkSuiteObj(cs, c.repoObj(r)))
}

// unsupportedChecks answers the check suite creation: suites are created
// with their first check run.
func (c *call) unsupportedChecks() error {
	if err := c.write(); err != nil {
		return err
	}
	return errf(http.StatusNotImplemented, "Check suites are created with their first check run on this server")
}
