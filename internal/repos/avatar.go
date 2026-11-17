package repos

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// AuthorizeAdmin returns owner/name if the caller administers it:
// ErrNotFound when it cannot even read it, ErrForbidden otherwise.
func (s *Service) AuthorizeAdmin(ctx context.Context, who auth.Principal, owner, name string) (Repo, error) {
	r, err := s.Get(ctx, who, owner, name)
	if err != nil {
		return Repo{}, err
	}
	if r.Permission < authz.PermAdmin {
		return Repo{}, ErrForbidden
	}
	return r, nil
}

// SetAvatar records avatarURL (stored by internal/avatars; empty removes it)
// as the image of owner/name. Admins of the repository only.
func (s *Service) SetAvatar(ctx context.Context, who auth.Principal, owner, name, avatarURL string, c identity.Client) (Repo, error) {
	var out Repo
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		r, _, err := s.resolve(ctx, q, who, owner, name)
		if err != nil {
			return err
		}
		if r.Permission < authz.PermAdmin {
			return ErrForbidden
		}
		if err := q.SetRepoAvatar(ctx, dbgen.SetRepoAvatarParams{ID: r.ID, AvatarUrl: avatarURL}); err != nil {
			return fmt.Errorf("set repo avatar: %w", err)
		}
		if err := audit.Record(ctx, q, event(who, audit.ActionRepoUpdated, r.ID, orgOf(r.OwnerKind, r.OwnerID), c,
			map[string]any{"fields": []string{"avatar_url"}})); err != nil {
			return err
		}
		out, err = s.byID(ctx, q, who, r.ID)
		return err
	})
	return out, err
}

// MarkOwnerAvatars sets OwnerAvatarURL on each repository of list, in one query.
func (s *Service) MarkOwnerAvatars(ctx context.Context, list []Repo) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(list))
	seen := map[uuid.UUID]bool{}
	for _, r := range list {
		if !seen[r.OwnerID] {
			seen[r.OwnerID] = true
			ids = append(ids, r.OwnerID)
		}
	}
	rows, err := s.q.ListNamespaceAvatars(ctx, ids)
	if err != nil {
		return fmt.Errorf("list owner avatars: %w", err)
	}
	urls := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		urls[r.ID] = r.AvatarUrl
	}
	for i := range list {
		list[i].OwnerAvatarURL = urls[list[i].OwnerID]
	}
	return nil
}
