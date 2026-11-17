package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	// Time zone names are validated without relying on the host zoneinfo.
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

const actionProfileDetailsUpdated = "user.profile_details_updated"

// Limits of the public profile details, in runes (URLs in bytes).
const (
	maxBio         = 640
	maxProfileLine = 100
	maxProfileURL  = 255
	maxSocialLinks = 4
	maxTimezone    = 64
)

// ProfileDetails are the public details of a profile: bio, company,
// location, links, the time zone of the local time and the public email.
type ProfileDetails struct {
	Bio         string
	Company     string
	Location    string
	Website     string
	SocialLinks []string
	Timezone    string
	PublicEmail string
}

// ProfileDetails returns the saved details of a user (zero when never saved).
func (s *Service) ProfileDetails(ctx context.Context, userID uuid.UUID) (ProfileDetails, error) {
	row, err := s.q.GetUserProfileDetails(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileDetails{SocialLinks: []string{}}, nil
	}
	if err != nil {
		return ProfileDetails{}, fmt.Errorf("get profile details: %w", err)
	}
	links := row.SocialLinks
	if links == nil {
		links = []string{}
	}
	return ProfileDetails{Bio: row.Bio, Company: row.Company, Location: row.Location, Website: row.Website,
		SocialLinks: links, Timezone: row.Timezone, PublicEmail: row.PublicEmail}, nil
}

// UpdateProfileDetails validates and saves the details. The public email
// must be a verified address of the account.
func (s *Service) UpdateProfileDetails(ctx context.Context, userID uuid.UUID, in ProfileDetails, c Client) (ProfileDetails, error) {
	d := ProfileDetails{Bio: strings.TrimSpace(in.Bio), Company: strings.TrimSpace(in.Company),
		Location: strings.TrimSpace(in.Location), Website: strings.TrimSpace(in.Website),
		Timezone: strings.TrimSpace(in.Timezone), PublicEmail: strings.TrimSpace(in.PublicEmail), SocialLinks: []string{}}
	if utf8.RuneCountInString(d.Bio) > maxBio || strings.ContainsRune(d.Bio, 0) {
		return ProfileDetails{}, fmt.Errorf("%w: bio", ErrInvalidProfile)
	}
	for field, v := range map[string]string{"company": d.Company, "location": d.Location} {
		if utf8.RuneCountInString(v) > maxProfileLine || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return ProfileDetails{}, fmt.Errorf("%w: %s", ErrInvalidProfile, field)
		}
	}
	if d.Website != "" && !webURL(d.Website) {
		return ProfileDetails{}, fmt.Errorf("%w: website must be an absolute http(s) URL", ErrInvalidProfile)
	}
	for _, l := range in.SocialLinks {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !webURL(l) {
			return ProfileDetails{}, fmt.Errorf("%w: social links must be absolute http(s) URLs", ErrInvalidProfile)
		}
		d.SocialLinks = append(d.SocialLinks, l)
	}
	if len(d.SocialLinks) > maxSocialLinks {
		return ProfileDetails{}, fmt.Errorf("%w: at most %d social links", ErrInvalidProfile, maxSocialLinks)
	}
	if d.Timezone != "" {
		// time.LoadLocation also accepts "Local" and file paths; only names are wanted.
		if _, err := time.LoadLocation(d.Timezone); err != nil || len(d.Timezone) > maxTimezone ||
			d.Timezone == "Local" || strings.HasPrefix(d.Timezone, "/") || strings.Contains(d.Timezone, "..") {
			return ProfileDetails{}, fmt.Errorf("%w: unknown time zone", ErrInvalidProfile)
		}
	}
	var emailID uuid.NullUUID
	if d.PublicEmail != "" {
		id, err := s.q.GetVerifiedEmailID(ctx, dbgen.GetVerifiedEmailIDParams{UserID: userID, Address: d.PublicEmail})
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileDetails{}, fmt.Errorf("%w: the public email must be a verified address of the account", ErrInvalidProfile)
		}
		if err != nil {
			return ProfileDetails{}, fmt.Errorf("find public email: %w", err)
		}
		emailID = uuid.NullUUID{UUID: id, Valid: true}
	}
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		err := q.UpsertUserProfileDetails(ctx, dbgen.UpsertUserProfileDetailsParams{UserID: userID, Bio: d.Bio,
			Company: d.Company, Location: d.Location, Website: d.Website, SocialLinks: d.SocialLinks,
			Timezone: d.Timezone, PublicEmailID: emailID})
		if err != nil {
			return fmt.Errorf("save profile details: %w", err)
		}
		return audit.Record(ctx, q, audit.Event{Actor: userID, Action: actionProfileDetailsUpdated, TargetType: "user",
			TargetID: userID.String(), IP: c.IP})
	})
	if err != nil {
		return ProfileDetails{}, err
	}
	return s.ProfileDetails(ctx, userID)
}

// webURL reports whether v is an absolute http or https URL without credentials.
func webURL(v string) bool {
	if len(v) > maxProfileURL {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}
