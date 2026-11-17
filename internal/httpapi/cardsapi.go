package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/cards"
	"github.com/Ervisio/ErvisioRepo/internal/dashboard"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Bounds of the data read for one card.
const (
	cardRepoPages     = 10 // × repos.MaxLimit repositories counted by the stats card
	cardStreakYears   = 15 // calendar years scanned for the longest streak
	cardHoursLookback = 365 * 24 * time.Hour
)

func (p problemResponse) VisitGetUserCardResponse(w http.ResponseWriter) error { return p.write(w) }

// svgCard is a rendered card. It is computed as a signed-out visitor, so it
// can be cached by shared caches and embedded by other sites.
type svgCard []byte

func (c svgCard) VisitGetUserCardResponse(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(c)))
	h.Set("Cache-Control", "public, max-age=1800")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(c)
	return err
}

// GetUserCard draws one SVG profile card. Whoever calls, the data is what an
// anonymous visitor may see: the same image for everyone, safe to cache.
func (s *v1Server) GetUserCard(ctx context.Context, req v1gen.GetUserCardRequestObject) (v1gen.GetUserCardResponseObject, error) {
	if s.dashboard == nil {
		return errDashboardUnavailable, nil
	}
	pr, fail, err := s.profileOf(ctx, req.Username)
	if fail != nil || err != nil {
		return failOr[v1gen.GetUserCardResponseObject](fail, err)
	}
	anon := auth.Principal{}
	theme := cards.ThemeOf(string(deref(req.Params.Theme)))
	hide := deref(req.Params.HideBorder)
	now := time.Now()

	switch req.Card {
	case "stats":
		st, err := s.cardStats(ctx, anon, pr, now)
		if err != nil {
			return dashFail[v1gen.GetUserCardResponseObject](err)
		}
		return svgCard(cards.StatsCard(theme, hide, st)), nil
	case "languages":
		if s.repos == nil || s.browse == nil {
			return errReposUnavailable, nil
		}
		list, err := s.repos.ListOwner(ctx, anon, pr.Username, 0, repos.MaxLimit)
		if err != nil {
			return nil, err
		}
		sum := s.sumLanguages(ctx, list)
		langs := make([]cards.Language, 0, len(sum.Items))
		for _, l := range sum.Items {
			langs = append(langs, cards.Language{Name: l.Name, Color: l.Color, Percent: l.Percent})
		}
		return svgCard(cards.LanguagesCard(theme, hide, pr.Username, pr.DisplayName, langs)), nil
	case "streak":
		days, total, since, err := s.cardAllDays(ctx, anon, pr, now)
		if err != nil {
			return dashFail[v1gen.GetUserCardResponseObject](err)
		}
		current, longest := cards.Streaks(days, now)
		return svgCard(cards.StreakCard(theme, hide, pr.Username, pr.DisplayName, total, since, current, longest)), nil
	case "activity":
		cal, err := s.dashboard.Contributions(ctx, anon, pr.ID, 0, now)
		if err != nil {
			return dashFail[v1gen.GetUserCardResponseObject](err)
		}
		return svgCard(cards.ActivityCard(theme, hide, pr.Username, pr.DisplayName, cardDays(cal.Days), cal.From, cal.To, cal.Total)), nil
	case "hours":
		tz := pr.Details.Timezone
		if _, err := time.LoadLocation(tz); tz == "" || err != nil {
			tz = "UTC"
		}
		hours, err := s.dashboard.HourCounts(ctx, anon, pr.ID, now.Add(-cardHoursLookback), tz)
		if err != nil {
			return dashFail[v1gen.GetUserCardResponseObject](err)
		}
		return svgCard(cards.HoursCard(theme, hide, pr.Username, pr.DisplayName, hours, tz)), nil
	}
	return badRequest("unknown card"), nil
}

// cardStats counts public repositories and their stars, the contributions of
// the last 53 weeks and, all time, pushes, opened pull requests and issues.
func (s *v1Server) cardStats(ctx context.Context, anon auth.Principal, pr identity.Profile, now time.Time) (cards.Stats, error) {
	st := cards.Stats{Username: pr.Username, DisplayName: pr.DisplayName}
	if s.repos != nil {
		for page := range cardRepoPages {
			list, err := s.repos.ListOwner(ctx, anon, pr.Username, page*repos.MaxLimit, repos.MaxLimit)
			if err != nil {
				return st, err
			}
			for _, r := range list {
				st.Repos++
				st.Stars += r.StarsCount
			}
			if len(list) < repos.MaxLimit {
				break
			}
		}
	}
	cal, err := s.dashboard.Contributions(ctx, anon, pr.ID, 0, now)
	if err != nil {
		return st, err
	}
	st.Contributions = cal.Total
	kinds, err := s.dashboard.KindCounts(ctx, anon, pr.ID, time.Time{}, now.Add(time.Minute))
	if err != nil {
		return st, err
	}
	st.Pushes = kinds["push/pushed"]
	st.PullRequests = kinds["pull_request/opened"]
	st.Issues = kinds["issue/opened"]
	st.Releases = kinds["release/published"]
	st.Comments = kinds["comment/created"]
	return st, nil
}

// cardAllDays reads the contribution days of every year with activity (the
// newest cardStreakYears), plus the total and the first day with activity.
func (s *v1Server) cardAllDays(ctx context.Context, anon auth.Principal, pr identity.Profile, now time.Time) ([]cards.Day, int64, time.Time, error) {
	cal, err := s.dashboard.Contributions(ctx, anon, pr.ID, now.UTC().Year(), now)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	days, total := cardDays(cal.Days), cal.Total
	years := slices.DeleteFunc(slices.Clone(cal.Years), func(y int) bool { return y >= now.UTC().Year() })
	for _, y := range years[:min(len(years), cardStreakYears-1)] {
		c, err := s.dashboard.Contributions(ctx, anon, pr.ID, y, now)
		if err != nil {
			return nil, 0, time.Time{}, err
		}
		days = append(days, cardDays(c.Days)...)
		total += c.Total
	}
	var first time.Time
	for _, d := range days {
		if first.IsZero() || d.Date.Before(first) {
			first = d.Date
		}
	}
	return days, total, first, nil
}

func cardDays(in []dashboard.Day) []cards.Day {
	out := make([]cards.Day, 0, len(in))
	for _, d := range in {
		out = append(out, cards.Day(d))
	}
	return out
}
