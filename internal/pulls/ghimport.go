package pulls

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
)

// Import of the pull requests of a GitHub repository (ghsync.Importer):
// the issue part (with labels, assignees, milestone), head and base,
// merge state, reviews and inline review comments. The heads come from
// refs/pull/<n>/head of GitHub, fetched into the local refs/pull/<n>/head
// first, so closed pull requests whose branch is gone keep their commits.
// The test merge is not computed (mergeable state unknown); the next push
// to the head or the base recomputes it.

type gitHubBranch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *struct {
		ID int64 `json:"id"`
	} `json:"repo"`
}

type gitHubPull struct {
	issues.GitHubIssue
	Draft          bool         `json:"draft"`
	MergedAt       *time.Time   `json:"merged_at"`
	MergeCommitSHA *string      `json:"merge_commit_sha"`
	Head           gitHubBranch `json:"head"`
	Base           gitHubBranch `json:"base"`
}

type gitHubReview struct {
	ID          int64              `json:"id"`
	User        *ghsync.GitHubUser `json:"user"`
	Body        *string            `json:"body"`
	State       string             `json:"state"`
	CommitID    string             `json:"commit_id"`
	SubmittedAt *time.Time         `json:"submitted_at"`
}

type gitHubReviewComment struct {
	ID                int64              `json:"id"`
	ReviewID          *int64             `json:"pull_request_review_id"`
	InReplyTo         *int64             `json:"in_reply_to_id"`
	PullURL           string             `json:"pull_request_url"`
	Path              string             `json:"path"`
	DiffHunk          string             `json:"diff_hunk"`
	Body              *string            `json:"body"`
	User              *ghsync.GitHubUser `json:"user"`
	CommitID          string             `json:"commit_id"`
	OriginalCommitID  string             `json:"original_commit_id"`
	Line              *int32             `json:"line"`
	OriginalLine      *int32             `json:"original_line"`
	StartLine         *int32             `json:"start_line"`
	OriginalStartLine *int32             `json:"original_start_line"`
	Side              *string            `json:"side"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

var hexSHA = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

const (
	maxRefLen  = 255
	maxBodyLen = 65536
)

// GitHubImporter returns the importer of the pull requests of a GitHub
// repository. comments imports the conversation comments (issue comments
// on GitHub) when the import does not include issues, whose importer
// does it otherwise.
func (s *Service) GitHubImporter(comments func(context.Context, ghsync.ImportContext) (int, error)) ghsync.Importer {
	return pullImporter{s: s, comments: comments}
}

type pullImporter struct {
	s        *Service
	comments func(context.Context, ghsync.ImportContext) (int, error)
}

func (pullImporter) Name() string { return ghsync.ImportPulls }

func (im pullImporter) Import(ctx context.Context, ic ghsync.ImportContext) error {
	s := im.s
	if err := issues.ReserveGitHubNumbers(ctx, s.q, ic); err != nil {
		return err
	}
	heads, err := ic.FetchPullHeads(ctx)
	if err != nil {
		return err
	}
	n, r := 0, 0
	err = ghsync.Each(ctx, ic.API, ic.Repo()+"/pulls?state=all&sort=created&direction=asc", func(gp gitHubPull) error {
		p, created, err := s.importPull(ctx, ic, gp, heads)
		if errors.Is(err, issues.ErrNumberTaken) || errors.Is(err, errBadPull) {
			ic.Log.WarnContext(ctx, "github pull request skipped", "repo", ic.RepoID, "number", gp.Number, "err", err)
			return nil
		}
		if err != nil {
			return err
		}
		if created {
			n++
		}
		k, err := s.importReviews(ctx, ic, p)
		r += k
		return err
	})
	if err != nil {
		return fmt.Errorf("import pull requests: %w", err)
	}
	c, err := s.importReviewComments(ctx, ic)
	if err != nil {
		return fmt.Errorf("import review comments: %w", err)
	}
	if !ic.Includes(ghsync.ImportIssues) && im.comments != nil {
		if _, err := im.comments(ctx, ic); err != nil {
			return fmt.Errorf("import pull request comments: %w", err)
		}
		if err := s.q.RecountIssueComments(ctx, ic.RepoID); err != nil {
			return fmt.Errorf("recount comments: %w", err)
		}
	}
	ic.Log.InfoContext(ctx, "github pull requests imported", "repo", ic.RepoID,
		"pulls", n, "reviews", r, "review_comments", c)
	return nil
}

var errBadPull = errors.New("unusable pull request data")

// importPull creates the issue and pull request rows of gp.
func (s *Service) importPull(ctx context.Context, ic ghsync.ImportContext, gp gitHubPull, heads map[int64]string) (dbgen.PullRequest, bool, error) {
	head := strings.ToLower(gp.Head.SHA)
	if !hexSHA.MatchString(head) {
		head = heads[gp.Number]
	}
	base := strings.ToLower(gp.Base.SHA)
	headRef := ghsync.Clip(gp.Head.Ref, maxRefLen)
	baseRef := ghsync.Clip(gp.Base.Ref, maxRefLen)
	if !hexSHA.MatchString(head) || !hexSHA.MatchString(base) || headRef == "" || baseRef == "" {
		return dbgen.PullRequest{}, false, fmt.Errorf("%w: head %q, base %q", errBadPull, headRef, baseRef)
	}
	headRepo := uuid.NullUUID{}
	if gp.Head.Repo != nil && gp.Base.Repo != nil && gp.Head.Repo.ID == gp.Base.Repo.ID {
		headRepo = nullID(ic.RepoID) // forks are not imported: their pull requests stay detached
	}
	mergeSHA := strings.ToLower(ptr(gp.MergeCommitSHA))
	if gp.MergedAt == nil || !hexSHA.MatchString(mergeSHA) {
		mergeSHA = ""
	}
	var p dbgen.PullRequest
	created := false
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		i, c, err := issues.ImportGitHubIssue(ctx, q, ic, gp.GitHubIssue, issues.KindPull)
		if err != nil {
			return err
		}
		created = c
		if err := q.ImportPullRequest(ctx, dbgen.ImportPullRequestParams{IssueID: i.ID, RepoID: ic.RepoID,
			Number: i.Number, HeadRepoID: headRepo, HeadRef: headRef, HeadSha: head, BaseRef: baseRef,
			BaseSha: base, Draft: gp.Draft, MergedAt: ghsync.TimePtr(gp.MergedAt), MergeCommitSha: mergeSHA,
			CreatedAt: ghsync.Stamp(gp.CreatedAt), UpdatedAt: ghsync.Stamp(gp.UpdatedAt)}); err != nil {
			return fmt.Errorf("import pull request: %w", err)
		}
		if p, err = q.GetPullRequestByID(ctx, i.ID); err != nil {
			return fmt.Errorf("get pull request: %w", err)
		}
		return nil
	})
	return p, created, err
}

func ptr[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func gitHubReviewState(s string) string {
	switch strings.ToUpper(s) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "DISMISSED":
		return "dismissed"
	case "COMMENTED":
		return "commented"
	}
	return ""
}

// importReviews imports the submitted reviews of p. A review of a GitHub
// user without a local account has no reviewer and names the user in the
// body; its approval or change request becomes a comment, so it cannot
// satisfy (or block) a branch protection rule here.
func (s *Service) importReviews(ctx context.Context, ic ghsync.ImportContext, p dbgen.PullRequest) (int, error) {
	n := 0
	path := ic.Repo() + "/pulls/" + strconv.FormatInt(p.Number, 10) + "/reviews"
	err := ghsync.Each(ctx, ic.API, path, func(gr gitHubReview) error {
		state := gitHubReviewState(gr.State)
		if state == "" {
			return nil // pending: a draft only its author sees
		}
		return s.tx(ctx, func(q *dbgen.Queries) error {
			if _, ok, err := ic.Mapped(ctx, q, ghsync.MapReview, gr.ID); err != nil || ok {
				return err
			}
			reviewer, err := ic.User(ctx, gr.User)
			if err != nil {
				return err
			}
			body := ptr(gr.Body)
			if !reviewer.Valid {
				switch state {
				case "approved":
					body = "_Approved on GitHub._\n\n" + body
					state = "commented"
				case "changes_requested":
					body = "_Requested changes on GitHub._\n\n" + body
					state = "commented"
				}
				body = ghsync.Attribution(gr.User) + body
			}
			sha := strings.ToLower(gr.CommitID)
			if !hexSHA.MatchString(sha) {
				sha = p.HeadSha
			}
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("review id: %w", err)
			}
			submitted := ghsync.TimePtr(gr.SubmittedAt)
			created := ghsync.Stamp(ptr(gr.SubmittedAt))
			if err := q.ImportPullReview(ctx, dbgen.ImportPullReviewParams{ID: id, PullID: p.IssueID, RepoID: p.RepoID,
				ReviewerID: reviewer, State: state, Body: ghsync.Clip(body, maxBodyLen), CommitSha: sha,
				SubmittedAt: submitted, CreatedAt: created, UpdatedAt: created}); err != nil {
				return fmt.Errorf("import review: %w", err)
			}
			n++
			return ic.Remember(ctx, q, ghsync.MapReview, gr.ID, id)
		})
	})
	return n, err
}

func posLine(p *int32) pgtype.Int4 {
	if p == nil || *p <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *p, Valid: true}
}

// pullNumber returns the number at the end of the pull_request_url of a
// GitHub review comment.
func pullNumber(u string) int64 {
	n, err := strconv.ParseInt(u[strings.LastIndexByte(u, '/')+1:], 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// importReviewComments imports the inline review comments of every pull
// request of the repository (one paginated listing, oldest first, so a
// reply comes after the comment it answers).
func (s *Service) importReviewComments(ctx context.Context, ic ghsync.ImportContext) (int, error) {
	n := 0
	pulls := map[int64]*dbgen.PullRequest{}
	err := ghsync.Each(ctx, ic.API, ic.Repo()+"/pulls/comments?sort=created&direction=asc", func(gc gitHubReviewComment) error {
		num := pullNumber(gc.PullURL)
		if num == 0 {
			return nil
		}
		p, ok := pulls[num]
		if !ok {
			row, err := s.q.GetPullRequest(ctx, dbgen.GetPullRequestParams{RepoID: ic.RepoID, Number: num})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return fmt.Errorf("get pull request: %w", err)
			default:
				p = &row
			}
			pulls[num] = p
		}
		path := ghsync.Clip(gc.Path, 4096)
		if p == nil || path == "" {
			return nil
		}
		return s.tx(ctx, func(q *dbgen.Queries) error {
			if _, ok, err := ic.Mapped(ctx, q, ghsync.MapReviewComment, gc.ID); err != nil || ok {
				return err
			}
			author, body, err := ic.Author(ctx, gc.User, ptr(gc.Body), maxBodyLen)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				body = "_(empty comment)_"
			}
			review, reply := uuid.NullUUID{}, uuid.NullUUID{}
			if gc.ReviewID != nil {
				id, ok, err := ic.Mapped(ctx, q, ghsync.MapReview, *gc.ReviewID)
				if err != nil {
					return err
				}
				review = uuid.NullUUID{UUID: id, Valid: ok}
			}
			if gc.InReplyTo != nil {
				id, ok, err := ic.Mapped(ctx, q, ghsync.MapReviewComment, *gc.InReplyTo)
				if err != nil {
					return err
				}
				reply = uuid.NullUUID{UUID: id, Valid: ok}
			}
			commit := strings.ToLower(gc.CommitID)
			if !hexSHA.MatchString(commit) {
				commit = p.HeadSha
			}
			orig := strings.ToLower(gc.OriginalCommitID)
			if !hexSHA.MatchString(orig) {
				orig = commit
			}
			line := posLine(gc.Line)
			origLine := posLine(gc.OriginalLine)
			if !origLine.Valid {
				origLine = line
			}
			if !origLine.Valid {
				origLine = pgtype.Int4{Int32: 1, Valid: true} // file-level comment
			}
			side := "RIGHT"
			if strings.EqualFold(ptr(gc.Side), "LEFT") {
				side = "LEFT"
			}
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("review comment id: %w", err)
			}
			if err := q.ImportPullReviewComment(ctx, dbgen.ImportPullReviewCommentParams{ID: id, PullID: p.IssueID,
				RepoID: p.RepoID, ReviewID: review, AuthorID: author, InReplyTo: reply, Path: path, Side: side,
				Line: line, StartLine: posLine(gc.StartLine), OriginalLine: origLine.Int32,
				OriginalStartLine: posLine(gc.OriginalStartLine), OriginalCommitSha: orig, CommitSha: commit,
				DiffHunk: ghsync.Clip(gc.DiffHunk, maxBodyLen), Body: body, Outdated: !line.Valid,
				CreatedAt: ghsync.Stamp(gc.CreatedAt), UpdatedAt: ghsync.Stamp(gc.UpdatedAt)}); err != nil {
				return fmt.Errorf("import review comment: %w", err)
			}
			n++
			return ic.Remember(ctx, q, ghsync.MapReviewComment, gc.ID, id)
		})
	})
	return n, err
}
