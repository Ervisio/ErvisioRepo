// Package webhooks delivers outgoing webhooks (PLAN §5.7). A hook belongs to
// a repository or to an organization (then it fires for every repository
// the organization owns) and receives the events of the bus either as
// GitHub-shaped payloads (format "github", with the X-GitHub-* headers and
// X-Hub-Signature-256) or as the native envelope (format "ervisio").
// Deliveries are River jobs retried with an exponential backoff; each
// delivery keeps the request and the (truncated) response of its last
// attempt, and can be redelivered. Connections are checked at dial time
// against non-public addresses (SSRF). Secrets are sealed at rest and never
// logged.
package webhooks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Limits.
const (
	MaxHooksPerScope   = 20
	MaxEventsPerHook   = 32
	MaxSecretLength    = 256
	DefaultTimeout     = 10 * time.Second
	DefaultMaxAttempts = 8
	defaultPageSize    = 30
	maxPageSize        = 100
)

// Content types and formats.
const (
	ContentJSON   = "json"
	ContentForm   = "form"
	FormatGitHub  = "github"
	FormatErvisio = "ervisio"
)

// Delivery states.
const (
	StatusPending = "pending"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

// Event names a hook can subscribe to; "*" means every event.
var knownEvents = []string{
	"*", "push", "create", "delete", "issues", "issue_comment", "label", "milestone", "release",
	"pull_request", "pull_request_review", "pull_request_review_comment", "status",
	"check_run", "check_suite",
}

var (
	// ErrNotFound means the hook or the delivery does not exist in the scope.
	ErrNotFound = errors.New("webhook not found")
	// ErrForbidden means the caller cannot manage the hooks of the scope.
	ErrForbidden = errors.New("managing webhooks needs admin permission")
	// ErrNoMasterKey means a secret cannot be stored without a master key.
	ErrNoMasterKey = errors.New("webhook secrets need security.master_key_file")
	// ErrTooMany means the scope already has MaxHooksPerScope hooks.
	ErrTooMany = fmt.Errorf("at most %d webhooks per repository or organization", MaxHooksPerScope)

	errSealed = errors.New("cannot open the webhook secret (master key missing or changed)")
)

// ValidationError is an invalid hook definition.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

func invalid(msg string) error { return &ValidationError{msg: msg} }

// Repos is the part of the repository service the hooks use.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// Enqueuer inserts jobs (jobs.Runner).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Options configure the service.
type Options struct {
	Pool *pgxpool.Pool
	// MasterKey seals the secrets; without it hooks cannot be signed.
	MasterKey []byte
	Config    config.Webhooks
	// PublicURL is the base of the URLs in the payloads.
	PublicURL string
	Log       *slog.Logger
}

// Service manages and delivers the hooks.
type Service struct {
	o           Options
	q           *dbgen.Queries
	seal        *sealer
	guard       *guard
	client      *http.Client
	base        string
	maxAttempts int
	repos       atomic.Value // Repos
	jobs        atomic.Value // Enqueuer
}

// New builds the service.
func New(o Options) (*Service, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	seal, err := newSealer(o.MasterKey)
	if err != nil {
		return nil, err
	}
	g, err := newGuard(o.Config.AllowPrivate, o.Config.AllowedCIDRs)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(o.Config.Timeout)
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	attempts := o.Config.MaxAttempts
	if attempts <= 0 {
		attempts = DefaultMaxAttempts
	}
	return &Service{o: o, q: dbgen.New(o.Pool), seal: seal, guard: g, client: newClient(g, timeout),
		base: strings.TrimRight(o.PublicURL, "/"), maxAttempts: attempts}, nil
}

// SetRepos sets the repository service (built after the job runner).
func (s *Service) SetRepos(r Repos) { s.repos.Store(r) }

// SetEnqueuer sets the job runner.
func (s *Service) SetEnqueuer(e Enqueuer) { s.jobs.Store(e) }

func (s *Service) enqueue(ctx context.Context, id uuid.UUID) error {
	e, _ := s.jobs.Load().(Enqueuer)
	if e == nil {
		return errors.New("webhooks: job runner not set")
	}
	if err := e.Enqueue(ctx, DeliverArgs{DeliveryID: id, MaxAttempts: s.maxAttempts}); err != nil {
		return fmt.Errorf("enqueue webhook delivery: %w", err)
	}
	return nil
}

// Scope is a repository or an organization whose hooks the caller manages.
type Scope struct {
	RepoID uuid.UUID
	OrgID  uuid.UUID
	// Repo is set for a repository scope.
	Repo *repos.Repo
	// OrgName is set for an organization scope.
	OrgName string
}

func (sc Scope) owns(h dbgen.Webhook) bool {
	if sc.RepoID != uuid.Nil {
		return h.RepoID.Valid && h.RepoID.UUID == sc.RepoID
	}
	return h.OrgID.Valid && h.OrgID.UUID == sc.OrgID
}

// RepoScope resolves a repository whose hooks who manages (repository
// admin). Repository errors (not found, moved) are returned as they are.
func (s *Service) RepoScope(ctx context.Context, who auth.Principal, owner, name string) (Scope, error) {
	r, _ := s.repos.Load().(Repos)
	if r == nil {
		return Scope{}, errors.New("webhooks: repository service not set")
	}
	repo, err := r.Get(ctx, who, owner, name)
	if err != nil {
		return Scope{}, err
	}
	if repo.Permission < authz.PermAdmin {
		return Scope{}, ErrForbidden
	}
	return Scope{RepoID: repo.ID, Repo: &repo}, nil
}

// OrgScope resolves an organization whose hooks who manages (organization
// owner or instance admin).
func (s *Service) OrgScope(ctx context.Context, who auth.Principal, org string) (Scope, error) {
	row, err := s.q.WebhookOrgRole(ctx, dbgen.WebhookOrgRoleParams{UserID: who.UserID, Name: org})
	if errors.Is(err, pgx.ErrNoRows) {
		return Scope{}, fmt.Errorf("organization %q: %w", org, ErrNotFound)
	}
	if err != nil {
		return Scope{}, fmt.Errorf("organization role: %w", err)
	}
	if !who.IsAdmin() && row.Role == "" {
		// Non-members do not learn whether the organization exists.
		return Scope{}, fmt.Errorf("organization %q: %w", org, ErrNotFound)
	}
	if !who.IsAdmin() && authz.OrgRole(row.Role) != authz.OrgOwner {
		return Scope{}, fmt.Errorf("managing organization webhooks needs the owner role: %w", ErrForbidden)
	}
	return Scope{OrgID: row.ID, OrgName: row.Name}, nil
}

// Hook is a webhook as shown to its managers (never with the secret).
type Hook struct {
	ID          uuid.UUID
	URL         string
	ContentType string
	Format      string
	Events      []string
	Active      bool
	HasSecret   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Last        *LastDelivery
}

// LastDelivery is the outcome of the last attempted delivery of a hook.
type LastDelivery struct {
	Status      string
	StatusCode  int
	DeliveredAt time.Time
}

// Input creates or updates a hook; nil fields keep their value (or take
// the default on create). An empty Secret removes it.
type Input struct {
	URL         *string
	ContentType *string
	Format      *string
	Events      *[]string
	Secret      *string
	Active      *bool
}

func (s *Service) hookOut(ctx context.Context, h dbgen.Webhook) (Hook, error) {
	out := Hook{ID: h.ID, URL: h.Url, ContentType: h.ContentType, Format: h.Format, Events: h.Events,
		Active: h.Active, HasSecret: len(h.Secret) > 0, CreatedAt: h.CreatedAt.Time, UpdatedAt: h.UpdatedAt.Time}
	if out.Events == nil {
		out.Events = []string{}
	}
	last, err := s.q.LastWebhookDelivery(ctx, h.ID)
	switch {
	case err == nil:
		out.Last = &LastDelivery{Status: last.Status, StatusCode: int(last.StatusCode), DeliveredAt: last.DeliveredAt.Time}
	case !errors.Is(err, pgx.ErrNoRows):
		return Hook{}, fmt.Errorf("last delivery: %w", err)
	}
	return out, nil
}

// List returns the hooks of a scope.
func (s *Service) List(ctx context.Context, sc Scope) ([]Hook, error) {
	var rows []dbgen.Webhook
	var err error
	if sc.RepoID != uuid.Nil {
		rows, err = s.q.ListRepoWebhooks(ctx, uuid.NullUUID{UUID: sc.RepoID, Valid: true})
	} else {
		rows, err = s.q.ListOrgWebhooks(ctx, uuid.NullUUID{UUID: sc.OrgID, Valid: true})
	}
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	out := make([]Hook, 0, len(rows))
	for _, h := range rows {
		o, err := s.hookOut(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Service) hook(ctx context.Context, sc Scope, id uuid.UUID) (dbgen.Webhook, error) {
	h, err := s.q.GetWebhook(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !sc.owns(h)) {
		return dbgen.Webhook{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Webhook{}, fmt.Errorf("get webhook: %w", err)
	}
	return h, nil
}

// Get returns one hook of a scope.
func (s *Service) Get(ctx context.Context, sc Scope, id uuid.UUID) (Hook, error) {
	h, err := s.hook(ctx, sc, id)
	if err != nil {
		return Hook{}, err
	}
	return s.hookOut(ctx, h)
}

// normalized is a validated definition.
type normalized struct {
	url, contentType, format string
	events                   []string
	active                   bool
}

func (s *Service) normalize(in Input, cur normalized) (normalized, error) {
	out := cur
	if in.URL != nil {
		out.url = strings.TrimSpace(*in.URL)
	}
	if err := s.guard.checkURL(out.url); err != nil {
		return normalized{}, err
	}
	if in.ContentType != nil {
		out.contentType = *in.ContentType
	}
	if out.contentType != ContentJSON && out.contentType != ContentForm {
		return normalized{}, invalid("content_type must be json or form")
	}
	if in.Format != nil {
		out.format = *in.Format
	}
	if out.format != FormatGitHub && out.format != FormatErvisio {
		return normalized{}, invalid("format must be github or ervisio")
	}
	if in.Events != nil {
		var evs []string
		for _, e := range *in.Events {
			e = strings.TrimSpace(e)
			if !slices.Contains(knownEvents, e) {
				return normalized{}, invalid(fmt.Sprintf("unknown event %q", e))
			}
			if !slices.Contains(evs, e) {
				evs = append(evs, e)
			}
		}
		if len(evs) == 0 {
			return normalized{}, invalid("events must not be empty")
		}
		out.events = evs
	}
	if len(out.events) > MaxEventsPerHook {
		return normalized{}, invalid(fmt.Sprintf("at most %d events", MaxEventsPerHook))
	}
	if in.Active != nil {
		out.active = *in.Active
	}
	return out, nil
}

// secret seals a new secret; keep reports that the stored one stays.
func (s *Service) secret(in *string, id uuid.UUID) (sealed []byte, keep bool, err error) {
	if in == nil {
		return nil, true, nil
	}
	if *in == "" {
		return nil, false, nil
	}
	if utf8.RuneCountInString(*in) > MaxSecretLength {
		return nil, false, invalid(fmt.Sprintf("secret is longer than %d characters", MaxSecretLength))
	}
	sealed, err = s.seal.seal([]byte(*in), id)
	return sealed, false, err
}

// Create adds a hook to a scope.
func (s *Service) Create(ctx context.Context, who auth.Principal, sc Scope, in Input) (Hook, error) {
	n, err := s.normalize(in, normalized{contentType: ContentJSON, format: FormatGitHub, events: []string{"push"}, active: true})
	if err != nil {
		return Hook{}, err
	}
	var count int64
	if sc.RepoID != uuid.Nil {
		count, err = s.q.CountRepoWebhooks(ctx, uuid.NullUUID{UUID: sc.RepoID, Valid: true})
	} else {
		count, err = s.q.CountOrgWebhooks(ctx, uuid.NullUUID{UUID: sc.OrgID, Valid: true})
	}
	if err != nil {
		return Hook{}, fmt.Errorf("count webhooks: %w", err)
	}
	if count >= MaxHooksPerScope {
		return Hook{}, ErrTooMany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Hook{}, fmt.Errorf("webhook id: %w", err)
	}
	sealed, _, err := s.secret(in.Secret, id)
	if err != nil {
		return Hook{}, err
	}
	p := dbgen.InsertWebhookParams{ID: id, Url: n.url, ContentType: n.contentType, Format: n.format, Secret: sealed,
		Events: n.events, Active: n.active, CreatedBy: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil}}
	if sc.RepoID != uuid.Nil {
		p.RepoID = uuid.NullUUID{UUID: sc.RepoID, Valid: true}
	} else {
		p.OrgID = uuid.NullUUID{UUID: sc.OrgID, Valid: true}
	}
	h, err := s.q.InsertWebhook(ctx, p)
	if err != nil {
		return Hook{}, fmt.Errorf("insert webhook: %w", err)
	}
	s.o.Log.InfoContext(ctx, "webhook created", "webhook_id", h.ID, "repo_id", sc.RepoID, "org_id", sc.OrgID,
		"user_id", who.UserID)
	return s.hookOut(ctx, h)
}

// Update changes a hook of a scope.
func (s *Service) Update(ctx context.Context, sc Scope, id uuid.UUID, in Input) (Hook, error) {
	cur, err := s.hook(ctx, sc, id)
	if err != nil {
		return Hook{}, err
	}
	n, err := s.normalize(in, normalized{url: cur.Url, contentType: cur.ContentType, format: cur.Format,
		events: cur.Events, active: cur.Active})
	if err != nil {
		return Hook{}, err
	}
	sealed, keep, err := s.secret(in.Secret, id)
	if err != nil {
		return Hook{}, err
	}
	if keep {
		sealed = cur.Secret
	}
	h, err := s.q.UpdateWebhook(ctx, dbgen.UpdateWebhookParams{ID: id, Url: n.url, ContentType: n.contentType,
		Format: n.format, Secret: sealed, Events: n.events, Active: n.active})
	if err != nil {
		return Hook{}, fmt.Errorf("update webhook: %w", err)
	}
	return s.hookOut(ctx, h)
}

// Delete removes a hook of a scope with its deliveries.
func (s *Service) Delete(ctx context.Context, sc Scope, id uuid.UUID) error {
	if _, err := s.hook(ctx, sc, id); err != nil {
		return err
	}
	if _, err := s.q.DeleteWebhook(ctx, id); err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	return nil
}

// Delivery is one delivery of a hook.
type Delivery struct {
	ID          uuid.UUID
	GUID        uuid.UUID
	Event       string
	Action      string
	Redelivery  bool
	Status      string
	Attempts    int
	StatusCode  int
	DurationMs  int
	Error       string
	CreatedAt   time.Time
	DeliveredAt *time.Time
}

// DeliveryDetail is a delivery with the request and the response of its
// last attempt.
type DeliveryDetail struct {
	Delivery
	URL             string
	RequestHeaders  map[string]string
	RequestBody     string
	ResponseHeaders map[string]string
	ResponseBody    string
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func deliveryOut(d dbgen.WebhookDelivery) Delivery {
	return Delivery{ID: d.ID, GUID: d.Guid, Event: d.Event, Action: d.Action, Redelivery: d.Redelivery,
		Status: d.Status, Attempts: int(d.Attempts), StatusCode: int(d.StatusCode), DurationMs: int(d.DurationMs),
		Error: d.Error, CreatedAt: d.CreatedAt.Time, DeliveredAt: tsPtr(d.DeliveredAt)}
}

// Deliveries lists the deliveries of a hook, newest first.
func (s *Service) Deliveries(ctx context.Context, sc Scope, id uuid.UUID, offset, limit int) ([]Delivery, error) {
	if _, err := s.hook(ctx, sc, id); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	offset = max(offset, 0)
	rows, err := s.q.ListWebhookDeliveries(ctx, dbgen.ListWebhookDeliveriesParams{WebhookID: id,
		Limit: int32(limit), Offset: int32(min(offset, 1<<30))}) //nolint:gosec // bounded above
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	out := make([]Delivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, Delivery{ID: r.ID, GUID: r.Guid, Event: r.Event, Action: r.Action, Redelivery: r.Redelivery,
			Status: r.Status, Attempts: int(r.Attempts), StatusCode: int(r.StatusCode), DurationMs: int(r.DurationMs),
			Error: r.Error, CreatedAt: r.CreatedAt.Time, DeliveredAt: tsPtr(r.DeliveredAt)})
	}
	return out, nil
}

func (s *Service) delivery(ctx context.Context, sc Scope, hookID, id uuid.UUID) (dbgen.WebhookDelivery, error) {
	if _, err := s.hook(ctx, sc, hookID); err != nil {
		return dbgen.WebhookDelivery{}, err
	}
	d, err := s.q.GetWebhookDelivery(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && d.WebhookID != hookID) {
		return dbgen.WebhookDelivery{}, fmt.Errorf("delivery: %w", ErrNotFound)
	}
	if err != nil {
		return dbgen.WebhookDelivery{}, fmt.Errorf("get delivery: %w", err)
	}
	return d, nil
}

// Delivery returns one delivery of a hook with its request and response.
func (s *Service) Delivery(ctx context.Context, sc Scope, hookID, id uuid.UUID) (DeliveryDetail, error) {
	d, err := s.delivery(ctx, sc, hookID, id)
	if err != nil {
		return DeliveryDetail{}, err
	}
	return DeliveryDetail{Delivery: deliveryOut(d), URL: d.Url, RequestHeaders: decodeHeaders(d.RequestHeaders),
		RequestBody: string(d.Payload), ResponseHeaders: decodeHeaders(d.ResponseHeaders), ResponseBody: d.ResponseBody}, nil
}

// Redeliver sends a delivery again, as a new delivery with the same GUID
// and payload.
func (s *Service) Redeliver(ctx context.Context, sc Scope, hookID, id uuid.UUID) (Delivery, error) {
	d, err := s.delivery(ctx, sc, hookID, id)
	if err != nil {
		return Delivery{}, err
	}
	return s.insertAndEnqueue(ctx, dbgen.InsertWebhookDeliveryParams{WebhookID: hookID, Guid: d.Guid, Event: d.Event,
		Action: d.Action, Redelivery: true, Payload: d.Payload})
}

// Ping sends the ping event to a hook.
func (s *Service) Ping(ctx context.Context, who auth.Principal, sc Scope, hookID uuid.UUID) (Delivery, error) {
	h, err := s.hook(ctx, sc, hookID)
	if err != nil {
		return Delivery{}, err
	}
	b := s.newBuilder(ctx)
	payload, err := b.ping(h, sc, who.UserID)
	if err != nil {
		return Delivery{}, err
	}
	guid, err := uuid.NewRandom()
	if err != nil {
		return Delivery{}, fmt.Errorf("delivery guid: %w", err)
	}
	return s.insertAndEnqueue(ctx, dbgen.InsertWebhookDeliveryParams{WebhookID: hookID, Guid: guid, Event: "ping",
		Payload: payload})
}

func (s *Service) insertAndEnqueue(ctx context.Context, p dbgen.InsertWebhookDeliveryParams) (Delivery, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Delivery{}, fmt.Errorf("delivery id: %w", err)
	}
	p.ID = id
	if _, err := s.q.InsertWebhookDelivery(ctx, p); err != nil {
		return Delivery{}, fmt.Errorf("insert delivery: %w", err)
	}
	if err := s.enqueue(ctx, id); err != nil {
		return Delivery{}, err
	}
	d, err := s.q.GetWebhookDelivery(ctx, id)
	if err != nil {
		return Delivery{}, fmt.Errorf("get delivery: %w", err)
	}
	return deliveryOut(d), nil
}
