package gitbrowse

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Account is the user a commit address belongs to: only what may be shown
// publicly, never the address itself.
type Account struct {
	Username  string
	AvatarURL string
}

const (
	authorCacheTTL  = 2 * time.Minute
	authorCacheSize = 4096
)

// authorCache remembers recent lookups, misses included, for a short time.
type authorCache struct {
	mu sync.Mutex
	m  map[string]authorEntry
}

type authorEntry struct {
	acc Account
	at  time.Time
}

// Authors maps commit addresses to active users for display only, using verified
// addresses, local no-reply addresses or the instance's pinned Claude alias.
func (s *Service) Authors(ctx context.Context, emails []string) (map[string]Account, error) {
	out := map[string]Account{}
	if s.q == nil || len(emails) == 0 {
		return out, nil
	}
	now := time.Now()
	noreply := "@" + strings.ToLower(s.o.NoReplyDomain)
	var verified, names []string
	var wantClaude bool
	byName := map[string][]string{} // login → addresses
	seen := map[string]bool{}
	s.authors.mu.Lock()
	for _, e := range emails {
		k := strings.ToLower(strings.TrimSpace(e))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		if s.claudeAuthor != nil && k == claudeAuthorEmail {
			wantClaude = true
			continue
		}
		if c, ok := s.authors.m[k]; ok && now.Sub(c.at) < authorCacheTTL {
			if c.acc.Username != "" {
				out[k] = c.acc
			}
			continue
		}
		if local, ok := strings.CutSuffix(k, noreply); ok {
			// A bot address is <numeric id>+<login>: usernames have no "+".
			if _, login, ok := strings.Cut(local, "+"); ok {
				local = login
			}
			if byName[local] == nil {
				names = append(names, local)
			}
			byName[local] = append(byName[local], k)
			continue
		}
		verified = append(verified, k)
	}
	s.authors.mu.Unlock()

	if wantClaude {
		acc, err := s.claudeAccount(ctx)
		if err != nil {
			return out, err
		}
		if acc.Username != "" {
			out[claudeAuthorEmail] = acc
		}
	}
	found := map[string]Account{}
	if len(verified) > 0 {
		rows, err := s.q.ListUsersByVerifiedEmails(ctx, verified)
		if err != nil {
			return out, fmt.Errorf("list users by email: %w", err)
		}
		for _, r := range rows {
			found[r.Email] = Account{Username: r.Username, AvatarURL: r.AvatarUrl}
		}
	}
	if len(names) > 0 {
		rows, err := s.q.ListActiveUsersByNames(ctx, names)
		if err != nil {
			return out, fmt.Errorf("list users by name: %w", err)
		}
		for _, r := range rows {
			for _, k := range byName[r.NameLower] {
				found[k] = Account{Username: r.Username, AvatarURL: r.AvatarUrl}
			}
		}
	}

	s.authors.mu.Lock()
	defer s.authors.mu.Unlock()
	if len(s.authors.m) > authorCacheSize {
		s.authors.m = map[string]authorEntry{}
	}
	remember := func(k string) {
		acc := found[k]
		s.authors.m[k] = authorEntry{acc: acc, at: now}
		if acc.Username != "" {
			out[k] = acc
		}
	}
	for _, k := range verified {
		remember(k)
	}
	for _, list := range byName {
		for _, k := range list {
			remember(k)
		}
	}
	return out, nil
}
