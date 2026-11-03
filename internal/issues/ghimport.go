package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
)

// Import of the labels, milestones, issues and issue comments of a GitHub
// repository (ghsync.Importer). Every object is created in one transaction
// with its github_import_map row, so a retried import skips what an
// earlier attempt imported. Issue numbers are kept; the issue counter is
// raised past the highest GitHub number first, so local issues opened
// during the import cannot take an imported number.

// GitHubLabel is a label of the GitHub API.
type GitHubLabel struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description"`
}

// GitHubMilestone is a milestone of the GitHub API.
type GitHubMilestone struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
}

// GitHubReactions is the reaction summary of a GitHub issue or comment.
type GitHubReactions struct {
	TotalCount int `json:"total_count"`
}

// GitHubIssue is an issue (or the issue part of a pull request) of the
// GitHub API.
type GitHubIssue struct {
	ID               int64               `json:"id"`
	Number           int64               `json:"number"`
	Title            string              `json:"title"`
	Body             *string             `json:"body"`
	State            string              `json:"state"`
	StateReason      *string             `json:"state_reason"`
	User             *ghsync.GitHubUser  `json:"user"`
	Labels           []GitHubLabel       `json:"labels"`
	Assignees        []ghsync.GitHubUser `json:"assignees"`
	Milestone        *GitHubMilestone    `json:"milestone"`
	Locked           bool                `json:"locked"`
	ActiveLockReason *string             `json:"active_lock_reason"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	ClosedAt         *time.Time          `json:"closed_at"`
	ClosedBy         *ghsync.GitHubUser  `json:"closed_by"`
	PullRequest      json.RawMessage     `json:"pull_request"`
	Reactions        *GitHubReactions    `json:"reactions"`
}

type gitHubComment struct {
	ID        int64              `json:"id"`
	IssueURL  string             `json:"issue_url"`
	Body      *string            `json:"body"`
	User      *ghsync.GitHubUser `json:"user"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Reactions *GitHubReactions   `json:"reactions"`
}

type gitHubReaction struct {
	User      *ghsync.GitHubUser `json:"user"`
	Content   string             `json:"content"`
	CreatedAt time.Time          `json:"created_at"`
}

// ErrNumberTaken reports an imported issue whose number a local issue
// not coming from the import already has.
var ErrNumberTaken = errors.New("issue number already taken")

const (
	maxTitle   = 256
	maxBody    = 65536
	maxLabel   = 50
	maxLabelD  = 100
	maxMsTitle = 255
	maxMsDesc  = 10000
)

// GitHubImporters returns the importers of the labels, milestones and
// issues (with their comments and reactions) of a GitHub repository.
func (s *Service) GitHubImporters() []ghsync.Importer {
	return []ghsync.Importer{labelImporter{s}, milestoneImporter{s}, issueImporter{s}}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ---- labels ----

type labelImporter struct{ s *Service }

func (labelImporter) Name() string { return ghsync.ImportLabels }

func (im labelImporter) Import(ctx context.Context, ic ghsync.ImportContext) error {
	n := 0
	err := ghsync.Each(ctx, ic.API, ic.Repo()+"/labels", func(l GitHubLabel) error {
		created, err := im.s.importLabel(ctx, ic, l)
		if created {
			n++
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("import labels: %w", err)
	}
	ic.Log.InfoContext(ctx, "github labels imported", "repo", ic.RepoID, "count", n)
	return nil
}

func labelColor(c string) string {
	c = strings.ToLower(strings.TrimPrefix(c, "#"))
	if len(c) != 6 {
		return "ededed"
	}
	for _, r := range c {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "ededed"
		}
	}
	return c
}

// importLabel creates the label l, or maps it to the local label of the
// same name.
func (s *Service) importLabel(ctx context.Context, ic ghsync.ImportContext, l GitHubLabel) (bool, error) {
	name := strings.TrimSpace(ghsync.Clip(l.Name, maxLabel))
	if name == "" {
		return false, nil
	}
	created := false
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if _, ok, err := ic.Mapped(ctx, q, ghsync.MapLabel, l.ID); err != nil || ok {
			return err
		}
		row, err := labelFor(ctx, q, ic.RepoID, name, l.Color, str(l.Description))
		if err != nil {
			return err
		}
		created = true
		return ic.Remember(ctx, q, ghsync.MapLabel, l.ID, row.ID)
	})
	return created, err
}

// labelFor returns the label name of the repository, creating it when
// missing.
func labelFor(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID, name, color, desc string) (dbgen.Label, error) {
	row, err := q.GetLabelByName(ctx, dbgen.GetLabelByNameParams{RepoID: repoID, Name: name})
	if err == nil {
		return row, nil
	}
	if !isNoRows(err) {
		return row, fmt.Errorf("get label: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return row, fmt.Errorf("label id: %w", err)
	}
	row, err = q.ImportLabel(ctx, dbgen.ImportLabelParams{ID: id, RepoID: repoID, Name: name,
		Color: labelColor(color), Description: ghsync.Clip(desc, maxLabelD)})
	if err != nil {
		return row, fmt.Errorf("import label: %w", err)
	}
	return row, nil
}

// ---- milestones ----

type milestoneImporter struct{ s *Service }

func (milestoneImporter) Name() string { return ghsync.ImportMilestones }

func (im milestoneImporter) Import(ctx context.Context, ic ghsync.ImportContext) error {
	n := 0
	err := ghsync.Each(ctx, ic.API, ic.Repo()+"/milestones?state=all&sort=due_on&direction=asc", func(m GitHubMilestone) error {
		created, err := im.s.importMilestone(ctx, ic, m)
		if created {
			n++
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("import milestones: %w", err)
	}
	ic.Log.InfoContext(ctx, "github milestones imported", "repo", ic.RepoID, "count", n)
	return nil
}

func (s *Service) importMilestone(ctx context.Context, ic ghsync.ImportContext, m GitHubMilestone) (bool, error) {
	title := strings.TrimSpace(ghsync.Clip(m.Title, maxMsTitle))
	if title == "" {
		return false, nil
	}
	created := false
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		if _, ok, err := ic.Mapped(ctx, q, ghsync.MapMilestone, m.ID); err != nil || ok {
			return err
		}
		id, err := milestoneFor(ctx, q, ic.RepoID, m, title)
		if err != nil {
			return err
		}
		created = true
		return ic.Remember(ctx, q, ghsync.MapMilestone, m.ID, id)
	})
	return created, err
}

// milestoneFor returns the milestone of the repository titled title,
// creating it from m when missing.
func milestoneFor(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID, m GitHubMilestone, title string) (uuid.UUID, error) {
	row, err := q.GetMilestoneByTitle(ctx, dbgen.GetMilestoneByTitleParams{RepoID: repoID, Title: title})
	if err == nil {
		return row.ID, nil
	}
	if !isNoRows(err) {
		return uuid.Nil, fmt.Errorf("get milestone: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("milestone id: %w", err)
	}
	state, closed := StateOpen, ghsync.TimePtr(nil)
	if m.State == StateClosed {
		state, closed = StateClosed, ghsync.TimePtr(m.ClosedAt)
		if !closed.Valid {
			closed = ghsync.Stamp(m.UpdatedAt)
		}
	}
	row, err = q.ImportMilestone(ctx, dbgen.ImportMilestoneParams{ID: id, RepoID: repoID, Title: title,
		Description: ghsync.Clip(str(m.Description), maxMsDesc), State: state, DueOn: ghsync.TimePtr(m.DueOn),
		CreatedAt: ghsync.Stamp(m.CreatedAt), UpdatedAt: ghsync.Stamp(m.UpdatedAt), ClosedAt: closed})
	if err != nil {
		return uuid.Nil, fmt.Errorf("import milestone: %w", err)
	}
	return row.ID, nil
}

// ---- issues ----

type issueImporter struct{ s *Service }

func (issueImporter) Name() string { return ghsync.ImportIssues }

func (im issueImporter) Import(ctx context.Context, ic ghsync.ImportContext) error {
	s := im.s
	if err := ReserveGitHubNumbers(ctx, s.q, ic); err != nil {
		return err
	}
	n := 0
	err := ghsync.Each(ctx, ic.API, ic.Repo()+"/issues?state=all&sort=created&direction=asc", func(gi GitHubIssue) error {
		if len(gi.PullRequest) > 0 && string(gi.PullRequest) != "null" {
			return nil // the pulls importer owns pull requests
		}
		var row dbgen.Issue
		created := false
		err := s.tx(ctx, func(q *dbgen.Queries) error {
			var err error
			row, created, err = ImportGitHubIssue(ctx, q, ic, gi, KindIssue)
			return err
		})
		if errors.Is(err, ErrNumberTaken) {
			ic.Log.WarnContext(ctx, "github issue skipped: number taken", "repo", ic.RepoID, "number", gi.Number)
			return nil
		}
		if err != nil {
			return err
		}
		if created {
			n++
		}
		return ImportGitHubReactions(ctx, s.q, ic, gi.Reactions,
			ic.Repo()+"/issues/"+strconv.FormatInt(gi.Number, 10)+"/reactions", row.ID, uuid.NullUUID{})
	})
	if err != nil {
		return fmt.Errorf("import issues: %w", err)
	}
	c, err := s.ImportGitHubComments(ctx, ic)
	if err != nil {
		return fmt.Errorf("import issue comments: %w", err)
	}
	if err := s.q.RecountIssueComments(ctx, ic.RepoID); err != nil {
		return fmt.Errorf("recount issue comments: %w", err)
	}
	ic.Log.InfoContext(ctx, "github issues imported", "repo", ic.RepoID, "issues", n, "comments", c)
	return nil
}

// ReserveGitHubNumbers raises the issue counter of the repository past
// the highest issue or pull request number of the GitHub repository.
func ReserveGitHubNumbers(ctx context.Context, q *dbgen.Queries, ic ghsync.ImportContext) error {
	var last []GitHubIssue
	if err := ic.API.Do(ctx, http.MethodGet, ic.Repo()+"/issues?state=all&sort=created&direction=desc&per_page=1", nil, &last); err != nil {
		return fmt.Errorf("latest github issue: %w", err)
	}
	if len(last) == 0 || last[0].Number <= 0 {
		return nil
	}
	if err := q.RaiseIssueCounter(ctx, dbgen.RaiseIssueCounterParams{RepoID: ic.RepoID, Number: last[0].Number}); err != nil {
		return fmt.Errorf("raise issue counter: %w", err)
	}
	return nil
}

func stateReason(state string, r *string) string {
	switch v := str(r); v {
	case ReasonCompleted, ReasonNotPlanned, ReasonDuplicate:
		if state == StateClosed {
			return v
		}
	case ReasonReopened:
		if state == StateOpen {
			return v
		}
	}
	if state == StateClosed {
		return ReasonCompleted
	}
	return ""
}

func lockReason(locked bool, r *string) string {
	switch v := str(r); v {
	case "off-topic", "too heated", "resolved", "spam":
		if locked {
			return v
		}
	}
	return ""
}

// ImportGitHubIssue creates with q the issue of kind (KindIssue, or
// KindPull with gi built from a pull request and its id) imported from gi, with its labels, linked assignees and
// milestone, keeping its number and timestamps; created is false when an
// earlier attempt imported it already (row is then the existing issue).
// Assignees without a local account are named in the body. It returns
// ErrNumberTaken when a local issue has the number.
func ImportGitHubIssue(ctx context.Context, q *dbgen.Queries, ic ghsync.ImportContext, gi GitHubIssue, kind string) (row dbgen.Issue, created bool, err error) {
	mapKind := ghsync.MapIssue
	if kind == KindPull {
		mapKind = ghsync.MapPull // gi.ID is the pull request id
	}
	if id, ok, err := ic.Mapped(ctx, q, mapKind, gi.ID); err != nil {
		return row, false, err
	} else if ok {
		row, err = q.GetIssueByID(ctx, id)
		if err != nil {
			return row, false, fmt.Errorf("get imported issue: %w", err)
		}
		return row, false, nil
	}
	if gi.Number <= 0 {
		return row, false, fmt.Errorf("github issue %d: no number", gi.ID)
	}
	var assignees []uuid.UUID
	var others []string
	for _, a := range gi.Assignees {
		u, err := ic.User(ctx, &a)
		if err != nil {
			return row, false, err
		}
		if u.Valid {
			assignees = append(assignees, u.UUID)
		} else if a.Login != "" {
			others = append(others, "@"+a.Login)
		}
	}
	body := str(gi.Body)
	if len(others) > 0 {
		body = "_Assigned on GitHub to " + strings.Join(others, ", ") + "._\n\n" + body
	}
	author, body, err := ic.Author(ctx, gi.User, body, maxBody)
	if err != nil {
		return row, false, err
	}
	milestone := uuid.NullUUID{}
	if m := gi.Milestone; m != nil {
		id, ok, err := ic.Mapped(ctx, q, ghsync.MapMilestone, m.ID)
		if err != nil {
			return row, false, err
		}
		if !ok {
			if title := strings.TrimSpace(ghsync.Clip(m.Title, maxMsTitle)); title != "" {
				if id, err = milestoneFor(ctx, q, ic.RepoID, *m, title); err != nil {
					return row, false, err
				}
				ok = true
			}
		}
		milestone = uuid.NullUUID{UUID: id, Valid: ok}
	}
	state := StateOpen
	closedAt, closedBy := ghsync.TimePtr(nil), uuid.NullUUID{}
	if gi.State == StateClosed {
		state = StateClosed
		if closedAt = ghsync.TimePtr(gi.ClosedAt); !closedAt.Valid {
			closedAt = ghsync.Stamp(gi.UpdatedAt)
		}
		if closedBy, err = ic.User(ctx, gi.ClosedBy); err != nil {
			return row, false, err
		}
	}
	title := strings.TrimSpace(ghsync.Clip(gi.Title, maxTitle))
	if title == "" {
		title = "(untitled)"
	}
	id, err := uuid.NewV7()
	if err != nil {
		return row, false, fmt.Errorf("issue id: %w", err)
	}
	row, err = q.ImportIssue(ctx, dbgen.ImportIssueParams{ID: id, RepoID: ic.RepoID, Number: gi.Number, Kind: kind,
		Title: title, Body: body, State: state, StateReason: stateReason(state, gi.StateReason),
		Locked: gi.Locked, LockReason: lockReason(gi.Locked, gi.ActiveLockReason), AuthorID: author,
		MilestoneID: milestone, ClosedAt: closedAt, ClosedBy: closedBy,
		CreatedAt: ghsync.Stamp(gi.CreatedAt), UpdatedAt: ghsync.Stamp(gi.UpdatedAt)})
	if isNoRows(err) {
		return row, false, fmt.Errorf("github issue %d: %w", gi.Number, ErrNumberTaken)
	}
	if err != nil {
		return row, false, fmt.Errorf("import issue: %w", err)
	}
	for _, l := range gi.Labels {
		name := strings.TrimSpace(ghsync.Clip(l.Name, maxLabel))
		if name == "" {
			continue
		}
		lr, err := labelFor(ctx, q, ic.RepoID, name, l.Color, str(l.Description))
		if err != nil {
			return row, false, err
		}
		if _, err := q.AddIssueLabel(ctx, dbgen.AddIssueLabelParams{IssueID: row.ID, LabelID: lr.ID}); err != nil {
			return row, false, fmt.Errorf("add issue label: %w", err)
		}
	}
	for _, a := range assignees {
		if _, err := q.AddIssueAssignee(ctx, dbgen.AddIssueAssigneeParams{IssueID: row.ID, UserID: a}); err != nil {
			return row, false, fmt.Errorf("add issue assignee: %w", err)
		}
	}
	if err := q.RaiseIssueCounter(ctx, dbgen.RaiseIssueCounterParams{RepoID: ic.RepoID, Number: gi.Number}); err != nil {
		return row, false, fmt.Errorf("raise issue counter: %w", err)
	}
	if err := ic.Remember(ctx, q, mapKind, gi.ID, row.ID); err != nil {
		return row, false, err
	}
	return row, true, nil
}

// ImportGitHubReactions imports the reactions listed at path (an issue or
// a comment, whose summary is sum) of the users with a local account;
// the others cannot be represented and are dropped. Reactions are unique
// per user and content, so a retry adds nothing twice.
func ImportGitHubReactions(ctx context.Context, q *dbgen.Queries, ic ghsync.ImportContext, sum *GitHubReactions, path string, issueID uuid.UUID, commentID uuid.NullUUID) error {
	if sum == nil || sum.TotalCount == 0 || issueID == uuid.Nil {
		return nil
	}
	err := ghsync.Each(ctx, ic.API, path, func(r gitHubReaction) error {
		if checkContent(r.Content) != nil {
			return nil
		}
		u, err := ic.User(ctx, r.User)
		if err != nil || !u.Valid {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("reaction id: %w", err)
		}
		if err := q.ImportIssueReaction(ctx, dbgen.ImportIssueReactionParams{ID: id, IssueID: issueID,
			CommentID: commentID, UserID: u.UUID, Content: r.Content, CreatedAt: ghsync.Stamp(r.CreatedAt)}); err != nil {
			return fmt.Errorf("import reaction: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("import reactions: %w", err)
	}
	return nil
}

// issueNumber returns the number at the end of the issue_url of a GitHub
// comment.
func issueNumber(issueURL string) int64 {
	i := strings.LastIndexByte(issueURL, '/')
	n, err := strconv.ParseInt(issueURL[i+1:], 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ImportGitHubComments imports the comments of every issue and pull
// request of the repository (one paginated listing), skipping those of
// issues not imported. Pull request conversation comments are issue
// comments on GitHub, so they come in here too when the pulls importer
// ran first; the pulls importer calls it itself when issues are not
// imported.
func (s *Service) ImportGitHubComments(ctx context.Context, ic ghsync.ImportContext) (int, error) {
	n := 0
	issueByNumber := map[int64]uuid.UUID{}
	err := ghsync.Each(ctx, ic.API, ic.Repo()+"/issues/comments?sort=created&direction=asc", func(c gitHubComment) error {
		num := issueNumber(c.IssueURL)
		if num == 0 {
			return nil
		}
		issueID, ok := issueByNumber[num]
		if !ok {
			row, err := s.q.GetIssueByNumber(ctx, dbgen.GetIssueByNumberParams{RepoID: ic.RepoID, Number: num})
			switch {
			case isNoRows(err):
			case err != nil:
				return fmt.Errorf("get issue: %w", err)
			default:
				issueID = row.ID
			}
			issueByNumber[num] = issueID
		}
		if issueID == uuid.Nil {
			return nil
		}
		var commentID uuid.UUID
		err := s.tx(ctx, func(q *dbgen.Queries) error {
			id, ok, err := ic.Mapped(ctx, q, ghsync.MapComment, c.ID)
			if err != nil || ok {
				commentID = id
				return err
			}
			author, body, err := ic.Author(ctx, c.User, str(c.Body), maxBody)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				body = "_(empty comment)_"
			}
			if commentID, err = uuid.NewV7(); err != nil {
				return fmt.Errorf("comment id: %w", err)
			}
			edited := ghsync.TimePtr(nil)
			if c.UpdatedAt.After(c.CreatedAt.Add(time.Second)) {
				edited = ghsync.Time(c.UpdatedAt)
			}
			if err := q.ImportIssueComment(ctx, dbgen.ImportIssueCommentParams{ID: commentID, IssueID: issueID,
				RepoID: ic.RepoID, AuthorID: author, Body: body, CreatedAt: ghsync.Stamp(c.CreatedAt),
				UpdatedAt: ghsync.Stamp(c.UpdatedAt), EditedAt: edited}); err != nil {
				return fmt.Errorf("import comment: %w", err)
			}
			n++
			return ic.Remember(ctx, q, ghsync.MapComment, c.ID, commentID)
		})
		if err != nil {
			return err
		}
		return ImportGitHubReactions(ctx, s.q, ic, c.Reactions,
			ic.Repo()+"/issues/comments/"+strconv.FormatInt(c.ID, 10)+"/reactions",
			issueID, uuid.NullUUID{UUID: commentID, Valid: true})
	})
	return n, err
}
