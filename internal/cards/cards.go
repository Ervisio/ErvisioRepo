// Package cards draws the SVG profile cards a README can embed: stats,
// languages, contribution streak, contribution calendar and hours of the day.
// The SVGs are self-contained (no fonts, images or scripts) and every string
// that comes from users is escaped.
package cards

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Theme is the palette of a card.
type Theme struct {
	Bg, Border, Title, Text, Muted, Accent, Track string
	// Levels colour the calendar cells, from no contribution to the most.
	Levels [5]string
}

// Themes are the palettes selectable with ?theme=; dark is the default.
var Themes = map[string]Theme{
	"dark": {Bg: "#0d1117", Border: "#3d444d", Title: "#4493f8", Text: "#f0f6fc", Muted: "#9198a1",
		Accent: "#4493f8", Track: "#212830", Levels: [5]string{"#151b23", "#0e4429", "#006d32", "#26a641", "#39d353"}},
	"light": {Bg: "#ffffff", Border: "#d1d9e0", Title: "#0969da", Text: "#1f2328", Muted: "#59636e",
		Accent: "#0969da", Track: "#eff2f5", Levels: [5]string{"#eff2f5", "#9be9a8", "#40c463", "#30a14e", "#216e39"}},
	"neon": {Bg: "#0f0c29", Border: "#302b63", Title: "#ff2e88", Text: "#ece9ff", Muted: "#a9a3d9",
		Accent: "#ff2e88", Track: "#1f1a45", Levels: [5]string{"#1c1840", "#3b2a78", "#7b3fb8", "#c2378f", "#ff2e88"}},
}

// ThemeOf returns the named theme, or dark.
func ThemeOf(name string) Theme {
	if t, ok := Themes[name]; ok {
		return t
	}
	return Themes["dark"]
}

const font = `-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif`

// esc makes s safe as XML text or attribute value; control characters are dropped.
func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&#39;")
		case unicode.IsControl(r) || r == unicode.ReplacementChar:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// color accepts only #rgb / #rrggbb values (language colours come from data).
func color(c, fallback string) string {
	if (len(c) == 4 || len(c) == 7) && c[0] == '#' {
		for _, r := range c[1:] {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return fallback
			}
		}
		return c
	}
	return fallback
}

type canvas struct {
	b strings.Builder
	t Theme
}

// open writes the root element, the styles, the background and the title.
func open(t Theme, w, h int, hideBorder bool, label, title string) *canvas {
	c := &canvas{t: t}
	stroke := 1
	if hideBorder {
		stroke = 0
	}
	fmt.Fprintf(&c.b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s">`,
		w, h, w, h, esc(label))
	fmt.Fprintf(&c.b, `<title>%s</title>`, esc(label))
	fmt.Fprintf(&c.b, `<style>text{font-family:%s}.t{font-size:18px;font-weight:600;fill:%s}`+
		`.l{font-size:14px;fill:%s}.v{font-size:14px;font-weight:700;fill:%s}.s{font-size:12px;fill:%s}`+
		`.b{font-size:28px;font-weight:700;fill:%s}.f{animation:f .6s ease-in-out backwards}`+
		`@keyframes f{from{opacity:0}}@media (prefers-reduced-motion:reduce){.f{animation:none}}</style>`,
		font, t.Title, t.Muted, t.Text, t.Muted, t.Text)
	fmt.Fprintf(&c.b, `<rect x="0.5" y="0.5" width="%d" height="%d" rx="6" fill="%s" stroke="%s" stroke-width="%d"/>`,
		w-1, h-1, t.Bg, t.Border, stroke)
	fmt.Fprintf(&c.b, `<text class="t f" x="25" y="35">%s</text>`, esc(title))
	return c
}

func (c *canvas) close() []byte {
	c.b.WriteString(`</svg>`)
	return []byte(c.b.String())
}

// delay staggers the fade-in of the i-th element.
func delay(i int) string { return fmt.Sprintf(` style="animation-delay:%dms"`, 150+i*80) }

// num formats n with thin grouping: 1234567 → 1.2M, 12345 → 12.3k.
func num(n int64) string {
	switch {
	case n >= 1_000_000:
		return trim(fmt.Sprintf("%.1f", float64(n)/1e6)) + "M"
	case n >= 10_000:
		return trim(fmt.Sprintf("%.1f", float64(n)/1e3)) + "k"
	}
	return fmt.Sprint(n)
}

func trim(s string) string { return strings.TrimSuffix(s, ".0") }

// Name is how a card calls the user: the display name, else the username.
func name(display, username string) string {
	if strings.TrimSpace(display) != "" {
		return display
	}
	return username
}

// Stats are the totals of the stats card.
type Stats struct {
	Username, DisplayName string
	Repos, Stars          int
	// Contributions are the events of the last 53 weeks.
	Contributions                int64
	Pushes, PullRequests, Issues int64
	Releases, Comments           int64
}

// StatsCard lists the totals with the yearly contributions in a ring.
func StatsCard(t Theme, hideBorder bool, s Stats) []byte {
	who := name(s.DisplayName, s.Username)
	c := open(t, 495, 195, hideBorder, who+" — Ervisio stats", who+"’s Ervisio stats")
	rows := []struct {
		label string
		v     int64
	}{
		{"Total stars", int64(s.Stars)},
		{"Public repositories", int64(s.Repos)},
		{"Pushes", s.Pushes},
		{"Pull requests opened", s.PullRequests},
		{"Issues opened", s.Issues},
	}
	for i, r := range rows {
		y := 68 + i*25
		fmt.Fprintf(&c.b, `<g class="f"%s><circle cx="31" cy="%d" r="4" fill="%s"/>`+
			`<text class="l" x="45" y="%d">%s:</text><text class="v" x="230" y="%d">%s</text></g>`,
			delay(i), y-5, t.Accent, y, esc(r.label), y, num(r.v))
	}
	// Ring: share of the last 53 weeks with at least one contribution is not
	// known here, so the ring is decorative and shows the yearly total.
	const cx, cy, rad = 400, 108, 50
	circ := 2 * math.Pi * rad
	fmt.Fprintf(&c.b, `<g class="f"%s><circle cx="%d" cy="%d" r="%d" fill="none" stroke="%s" stroke-width="7"/>`,
		delay(len(rows)), cx, cy, rad, t.Track)
	fmt.Fprintf(&c.b, `<circle cx="%d" cy="%d" r="%d" fill="none" stroke="%s" stroke-width="7" stroke-linecap="round" `+
		`stroke-dasharray="%.1f %.1f" transform="rotate(-90 %d %d)"/>`,
		cx, cy, rad, t.Accent, circ*ringShare(s.Contributions), circ, cx, cy)
	fmt.Fprintf(&c.b, `<text class="b" x="%d" y="%d" text-anchor="middle">%s</text>`, cx, cy+8, num(s.Contributions))
	fmt.Fprintf(&c.b, `<text class="s" x="%d" y="%d" text-anchor="middle">contributions · 1 year</text></g>`, cx, cy+76)
	return c.close()
}

// ringShare fills the ring logarithmically, full at 2000 contributions.
func ringShare(n int64) float64 {
	if n <= 0 {
		return 0.02
	}
	return math.Min(1, math.Log10(float64(n)+1)/math.Log10(2001))
}

// Language is one slice of the languages card.
type Language struct {
	Name, Color string
	Percent     float64
}

// LanguagesCard shows the share of each language in a bar and a two-column legend.
func LanguagesCard(t Theme, hideBorder bool, username, display string, langs []Language) []byte {
	who := name(display, username)
	if len(langs) > 10 {
		rest := 0.0
		for _, l := range langs[9:] {
			rest += l.Percent
		}
		langs = append(langs[:9:9], Language{Name: "Other", Color: t.Muted, Percent: rest})
	}
	rows := (len(langs) + 1) / 2
	h := max(195, 95+rows*25)
	c := open(t, 495, h, hideBorder, who+" — most used languages", "Most used languages")
	if len(langs) == 0 {
		fmt.Fprintf(&c.b, `<text class="l f" x="25" y="80">No public code yet</text>`)
		return c.close()
	}
	// Bar: one rect per language inside a rounded clip.
	fmt.Fprintf(&c.b, `<clipPath id="bar"><rect x="25" y="55" width="445" height="10" rx="5"/></clipPath><g clip-path="url(#bar)" class="f">`)
	x := 25.0
	for _, l := range langs {
		w := 445 * l.Percent / 100
		fmt.Fprintf(&c.b, `<rect x="%.2f" y="55" width="%.2f" height="10" fill="%s"/>`, x, w+0.5, color(l.Color, t.Muted))
		x += w
	}
	c.b.WriteString(`</g>`)
	for i, l := range langs {
		col, row := i%2, i/2
		lx, ly := 25+col*225, 95+row*25
		fmt.Fprintf(&c.b, `<g class="f"%s><circle cx="%d" cy="%d" r="5" fill="%s"/>`+
			`<text class="l" x="%d" y="%d"><tspan class="v">%s</tspan> %s%%</text></g>`,
			delay(i), lx+5, ly-5, color(l.Color, t.Muted), lx+18, ly, esc(l.Name), trim(fmt.Sprintf("%.1f", l.Percent)))
	}
	return c.close()
}

// Day is the number of contributions of one UTC day.
type Day struct {
	Date  time.Time
	Count int64
}

// Run is a series of consecutive days with contributions; zero when none.
type Run struct {
	Days     int
	From, To time.Time
}

// Streaks finds the current and the longest run in days (any order, UTC
// dates). The current run may end yesterday: today is not over yet.
func Streaks(days []Day, today time.Time) (current, longest Run) {
	set := map[time.Time]bool{}
	for _, d := range days {
		if d.Count > 0 {
			set[d.Date.UTC().Truncate(24*time.Hour)] = true
		}
	}
	today = today.UTC().Truncate(24 * time.Hour)
	for d := range set {
		if set[d.AddDate(0, 0, -1)] {
			continue // not the first day of a run
		}
		r := Run{From: d, To: d, Days: 1}
		for set[r.To.AddDate(0, 0, 1)] {
			r.To = r.To.AddDate(0, 0, 1)
			r.Days++
		}
		if r.Days > longest.Days || (r.Days == longest.Days && r.To.After(longest.To)) {
			longest = r
		}
		if r.To.Equal(today) || r.To.Equal(today.AddDate(0, 0, -1)) {
			current = r
		}
	}
	return current, longest
}

func dateRange(r Run) string {
	if r.Days == 0 {
		return "—"
	}
	if r.Days == 1 {
		return r.From.Format("Jan 2, 2006")
	}
	if r.From.Year() == r.To.Year() {
		return r.From.Format("Jan 2") + " – " + r.To.Format("Jan 2, 2006")
	}
	return r.From.Format("Jan 2, 2006") + " – " + r.To.Format("Jan 2, 2006")
}

// StreakCard shows the total, the current streak (in a ring) and the longest streak.
func StreakCard(t Theme, hideBorder bool, username, display string, total int64, since time.Time, current, longest Run) []byte {
	who := name(display, username)
	c := open(t, 495, 195, hideBorder, who+" — contribution streak", "Contribution streak")
	sinceText := "—"
	if !since.IsZero() {
		sinceText = since.Format("Jan 2, 2006") + " – Present"
	}
	cols := []struct {
		x            int
		value, label string
		sub          string
	}{
		{90, num(total), "Total contributions", sinceText},
		{405, fmt.Sprint(longest.Days), "Longest streak", dateRange(longest)},
	}
	for i, col := range cols {
		fmt.Fprintf(&c.b, `<g class="f"%s><text class="b" x="%d" y="100" text-anchor="middle">%s</text>`+
			`<text class="l" x="%d" y="130" text-anchor="middle">%s</text>`+
			`<text class="s" x="%d" y="152" text-anchor="middle">%s</text></g>`,
			delay(i*2), col.x, esc(col.value), col.x, esc(col.label), col.x, esc(col.sub))
	}
	fmt.Fprintf(&c.b, `<line x1="165" y1="65" x2="165" y2="160" stroke="%s"/><line x1="330" y1="65" x2="330" y2="160" stroke="%s"/>`,
		t.Border, t.Border)
	fmt.Fprintf(&c.b, `<g class="f"%s><circle cx="247.5" cy="92" r="34" fill="none" stroke="%s" stroke-width="5"/>`+
		`<text class="b" x="247.5" y="102" text-anchor="middle" style="fill:%s">%d</text>`+
		`<text class="v" x="247.5" y="148" text-anchor="middle" style="fill:%s">Current streak</text>`+
		`<text class="s" x="247.5" y="168" text-anchor="middle">%s</text></g>`,
		delay(1), t.Accent, t.Accent, current.Days, t.Accent, esc(dateRange(current)))
	return c.close()
}

// ActivityCard draws the contribution calendar of [from, to] (from is a
// Sunday) as 7 rows of weeks, like the profile page.
func ActivityCard(t Theme, hideBorder bool, username, display string, days []Day, from, to time.Time, total int64) []byte {
	who := name(display, username)
	from, to = from.UTC().Truncate(24*time.Hour), to.UTC().Truncate(24*time.Hour)
	weeks := int(to.Sub(from).Hours()/24)/7 + 1
	const cell, gap, left, top = 11, 3, 45, 70
	w := left + weeks*(cell+gap) + 22
	h := top + 7*(cell+gap) + 40
	c := open(t, w, h, hideBorder, fmt.Sprintf("%s — %d contributions in the last year", who, total),
		fmt.Sprintf("%s contributions in the last year", num(total)))
	counts := map[time.Time]int64{}
	var top1 int64
	for _, d := range days {
		k := d.Date.UTC().Truncate(24 * time.Hour)
		counts[k] += d.Count
		top1 = max(top1, counts[k])
	}
	// Month labels above the first week that starts in a new month.
	last := -1
	for wk := range weeks {
		d := from.AddDate(0, 0, wk*7)
		if m := int(d.Month()); m != last && d.Day() <= 7 {
			fmt.Fprintf(&c.b, `<text class="s" x="%d" y="%d">%s</text>`, left+wk*(cell+gap), top-8, d.Format("Jan"))
			last = m
		}
	}
	for i, l := range []string{"Mon", "Wed", "Fri"} {
		fmt.Fprintf(&c.b, `<text class="s" x="12" y="%d">%s</text>`, top+(1+2*i)*(cell+gap)+cell-1, l)
	}
	c.b.WriteString(`<g class="f">`)
	for wk := range weeks {
		for wd := range 7 {
			d := from.AddDate(0, 0, wk*7+wd)
			if d.After(to) {
				break
			}
			n := counts[d]
			fmt.Fprintf(&c.b, `<rect x="%d" y="%d" width="%d" height="%d" rx="2" fill="%s"><title>%s: %d</title></rect>`,
				left+wk*(cell+gap), top+wd*(cell+gap), cell, cell, t.Levels[level(n, top1)], d.Format("Jan 2, 2006"), n)
		}
	}
	c.b.WriteString(`</g>`)
	// Legend.
	lx := w - 22 - 5*(cell+gap) - 70
	ly := top + 7*(cell+gap) + 14
	fmt.Fprintf(&c.b, `<text class="s" x="%d" y="%d">Less</text>`, lx, ly+cell-1)
	for i, col := range t.Levels {
		fmt.Fprintf(&c.b, `<rect x="%d" y="%d" width="%d" height="%d" rx="2" fill="%s"/>`, lx+32+i*(cell+gap), ly, cell, cell, col)
	}
	fmt.Fprintf(&c.b, `<text class="s" x="%d" y="%d">More</text>`, lx+32+5*(cell+gap)+4, ly+cell-1)
	return c.close()
}

// level maps a count to one of the 4 non-empty colours, by quarters of the busiest day.
func level(n, top int64) int {
	if n <= 0 || top <= 0 {
		return 0
	}
	return min(4, int(math.Ceil(float64(n)*4/float64(top))))
}

// HoursCard draws the contributions per hour of the day as bars.
func HoursCard(t Theme, hideBorder bool, username, display string, hours [24]int64, tz string) []byte {
	who := name(display, username)
	c := open(t, 495, 195, hideBorder, who+" — productive hours", "Productive hours")
	fmt.Fprintf(&c.b, `<text class="s" x="470" y="35" text-anchor="end">%s</text>`, esc(tz))
	var top1 int64
	for _, n := range hours {
		top1 = max(top1, n)
	}
	const base, height, left, bw = 155, 90, 30, 14
	fmt.Fprintf(&c.b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s"/>`, left-4, base+1, left+24*18, base+1, t.Border)
	c.b.WriteString(`<g class="f">`)
	for h, n := range hours {
		bh := 2.0
		if top1 > 0 && n > 0 {
			bh = math.Max(3, float64(height)*float64(n)/float64(top1))
		}
		fill := t.Track
		if n > 0 {
			fill = t.Accent
		}
		fmt.Fprintf(&c.b, `<rect x="%d" y="%.1f" width="%d" height="%.1f" rx="3" fill="%s"><title>%02d:00 — %d</title></rect>`,
			left+h*18, float64(base)-bh, bw, bh, fill, h, n)
	}
	c.b.WriteString(`</g>`)
	for h := 0; h < 24; h += 3 {
		fmt.Fprintf(&c.b, `<text class="s" x="%d" y="%d" text-anchor="middle">%02d</text>`, left+h*18+bw/2, base+18, h)
	}
	return c.close()
}
