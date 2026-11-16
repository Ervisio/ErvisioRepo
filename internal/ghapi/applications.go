package ghapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/credentials"
)

// OAuthApps serves the OAuth app token endpoints (satisfied by
// *credentials.Service).
type OAuthApps interface {
	CheckAppToken(ctx context.Context, clientID, secret, token string) (credentials.AppToken, error)
	ResetAppToken(ctx context.Context, clientID, secret, token, ip string) (credentials.AppToken, error)
	DeleteAppToken(ctx context.Context, clientID, secret, token, ip string) error
	DeleteAppGrant(ctx context.Context, clientID, secret, token, ip string) error
}

// applicationsPrefix is the path of the OAuth app endpoints; they
// authenticate with the client id and secret (HTTP Basic), not a token.
const applicationsPrefix = Prefix + "/applications/"

type ghAuthorizationApp struct {
	ClientID string `json:"client_id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

// ghAuthorization is the authorization object of /applications/{client_id}/token.
type ghAuthorization struct {
	ID             int64              `json:"id"`
	URL            string             `json:"url"`
	Scopes         []string           `json:"scopes"`
	Token          string             `json:"token"`
	TokenLastEight string             `json:"token_last_eight"`
	HashedToken    string             `json:"hashed_token"`
	App            ghAuthorizationApp `json:"app"`
	Note           *string            `json:"note"`
	NoteURL        *string            `json:"note_url"`
	Fingerprint    *string            `json:"fingerprint"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	ExpiresAt      *time.Time         `json:"expires_at"`
	User           *ghUser            `json:"user"`
	Installation   *struct{}          `json:"installation"`
}

func (s *Server) applicationRoutes(r chi.Router) {
	r.Post("/applications/{client_id}/token", s.h((*call).checkAppToken))
	r.Patch("/applications/{client_id}/token", s.h((*call).resetAppToken))
	r.Delete("/applications/{client_id}/token", s.h((*call).deleteAppToken))
	r.Delete("/applications/{client_id}/grant", s.h((*call).deleteAppGrant))
}

// appRequest reads the client credentials and the access_token body of an
// /applications request.
func (c *call) appRequest() (clientID, secret, token string, err error) {
	if c.s.d.OAuthApps == nil {
		return "", "", "", errNotFound
	}
	clientID = chi.URLParam(c.r, "client_id")
	user, pass, ok := c.r.BasicAuth()
	if !ok {
		return "", "", "", errAuth
	}
	if user != clientID {
		return "", "", "", errNotFound
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.decode(&body); err != nil {
		return "", "", "", err
	}
	if body.AccessToken == "" {
		return "", "", "", errf(http.StatusUnprocessableEntity, "Validation Failed: access_token is required")
	}
	return clientID, pass, body.AccessToken, nil
}

// appErr maps the errors of the OAuth app service; wrong client
// credentials and unknown tokens are 404, as on GitHub.
func appErr(err error) error {
	if errors.Is(err, credentials.ErrAppCredentials) || errors.Is(err, credentials.ErrNotFound) {
		return errNotFound
	}
	return err
}

func (c *call) authorizationObj(t credentials.AppToken, token string) ghAuthorization {
	sum := sha256.Sum256([]byte(token))
	id := c.ids.of(t.ID)
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return ghAuthorization{ID: id, URL: c.s.api + "/authorizations/" + strconv.FormatInt(id, 10), Scopes: scopes, Token: token,
		TokenLastEight: t.Hint, HashedToken: hex.EncodeToString(sum[:]),
		App:       ghAuthorizationApp{ClientID: t.App.ClientID, Name: t.App.Name, URL: t.App.HomepageURL},
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, ExpiresAt: t.ExpiresAt,
		User: c.userObj(t.UserID, "user", t.Username)}
}

func (c *call) checkAppToken() error {
	clientID, secret, token, err := c.appRequest()
	if err != nil {
		return err
	}
	t, err := c.s.d.OAuthApps.CheckAppToken(c.ctx, clientID, secret, token)
	if err != nil {
		return appErr(err)
	}
	return c.ok(http.StatusOK, c.authorizationObj(t, token))
}

func (c *call) resetAppToken() error {
	clientID, secret, token, err := c.appRequest()
	if err != nil {
		return err
	}
	t, err := c.s.d.OAuthApps.ResetAppToken(c.ctx, clientID, secret, token, c.client().IP)
	if err != nil {
		return appErr(err)
	}
	return c.ok(http.StatusOK, c.authorizationObj(t, t.Token))
}

func (c *call) deleteAppToken() error {
	clientID, secret, token, err := c.appRequest()
	if err != nil {
		return err
	}
	if err := c.s.d.OAuthApps.DeleteAppToken(c.ctx, clientID, secret, token, c.client().IP); err != nil {
		return appErr(err)
	}
	return c.noContent()
}

func (c *call) deleteAppGrant() error {
	clientID, secret, token, err := c.appRequest()
	if err != nil {
		return err
	}
	if err := c.s.d.OAuthApps.DeleteAppGrant(c.ctx, clientID, secret, token, c.client().IP); err != nil {
		return appErr(err)
	}
	return c.noContent()
}
