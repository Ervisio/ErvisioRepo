package issues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// MaxPushCommits bounds the commits of one ref update scanned for
// references and closing keywords.
const MaxPushCommits = 250

var closeRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[ \t]*:?[ \t]+(?:([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]{1,100}))?#(\d{1,18})\b`)

// ClosingRefs returns the issues that text closes with a keyword (close,
// closes, closed, fix, fixes, fixed, resolve, resolves, resolved) followed
// by #N or owner/repo#N, outside code, without duplicates.
func ClosingRefs(text string) []Ref {
	var out []Ref
	seen := map[string]bool{}
	for _, m := range closeRe.FindAllStringSubmatch(stripCode(text), -1) {
		n, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		owner, repo := m[1], strings.TrimSuffix(m[2], ".git")
		key := strings.ToLower(owner+"/"+repo) + "#" + m[3]
		if seen[key] || len(out) >= maxRefsPerKind {
			continue
		}
		seen[key] = true
		out = append(out, Ref{Kind: "issue", Owner: owner, Repo: repo, Number: n})
	}
	return out
}

// CloseSource tells what closes issues: a commit on the default branch or a
// merged pull request of the same repository.
type CloseSource struct {
	Commit string // full commit id
	Pull   int64  // pull request number
}

func (c CloseSource) data() map[string]any {
	d := map[string]any{}
	if c.Commit != "" {
		d["commit"] = c.Commit
	}
	if c.Pull > 0 {
		d["pull"] = c.Pull
	}
	return d
}

// principalOf builds the principal of a user; ok is false for a missing or
// suspended user.
func (s *Service) principalOf(ctx context.Context, user uuid.UUID) (auth.Principal, bool, error) {
	if user == uuid.Nil {
		return auth.Principal{}, false, nil
	}
	u, err := s.q.GetUserByID(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, false, nil
	}
	if err != nil {
		return auth.Principal{}, false, fmt.Errorf("get user: %w", err)
	}
	if u.SuspendedAt.Valid {
		return auth.Principal{}, false, nil
	}
	return auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role}, true, nil
}

// CloseFromText closes, as completed, the open issues that text closes
// with a keyword (ClosingRefs). text was written in repository repoID by
// actorID, who is trusted to close the issues of repoID (they pushed to its
// default branch or merged one of its pull requests); issues of other
// repositories are closed only when actorID can triage them. Pull requests
// are never closed this way. It returns the references actually closed.
func (s *Service) CloseFromText(ctx context.Context, repoID, actorID uuid.UUID, text string, src CloseSource) ([]Ref, error) {
	refs := ClosingRefs(text)
	if len(refs) == 0 {
		return nil, nil
	}
	r, err := s.o.Repos.GetByID(ctx, systemPrincipal, repoID)
	if err != nil {
		return nil, fmt.Errorf("close from text: %w", err)
	}
	who, known, err := s.principalOf(ctx, actorID)
	if err != nil {
		return nil, err
	}
	var closed []Ref
	for _, ref := range refs {
		tr := r
		if ref.Owner != "" && (!strings.EqualFold(ref.Owner, r.Owner) || !strings.EqualFold(ref.Repo, r.Name)) {
			if !known {
				continue
			}
			t, ok, err := s.targetRepo(ctx, who, r, ref)
			if err != nil {
				return closed, err
			}
			if !ok || !canTriage(t) {
				continue
			}
			tr = t
		}
		done := false
		err := s.tx(ctx, func(q *dbgen.Queries) error {
			i, err := s.lockIssue(ctx, q, tr, ref.Number)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if i.Kind != KindIssue || i.State != StateOpen {
				return nil
			}
			data := src.data()
			data["repo_id"] = r.ID
			if err := s.setState(ctx, q, actorID, &i, StateClosed, ReasonCompleted, data); err != nil {
				return err
			}
			done = true
			return nil
		})
		if err != nil {
			return closed, err
		}
		if done {
			closed = append(closed, Ref{Kind: "issue", Owner: tr.Owner, Repo: tr.Name, Number: ref.Number})
		}
	}
	return closed, nil
}

// PushHandler handles push events (events.KindPush): the commits pushed to
// the default branch of a repository have their references recorded
// (referenced entries on the timeline of the issues they mention) and close
// the issues named with a closing keyword.
func (s *Service) PushHandler() events.Handler {
	return events.HandlerFunc(func(ctx context.Context, ev events.Event) error {
		if ev.Kind != events.KindPush || s.o.Browse == nil {
			return nil
		}
		var p events.PushPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		r, err := s.o.Repos.GetByID(ctx, systemPrincipal, ev.RepoID)
		if errors.Is(err, repos.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("push handler: %w", err)
		}
		who, known, err := s.principalOf(ctx, ev.ActorID)
		if err != nil {
			return err
		}
		if !known {
			who = auth.Principal{}
		}
		branch := "refs/heads/" + r.DefaultBranch
		for _, u := range p.Updates {
			if u.Ref != branch || strings.Trim(u.New, "0") == "" {
				continue
			}
			commits, err := s.o.Browse.CommitsBetween(ctx, gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name,
				DefaultBranch: r.DefaultBranch}, u.Old, u.New, MaxPushCommits)
			if errors.Is(err, gitbrowse.ErrInvalid) || errors.Is(err, gitbrowse.ErrNotFound) || errors.Is(err, gitbrowse.ErrEmpty) {
				s.log.WarnContext(ctx, "push handler: cannot list pushed commits", "repo_id", r.ID, "ref", u.Ref, "err", err)
				continue
			}
			if err != nil {
				return fmt.Errorf("push handler: list commits: %w", err)
			}
			slices.Reverse(commits) // oldest first
			for _, c := range commits {
				if err := s.tx(ctx, func(q *dbgen.Queries) error {
					return s.recordRefs(ctx, q, r, who, refSource{repo: r.ID, commit: c.SHA}, c.Message)
				}); err != nil {
					return err
				}
				if _, err := s.CloseFromText(ctx, r.ID, ev.ActorID, c.Message, CloseSource{Commit: c.SHA}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
