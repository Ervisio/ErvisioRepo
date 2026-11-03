package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// EventKinds are the bus kinds that notify.
var EventKinds = []string{
	events.KindIssue, events.KindIssueComment, events.KindPull, events.KindPullReview,
	events.KindPullReviewComment, events.KindRelease,
}

// EventHandler turns bus events into notification threads. A repeated
// event changes nothing (the thread remembers its last event).
func (s *Service) EventHandler() events.Handler { return events.HandlerFunc(s.handle) }

// payload holds the fields of the event payloads notify reads
// (issues.EventPayload, events.PullPayload, events.PullReviewPayload,
// events.PullReviewCommentPayload and the release payload).
type payload struct {
	Number    int64      `json:"number"`
	IssueKind string     `json:"issue_kind"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Assignee  *uuid.UUID `json:"assignee"`
	Reviewer  *uuid.UUID `json:"reviewer"`
	Tag       string     `json:"tag"`
	Name      string     `json:"name"`
}

// change is what an event means for notifications.
type change struct {
	// action is the verb of the mail template (see notification.txt).
	action string
	// stateChange turns the plain commenters into state_change.
	stateChange bool
	// source is the key whose mentions notify (issue:<id> or comment:<id>).
	source string
	// direct is a user targeted by the event itself, with its reason; when
	// set only that user is notified (an assignment or review request).
	direct       uuid.UUID
	directReason string
}

// strength orders the reasons, the strongest first.
var strength = map[string]int{
	ReasonReviewRequested: 7, ReasonAssigned: 6, ReasonMentioned: 5, ReasonAuthor: 4,
	ReasonComment: 3, ReasonStateChange: 2, ReasonSubscribed: 1,
}

// recipients collects users with their strongest reason.
type recipients map[uuid.UUID]string

func (r recipients) add(id uuid.UUID, reason string) {
	if id == uuid.Nil {
		return
	}
	if cur, ok := r[id]; !ok || strength[reason] > strength[cur] {
		r[id] = reason
	}
}

func (s *Service) handle(ctx context.Context, ev events.Event) error {
	if ev.RepoID == uuid.Nil {
		return nil
	}
	var p payload
	if err := ev.Decode(&p); err != nil {
		return err
	}
	switch ev.Kind {
	case events.KindRelease:
		if ev.Action != "published" {
			return nil
		}
		return s.release(ctx, ev, p)
	case events.KindIssue, events.KindIssueComment, events.KindPull, events.KindPullReview,
		events.KindPullReviewComment:
		c, ok := classify(ev, p)
		if !ok {
			return nil
		}
		return s.issue(ctx, ev, p, c)
	}
	return nil
}

// classify maps an issue or pull request event to a change; false means
// the event does not notify.
func classify(ev events.Event, p payload) (change, bool) {
	pull := (ev.Kind != events.KindIssue && ev.Kind != events.KindIssueComment) || p.IssueKind == "pull"
	switch ev.Kind {
	case events.KindIssue:
		switch ev.Action {
		case "opened", "closed", "reopened":
			if pull {
				return change{}, false // the pull request events carry these
			}
			c := change{action: ev.Action, stateChange: ev.Action != "opened"}
			if ev.Action == "opened" {
				c.source = "issue:" + ev.SubjectID.String()
			}
			return c, true
		case "assigned":
			if p.Assignee == nil {
				return change{}, false
			}
			return change{action: "assigned", direct: *p.Assignee, directReason: ReasonAssigned}, true
		}
	case events.KindIssueComment:
		if ev.Action == "created" {
			return change{action: "commented", source: "comment:" + ev.SubjectID.String()}, true
		}
	case events.KindPull:
		switch ev.Action {
		case "opened":
			return change{action: "opened", source: "issue:" + ev.SubjectID.String()}, true
		case "closed", "reopened":
			return change{action: ev.Action, stateChange: true}, true
		case "merged":
			return change{action: "merged", stateChange: true}, true
		case "ready_for_review":
			return change{action: "ready_for_review"}, true
		case "review_requested":
			if p.Reviewer == nil {
				return change{}, false
			}
			return change{action: "review_requested", direct: *p.Reviewer, directReason: ReasonReviewRequested}, true
		}
	case events.KindPullReview:
		if ev.Action == "submitted" {
			return change{action: "reviewed"}, true
		}
	case events.KindPullReviewComment:
		if ev.Action == "created" {
			return change{action: "commented"}, true
		}
	}
	return change{}, false
}

// issue notifies about an issue or a pull request.
func (s *Service) issue(ctx context.Context, ev events.Event, p payload, c change) error {
	var i dbgen.Issue
	var err error
	if (ev.Kind == events.KindIssue || ev.Kind == events.KindPull) && ev.SubjectID != uuid.Nil {
		i, err = s.q.WebhookIssue(ctx, ev.SubjectID)
	} else {
		i, err = s.q.WebhookIssueByNumber(ctx, dbgen.WebhookIssueByNumberParams{RepoID: ev.RepoID, Number: p.Number})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted since
	}
	if err != nil {
		return fmt.Errorf("notification subject: %w", err)
	}
	key := "issue:" + i.ID.String()
	subject := SubjectIssue
	if i.Kind == "pull" {
		subject = SubjectPull
	}
	to := recipients{}
	mentioned := map[uuid.UUID]bool{}
	if c.direct != uuid.Nil {
		to.add(c.direct, c.directReason)
		mentioned[c.direct] = true
	} else {
		parts, err := s.q.NotifyIssueParticipants(ctx, i.ID)
		if err != nil {
			return fmt.Errorf("notification participants: %w", err)
		}
		for _, pr := range parts {
			reason := pr.Why
			if reason == ReasonComment && c.stateChange {
				reason = ReasonStateChange
			}
			to.add(pr.UserID, reason)
		}
		if c.source != "" {
			ids, err := s.q.NotifyMentions(ctx, c.source)
			if err != nil {
				return fmt.Errorf("notification mentions: %w", err)
			}
			for _, id := range ids {
				to.add(id, ReasonMentioned)
				mentioned[id] = true
			}
		}
		if err := s.addWatchers(ctx, ev.RepoID, to); err != nil {
			return err
		}
	}
	if err := s.applySubscriptions(ctx, key, to, mentioned, c.direct == uuid.Nil); err != nil {
		return err
	}
	return s.deliver(ctx, ev, to, thread{subject: subject, key: key, number: i.Number, title: i.Title,
		ref: "#" + strconv.FormatInt(i.Number, 10), action: c.action})
}

// release notifies the watchers of a published release.
func (s *Service) release(ctx context.Context, ev events.Event, p payload) error {
	title := p.Name
	if title == "" {
		title = p.Tag
	}
	if r, err := s.q.WebhookRelease(ctx, ev.SubjectID); err == nil {
		title, p.Tag = r.Name, r.Tag
		if title == "" {
			title = r.Tag
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("notification release: %w", err)
	}
	to := recipients{}
	if err := s.addWatchers(ctx, ev.RepoID, to); err != nil {
		return err
	}
	key := "release:" + ev.SubjectID.String()
	if err := s.applySubscriptions(ctx, key, to, nil, true); err != nil {
		return err
	}
	return s.deliver(ctx, ev, to, thread{subject: SubjectRelease, key: key, title: title, ref: p.Tag,
		action: "published"})
}

func (s *Service) addWatchers(ctx context.Context, repoID uuid.UUID, to recipients) error {
	ids, err := s.q.NotifyRepoWatchers(ctx, repoID)
	if err != nil {
		return fmt.Errorf("notification watchers: %w", err)
	}
	for _, id := range ids {
		to.add(id, ReasonSubscribed)
	}
	return nil
}

// applySubscriptions adds the users subscribed to the subject (when add)
// and removes the ones who unsubscribed, unless the event targets them.
func (s *Service) applySubscriptions(ctx context.Context, key string, to recipients, keep map[uuid.UUID]bool, add bool) error {
	subs, err := s.q.NotifySubjectSubscriptions(ctx, key)
	if err != nil {
		return fmt.Errorf("notification subscriptions: %w", err)
	}
	for _, sub := range subs {
		switch {
		case sub.Subscribed && add:
			to.add(sub.UserID, ReasonSubscribed)
		case !sub.Subscribed && !keep[sub.UserID]:
			delete(to, sub.UserID)
		}
	}
	return nil
}

// thread describes the subject of a notification.
type thread struct {
	subject, key string
	number       int64
	title, ref   string
	action       string
}

// deliver writes the threads of the recipients that may read the
// repository and queues their mails.
func (s *Service) deliver(ctx context.Context, ev events.Event, to recipients, t thread) error {
	delete(to, ev.ActorID)
	if len(to) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(to))
	for id := range to {
		ids = append(ids, id)
	}
	levels, err := s.q.NotifyRepoLevels(ctx, dbgen.NotifyRepoLevelsParams{RepoID: ev.RepoID, UserIds: ids})
	if err != nil {
		return fmt.Errorf("notification levels: %w", err)
	}
	for _, l := range levels {
		if l.Level == LevelIgnoring {
			delete(to, l.UserID)
		}
	}
	if len(to) == 0 {
		return nil
	}
	users, err := s.q.NotifyUsers(ctx, ids)
	if err != nil {
		return fmt.Errorf("notification users: %w", err)
	}
	rs, err := s.reposSvc()
	if err != nil {
		return err
	}
	actor := ""
	if ev.ActorID != uuid.Nil {
		if n, err := s.q.NotifyUserName(ctx, ev.ActorID); err == nil {
			actor = n
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("notification actor: %w", err)
		}
	}
	summary := summarize(actor, t)
	for _, u := range users {
		reason, ok := to[u.ID]
		if !ok {
			continue
		}
		// Recipients come from history (watchers, past participants):
		// check that they can still read the repository.
		if _, err := rs.GetByID(ctx, auth.Principal{UserID: u.ID, Username: u.Name, Role: u.Role}, ev.RepoID); err != nil {
			continue
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("notification id: %w", err)
		}
		tid, err := s.q.UpsertNotificationThread(ctx, dbgen.UpsertNotificationThreadParams{ID: id, UserID: u.ID,
			RepoID: ev.RepoID, SubjectType: t.subject, SubjectKey: t.key, Number: t.number, Title: t.title,
			Reason: reason, Summary: summary, LastEventID: uuid.NullUUID{UUID: ev.ID, Valid: true},
			LastActorID: uuid.NullUUID{UUID: ev.ActorID, Valid: ev.ActorID != uuid.Nil}})
		if errors.Is(err, pgx.ErrNoRows) {
			continue // this event was handled already
		}
		if err != nil {
			return fmt.Errorf("upsert notification: %w", err)
		}
		if u.EmailNotifications && u.Email != "" && s.o.Sender != nil {
			if err := s.enqueueMail(ctx, MailArgs{ThreadID: tid, EventID: ev.ID, Actor: actor, Action: t.action,
				Ref: t.ref}); err != nil {
				return err
			}
		}
	}
	return nil
}

// summarize is the one-line English summary stored on the thread.
func summarize(actor string, t thread) string {
	if actor == "" {
		actor = "someone"
	}
	verbs := map[string]string{
		"opened": "opened", "closed": "closed", "reopened": "reopened", "merged": "merged",
		"commented": "commented on", "assigned": "was assigned by", "review_requested": "review requested by",
		"reviewed": "reviewed", "ready_for_review": "marked ready for review", "published": "published",
	}
	switch t.action {
	case "assigned", "review_requested":
		return t.ref + " " + verbs[t.action] + " " + actor
	}
	verb, ok := verbs[t.action]
	if !ok {
		verb = "updated"
	}
	return actor + " " + verb + " " + t.ref
}
