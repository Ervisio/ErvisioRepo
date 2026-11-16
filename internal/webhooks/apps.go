package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// Webhooks of Apps (APPS-PLAN A2). An App has at most one hook, a
// webhooks row with app_id, always in the GitHub format. It receives the
// events of the repositories its active installations cover, filtered by
// the events and permissions each installation accepted, with the
// "installation" object in the payload, plus the installation events.

// AppScope is the hook of an App; the caller checked that who manages it.
func AppScope(appID uuid.UUID) Scope { return Scope{AppID: appID} }

// AppHook returns the hook of an App, nil when it has none.
func (s *Service) AppHook(ctx context.Context, appID uuid.UUID) (*Hook, error) {
	h, err := s.q.GetAppWebhook(ctx, uuid.NullUUID{UUID: appID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app webhook: %w", err)
	}
	out, err := s.hookOut(ctx, h)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SetAppHook creates, changes or (with an empty URL) removes the hook of
// an App. A nil secret keeps the stored one, an empty one removes it.
func (s *Service) SetAppHook(ctx context.Context, q *dbgen.Queries, appID, by uuid.UUID, url string, secret *string, active bool) error {
	if q == nil {
		q = s.q
	}
	cur, err := q.GetAppWebhook(ctx, uuid.NullUUID{UUID: appID, Valid: true})
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get app webhook: %w", err)
	}
	if url == "" {
		if !exists {
			return nil
		}
		if err := q.DeleteAppWebhook(ctx, uuid.NullUUID{UUID: appID, Valid: true}); err != nil {
			return fmt.Errorf("delete app webhook: %w", err)
		}
		return nil
	}
	n, err := s.normalize(Input{URL: &url}, normalized{contentType: ContentJSON, format: FormatGitHub, events: []string{"*"}})
	if err != nil {
		return err
	}
	id := cur.ID
	if !exists {
		if id, err = uuid.NewV7(); err != nil {
			return fmt.Errorf("webhook id: %w", err)
		}
	}
	sealed, keep, err := s.secret(secret, id)
	if err != nil {
		return err
	}
	if keep {
		sealed = cur.Secret
	}
	if !exists {
		err = q.InsertAppWebhook(ctx, dbgen.InsertAppWebhookParams{ID: id, AppID: uuid.NullUUID{UUID: appID, Valid: true},
			Url: n.url, Secret: sealed, Active: active, CreatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}})
	} else {
		err = q.UpdateAppWebhook(ctx, dbgen.UpdateAppWebhookParams{ID: id, Url: n.url, Secret: sealed, Active: active})
	}
	if err != nil {
		return fmt.Errorf("save app webhook: %w", err)
	}
	return nil
}

// DeliverAppEvent queues a delivery of an App event (installation,
// installation_repositories) to the App's hook, if it has an active one.
func (s *Service) DeliverAppEvent(ctx context.Context, appID uuid.UUID, event, action string, payload []byte) error {
	h, err := s.q.GetAppWebhook(ctx, uuid.NullUUID{UUID: appID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !h.Active) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get app webhook: %w", err)
	}
	guid, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("delivery guid: %w", err)
	}
	_, err = s.insertAndEnqueue(ctx, dbgen.InsertWebhookDeliveryParams{WebhookID: h.ID, Guid: guid, Event: event,
		Action: action, Payload: payload})
	return err
}

// withInstallation adds {"installation": {...}} to a JSON object payload.
func withInstallation(payload []byte, numID int64, nodeID uuid.UUID) []byte {
	payload = bytes.TrimSpace(payload)
	if len(payload) < 2 || payload[0] != '{' {
		return payload
	}
	inst, _ := json.Marshal(map[string]any{"id": numID, "node_id": nodeID.String()})
	out := make([]byte, 0, len(payload)+len(inst)+20)
	out = append(out, `{"installation":`...)
	out = append(out, inst...)
	rest := bytes.TrimSpace(payload[1:])
	if len(rest) > 0 && rest[0] != '}' {
		out = append(out, ',')
	}
	return append(out, rest...)
}

// handleApps queues the deliveries of a bus event to the hooks of the Apps
// installed on its repository. b has the repository loaded; items builds
// the GitHub payloads once.
func (s *Service) handleApps(ctx context.Context, ev events.Event, items func() ([]item, error)) error {
	insts, err := s.q.InstallationsForRepo(ctx, ev.RepoID)
	if err != nil {
		return fmt.Errorf("installations for repo: %w", err)
	}
	if len(insts) == 0 {
		return nil
	}
	gh, err := items()
	if err != nil {
		return err
	}
	for _, in := range insts {
		var perms authz.AppPermissions
		_ = json.Unmarshal(in.Permissions, &perms)
		h := dbgen.Webhook{ID: in.WebhookID}
		for i, it := range gh {
			if i >= nativeSeq {
				break
			}
			if !subscribed(in.Events, it.event) || !authz.AppEventAllowed(perms, it.event) {
				continue
			}
			payload := withInstallation(it.payload, in.NumID.Int64, in.ID)
			if err := s.queue(ctx, h, ev, i, it.event, it.action, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// appHeaders are the headers GitHub adds to the deliveries of an App hook.
func (s *Service) appHeaders(ctx context.Context, h dbgen.Webhook, headers map[string]string) error {
	if !h.AppID.Valid {
		return nil
	}
	num, err := s.q.GetAppNumID(ctx, h.AppID.UUID)
	if err != nil {
		return fmt.Errorf("get app id: %w", err)
	}
	headers["X-GitHub-Hook-Installation-Target-Type"] = "integration"
	headers["X-GitHub-Hook-Installation-Target-ID"] = strconv.FormatInt(num.Int64, 10)
	return nil
}

// InstallationInfo describes an installation for its webhook events.
type InstallationInfo struct {
	ID                  uuid.UUID
	NumID               int64
	AppID               uuid.UUID
	AppNumID            int64
	AppSlug             string
	AccountID           uuid.UUID
	RepositorySelection string
	Permissions         map[string]string
	Events              []string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	SuspendedAt         *time.Time
	SuspendedBy         uuid.UUID
}

// installationObject is the "installation" of the installation events.
func (b *builder) installationObject(in InstallationInfo) (obj, error) {
	n, err := b.q.WebhookNamespace(b.ctx, in.AccountID)
	if err != nil {
		return nil, fmt.Errorf("webhook account: %w", err)
	}
	typ := "User"
	if n.Kind == "org" {
		typ = "Organization"
	}
	esc := url.PathEscape(n.Name)
	account := obj{"login": n.Name, "id": numID(n.ID), "node_id": n.ID.String(), "type": typ,
		"html_url": b.url(esc), "site_admin": false}
	suspendedBy, err := b.user(in.SuspendedBy)
	if err != nil {
		return nil, err
	}
	events := in.Events
	if events == nil {
		events = []string{}
	}
	num := strconv.FormatInt(in.NumID, 10)
	o := obj{"id": in.NumID, "node_id": in.ID.String(), "account": account, "repository_selection": in.RepositorySelection,
		"access_tokens_url": b.url("api", "v3", "app", "installations", num, "access_tokens"),
		"repositories_url":  b.url("api", "v3", "installation", "repositories"),
		"html_url":          b.url("settings", "installations", num),
		"app_id":            in.AppNumID, "app_slug": in.AppSlug, "target_id": numID(n.ID), "target_type": typ,
		"permissions": in.Permissions, "events": events, "created_at": in.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": in.UpdatedAt.UTC().Format(time.RFC3339), "single_file_name": nil,
		"has_multiple_single_files": false, "single_file_paths": []any{}, "suspended_by": suspendedBy,
		"suspended_at": nil}
	if in.SuspendedAt != nil {
		o["suspended_at"] = in.SuspendedAt.UTC().Format(time.RFC3339)
	}
	return o, nil
}

// repoList is the short repository objects of the installation events.
func (b *builder) repoList(ids []uuid.UUID) ([]any, error) {
	out := []any{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := b.q.ReposByIDs(b.ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("webhook repositories: %w", err)
	}
	owners := map[uuid.UUID]string{}
	for _, r := range rows {
		owner, ok := owners[r.OwnerID]
		if !ok {
			n, err := b.q.WebhookNamespace(b.ctx, r.OwnerID)
			if err != nil {
				return nil, fmt.Errorf("webhook repository owner: %w", err)
			}
			owner, owners[r.OwnerID] = n.Name, n.Name
		}
		full, err := b.q.GetRepo(b.ctx, r.ID)
		if err != nil {
			return nil, fmt.Errorf("webhook repository: %w", err)
		}
		out = append(out, obj{"id": numID(r.ID), "node_id": r.ID.String(), "name": r.Name,
			"full_name": owner + "/" + r.Name, "private": full.Repo.Visibility != string(authz.Public)})
	}
	return out, nil
}

// InstallationEvent delivers an installation or installation_repositories
// event of an installation to its App's hook. repos lists the repositories
// of a new installation; added and removed those of a selection change.
func (s *Service) InstallationEvent(ctx context.Context, in InstallationInfo, event, action string, sender uuid.UUID, repos, added, removed []uuid.UUID) error {
	b := s.newBuilder(ctx)
	inst, err := b.installationObject(in)
	if err != nil {
		return err
	}
	snd, err := b.user(sender)
	if err != nil {
		return err
	}
	body := obj{"action": action, "installation": inst, "requester": nil, "sender": snd}
	if event == "installation_repositories" {
		body["repository_selection"] = in.RepositorySelection
		if body["repositories_added"], err = b.repoList(added); err != nil {
			return err
		}
		if body["repositories_removed"], err = b.repoList(removed); err != nil {
			return err
		}
	} else if action == "created" {
		if body["repositories"], err = b.repoList(repos); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("webhook payload: %w", err)
	}
	return s.DeliverAppEvent(ctx, in.AppID, event, action, raw)
}
