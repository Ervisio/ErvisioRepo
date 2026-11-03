package webhooks

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// obj is a JSON object of a payload.
type obj = map[string]any

// eventPayload is the payload of the issue, comment, label and milestone
// events (issues.EventPayload) and of the release events.
type eventPayload struct {
	Number      int64      `json:"number"`
	IssueKind   string     `json:"issue_kind"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason"`
	Label       string     `json:"label"`
	Assignee    *uuid.UUID `json:"assignee"`
	Milestone   string     `json:"milestone"`
	CommentID   *uuid.UUID `json:"comment_id"`
	Tag         string     `json:"tag"`
	Name        string     `json:"name"`
	Prerelease  bool       `json:"prerelease"`
}

// numID maps a UUID to the positive numeric id GitHub payloads carry. The
// low bits of a UUIDv7 are random, so collisions are negligible; node_id
// carries the UUID itself.
func numID(u uuid.UUID) int64 {
	return int64(binary.BigEndian.Uint64(u[8:]) >> 11) //nolint:gosec // shifted below 2^53
}

func ts(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// builder builds the payloads of one event, caching the lookups.
type builder struct {
	s     *Service
	ctx   context.Context
	q     *dbgen.Queries
	users map[uuid.UUID]any
	// Set by loadRepo.
	repoID   uuid.UUID
	repo     obj
	org      obj
	fullName string
	html     string
}

func (s *Service) newBuilder(ctx context.Context) *builder {
	return &builder{s: s, ctx: ctx, q: s.q, users: map[uuid.UUID]any{}}
}

func (b *builder) url(parts ...string) string {
	var sb strings.Builder
	sb.WriteString(b.s.base)
	for _, p := range parts {
		sb.WriteByte('/')
		sb.WriteString(p)
	}
	return sb.String()
}

// user returns a GitHub user object; nil for no user.
func (b *builder) user(id uuid.UUID) (any, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	if u, ok := b.users[id]; ok {
		return u, nil
	}
	row, err := b.q.WebhookUser(b.ctx, id)
	var u any
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		u = obj{"login": "ghost", "id": numID(id), "node_id": id.String(), "type": "User", "site_admin": false}
	case err != nil:
		return nil, fmt.Errorf("webhook user: %w", err)
	default:
		u = obj{"login": row.Name, "id": numID(row.ID), "node_id": row.ID.String(), "name": nonEmpty(row.DisplayName),
			"avatar_url": row.AvatarUrl, "html_url": b.url(url.PathEscape(row.Name)),
			"url": b.url("api", "v1", "users", url.PathEscape(row.Name)), "type": "User", "site_admin": row.Role == "admin"}
	}
	b.users[id] = u
	return u, nil
}

func (b *builder) userOf(id uuid.NullUUID) (any, error) {
	if !id.Valid {
		return nil, nil
	}
	return b.user(id.UUID)
}

// loadRepo loads the repository (and organization) objects.
func (b *builder) loadRepo(id uuid.UUID) error {
	row, err := b.q.GetRepo(b.ctx, id)
	if err != nil {
		return fmt.Errorf("webhook repository: %w", err)
	}
	r := row.Repo
	ownerType := "User"
	if row.OwnerKind == "org" {
		ownerType = "Organization"
	}
	ownerEsc, nameEsc := url.PathEscape(row.OwnerName), url.PathEscape(r.Name)
	owner := obj{"login": row.OwnerName, "id": numID(r.OwnerID), "node_id": r.OwnerID.String(), "type": ownerType,
		"html_url": b.url(ownerEsc), "site_admin": false}
	b.repoID = r.ID
	b.fullName = row.OwnerName + "/" + r.Name
	b.html = b.url(ownerEsc, nameEsc)
	api := b.url("api", "v1", "repos", ownerEsc, nameEsc)
	b.repo = obj{"id": numID(r.ID), "node_id": r.ID.String(), "name": r.Name, "full_name": b.fullName,
		"private": r.Visibility != string(authz.Public), "visibility": r.Visibility, "owner": owner,
		"html_url": b.html, "description": nonEmpty(r.Description), "fork": false, "url": api,
		"clone_url": b.html + ".git", "default_branch": r.DefaultBranch, "master_branch": r.DefaultBranch,
		"created_at": ts(r.CreatedAt), "updated_at": ts(r.UpdatedAt)}
	b.org = nil
	if row.OwnerKind == "org" {
		b.org = obj{"login": row.OwnerName, "id": numID(r.OwnerID), "node_id": r.OwnerID.String(),
			"url": b.url("api", "v1", "orgs", ownerEsc), "html_url": b.url(ownerEsc), "description": nil}
	}
	return nil
}

// envelope adds the repository, organization and sender to a payload.
func (b *builder) envelope(body obj, actor uuid.UUID) (obj, error) {
	sender, err := b.user(actor)
	if err != nil {
		return nil, err
	}
	body["repository"] = b.repo
	if b.org != nil {
		body["organization"] = b.org
	}
	body["sender"] = sender
	return body, nil
}

func (b *builder) issueHTML(kind string, number int64) string {
	seg := "issues"
	if kind == "pull" {
		seg = "pull"
	}
	return b.html + "/" + seg + "/" + strconv.FormatInt(number, 10)
}

func (b *builder) issueAPI(number int64) string {
	return b.repo["url"].(string) + "/issues/" + strconv.FormatInt(number, 10)
}

// issue returns the GitHub issue object of an issue or pull request.
func (b *builder) issue(i dbgen.Issue) (obj, error) {
	author, err := b.userOf(i.AuthorID)
	if err != nil {
		return nil, err
	}
	ids, err := b.q.WebhookIssueAssignees(b.ctx, i.ID)
	if err != nil {
		return nil, fmt.Errorf("webhook assignees: %w", err)
	}
	assignees := make([]any, 0, len(ids))
	for _, id := range ids {
		u, err := b.user(id)
		if err != nil {
			return nil, err
		}
		assignees = append(assignees, u)
	}
	var assignee any
	if len(assignees) > 0 {
		assignee = assignees[0]
	}
	lrows, err := b.q.WebhookIssueLabels(b.ctx, i.ID)
	if err != nil {
		return nil, fmt.Errorf("webhook labels: %w", err)
	}
	labels := make([]any, 0, len(lrows))
	for _, l := range lrows {
		labels = append(labels, obj{"name": l.Name, "color": strings.TrimPrefix(l.Color, "#"),
			"description": nonEmpty(l.Description), "default": false})
	}
	var milestone any
	if i.MilestoneID.Valid {
		m, err := b.q.WebhookMilestone(b.ctx, i.MilestoneID.UUID)
		switch {
		case err == nil:
			milestone = b.milestone(m)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("webhook milestone: %w", err)
		}
	}
	closedBy, err := b.userOf(i.ClosedBy)
	if err != nil {
		return nil, err
	}
	api := b.issueAPI(i.Number)
	out := obj{"id": numID(i.ID), "node_id": i.ID.String(), "number": i.Number, "title": i.Title, "body": nonEmpty(i.Body),
		"state": i.State, "state_reason": nonEmpty(i.StateReason), "locked": i.Locked,
		"active_lock_reason": nonEmpty(i.LockReason), "user": author, "labels": labels, "assignee": assignee,
		"assignees": assignees, "milestone": milestone, "comments": i.CommentCount, "created_at": ts(i.CreatedAt),
		"updated_at": ts(i.UpdatedAt), "closed_at": ts(i.ClosedAt), "closed_by": closedBy,
		"author_association": "NONE", "html_url": b.issueHTML(i.Kind, i.Number), "url": api,
		"repository_url": b.repo["url"], "comments_url": api + "/comments", "labels_url": api + "/labels{/name}"}
	if i.Kind == "pull" {
		pr := b.repo["url"].(string) + "/pulls/" + strconv.FormatInt(i.Number, 10)
		html := b.issueHTML(i.Kind, i.Number)
		out["pull_request"] = obj{"url": pr, "html_url": html, "diff_url": html + ".diff", "patch_url": html + ".patch"}
	}
	return out, nil
}

// issueOrFallback loads an issue by id, or by number when id is nil; a
// deleted issue is described from the event payload.
func (b *builder) issueOrFallback(id uuid.UUID, p eventPayload) (obj, error) {
	var i dbgen.Issue
	var err error
	if id != uuid.Nil {
		i, err = b.q.WebhookIssue(b.ctx, id)
	} else {
		i, err = b.q.WebhookIssueByNumber(b.ctx, dbgen.WebhookIssueByNumberParams{RepoID: b.repoID, Number: p.Number})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return obj{"number": p.Number, "title": p.Title, "state": nonEmpty(p.State),
			"state_reason": nonEmpty(p.StateReason), "html_url": b.issueHTML(p.IssueKind, p.Number),
			"url": b.issueAPI(p.Number)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("webhook issue: %w", err)
	}
	return b.issue(i)
}

func (b *builder) milestone(m dbgen.Milestone) obj {
	return obj{"id": numID(m.ID), "node_id": m.ID.String(), "title": m.Title, "description": nonEmpty(m.Description),
		"state": m.State, "due_on": ts(m.DueOn), "created_at": ts(m.CreatedAt), "updated_at": ts(m.UpdatedAt),
		"closed_at": ts(m.ClosedAt), "html_url": b.html + "/milestones"}
}

func (b *builder) label(l dbgen.Label) obj {
	return obj{"id": numID(l.ID), "node_id": l.ID.String(), "name": l.Name, "color": strings.TrimPrefix(l.Color, "#"),
		"description": nonEmpty(l.Description), "default": false,
		"url": b.repo["url"].(string) + "/labels/" + url.PathEscape(l.Name)}
}

func (b *builder) comment(c dbgen.IssueComment, issue obj) (obj, error) {
	author, err := b.userOf(c.AuthorID)
	if err != nil {
		return nil, err
	}
	html, _ := issue["html_url"].(string)
	issueURL, _ := issue["url"].(string)
	return obj{"id": numID(c.ID), "node_id": c.ID.String(), "body": c.Body, "user": author,
		"created_at": ts(c.CreatedAt), "updated_at": ts(c.UpdatedAt), "author_association": "NONE",
		"html_url": html + "#issuecomment-" + c.ID.String(), "issue_url": issueURL,
		"url": b.repo["url"].(string) + "/issues/comments/" + c.ID.String()}, nil
}

func (b *builder) release(r dbgen.Release) (obj, error) {
	author, err := b.userOf(r.AuthorID)
	if err != nil {
		return nil, err
	}
	api := b.repo["url"].(string) + "/releases/" + r.ID.String()
	return obj{"id": numID(r.ID), "node_id": r.ID.String(), "tag_name": r.Tag, "target_commitish": r.Target,
		"name": nonEmpty(r.Name), "body": nonEmpty(r.Body), "draft": r.Draft, "prerelease": r.Prerelease,
		"created_at": ts(r.CreatedAt), "published_at": ts(r.PublishedAt), "author": author, "assets": []any{},
		"html_url": b.html + "/releases/" + r.ID.String(), "url": api,
		"tarball_url": b.html + "/archive/" + url.PathEscape(r.Tag) + ".tar.gz",
		"zipball_url": b.html + "/archive/" + url.PathEscape(r.Tag) + ".zip"}, nil
}

// item is one GitHub delivery made for an event.
type item struct {
	event, action string
	payload       []byte
}

// pullOnlyActions are the issue actions of a pull request that the pull
// request events do not carry; the others arrive as KindPull events.
var pullOnlyActions = map[string]bool{"milestoned": true, "demilestoned": true, "locked": true, "unlocked": true}

// githubEvent is the event name of a bus event for subscriptions; "" means
// no delivery.
func githubEvent(ev events.Event) string {
	if ev.Kind == events.KindIssue {
		var p struct {
			IssueKind string `json:"issue_kind"`
		}
		_ = ev.Decode(&p)
		if p.IssueKind == "pull" {
			return events.KindPull
		}
		return "issues"
	}
	return ev.Kind
}

// items builds the GitHub-shaped deliveries of an event.
func (b *builder) items(ev events.Event) ([]item, error) {
	var bodies []obj
	var names []string
	add := func(name string, body obj) {
		names = append(names, name)
		bodies = append(bodies, body)
	}
	switch ev.Kind {
	case events.KindIssue:
		var p eventPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		name := "issues"
		key := "issue"
		if p.IssueKind == "pull" {
			if !pullOnlyActions[ev.Action] {
				return nil, nil
			}
			name, key = events.KindPull, "pull_request"
		}
		issue, err := b.issueOrFallback(ev.SubjectID, p)
		if err != nil {
			return nil, err
		}
		body := obj{"action": ev.Action, key: issue}
		if key == "pull_request" {
			body["number"] = p.Number
		}
		switch ev.Action {
		case "labeled", "unlabeled":
			body["label"] = obj{"name": p.Label}
			if ls, ok := issue["labels"].([]any); ok {
				for _, l := range ls {
					if lo, ok := l.(obj); ok && lo["name"] == p.Label {
						body["label"] = lo
					}
				}
			}
		case "assigned", "unassigned":
			if p.Assignee != nil {
				u, err := b.user(*p.Assignee)
				if err != nil {
					return nil, err
				}
				body["assignee"] = u
			}
		case "milestoned", "demilestoned":
			if m := issue["milestone"]; m != nil {
				body["milestone"] = m
			} else {
				body["milestone"] = obj{"title": p.Milestone}
			}
		}
		add(name, body)
	case events.KindIssueComment:
		var p eventPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		issue, err := b.issueOrFallback(uuid.Nil, p)
		if err != nil {
			return nil, err
		}
		comment := obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String()}
		c, err := b.q.WebhookIssueComment(b.ctx, ev.SubjectID)
		switch {
		case err == nil:
			if comment, err = b.comment(c, issue); err != nil {
				return nil, err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("webhook comment: %w", err)
		}
		add(events.KindIssueComment, obj{"action": ev.Action, "issue": issue, "comment": comment})
	case events.KindLabel:
		var p eventPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		label := obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String(), "name": p.Label}
		l, err := b.q.WebhookLabel(b.ctx, ev.SubjectID)
		switch {
		case err == nil:
			label = b.label(l)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("webhook label: %w", err)
		}
		add(events.KindLabel, obj{"action": ev.Action, "label": label})
	case events.KindMilestone:
		var p eventPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		ms := obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String(), "title": p.Milestone,
			"state": nonEmpty(p.State)}
		m, err := b.q.WebhookMilestone(b.ctx, ev.SubjectID)
		switch {
		case err == nil:
			ms = b.milestone(m)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("webhook milestone: %w", err)
		}
		add(events.KindMilestone, obj{"action": ev.Action, "milestone": ms})
	case events.KindRelease:
		var p eventPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		rel := obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String(), "tag_name": p.Tag,
			"name": nonEmpty(p.Name), "prerelease": p.Prerelease, "draft": false, "assets": []any{}}
		r, err := b.q.WebhookRelease(b.ctx, ev.SubjectID)
		switch {
		case err == nil:
			if rel, err = b.release(r); err != nil {
				return nil, err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("webhook release: %w", err)
		}
		add(events.KindRelease, obj{"action": ev.Action, "release": rel})
	case events.KindPush:
		var p events.PushPayload
		if err := ev.Decode(&p); err != nil {
			return nil, err
		}
		pusher, err := b.user(ev.ActorID)
		if err != nil {
			return nil, err
		}
		pusherName := ""
		if u, ok := pusher.(obj); ok {
			pusherName, _ = u["login"].(string)
		}
		for _, u := range p.Updates {
			created, deleted := zeroSHA(u.Old), zeroSHA(u.New)
			add(events.KindPush, obj{"ref": u.Ref, "before": u.Old, "after": u.New, "created": created,
				"deleted": deleted, "forced": false, "base_ref": nil,
				"compare": b.html + "/compare/" + shortSHA(u.Old) + "..." + shortSHA(u.New),
				"commits": []any{}, "head_commit": nil, "pusher": obj{"name": pusherName, "email": nil}})
			refType, short := refKind(u.Ref)
			if refType == "" || (!created && !deleted) {
				continue
			}
			if created {
				add("create", obj{"ref": short, "ref_type": refType, "master_branch": b.repo["default_branch"],
					"description": b.repo["description"], "pusher_type": "user"})
			} else {
				add("delete", obj{"ref": short, "ref_type": refType, "pusher_type": "user"})
			}
		}
	case events.KindPull, events.KindPullReview, events.KindPullReviewComment:
		body, err := b.pullBody(ev)
		if err != nil {
			return nil, err
		}
		add(ev.Kind, body)
	case events.KindStatus:
		m := obj{}
		_ = json.Unmarshal(ev.Payload, &m)
		add(events.KindStatus, m)
	default:
		return nil, nil
	}
	out := make([]item, 0, len(bodies))
	for i, body := range bodies {
		body, err := b.envelope(body, ev.ActorID)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("webhook payload: %w", err)
		}
		action, _ := body["action"].(string)
		out = append(out, item{event: names[i], action: action, payload: raw})
	}
	return out, nil
}

// pullBody builds a pull request event from the issue row of the pull
// request and the event payload (events.PullPayload and friends).
func (b *builder) pullBody(ev events.Event) (obj, error) {
	var p struct {
		events.PullPayload
		PullID   uuid.UUID  `json:"pull_id"`
		ReviewID uuid.UUID  `json:"review_id"`
		Commit   string     `json:"commit_sha"`
		InReply  *uuid.UUID `json:"in_reply_to"`
		Path     string     `json:"path"`
		Line     int        `json:"line"`
	}
	if err := ev.Decode(&p); err != nil {
		return nil, err
	}
	// SubjectID is the pull request for KindPull; reviews and review
	// comments carry its id, and their state is the review state.
	pullID, prState := p.PullID, ""
	if ev.Kind == events.KindPull {
		pullID, prState = ev.SubjectID, p.State
	}
	pr, err := b.issueOrFallback(pullID, eventPayload{Number: p.Number, IssueKind: "pull", Title: p.Title,
		State: prState})
	if err != nil {
		return nil, err
	}
	delete(pr, "pull_request")
	html := b.issueHTML("pull", p.Number)
	pr["url"] = b.repo["url"].(string) + "/pulls/" + strconv.FormatInt(p.Number, 10)
	pr["issue_url"] = b.issueAPI(p.Number)
	pr["diff_url"], pr["patch_url"] = html+".diff", html+".patch"
	merged := prState == "merged" || p.MergeCommit != ""
	if merged {
		pr["state"] = "closed"
	}
	pr["merged"] = merged
	pr["merge_commit_sha"] = nonEmpty(p.MergeCommit)
	pr["draft"] = p.Draft
	if p.HeadRef != "" || p.HeadSHA != "" {
		pr["head"] = obj{"ref": p.HeadRef, "sha": p.HeadSHA, "label": p.HeadRef}
	}
	if p.BaseRef != "" || p.BaseSHA != "" {
		pr["base"] = obj{"ref": p.BaseRef, "sha": p.BaseSHA, "label": p.BaseRef, "repo": b.repo}
	}
	body := obj{"action": ev.Action, "number": p.Number, "pull_request": pr}
	switch ev.Kind {
	case events.KindPull:
		if p.Reviewer != nil {
			u, err := b.user(*p.Reviewer)
			if err != nil {
				return nil, err
			}
			body["requested_reviewer"] = u
		}
		if ev.Action == "synchronize" && p.Before != "" {
			body["before"], body["after"] = p.Before, p.HeadSHA
		}
	case events.KindPullReview:
		user, err := b.user(ev.ActorID)
		if err != nil {
			return nil, err
		}
		body["review"] = obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String(), "state": p.State,
			"commit_id": nonEmpty(p.Commit), "user": user, "pull_request_url": pr["url"],
			"html_url": html + "#pullrequestreview-" + ev.SubjectID.String()}
	case events.KindPullReviewComment:
		user, err := b.user(ev.ActorID)
		if err != nil {
			return nil, err
		}
		c := obj{"id": numID(ev.SubjectID), "node_id": ev.SubjectID.String(), "path": nonEmpty(p.Path),
			"line": p.Line, "user": user, "pull_request_url": pr["url"],
			"html_url": html + "#discussion_r" + ev.SubjectID.String()}
		if p.InReply != nil {
			c["in_reply_to_id"] = numID(*p.InReply)
		}
		if p.ReviewID != uuid.Nil {
			c["pull_request_review_id"] = numID(p.ReviewID)
		}
		body["comment"] = c
	}
	return body, nil
}

// native builds the envelope of the ervisio format.
func (b *builder) native(ev events.Event) ([]byte, error) {
	actor, err := b.user(ev.ActorID)
	if err != nil {
		return nil, err
	}
	payload := ev.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	env := obj{"event_id": ev.ID, "kind": ev.Kind, "action": ev.Action, "created_at": ev.CreatedAt.UTC(),
		"repository": obj{"id": b.repoID, "full_name": b.fullName, "html_url": b.html, "private": b.repo["private"]},
		"actor":      actor, "payload": payload}
	if ev.SubjectID != uuid.Nil {
		env["subject_id"] = ev.SubjectID
	}
	if b.org != nil {
		env["organization"] = obj{"id": b.org["node_id"], "login": b.org["login"]}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("webhook payload: %w", err)
	}
	return raw, nil
}

// ping builds the ping payload of a hook.
func (b *builder) ping(h dbgen.Webhook, sc Scope, actor uuid.UUID) ([]byte, error) {
	hookType := "Repository"
	if sc.OrgID != uuid.Nil {
		hookType = "Organization"
	}
	events := h.Events
	if events == nil {
		events = []string{}
	}
	body := obj{"zen": "Keep it logically awesome.", "hook_id": numID(h.ID),
		"hook": obj{"type": hookType, "id": numID(h.ID), "node_id": h.ID.String(), "name": "web", "active": h.Active,
			"events": events, "config": obj{"content_type": h.ContentType, "url": h.Url, "insecure_ssl": "0"},
			"created_at": ts(h.CreatedAt), "updated_at": ts(h.UpdatedAt)}}
	if sc.RepoID != uuid.Nil {
		if err := b.loadRepo(sc.RepoID); err != nil {
			return nil, err
		}
		body["repository"] = b.repo
		if b.org != nil {
			body["organization"] = b.org
		}
	} else {
		n, err := b.q.WebhookNamespace(b.ctx, sc.OrgID)
		if err != nil {
			return nil, fmt.Errorf("webhook organization: %w", err)
		}
		esc := url.PathEscape(n.Name)
		body["organization"] = obj{"login": n.Name, "id": numID(n.ID), "node_id": n.ID.String(),
			"url": b.url("api", "v1", "orgs", esc), "html_url": b.url(esc), "description": nil}
	}
	sender, err := b.user(actor)
	if err != nil {
		return nil, err
	}
	body["sender"] = sender
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("webhook payload: %w", err)
	}
	return raw, nil
}

func zeroSHA(s string) bool { return s == "" || strings.Trim(s, "0") == "" }

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// refKind returns branch or tag and the short name of a ref; "" for other
// refs.
func refKind(ref string) (string, string) {
	if s, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return "branch", s
	}
	if s, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
		return "tag", s
	}
	return "", ""
}
