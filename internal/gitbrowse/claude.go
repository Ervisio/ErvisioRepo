package gitbrowse

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const (
	claudeAuthorEmail = "noreply@anthropic.com"
	claudeProfileName = "claude"
	claudeProfileHost = "git.ervisio.app"
	claudeAvatarURL   = "https://" + claudeProfileHost + "/claude-avatar.svg"
)

// BindClaudeAuthor runs before the service accepts requests, using only the
// configured public URL. Even a missing account is pinned to prevent a later claim.
func (s *Service) BindClaudeAuthor(ctx context.Context, publicURL string) error {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Host, claudeProfileHost) || s.q == nil {
		return nil
	}
	if err := s.q.PinGitAuthorAlias(ctx, dbgen.PinGitAuthorAliasParams{
		EmailLower: claudeAuthorEmail,
		Username:   claudeProfileName,
	}); err != nil {
		return fmt.Errorf("pin Claude author alias: %w", err)
	}
	id, err := s.q.GetGitAuthorAlias(ctx, claudeAuthorEmail)
	if err != nil {
		return fmt.Errorf("load Claude author alias: %w", err)
	}
	bound := uuid.Nil
	if id.Valid {
		bound = id.UUID
	}
	s.claudeAuthor = &bound
	return nil
}

// ProfileAliasAvatar supplies an image only for the account originally pinned.
func (s *Service) ProfileAliasAvatar(id uuid.UUID) string {
	if s.claudeAuthor != nil && id != uuid.Nil && id == *s.claudeAuthor {
		return claudeAvatarURL
	}
	return ""
}

// ClaudeAliasEmail is usable for display only where the canonical binding exists.
func (s *Service) ClaudeAliasEmail() string {
	if s.claudeAuthor != nil && *s.claudeAuthor != uuid.Nil {
		return claudeAuthorEmail
	}
	return ""
}

func hasClaudeCoauthor(trailers string) bool {
	for trailer := range strings.SplitSeq(trailers, "\n") {
		value, ok := strings.CutSuffix(strings.TrimSpace(trailer), ">")
		if !ok {
			continue
		}
		name, email, ok := strings.Cut(value, " <")
		if ok && strings.EqualFold(email, claudeAuthorEmail) &&
			(name == "Claude" || strings.HasPrefix(name, "Claude ") && !strings.ContainsAny(name, "<>")) {
			return true
		}
	}
	return false
}

func (s *Service) claudeAccount(ctx context.Context) (Account, error) {
	if *s.claudeAuthor == uuid.Nil {
		return Account{}, nil
	}
	u, err := s.q.GetUserByID(ctx, *s.claudeAuthor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, nil
	}
	if err != nil {
		return Account{}, fmt.Errorf("get Claude author profile: %w", err)
	}
	if u.SuspendedAt.Valid {
		return Account{}, nil
	}
	avatar := u.AvatarUrl
	if avatar == "" {
		avatar = claudeAvatarURL
	}
	return Account{Username: u.Username, AvatarURL: avatar}, nil
}
