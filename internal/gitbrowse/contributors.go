package gitbrowse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// contributorScan bounds how many of the newest commits are grouped.
const contributorScan = 50000

// Contributor is an author of a ref. The e-mail address stays internal.
type Contributor struct {
	Name    string
	Commits int
	// Username and AvatarURL are set when a verified address of a user matches.
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
		out, err := run(ctx, rp, readTimeout, "log", "--format=%aN%x1f%aE", "--max-count="+strconv.Itoa(contributorScan), r.Commit, "--")
		if err != nil {
			return fmt.Errorf("log authors: %w", err)
		}
		byEmail := map[string]*tally{}
		for _, line := range bytes.Split(out, []byte{'\n'}) {
			name, email, ok := strings.Cut(string(line), "\x1f")
			if !ok {
				continue
			}
			key := strings.ToLower(email)
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
	for _, t := range all[:min(limit, len(all))] {
		c := Contributor{Name: t.name, Commits: t.commits}
		c.Username, c.AvatarURL = s.contributorUser(ctx, t.email)
		res.Items = append(res.Items, c)
	}
	return res, nil
}

// contributorUser maps a commit address to an active user: a verified address
// of the user, or the no-reply address of a web edit. A failed lookup leaves
// the contributor anonymous.
func (s *Service) contributorUser(ctx context.Context, email string) (username, avatar string) {
	if s.q == nil {
		return "", ""
	}
	var u dbgen.GetUserByIDRow
	if local, ok := strings.CutSuffix(email, "@"+strings.ToLower(s.o.NoReplyDomain)); ok {
		byName, err := s.q.GetUserByUsername(ctx, local)
		if err != nil {
			s.logLookup(ctx, err)
			return "", ""
		}
		if u, err = s.q.GetUserByID(ctx, byName.ID); err != nil {
			s.logLookup(ctx, err)
			return "", ""
		}
	} else {
		e, err := s.q.GetEmailByAddress(ctx, email)
		if err != nil || !e.VerifiedAt.Valid {
			s.logLookup(ctx, err)
			return "", ""
		}
		if u, err = s.q.GetUserByID(ctx, e.UserID); err != nil {
			s.logLookup(ctx, err)
			return "", ""
		}
	}
	if u.SuspendedAt.Valid {
		return "", ""
	}
	return u.Username, u.AvatarUrl
}

func (s *Service) logLookup(ctx context.Context, err error) {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.log.WarnContext(ctx, "contributor user lookup failed", "err", err)
	}
}
