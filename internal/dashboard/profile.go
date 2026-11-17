package dashboard

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Weeks covered by WeeklyActivity, and the most repositories it takes.
const (
	ActivityWeeks    = 16
	MaxActivityRepos = 100
)

// Day is the number of contributions on one UTC day.
type Day struct {
	Date  time.Time
	Count int64
}

// Calendar is the contribution calendar of a user over [From, To] (UTC days).
type Calendar struct {
	From, To time.Time
	Total    int64
	Days     []Day
	Years    []int
}

// Contributions counts the activity events of user per UTC day in the
// repositories who can read. year 0 is the last 53 weeks up to today, the
// first day being a Sunday like the columns of the calendar.
func (s *Service) Contributions(ctx context.Context, who auth.Principal, user uuid.UUID, year int, now time.Time) (Calendar, error) {
	sc := scopeOf(who)
	today := now.UTC().Truncate(24 * time.Hour)
	var from, to time.Time
	if year == 0 {
		to = today
		from = to.AddDate(0, 0, -364)
		from = from.AddDate(0, 0, -int(from.Weekday()))
	} else {
		if year < 2000 || year > 9999 {
			return Calendar{}, invalid("year out of range")
		}
		from = time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		to = time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
		if to.After(today) {
			to = today
		}
	}
	cal := Calendar{From: from, To: to, Days: []Day{}, Years: []int{}}
	if from.After(to) {
		return cal, nil
	}
	rows, err := s.q.CountUserContributions(ctx, dbgen.CountUserContributionsParams{Actor: user,
		Since: pgtype.Timestamptz{Time: from, Valid: true}, Until: pgtype.Timestamptz{Time: to.AddDate(0, 0, 1), Valid: true},
		Viewer: sc.viewer, AllRepos: sc.allRepos, Internal: sc.internal})
	if err != nil {
		return Calendar{}, fmt.Errorf("count contributions: %w", err)
	}
	for _, r := range rows {
		cal.Days = append(cal.Days, Day{Date: r.Day.Time, Count: r.Events})
		cal.Total += r.Events
	}
	years, err := s.q.ListUserContributionYears(ctx, dbgen.ListUserContributionYearsParams{Actor: user,
		Viewer: sc.viewer, AllRepos: sc.allRepos, Internal: sc.internal})
	if err != nil {
		return Calendar{}, fmt.Errorf("list contribution years: %w", err)
	}
	for _, y := range years {
		cal.Years = append(cal.Years, int(y))
	}
	return cal, nil
}

// UserActivity lists the newest events of user in the repositories who can read.
func (s *Service) UserActivity(ctx context.Context, who auth.Principal, user uuid.UUID, offset, limit int) ([]Item, error) {
	sc := scopeOf(who)
	if limit <= 0 {
		limit = DefaultPerPage
	}
	limit = min(limit, MaxPerPage)
	off := int32(min(max(offset, 0), math.MaxInt32)) //nolint:gosec // clamped
	rows, err := s.q.ListActorActivity(ctx, dbgen.ListActorActivityParams{Actor: user, Viewer: sc.viewer,
		AllRepos: sc.allRepos, Internal: sc.internal, Off: off, Lim: int32(limit)}) //nolint:gosec // clamped
	if err != nil {
		return nil, fmt.Errorf("list user activity: %w", err)
	}
	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, Item{ID: r.ID, Kind: r.Kind, Action: r.Action, Owner: r.OwnerName, Repo: r.RepoName,
			Number: r.Number, Title: r.Title, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// WeeklyActivity counts the events of each repository per week over the last
// ActivityWeeks weeks, oldest first. Repositories who cannot read are left out.
func (s *Service) WeeklyActivity(ctx context.Context, who auth.Principal, ids []uuid.UUID, now time.Time) (map[uuid.UUID][]int64, error) {
	if len(ids) > MaxActivityRepos {
		return nil, invalid("at most %d repositories", MaxActivityRepos)
	}
	out := map[uuid.UUID][]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	sc := scopeOf(who)
	since := now.UTC().Add(-ActivityWeeks * 7 * 24 * time.Hour)
	rows, err := s.q.CountRepoWeeklyActivity(ctx, dbgen.CountRepoWeeklyActivityParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, Ids: ids,
		Viewer: sc.viewer, AllRepos: sc.allRepos, Internal: sc.internal})
	if err != nil {
		return nil, fmt.Errorf("count weekly activity: %w", err)
	}
	readable, err := s.q.ListReadableRepoIDs(ctx, dbgen.ListReadableRepoIDsParams{Ids: ids, Viewer: sc.viewer,
		AllRepos: sc.allRepos, Internal: sc.internal})
	if err != nil {
		return nil, fmt.Errorf("check repository access: %w", err)
	}
	for _, id := range readable {
		out[id] = make([]int64, ActivityWeeks)
	}
	for _, r := range rows {
		w, ok := out[r.RepoID]
		if ok && r.Week >= 0 && int(r.Week) < ActivityWeeks {
			w[r.Week] += r.Events
		}
	}
	return out, nil
}
