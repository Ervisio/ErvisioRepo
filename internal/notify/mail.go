package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

// MailArgs mails one update of a thread.
type MailArgs struct {
	ThreadID uuid.UUID `json:"thread_id"`
	EventID  uuid.UUID `json:"event_id"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Ref      string    `json:"ref"`
}

// Kind implements river.JobArgs.
func (MailArgs) Kind() string { return "notification_mail" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (MailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: river.QueueDefault, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// JobsOption registers the mail worker.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &mailWorker{s: s})
	}
}

func (s *Service) enqueueMail(ctx context.Context, a MailArgs) error {
	e, _ := s.jobs.Load().(Enqueuer)
	if e == nil {
		return nil // no runner: in-app notifications only
	}
	if err := e.Enqueue(ctx, a); err != nil {
		return fmt.Errorf("enqueue notification mail: %w", err)
	}
	return nil
}

type mailWorker struct {
	river.WorkerDefaults[MailArgs]
	s *Service
}

func (w *mailWorker) Work(ctx context.Context, j *river.Job[MailArgs]) error {
	return w.s.sendMail(ctx, j.Args)
}

// mailData is the data of the notification template.
type mailData struct {
	Instance, Repo, Title, Ref, Actor, Action, Reason, Link, Settings string
}

// sendMail mails a thread update unless it was read, done or superseded
// meanwhile, or the user turned mail off.
func (s *Service) sendMail(ctx context.Context, a MailArgs) error {
	if s.o.Sender == nil {
		return nil
	}
	row, err := s.q.GetNotificationForMail(ctx, a.ThreadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get notification: %w", err)
	}
	t := row.NotificationThread
	if t.Done || !t.Unread || !t.LastEventID.Valid || t.LastEventID.UUID != a.EventID {
		return nil
	}
	users, err := s.q.NotifyUsers(ctx, []uuid.UUID{t.UserID})
	if err != nil {
		return fmt.Errorf("notification recipient: %w", err)
	}
	if len(users) == 0 || !users[0].EmailNotifications || users[0].Email == "" {
		return nil
	}
	u := users[0]
	actor := a.Actor
	if actor == "" {
		actor = s.instance()
	}
	base := strings.TrimRight(s.o.PublicURL, "/")
	repo := row.OwnerName + "/" + row.RepoName
	link := base + "/" + url.PathEscape(row.OwnerName) + "/" + url.PathEscape(row.RepoName)
	switch t.SubjectType {
	case SubjectIssue:
		link += "/issues/" + strconv.FormatInt(t.Number, 10)
	case SubjectPull:
		link += "/pull/" + strconv.FormatInt(t.Number, 10)
	case SubjectRelease:
		if id, ok := strings.CutPrefix(t.SubjectKey, "release:"); ok {
			link += "/releases/" + id
		}
	}
	subject, body, err := mail.Render(u.Locale, mail.TemplateNotification, mailData{Instance: s.instance(),
		Repo: repo, Title: t.Title, Ref: a.Ref, Actor: actor, Action: a.Action, Reason: t.Reason, Link: link,
		Settings: base + "/notifications"})
	if err != nil {
		return fmt.Errorf("render notification: %w", err)
	}
	if err := s.o.Sender.Send(ctx, mail.Message{To: u.Email, Subject: subject, Body: body}); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	if err := s.q.MarkNotificationEmailed(ctx, t.ID); err != nil {
		return fmt.Errorf("mark notification emailed: %w", err)
	}
	return nil
}

func (s *Service) instance() string {
	if u, err := url.Parse(s.o.PublicURL); err == nil && u.Host != "" {
		return u.Host
	}
	return s.o.PublicURL
}
