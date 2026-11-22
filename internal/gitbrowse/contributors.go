package gitbrowse

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// contributorScan bounds how many of the newest commits are grouped.
const contributorScan = 50000

// Contributor is an author of a ref. The e-mail address stays internal.
type Contributor struct {
	Name    string
	Commits int
	// Username and AvatarURL are set when the author's address resolves.
	Username  string
	AvatarURL string
}

// Contributors is the authors of a ref, most commits first.
type Contributors struct {
	Ref Resolved
	// Total is the number of distinct authors found, before limit.
	Total int
	Items []Contributor
}

// Contributors groups the authors of the newest commits of ref by e-mail
// address (case-insensitive), keeping the most recent name of each.
func (s *Service) Contributors(ctx context.Context, repo Repo, ref string, limit int) (Contributors, error) {
	if limit <= 0 || limit > MaxLogLimit {
		limit = min(max(limit, 30), MaxLogLimit)
	}
	type tally struct {
		name, email string
		commits     int
	}
	var res Contributors
	var all []*tally
	err := s.read(ctx, repo, func(rp string) error {
		r, err := s.resolve(ctx, rp, repo, ref)
		if err != nil {
			return err
		}
		res.Ref = r
		out, err := run(ctx, rp, readTimeout, "log", "--format=%aN%x1f%aE%x1f%ae", "--max-count="+strconv.Itoa(contributorScan), r.Commit, "--")
		if err != nil {
			return fmt.Errorf("log authors: %w", err)
		}
		byEmail := map[string]*tally{}
		for _, line := range bytes.Split(out, []byte{'\n'}) {
			f := strings.SplitN(string(line), "\x1f", 3)
			if len(f) != 3 {
				continue
			}
			name := f[0]
			key := strings.ToLower(strings.TrimSpace(f[1]))
			raw := strings.ToLower(strings.TrimSpace(f[2]))
			// A repository mailmap cannot add or remove the instance alias.
			if s.claudeAuthor != nil && (key == claudeAuthorEmail || raw == claudeAuthorEmail) {
				key = raw
			}
			t := byEmail[key]
			if t == nil {
				t = &tally{name: name, email: key}
				byEmail[key] = t
				all = append(all, t)
			}
			t.commits++
		}
		return nil
	})
	if err != nil {
		return Contributors{}, err
	}
	slices.SortStableFunc(all, func(a, b *tally) int { return b.commits - a.commits })
	res.Total = len(all)
	selected := all[:min(limit, len(all))]
	emails := make([]string, 0, len(selected))
	for _, t := range selected {
		emails = append(emails, t.email)
	}
	accounts, err := s.Authors(ctx, emails)
	if err != nil {
		s.log.WarnContext(ctx, "contributor user lookup failed", "err", err)
	}
	for _, t := range selected {
		account := accounts[t.email]
		res.Items = append(res.Items, Contributor{Name: t.name, Commits: t.commits,
			Username: account.Username, AvatarURL: account.AvatarURL})
	}
	return res, nil
}
