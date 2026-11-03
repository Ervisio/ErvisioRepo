package pulls

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Review events of CreateReview and SubmitReview.
const (
	EventApprove        = "approve"
	EventRequestChanges = "request_changes"
	EventComment        = "comment"
)

// DraftComment is an inline comment of a new review.
type DraftComment struct {
	Path      string
	Side      string
	Line      int
	StartLine int
	Body      string
}

// ReviewInput creates a review.
type ReviewInput struct {
	Event     string // empty leaves the review pending
	Body      string
	CommitSHA string
	Comments  []DraftComment
}

func reviewState(event string) (string, error) {
	switch event {
	case EventApprove:
		return ReviewApproved, nil
	case EventRequestChanges:
		return ReviewChangesRequested, nil
	case EventComment:
		return ReviewCommented, nil
	}
	return "", invalid("event must be approve, request_changes or comment")
}

// ---- ReviewSource ----

// Reviews implements protection.ReviewSource: the latest decisive review
// of each reviewer.
func (s *Service) Reviews(ctx context.Context, q protection.ReviewQuery) ([]protection.Review, error) {
	rows, err := s.q.LatestPullReviews(ctx, q.PullID)
	if err != nil {
		return nil, fmt.Errorf("latest reviews: %w", err)
	}
	out := make([]protection.Review, 0, len(rows))
	for _, r := range rows {
		if !r.ReviewerID.Valid {
			continue
		}
		out = append(out, protection.Review{ReviewerID: r.ReviewerID.UUID, CommitSHA: r.CommitSha, Approved: r.State == ReviewApproved})
	}
	return out, nil
}

var _ protection.ReviewSource = (*Service)(nil)

// ---- conversions ----

func (s *Service) reviews(ctx context.Context, rows []dbgen.PullReview) ([]Review, error) {
	var ids []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.ReviewerID.UUID, r.DismissedBy.UUID)
	}
	names, err := s.names(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, Review{ID: r.ID, Reviewer: names[r.ReviewerID.UUID], ReviewerID: r.ReviewerID.UUID, State: r.State,
			Body: r.Body, CommitSHA: r.CommitSha, SubmittedAt: tsPtr(r.SubmittedAt), DismissedAt: tsPtr(r.DismissedAt),
			DismissedBy: names[r.DismissedBy.UUID], DismissMessage: r.DismissMessage, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

func (s *Service) review(ctx context.Context, r dbgen.PullReview) (Review, error) {
	out, err := s.reviews(ctx, []dbgen.PullReview{r})
	if err != nil {
		return Review{}, err
	}
	return out[0], nil
}

// ---- reading ----

// ListReviews returns the submitted reviews of a pull request and the
// caller's pending one.
func (s *Service) ListReviews(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]Review, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	_, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListPullReviews(ctx, dbgen.ListPullReviewsParams{PullID: p.IssueID, ViewerID: nullID(who.UserID), Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	return s.reviews(ctx, rows)
}

// loadReview returns a review the caller can see.
func (s *Service) loadReview(ctx context.Context, who auth.Principal, p dbgen.PullRequest, id uuid.UUID) (dbgen.PullReview, error) {
	rv, err := s.q.GetPullReview(ctx, dbgen.GetPullReviewParams{ID: id, PullID: p.IssueID})
	if err != nil {
		return dbgen.PullReview{}, notFound(err, ErrReviewNotFound, "get review")
	}
	if rv.State == ReviewPending && (who.UserID == uuid.Nil || rv.ReviewerID.UUID != who.UserID) {
		return dbgen.PullReview{}, ErrReviewNotFound
	}
	return rv, nil
}

// GetReview returns one review.
func (s *Service) GetReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) (Review, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return Review{}, err
	}
	_, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Review{}, err
	}
	rv, err := s.loadReview(ctx, who, p, id)
	if err != nil {
		return Review{}, err
	}
	return s.review(ctx, rv)
}

// ---- writing ----

// openForReview loads an open pull request the caller may review.
func (s *Service) openForReview(ctx context.Context, who auth.Principal, owner, name string, number int64) (repos.Repo, dbgen.Issue, dbgen.PullRequest, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, dbgen.Issue{}, dbgen.PullRequest{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return repos.Repo{}, dbgen.Issue{}, dbgen.PullRequest{}, err
	}
	if i.Locked && !canTriage(r) {
		return repos.Repo{}, dbgen.Issue{}, dbgen.PullRequest{}, ErrLocked
	}
	return r, i, p, nil
}

func (s *Service) emitReview(ctx context.Context, q *dbgen.Queries, action string, actor uuid.UUID, p dbgen.PullRequest, rv dbgen.PullReview) error {
	return events.Emit(ctx, q, events.Event{Kind: events.KindPullReview, Action: action, RepoID: p.RepoID, ActorID: actor, SubjectID: rv.ID},
		events.PullReviewPayload{Number: p.Number, PullID: p.IssueID, ReviewID: rv.ID, State: rv.State, CommitSHA: rv.CommitSha})
}

// submitted records the timeline entry and the events of a submitted
// review and of its comments.
func (s *Service) submitted(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, i dbgen.Issue, p dbgen.PullRequest, rv dbgen.PullReview) error {
	if err := s.timeline(ctx, q, i, actor, "reviewed", map[string]any{"review": rv.ID.String(), "state": rv.State}); err != nil {
		return err
	}
	if _, err := q.RemovePullReviewRequest(ctx, dbgen.RemovePullReviewRequestParams{PullID: p.IssueID, ReviewerID: actor}); err != nil {
		return fmt.Errorf("clear review request: %w", err)
	}
	cs, err := q.ListPullReviewComments(ctx, dbgen.ListPullReviewCommentsParams{PullID: p.IssueID, ViewerID: nullID(actor),
		ReviewID: rv.ID, Off: 0, Lim: MaxReviewComments * 10})
	if err != nil {
		return fmt.Errorf("review comments: %w", err)
	}
	for _, c := range cs {
		if err := s.emitComment(ctx, q, "created", actor, p, c); err != nil {
			return err
		}
	}
	return s.emitReview(ctx, q, "submitted", actor, p, rv)
}

// CreateReview starts a review, submitted at once when in.Event is set.
func (s *Service) CreateReview(ctx context.Context, who auth.Principal, owner, name string, number int64, in ReviewInput) (Review, error) {
	r, i, p, err := s.openForReview(ctx, who, owner, name, number)
	if err != nil {
		return Review{}, err
	}
	if p.MergedAt.Valid || i.State != issues.StateOpen {
		if in.Event != EventComment && in.Event != "" {
			return Review{}, fmt.Errorf("%w: the pull request is not open", ErrConflict)
		}
	}
	state := ReviewPending
	if in.Event != "" {
		if state, err = reviewState(in.Event); err != nil {
			return Review{}, err
		}
	}
	if err := checkText("body", in.Body, MaxBody); err != nil {
		return Review{}, err
	}
	if len(in.Comments) > MaxReviewComments {
		return Review{}, invalid("a review has at most %d comments", MaxReviewComments)
	}
	author := i.AuthorID.Valid && i.AuthorID.UUID == who.UserID
	if author && (state == ReviewApproved || state == ReviewChangesRequested) {
		return Review{}, invalid("you cannot approve or request changes on your own pull request")
	}
	if state == ReviewChangesRequested && strings.TrimSpace(in.Body) == "" {
		return Review{}, invalid("requesting changes needs a body")
	}
	if state == ReviewCommented && strings.TrimSpace(in.Body) == "" && len(in.Comments) == 0 {
		return Review{}, invalid("a comment review needs a body or comments")
	}
	commit := p.HeadSha
	if in.CommitSHA != "" && in.CommitSHA != p.HeadSha {
		return Review{}, fmt.Errorf("%w: the head moved to %s", ErrConflict, p.HeadSha)
	}
	drafts := make([]dbgen.InsertPullReviewCommentParams, 0, len(in.Comments))
	for _, c := range in.Comments {
		arg, err := s.anchorNew(ctx, r, p, CommentInput{Body: c.Body, Path: c.Path, Side: c.Side, Line: c.Line, StartLine: c.StartLine})
		if err != nil {
			return Review{}, err
		}
		drafts = append(drafts, arg)
	}
	if _, err := s.q.GetPendingPullReview(ctx, dbgen.GetPendingPullReviewParams{PullID: p.IssueID, ReviewerID: nullID(who.UserID)}); err == nil {
		return Review{}, fmt.Errorf("%w: you already have a pending review", ErrConflict)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Review{}, fmt.Errorf("get pending review: %w", err)
	}
	var rv dbgen.PullReview
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		id, err := newID()
		if err != nil {
			return err
		}
		arg := dbgen.InsertPullReviewParams{ID: id, PullID: p.IssueID, RepoID: r.ID, ReviewerID: nullID(who.UserID),
			State: state, Body: in.Body, CommitSha: commit}
		if state != ReviewPending {
			arg.SubmittedAt = ts(s.o.Now())
		}
		rv, err = q.InsertPullReview(ctx, arg)
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: you already have a pending review", ErrConflict)
			}
			return fmt.Errorf("insert review: %w", err)
		}
		for _, d := range drafts {
			if d.ID, err = newID(); err != nil {
				return err
			}
			d.ReviewID, d.AuthorID = nullID(rv.ID), nullID(who.UserID)
			if _, err := q.InsertPullReviewComment(ctx, d); err != nil {
				return fmt.Errorf("insert review comment: %w", err)
			}
		}
		if state == ReviewPending {
			return nil
		}
		return s.submitted(ctx, q, who.UserID, i, p, rv)
	})
	if err != nil {
		return Review{}, err
	}
	return s.review(ctx, rv)
}

// SubmitReview submits the caller's pending review.
func (s *Service) SubmitReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, event, body string) (Review, error) {
	_, i, p, err := s.openForReview(ctx, who, owner, name, number)
	if err != nil {
		return Review{}, err
	}
	state, err := reviewState(event)
	if err != nil {
		return Review{}, err
	}
	if err := checkText("body", body, MaxBody); err != nil {
		return Review{}, err
	}
	rv, err := s.loadReview(ctx, who, p, id)
	if err != nil {
		return Review{}, err
	}
	if rv.State != ReviewPending {
		return Review{}, fmt.Errorf("%w: the review is already submitted", ErrConflict)
	}
	if i.AuthorID.Valid && i.AuthorID.UUID == who.UserID && state != ReviewCommented {
		return Review{}, invalid("you cannot approve or request changes on your own pull request")
	}
	if (p.MergedAt.Valid || i.State != issues.StateOpen) && state != ReviewCommented {
		return Review{}, fmt.Errorf("%w: the pull request is not open", ErrConflict)
	}
	if state == ReviewChangesRequested && strings.TrimSpace(body) == "" {
		return Review{}, invalid("requesting changes needs a body")
	}
	if state == ReviewCommented && strings.TrimSpace(body) == "" {
		n, err := s.q.CountPendingReviewComments(ctx, nullID(rv.ID))
		if err != nil {
			return Review{}, fmt.Errorf("count review comments: %w", err)
		}
		if n == 0 {
			return Review{}, invalid("a comment review needs a body or comments")
		}
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		nr, err := q.SubmitPullReview(ctx, dbgen.SubmitPullReviewParams{State: state, Body: body, ID: rv.ID})
		if err != nil {
			return notFound(err, ErrConflict, "submit review")
		}
		rv = nr
		return s.submitted(ctx, q, who.UserID, i, p, nr)
	})
	if err != nil {
		return Review{}, err
	}
	return s.review(ctx, rv)
}

// DeletePendingReview discards the caller's pending review and its
// comments.
func (s *Service) DeletePendingReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) error {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return err
	}
	_, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return err
	}
	rv, err := s.loadReview(ctx, who, p, id)
	if err != nil {
		return err
	}
	if rv.State != ReviewPending {
		return fmt.Errorf("%w: a submitted review cannot be deleted", ErrConflict)
	}
	n, err := s.q.DeletePendingPullReview(ctx, rv.ID)
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	if n == 0 {
		return ErrReviewNotFound
	}
	return nil
}

// DismissReview dismisses an approving or change-requesting review.
func (s *Service) DismissReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, message string) (Review, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Review{}, err
	}
	if !canWrite(r) {
		return Review{}, ErrForbidden
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return Review{}, invalid("message is required")
	}
	if err := checkText("message", message, MaxDismissMessage); err != nil {
		return Review{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Review{}, err
	}
	rv, err := s.loadReview(ctx, who, p, id)
	if err != nil {
		return Review{}, err
	}
	if rv.State != ReviewApproved && rv.State != ReviewChangesRequested {
		return Review{}, fmt.Errorf("%w: only approvals and change requests can be dismissed", ErrConflict)
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		nr, err := q.DismissPullReview(ctx, dbgen.DismissPullReviewParams{DismissedBy: nullID(who.UserID), DismissMessage: message, ID: rv.ID})
		if err != nil {
			return fmt.Errorf("dismiss review: %w", err)
		}
		rv = nr
		if err := s.timeline(ctx, q, i, who.UserID, "review_dismissed", map[string]any{"review": rv.ID.String(), "message": message}); err != nil {
			return err
		}
		return s.emitReview(ctx, q, "dismissed", who.UserID, p, nr)
	})
	if err != nil {
		return Review{}, err
	}
	return s.review(ctx, rv)
}

// ---- review requests ----

// RequestReviewers asks users to review a pull request.
func (s *Service) RequestReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (Pull, error) {
	return s.changeReviewers(ctx, who, owner, name, number, reviewers, true)
}

// RemoveReviewers withdraws review requests.
func (s *Service) RemoveReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (Pull, error) {
	return s.changeReviewers(ctx, who, owner, name, number, reviewers, false)
}

func (s *Service) changeReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string, add bool) (Pull, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	if !canTriage(r) && !isUser(i.AuthorID, who.UserID) {
		return Pull{}, ErrForbidden
	}
	if len(reviewers) > MaxReviewers {
		return Pull{}, invalid("at most %d reviewers", MaxReviewers)
	}
	lower := make([]string, 0, len(reviewers))
	for _, n := range reviewers {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			lower = append(lower, n)
		}
	}
	var users []dbgen.IssueUsersByNamesRow
	if len(lower) > 0 {
		if users, err = s.q.IssueUsersByNames(ctx, lower); err != nil {
			return Pull{}, fmt.Errorf("resolve reviewers: %w", err)
		}
	}
	found := map[string]bool{}
	for _, u := range users {
		found[strings.ToLower(u.Name)] = true
	}
	for _, n := range lower {
		if !found[n] {
			return Pull{}, invalid("unknown user %q", n)
		}
	}
	if add {
		if p.MergedAt.Valid || i.State != issues.StateOpen {
			return Pull{}, fmt.Errorf("%w: the pull request is not open", ErrConflict)
		}
		for _, u := range users {
			if i.AuthorID.Valid && u.ID == i.AuthorID.UUID {
				return Pull{}, invalid("the author cannot review their own pull request")
			}
			pr, ok, err := s.principalOf(ctx, u.ID)
			if err != nil {
				return Pull{}, err
			}
			if ok {
				_, err = s.o.Repos.GetByID(ctx, pr, r.ID)
			}
			if !ok || err != nil {
				return Pull{}, invalid("%s cannot read this repository", u.Name)
			}
		}
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		for _, u := range users {
			var n int64
			var err error
			action := "review_requested"
			if add {
				n, err = q.AddPullReviewRequest(ctx, dbgen.AddPullReviewRequestParams{PullID: p.IssueID, ReviewerID: u.ID})
			} else {
				action = "review_request_removed"
				n, err = q.RemovePullReviewRequest(ctx, dbgen.RemovePullReviewRequestParams{PullID: p.IssueID, ReviewerID: u.ID})
			}
			if err != nil {
				return fmt.Errorf("%s: %w", action, err)
			}
			if n == 0 {
				continue
			}
			if err := s.timeline(ctx, q, i, who.UserID, action, map[string]any{"reviewer": u.Name}); err != nil {
				return err
			}
			reviewer := u.ID
			if err := s.emitPull(ctx, q, action, who.UserID, i, p, func(pl *events.PullPayload) { pl.Reviewer = &reviewer }); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Pull{}, err
	}
	return s.one(ctx, who.UserID, r, i, p)
}
