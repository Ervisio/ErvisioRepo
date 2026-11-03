package webhooks

import (
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// GitHub-shaped bodies of the status, check_run and check_suite events,
// built from the event payloads.

func tsTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func tsTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return tsTime(*t)
}

func (b *builder) repoAPI() string {
	s, _ := b.repo["url"].(string)
	return s
}

func (b *builder) statusBody(ev events.Event, p events.StatusPayload) obj {
	return obj{"id": numID(ev.SubjectID), "sha": p.SHA, "name": b.fullName, "target_url": nonEmpty(p.TargetURL),
		"context": p.Context, "description": nonEmpty(p.Description), "state": p.State,
		"commit":   obj{"sha": p.SHA, "url": b.repoAPI() + "/commits/" + p.SHA, "html_url": b.html + "/commit/" + p.SHA},
		"branches": []any{}, "created_at": tsTime(p.CreatedAt), "updated_at": tsTime(p.CreatedAt)}
}

func (b *builder) app(name string) obj {
	return obj{"id": 0, "slug": name, "name": name, "owner": nil, "html_url": b.s.base}
}

func (b *builder) checkSuite(p events.CheckSuitePayload) obj {
	return obj{"id": numID(p.ID), "node_id": p.ID.String(), "head_branch": nonEmpty(p.HeadBranch), "head_sha": p.HeadSHA,
		"status": p.Status, "conclusion": nonEmpty(p.Conclusion), "url": b.repoAPI() + "/check-suites/" + p.ID.String(),
		"before": nil, "after": p.HeadSHA, "pull_requests": []any{}, "app": b.app(p.App),
		"created_at": tsTime(p.CreatedAt), "updated_at": tsTime(p.UpdatedAt)}
}

func (b *builder) checkRun(p events.CheckRunPayload) obj {
	self := b.repoAPI() + "/check-runs/" + p.ID.String()
	return obj{"id": numID(p.ID), "node_id": p.ID.String(), "name": p.Name, "head_sha": p.HeadSHA,
		"external_id": p.ExternalID, "url": self, "html_url": b.html + "/commit/" + p.HeadSHA,
		"details_url": p.DetailsURL, "status": p.Status, "conclusion": nonEmpty(p.Conclusion),
		"started_at": tsTimePtr(p.StartedAt), "completed_at": tsTimePtr(p.CompletedAt),
		"output": obj{"title": nonEmpty(p.Title), "summary": nonEmpty(p.Summary), "text": nil,
			"annotations_count": p.Annotations, "annotations_url": self + "/annotations"},
		"check_suite": b.checkSuite(p.Suite), "app": b.app(p.Suite.App), "pull_requests": []any{}}
}
