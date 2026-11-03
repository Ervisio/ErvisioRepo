package statuses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// conclusionRank orders conclusions from the one that dominates a suite to
// the one that matters least; a suite concludes with the first conclusion of
// this list found among its runs.
var conclusionRank = []string{"action_required", "startup_failure", "failure", "timed_out", "cancelled",
	"stale", "success", "neutral", "skipped"}

func validStatus(s string) bool {
	return s == StatusQueued || s == StatusInProgress || s == StatusCompleted
}

func validConclusion(c string) bool {
	for _, x := range conclusionRank {
		if x == c {
			return true
		}
	}
	return false
}

func validateOutput(o *Output) error {
	if o == nil {
		return nil
	}
	if err := checkLen("output.title", o.Title, 1024); err != nil {
		return err
	}
	if err := checkLen("output.summary", o.Summary, 65535); err != nil {
		return err
	}
	if err := checkLen("output.text", o.Text, 65535); err != nil {
		return err
	}
	if len(o.Annotations) > MaxAnnotationsPerRequest {
		return invalid("at most %d annotations can be sent per request", MaxAnnotationsPerRequest)
	}
	for i := range o.Annotations {
		a := &o.Annotations[i]
		a.Path = strings.TrimSpace(a.Path)
		if a.Path == "" || len(a.Path) > 4096 {
			return invalid("annotations[%d].path is required", i)
		}
		if a.StartLine < 1 || a.EndLine < a.StartLine {
			return invalid("annotations[%d]: start_line must be >= 1 and end_line >= start_line", i)
		}
		if (a.StartColumn != nil || a.EndColumn != nil) && a.StartLine != a.EndLine {
			return invalid("annotations[%d]: columns are only allowed on a single line", i)
		}
		switch a.Level {
		case "notice", "warning", "failure":
		default:
			return invalid("annotations[%d].annotation_level must be notice, warning or failure", i)
		}
		if strings.TrimSpace(a.Message) == "" {
			return invalid("annotations[%d].message is required", i)
		}
		if err := checkLen(fmt.Sprintf("annotations[%d].message", i), a.Message, 65535); err != nil {
			return err
		}
		if err := checkLen(fmt.Sprintf("annotations[%d].title", i), a.Title, 255); err != nil {
			return err
		}
		if err := checkLen(fmt.Sprintf("annotations[%d].raw_details", i), a.RawDetails, 65535); err != nil {
			return err
		}
	}
	return nil
}

func decodeAnnotations(b []byte) []Annotation {
	var out []Annotation
	if len(b) > 0 {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func appendAnnotations(old []byte, add []Annotation) ([]byte, error) {
	list := decodeAnnotations(old)
	if len(list)+len(add) > MaxAnnotations {
		return nil, invalid("a check run holds at most %d annotations", MaxAnnotations)
	}
	list = append(list, add...)
	if list == nil {
		list = []Annotation{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return nil, fmt.Errorf("marshal annotations: %w", err)
	}
	return b, nil
}

// app names the reporter of a check run.
func app(who auth.Principal, given string) string {
	if a := strings.TrimSpace(given); a != "" {
		return a
	}
	if who.Username != "" {
		return who.Username
	}
	return DefaultApp
}

// CreateCheckRun records a check run on the commit in.HeadSHA resolves to,
// in the suite of (commit, app). It needs the push permission.
func (s *Service) CreateCheckRun(ctx context.Context, who auth.Principal, owner, name string, in CheckRunInput) (CheckRun, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return CheckRun{}, invalid("name is required")
	}
	if err := checkLen("name", in.Name, 255); err != nil {
		return CheckRun{}, err
	}
	if in.Status == "" {
		in.Status = StatusQueued
	}
	if !validStatus(in.Status) {
		return CheckRun{}, invalid("status must be queued, in_progress or completed")
	}
	if in.Conclusion != "" {
		if !validConclusion(in.Conclusion) {
			return CheckRun{}, invalid("unknown conclusion %q", in.Conclusion)
		}
		in.Status = StatusCompleted
	} else if in.Status == StatusCompleted {
		return CheckRun{}, invalid("conclusion is required when status is completed")
	}
	if err := checkLen("external_id", in.ExternalID, 255); err != nil {
		return CheckRun{}, err
	}
	if err := checkURL("details_url", in.DetailsURL); err != nil {
		return CheckRun{}, err
	}
	if err := validateOutput(in.Output); err != nil {
		return CheckRun{}, err
	}
	appName := app(who, in.App)
	if err := checkLen("app", appName, 255); err != nil {
		return CheckRun{}, err
	}
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return CheckRun{}, err
	}
	sha, branch, err := s.resolve(ctx, r, in.HeadSHA)
	if err != nil {
		return CheckRun{}, err
	}
	now := s.o.Now().UTC()
	started := in.StartedAt
	if started == nil {
		started = &now
	}
	var completed *time.Time
	if in.Status == StatusCompleted {
		completed = in.CompletedAt
		if completed == nil {
			completed = &now
		}
	}
	out := Output{}
	if in.Output != nil {
		out = *in.Output
	}
	ann, err := appendAnnotations(nil, out.Annotations)
	if err != nil {
		return CheckRun{}, err
	}
	suiteID, err := uuid.NewV7()
	if err != nil {
		return CheckRun{}, fmt.Errorf("suite id: %w", err)
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return CheckRun{}, fmt.Errorf("check run id: %w", err)
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		// The upsert locks the suite row until the end of the transaction,
		// so concurrent reports of one suite are serialized.
		suite, err := q.UpsertCheckSuite(ctx, dbgen.UpsertCheckSuiteParams{ID: suiteID, RepoID: r.ID, HeadSha: sha,
			HeadBranch: branch, App: appName})
		if err != nil {
			return fmt.Errorf("upsert check suite: %w", err)
		}
		if _, err := q.InsertCheckRun(ctx, dbgen.InsertCheckRunParams{ID: runID, SuiteID: suite.ID, RepoID: r.ID,
			HeadSha: sha, Name: in.Name, Status: in.Status, Conclusion: in.Conclusion, ExternalID: in.ExternalID,
			DetailsUrl: in.DetailsURL, OutputTitle: out.Title, OutputSummary: out.Summary, OutputText: out.Text,
			Annotations: ann, CreatorID: nullID(who.UserID), StartedAt: pgTime(started), CompletedAt: pgTime(completed)}); err != nil {
			return fmt.Errorf("insert check run: %w", err)
		}
		suite, err = s.refreshSuite(ctx, q, suite, who.UserID)
		if err != nil {
			return err
		}
		run, err := q.GetCheckRun(ctx, dbgen.GetCheckRunParams{ID: runID, RepoID: r.ID})
		if err != nil {
			return fmt.Errorf("get check run: %w", err)
		}
		if err := s.emitRun(ctx, q, "created", run, suite, who.UserID); err != nil {
			return err
		}
		if run.Status == StatusCompleted {
			return s.emitRun(ctx, q, "completed", run, suite, who.UserID)
		}
		return nil
	})
	if err != nil {
		return CheckRun{}, err
	}
	s.kick(ctx)
	s.log.InfoContext(ctx, "check run created", "repo", r.ID, "sha", sha, "name", in.Name, "status", in.Status)
	return s.GetCheckRun(ctx, who, owner, name, runID)
}

// UpdateCheckRun changes a check run. Setting a conclusion completes it;
// output annotations are appended. It needs the push permission.
func (s *Service) UpdateCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, p CheckRunPatch) (CheckRun, error) {
	if err := validateOutput(p.Output); err != nil {
		return CheckRun{}, err
	}
	if p.DetailsURL != nil {
		if err := checkURL("details_url", *p.DetailsURL); err != nil {
			return CheckRun{}, err
		}
	}
	if p.ExternalID != nil {
		if err := checkLen("external_id", *p.ExternalID, 255); err != nil {
			return CheckRun{}, err
		}
	}
	r, err := s.access(ctx, who, owner, name, true)
	if err != nil {
		return CheckRun{}, err
	}
	cur, err := s.q.GetCheckRun(ctx, dbgen.GetCheckRunParams{ID: id, RepoID: r.ID})
	if err != nil {
		if isNoRows(err) {
			return CheckRun{}, ErrCheckRunNotFound
		}
		return CheckRun{}, fmt.Errorf("get check run: %w", err)
	}
	completedNow := false
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		// Lock the suite, then the run: the same order as CreateCheckRun.
		suite, err := q.GetCheckSuiteForUpdate(ctx, cur.SuiteID)
		if err != nil {
			return fmt.Errorf("lock check suite: %w", err)
		}
		row, err := q.GetCheckRunForUpdate(ctx, dbgen.GetCheckRunForUpdateParams{ID: id, RepoID: r.ID})
		if err != nil {
			if isNoRows(err) {
				return ErrCheckRunNotFound
			}
			return fmt.Errorf("lock check run: %w", err)
		}
		next, err := s.patch(row, p)
		if err != nil {
			return err
		}
		completedNow = row.Status != StatusCompleted && next.Status == StatusCompleted
		if err := q.UpdateCheckRun(ctx, next); err != nil {
			return fmt.Errorf("update check run: %w", err)
		}
		suite, err = s.refreshSuite(ctx, q, suite, who.UserID)
		if err != nil {
			return err
		}
		if !completedNow {
			return nil
		}
		run, err := q.GetCheckRun(ctx, dbgen.GetCheckRunParams{ID: id, RepoID: r.ID})
		if err != nil {
			return fmt.Errorf("get check run: %w", err)
		}
		return s.emitRun(ctx, q, "completed", run, suite, who.UserID)
	})
	if err != nil {
		return CheckRun{}, err
	}
	if completedNow {
		s.kick(ctx)
	}
	return s.GetCheckRun(ctx, who, owner, name, id)
}

// patch applies p to row, enforcing the status/conclusion lifecycle.
func (s *Service) patch(row dbgen.CheckRun, p CheckRunPatch) (dbgen.UpdateCheckRunParams, error) {
	n := dbgen.UpdateCheckRunParams{ID: row.ID, Name: row.Name, Status: row.Status, Conclusion: row.Conclusion,
		ExternalID: row.ExternalID, DetailsUrl: row.DetailsUrl, OutputTitle: row.OutputTitle,
		OutputSummary: row.OutputSummary, OutputText: row.OutputText, Annotations: row.Annotations,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt}
	if p.Name != nil {
		v := strings.TrimSpace(*p.Name)
		if v == "" {
			return n, invalid("name cannot be empty")
		}
		if err := checkLen("name", v, 255); err != nil {
			return n, err
		}
		n.Name = v
	}
	if p.Status != nil {
		if !validStatus(*p.Status) {
			return n, invalid("status must be queued, in_progress or completed")
		}
		n.Status = *p.Status
		if n.Status != StatusCompleted {
			n.Conclusion, n.CompletedAt = "", pgtype.Timestamptz{}
		}
	}
	if p.Conclusion != nil && *p.Conclusion != "" {
		if !validConclusion(*p.Conclusion) {
			return n, invalid("unknown conclusion %q", *p.Conclusion)
		}
		n.Conclusion, n.Status = *p.Conclusion, StatusCompleted
	}
	if n.Status == StatusCompleted && n.Conclusion == "" {
		return n, invalid("conclusion is required when status is completed")
	}
	if p.ExternalID != nil {
		n.ExternalID = *p.ExternalID
	}
	if p.DetailsURL != nil {
		n.DetailsUrl = *p.DetailsURL
	}
	if p.StartedAt != nil {
		n.StartedAt = pgTime(p.StartedAt)
	}
	if n.Status == StatusCompleted {
		if p.CompletedAt != nil {
			n.CompletedAt = pgTime(p.CompletedAt)
		} else if !n.CompletedAt.Valid {
			now := s.o.Now()
			n.CompletedAt = pgTime(&now)
		}
	}
	if n.Status != StatusQueued && !n.StartedAt.Valid {
		now := s.o.Now()
		n.StartedAt = pgTime(&now)
	}
	if p.Output != nil {
		n.OutputTitle, n.OutputSummary, n.OutputText = p.Output.Title, p.Output.Summary, p.Output.Text
		ann, err := appendAnnotations(row.Annotations, p.Output.Annotations)
		if err != nil {
			return n, err
		}
		n.Annotations = ann
	}
	return n, nil
}

// refreshSuite recomputes the status and conclusion of a (locked) suite from
// the latest run of each name, and emits check_suite completed when the
// suite becomes completed.
func (s *Service) refreshSuite(ctx context.Context, q *dbgen.Queries, suite dbgen.CheckSuite, actor uuid.UUID) (dbgen.CheckSuite, error) {
	runs, err := q.SuiteRunStates(ctx, suite.ID)
	if err != nil {
		return suite, fmt.Errorf("suite runs: %w", err)
	}
	status, conclusion := StatusQueued, ""
	if len(runs) > 0 {
		done, started := 0, 0
		best := len(conclusionRank)
		for _, x := range runs {
			if x.Status != StatusQueued {
				started++
			}
			if x.Status == StatusCompleted {
				done++
				for i, c := range conclusionRank {
					if c == x.Conclusion && i < best {
						best = i
					}
				}
			}
		}
		switch {
		case done == len(runs):
			status = StatusCompleted
			conclusion = "neutral"
			if best < len(conclusionRank) {
				conclusion = conclusionRank[best]
			}
		case started > 0:
			status = StatusInProgress
		}
	}
	if status == suite.Status && conclusion == suite.Conclusion {
		return suite, nil
	}
	was := suite.Status
	if err := q.UpdateCheckSuiteState(ctx, dbgen.UpdateCheckSuiteStateParams{ID: suite.ID, Status: status, Conclusion: conclusion}); err != nil {
		return suite, fmt.Errorf("update check suite: %w", err)
	}
	suite.Status, suite.Conclusion = status, conclusion
	suite.UpdatedAt = pgtype.Timestamptz{Time: s.o.Now().UTC(), Valid: true}
	if status == StatusCompleted && was != StatusCompleted {
		if err := events.Emit(ctx, q, events.Event{Kind: events.KindCheckSuite, Action: "completed", RepoID: suite.RepoID,
			ActorID: actor, SubjectID: suite.ID}, suitePayload(suite)); err != nil {
			return suite, err
		}
	}
	return suite, nil
}

func suitePayload(x dbgen.CheckSuite) events.CheckSuitePayload {
	return events.CheckSuitePayload{ID: x.ID, HeadSHA: x.HeadSha, HeadBranch: x.HeadBranch, App: x.App,
		Status: x.Status, Conclusion: x.Conclusion, CreatedAt: x.CreatedAt.Time, UpdatedAt: x.UpdatedAt.Time}
}

func (s *Service) emitRun(ctx context.Context, q *dbgen.Queries, action string, x dbgen.GetCheckRunRow, suite dbgen.CheckSuite, actor uuid.UUID) error {
	run := runOf(x)
	return events.Emit(ctx, q, events.Event{Kind: events.KindCheckRun, Action: action, RepoID: x.RepoID,
		ActorID: actor, SubjectID: x.ID}, events.CheckRunPayload{ID: run.ID, Name: run.Name, HeadSHA: run.HeadSHA,
		Status: run.Status, Conclusion: run.Conclusion, ExternalID: run.ExternalID, DetailsURL: run.DetailsURL,
		Title: run.Title, Summary: run.Summary, Annotations: run.Annotations, StartedAt: run.StartedAt,
		CompletedAt: run.CompletedAt, Suite: suitePayload(suite)})
}

// ---- reads ----

// GetCheckRun returns a check run of the repository.
func (s *Service) GetCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (CheckRun, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return CheckRun{}, err
	}
	x, err := s.q.GetCheckRun(ctx, dbgen.GetCheckRunParams{ID: id, RepoID: r.ID})
	if err != nil {
		if isNoRows(err) {
			return CheckRun{}, ErrCheckRunNotFound
		}
		return CheckRun{}, fmt.Errorf("get check run: %w", err)
	}
	return runOf(x), nil
}

// ListCheckRunAnnotations returns a page of the annotations of a check run.
func (s *Service) ListCheckRunAnnotations(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, offset, limit int) ([]Annotation, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, err
	}
	x, err := s.q.GetCheckRun(ctx, dbgen.GetCheckRunParams{ID: id, RepoID: r.ID})
	if err != nil {
		if isNoRows(err) {
			return nil, ErrCheckRunNotFound
		}
		return nil, fmt.Errorf("get check run: %w", err)
	}
	list := decodeAnnotations(x.Annotations)
	off, lim := page(offset, limit)
	if int(off) >= len(list) {
		return []Annotation{}, nil
	}
	end := min(int(off)+int(lim), len(list))
	return list[off:end], nil
}

// ListCheckRuns lists the check runs of the commit ref resolves to, oldest
// first, with the total count of the filter. It returns the resolved sha too.
func (s *Service) ListCheckRuns(ctx context.Context, who auth.Principal, owner, name, ref string, f CheckRunFilter) ([]CheckRun, int, string, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, 0, "", err
	}
	sha, _, err := s.resolve(ctx, r, ref)
	if err != nil {
		return nil, 0, "", err
	}
	list, total, err := s.listRuns(ctx, r.ID, sha, uuid.Nil, f)
	return list, total, sha, err
}

// ListSuiteCheckRuns lists the check runs of a check suite.
func (s *Service) ListSuiteCheckRuns(ctx context.Context, who auth.Principal, owner, name string, suiteID uuid.UUID, f CheckRunFilter) ([]CheckRun, int, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.q.GetCheckSuite(ctx, dbgen.GetCheckSuiteParams{ID: suiteID, RepoID: r.ID}); err != nil {
		if isNoRows(err) {
			return nil, 0, ErrCheckSuiteNotFound
		}
		return nil, 0, fmt.Errorf("get check suite: %w", err)
	}
	return s.listRuns(ctx, r.ID, "", suiteID, f)
}

func (s *Service) listRuns(ctx context.Context, repoID uuid.UUID, sha string, suiteID uuid.UUID, f CheckRunFilter) ([]CheckRun, int, error) {
	if f.Status != "" && !validStatus(f.Status) {
		return nil, 0, invalid("status must be queued, in_progress or completed")
	}
	off, lim := page(f.Offset, f.Limit)
	bySuite := suiteID != uuid.Nil
	rows, err := s.q.ListCheckRuns(ctx, dbgen.ListCheckRunsParams{RepoID: repoID, HeadSha: sha, BySuite: bySuite,
		SuiteID: suiteID, Name: f.Name, Status: f.Status, App: f.App, LatestOnly: !f.All, Off: off, Lim: lim})
	if err != nil {
		return nil, 0, fmt.Errorf("list check runs: %w", err)
	}
	total, err := s.q.CountCheckRuns(ctx, dbgen.CountCheckRunsParams{RepoID: repoID, HeadSha: sha, BySuite: bySuite,
		SuiteID: suiteID, Name: f.Name, Status: f.Status, App: f.App, LatestOnly: !f.All})
	if err != nil {
		return nil, 0, fmt.Errorf("count check runs: %w", err)
	}
	out := make([]CheckRun, 0, len(rows))
	for _, x := range rows {
		out = append(out, runOf(dbgen.GetCheckRunRow(x)))
	}
	return out, int(total), nil
}

// GetCheckSuite returns a check suite of the repository.
func (s *Service) GetCheckSuite(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (CheckSuite, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return CheckSuite{}, err
	}
	x, err := s.q.GetCheckSuite(ctx, dbgen.GetCheckSuiteParams{ID: id, RepoID: r.ID})
	if err != nil {
		if isNoRows(err) {
			return CheckSuite{}, ErrCheckSuiteNotFound
		}
		return CheckSuite{}, fmt.Errorf("get check suite: %w", err)
	}
	list, err := s.withRuns(ctx, []dbgen.CheckSuite{x})
	if err != nil {
		return CheckSuite{}, err
	}
	return list[0], nil
}

// ListCheckSuites lists the check suites of the commit ref resolves to,
// optionally of one app, with their total count. It returns the resolved
// sha too.
func (s *Service) ListCheckSuites(ctx context.Context, who auth.Principal, owner, name, ref, appName string, offset, limit int) ([]CheckSuite, int, string, error) {
	r, err := s.access(ctx, who, owner, name, false)
	if err != nil {
		return nil, 0, "", err
	}
	sha, _, err := s.resolve(ctx, r, ref)
	if err != nil {
		return nil, 0, "", err
	}
	off, lim := page(offset, limit)
	rows, err := s.q.ListCheckSuites(ctx, dbgen.ListCheckSuitesParams{RepoID: r.ID, HeadSha: sha, App: appName, Off: off, Lim: lim})
	if err != nil {
		return nil, 0, "", fmt.Errorf("list check suites: %w", err)
	}
	total, err := s.q.CountCheckSuites(ctx, dbgen.CountCheckSuitesParams{RepoID: r.ID, HeadSha: sha, App: appName})
	if err != nil {
		return nil, 0, "", fmt.Errorf("count check suites: %w", err)
	}
	list, err := s.withRuns(ctx, rows)
	return list, int(total), sha, err
}

func (s *Service) withRuns(ctx context.Context, rows []dbgen.CheckSuite) ([]CheckSuite, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, x := range rows {
		ids = append(ids, x.ID)
	}
	counts := map[uuid.UUID]int{}
	if len(ids) > 0 {
		cs, err := s.q.CountSuiteRuns(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("count suite runs: %w", err)
		}
		for _, c := range cs {
			counts[c.SuiteID] = int(c.Runs)
		}
	}
	out := make([]CheckSuite, 0, len(rows))
	for _, x := range rows {
		out = append(out, CheckSuite{ID: x.ID, RepoID: x.RepoID, HeadSHA: x.HeadSha, HeadBranch: x.HeadBranch,
			App: x.App, Status: x.Status, Conclusion: x.Conclusion, Runs: counts[x.ID],
			CreatedAt: x.CreatedAt.Time, UpdatedAt: x.UpdatedAt.Time})
	}
	return out, nil
}

func runOf(x dbgen.GetCheckRunRow) CheckRun {
	return CheckRun{ID: x.ID, SuiteID: x.SuiteID, RepoID: x.RepoID, HeadSHA: x.HeadSha, HeadBranch: x.HeadBranch,
		Name: x.Name, App: x.App, Status: x.Status, Conclusion: x.Conclusion, ExternalID: x.ExternalID,
		DetailsURL: x.DetailsUrl, Title: x.OutputTitle, Summary: x.OutputSummary, Text: x.OutputText,
		Annotations: len(decodeAnnotations(x.Annotations)), StartedAt: tsPtr(x.StartedAt),
		CompletedAt: tsPtr(x.CompletedAt), CreatedAt: x.CreatedAt.Time, UpdatedAt: x.UpdatedAt.Time}
}
