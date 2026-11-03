package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// SubjectRef names the subject of a notification: an issue or pull
// request by number, or a release by id (exactly one of them).
type SubjectRef struct {
	Owner, Name string
	Number      int64
	ReleaseID   uuid.UUID
}

// Subject is the caller's view of a subject: its thread, when the caller
// received one, and its subscription.
type Subject struct {
	Thread       *Thread
	Subscription ThreadSubscription
}

// subject is a resolved subject.
type subject struct {
	repoID  uuid.UUID
	key     string
	issueID uuid.UUID // uuid.Nil for a release
}

// resolveSubject finds the subject in a repository the caller can read.
// Repository errors are returned as they are.
func (s *Service) resolveSubject(ctx context.Context, who auth.Principal, ref SubjectRef) (subject, error) {
	if err := signedIn(who); err != nil {
		return subject{}, err
	}
	if (ref.Number > 0) == (ref.ReleaseID != uuid.Nil) {
		return subject{}, &ValidationError{msg: "exactly one of number and release is required"}
	}
	rs, err := s.reposSvc()
	if err != nil {
		return subject{}, err
	}
	repo, err := rs.Get(ctx, who, ref.Owner, ref.Name)
	if err != nil {
		return subject{}, err
	}
	if ref.Number > 0 {
		i, err := s.q.GetIssueByNumber(ctx, dbgen.GetIssueByNumberParams{RepoID: repo.ID, Number: ref.Number})
		if errors.Is(err, pgx.ErrNoRows) {
			return subject{}, ErrNotFound
		}
		if err != nil {
			return subject{}, fmt.Errorf("get notification subject: %w", err)
		}
		return subject{repoID: repo.ID, key: "issue:" + i.ID.String(), issueID: i.ID}, nil
	}
	r, err := s.q.GetRelease(ctx, dbgen.GetReleaseParams{ID: ref.ReleaseID, RepoID: repo.ID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && r.Draft) {
		// Drafts are never notified.
		return subject{}, ErrNotFound
	}
	if err != nil {
		return subject{}, fmt.Errorf("get notification subject: %w", err)
	}
	return subject{repoID: repo.ID, key: "release:" + r.ID.String()}, nil
}

// subjectView builds the caller's view of a resolved subject. An explicit
// choice wins; otherwise ignoring the repository means not subscribed,
// and the caller is subscribed when it watches the repository, has a
// thread or takes part in the issue or pull request.
func (s *Service) subjectView(ctx context.Context, who auth.Principal, sub subject) (Subject, error) {
	var out Subject
	row, err := s.q.GetNotificationThreadBySubject(ctx, dbgen.GetNotificationThreadBySubjectParams{UserID: who.UserID,
		SubjectKey: sub.key})
	switch {
	case err == nil:
		t := threadOut(row.NotificationThread, row.OwnerName, row.RepoName)
		out.Thread = &t
	case !errors.Is(err, pgx.ErrNoRows):
		return Subject{}, fmt.Errorf("get notification thread: %w", err)
	}
	ts, err := s.q.GetThreadSubscription(ctx, dbgen.GetThreadSubscriptionParams{UserID: who.UserID, SubjectKey: sub.key})
	if err == nil {
		out.Subscription = ThreadSubscription{Subscribed: ts.Subscribed, Ignored: !ts.Subscribed, Explicit: true}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Subject{}, fmt.Errorf("get thread subscription: %w", err)
	}
	level := LevelParticipating
	rsub, err := s.q.GetRepoSubscription(ctx, dbgen.GetRepoSubscriptionParams{UserID: who.UserID, RepoID: sub.repoID})
	switch {
	case err == nil:
		level = rsub.Level
	case !errors.Is(err, pgx.ErrNoRows):
		return Subject{}, fmt.Errorf("get repository subscription: %w", err)
	}
	switch {
	case level == LevelIgnoring:
		return out, nil
	case level == LevelWatching, out.Thread != nil:
		out.Subscription.Subscribed = true
		return out, nil
	}
	if sub.issueID != uuid.Nil {
		parts, err := s.q.NotifyIssueParticipants(ctx, sub.issueID)
		if err != nil {
			return Subject{}, fmt.Errorf("notification participants: %w", err)
		}
		for _, p := range parts {
			if p.UserID == who.UserID {
				out.Subscription.Subscribed = true
				break
			}
		}
	}
	return out, nil
}

// Subject returns the caller's thread and subscription for a subject.
func (s *Service) Subject(ctx context.Context, who auth.Principal, ref SubjectRef) (Subject, error) {
	sub, err := s.resolveSubject(ctx, who, ref)
	if err != nil {
		return Subject{}, err
	}
	return s.subjectView(ctx, who, sub)
}

// SetSubjectSubscription subscribes the caller to a subject, or
// unsubscribes it when ignored.
func (s *Service) SetSubjectSubscription(ctx context.Context, who auth.Principal, ref SubjectRef, ignored bool) (Subject, error) {
	sub, err := s.resolveSubject(ctx, who, ref)
	if err != nil {
		return Subject{}, err
	}
	if _, err := s.q.UpsertThreadSubscription(ctx, dbgen.UpsertThreadSubscriptionParams{UserID: who.UserID,
		RepoID: sub.repoID, SubjectKey: sub.key, Subscribed: !ignored}); err != nil {
		return Subject{}, fmt.Errorf("set thread subscription: %w", err)
	}
	return s.subjectView(ctx, who, sub)
}

// DeleteSubjectSubscription returns a subject to the default rules.
func (s *Service) DeleteSubjectSubscription(ctx context.Context, who auth.Principal, ref SubjectRef) (Subject, error) {
	sub, err := s.resolveSubject(ctx, who, ref)
	if err != nil {
		return Subject{}, err
	}
	if err := s.q.DeleteThreadSubscription(ctx, dbgen.DeleteThreadSubscriptionParams{UserID: who.UserID,
		SubjectKey: sub.key}); err != nil {
		return Subject{}, fmt.Errorf("delete thread subscription: %w", err)
	}
	return s.subjectView(ctx, who, sub)
}
