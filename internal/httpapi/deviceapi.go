package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// deviceGrantType is the grant_type of a device flow token request.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// maxOAuthBody bounds the body of the OAuth endpoints.
const maxOAuthBody = 16 << 10

// mountDeviceFlow mounts the GitHub-compatible OAuth device flow endpoints
// (RFC 8628). They take form-encoded or JSON bodies and answer JSON when
// the client accepts it, form-encoded otherwise, as GitHub does. They use
// no cookies, so they need no CSRF protection. The approval page
// /login/device is an SPA route backed by /api/v1/user/device/{userCode}.
func mountDeviceFlow(r chi.Router, d Deps) {
	if d.Credentials == nil {
		return
	}
	creds := d.Credentials
	r.Post("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		f, ok := oauthForm(w, r)
		if !ok {
			return
		}
		ip := auth.ClientIP(r, d.Auth.TrustedProxies)
		start, err := creds.StartDevice(r.Context(), f.Get("client_id"), f.Get("scope"), f.Get("device_name"), ip)
		if err != nil {
			writeDeviceError(w, r, d, err, deviceCodeStatus(err))
			return
		}
		writeOAuth(w, r, http.StatusOK, []oauthField{
			{"device_code", start.DeviceCode},
			{"user_code", start.UserCode},
			{"verification_uri", strings.TrimRight(d.Meta.PublicURL, "/") + "/login/device"},
			{"expires_in", start.ExpiresIn},
			{"interval", start.Interval},
		})
	})
	r.Post("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		f, ok := oauthForm(w, r)
		if !ok {
			return
		}
		if f.Get("grant_type") != deviceGrantType {
			writeDeviceError(w, r, d, credentials.ErrUnsupportedGrant, http.StatusOK)
			return
		}
		ip := auth.ClientIP(r, d.Auth.TrustedProxies)
		tok, err := creds.PollDevice(r.Context(), f.Get("client_id"), f.Get("device_code"), ip)
		if err != nil {
			// GitHub answers the OAuth errors of the token endpoint with 200.
			writeDeviceError(w, r, d, err, http.StatusOK)
			return
		}
		writeOAuth(w, r, http.StatusOK, []oauthField{
			{"access_token", tok.AccessToken},
			{"token_type", "bearer"},
			{"scope", tok.Scope},
		})
	})
}

// deviceCodeStatus is the HTTP status of a failed device code request.
func deviceCodeStatus(err error) int {
	switch {
	case errors.Is(err, credentials.ErrIncorrectClient):
		return http.StatusUnauthorized
	case errors.Is(err, credentials.ErrDeviceRateLimited):
		return http.StatusTooManyRequests
	}
	return http.StatusBadRequest
}

type oauthField struct {
	key   string
	value any
}

// oauthForm parses a form-encoded or JSON body.
func oauthForm(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBody)
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		var m map[string]any
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			writeOAuth(w, r, http.StatusBadRequest, []oauthField{{"error", "invalid_request"},
				{"error_description", "The request body is not valid JSON."}})
			return nil, false
		}
		out := url.Values{}
		for k, v := range m {
			switch v := v.(type) {
			case string:
				out.Set(k, v)
			case float64:
				out.Set(k, strconv.FormatFloat(v, 'f', -1, 64))
			}
		}
		// Query parameters are accepted too, as on GitHub.
		for k, v := range r.URL.Query() {
			if !out.Has(k) && len(v) > 0 {
				out.Set(k, v[0])
			}
		}
		return out, true
	}
	if err := r.ParseForm(); err != nil {
		writeOAuth(w, r, http.StatusBadRequest, []oauthField{{"error", "invalid_request"},
			{"error_description", "The request body is not a valid form."}})
		return nil, false
	}
	return r.Form, true
}

func writeDeviceError(w http.ResponseWriter, r *http.Request, d Deps, err error, status int) {
	var de *credentials.DeviceError
	if !errors.As(err, &de) {
		d.Log.ErrorContext(r.Context(), "oauth device flow", "path", r.URL.Path, "error", err)
		writeOAuth(w, r, http.StatusInternalServerError, []oauthField{{"error", "server_error"},
			{"error_description", "Internal server error."}})
		return
	}
	fields := []oauthField{{"error", de.Code}, {"error_description", de.Description}}
	if de.Interval > 0 {
		fields = append(fields, oauthField{"interval", de.Interval})
	}
	writeOAuth(w, r, status, fields)
}

// writeOAuth answers JSON when the client accepts it, form-encoded
// otherwise.
func writeOAuth(w http.ResponseWriter, r *http.Request, status int, fields []oauthField) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		var b strings.Builder
		b.WriteByte('{')
		for i, f := range fields {
			if i > 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(f.key)
			v, _ := json.Marshal(f.value)
			b.Write(k)
			b.WriteByte(':')
			b.Write(v)
		}
		b.WriteByte('}')
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(b.String()))
		return
	}
	v := url.Values{}
	for _, f := range fields {
		switch x := f.value.(type) {
		case string:
			v.Set(f.key, x)
		case int:
			v.Set(f.key, strconv.Itoa(x))
		}
	}
	w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(v.Encode()))
}

// ---- /api/v1/user/device/{userCode} ----

func (p problemResponse) VisitGetDeviceAuthorizationResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDecideDeviceAuthorizationResponse(w http.ResponseWriter) error {
	return p.write(w)
}

// deviceProblem maps the errors of the device flow approval.
func deviceProblem(err error) (problemResponse, bool) {
	var lim *credentials.ErrDeviceLimited
	switch {
	case err == nil:
		return problemResponse{}, false
	case errors.As(err, &lim):
		return problemResponse{status: http.StatusTooManyRequests, detail: lim.Error(),
			retryAfter: retryAfterSeconds(lim.Wait)}, true
	case errors.Is(err, credentials.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "unknown or expired code"}, true
	}
	return problemResponse{}, false
}

func deviceDTO(d credentials.DeviceRequest) v1gen.DeviceAuthorization {
	out := v1gen.DeviceAuthorization{UserCode: d.UserCode, ClientId: d.ClientID, AppName: d.AppName,
		RequestedScopes: nonNil(d.RequestedScopes), Scopes: nonNil(d.Scopes), CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt}
	if d.RequestIP != "" {
		ip := d.RequestIP
		out.RequestIp = &ip
	}
	return out
}

func (s *v1Server) GetDeviceAuthorization(ctx context.Context, req v1gen.GetDeviceAuthorizationRequestObject) (v1gen.GetDeviceAuthorizationResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	d, err := s.creds.LookupDevice(ctx, p.UserID, req.UserCode, clientOf(ctx).IP)
	if pr, ok := deviceProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetDeviceAuthorization200JSONResponse(deviceDTO(d)), nil
}

func (s *v1Server) DecideDeviceAuthorization(ctx context.Context, req v1gen.DecideDeviceAuthorizationRequestObject) (v1gen.DecideDeviceAuthorizationResponseObject, error) {
	if s.creds == nil {
		return errCredentialsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	err := s.creds.DecideDevice(ctx, p.UserID, req.UserCode, req.Body.Approve, clientOf(ctx).IP)
	if pr, ok := deviceProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.DecideDeviceAuthorization204Response{}, nil
}
