package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	// githubAPI is where the releases are listed (unauthenticated: 60
	// requests per hour per address, the check runs every few hours).
	githubAPI = "https://api.github.com"
	// maxReleasesBody bounds the release list answer.
	maxReleasesBody = 4 << 20
	// maxNotes bounds the release notes kept per release.
	maxNotes = 16 << 10
	// keepReleases bounds the releases stored after a check.
	keepReleases = 15
)

// Release is a published release of the server.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"publishedAt"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// fetchReleases lists the published (not draft, not pre-release) releases
// of repo with an x.y.z tag, newest version first.
func (s *Service) fetchReleases(ctx context.Context) ([]Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	url := githubAPI + "/repos/" + s.o.Repository + "/releases?per_page=30"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ErvisioRepo/"+s.o.Current)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("list releases: GitHub rate limit reached (HTTP %d), retry later", resp.StatusCode)
		}
		return nil, fmt.Errorf("list releases: GitHub answered HTTP %d", resp.StatusCode)
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleasesBody)).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	out := make([]Release, 0, len(list))
	for _, r := range list {
		v, ok := parseVersion(r.TagName)
		if r.Draft || r.Prerelease || !ok || !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		notes := r.Body
		if len(notes) > maxNotes {
			notes = strings.ToValidUTF8(notes[:maxNotes], "") + "\n…"
		}
		out = append(out, Release{Version: v.String(), Name: strings.TrimSpace(r.Name), URL: r.HTMLURL,
			Notes: notes, PublishedAt: r.PublishedAt})
	}
	slices.SortFunc(out, func(a, b Release) int {
		va, _ := parseVersion(a.Version)
		vb, _ := parseVersion(b.Version)
		switch {
		case vb.less(va):
			return -1
		case va.less(vb):
			return 1
		}
		return 0
	})
	out = slices.CompactFunc(out, func(a, b Release) bool { return a.Version == b.Version })
	if len(out) > keepReleases {
		out = out[:keepReleases]
	}
	return out, nil
}
