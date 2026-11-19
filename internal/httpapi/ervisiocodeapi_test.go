package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

func TestErvisioCodeDeviceHTTP(t *testing.T) {
	h, env := credentialsRouter(t)
	admin := withAdmin(t, h)
	client := newBrowser(t, h, "198.51.100.20")
	const accountPath = "/api/v1/user/ervisiocode"
	const devicesPath = accountPath + "/devices"
	const remotePath = accountPath + "/remote"

	start := func(scope string) *httptest.ResponseRecorder {
		return client.do(http.MethodPost, "/login/device/code", map[string]string{
			"client_id": credentials.ErvisioCodeClientID, "scope": scope, "device_name": "Laptop",
		}, acceptOAuthJSON)
	}
	for _, scope := range []string{"", "repo workflow", "ervisiocode:device repo"} {
		if result := readOAuthJSON(t, start(scope), http.StatusBadRequest); result["error"] != "invalid_scope" {
			t.Errorf("scope %q: expected invalid_scope", scope)
		}
	}
	poll := func(clientID, code string) map[string]string {
		return readOAuthJSON(t, client.do(http.MethodPost, "/login/oauth/access_token", map[string]string{
			"client_id": clientID, "device_code": code, "grant_type": deviceGrantType,
		}, acceptOAuthJSON), http.StatusOK)
	}
	enroll := func(checkBinding bool) string {
		t.Helper()
		var code struct {
			Device string `json:"device_code"`
			User   string `json:"user_code"`
			URI    string `json:"verification_uri"`
		}
		rec := start(auth.ScopeErvisioCodeDevice)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &code) != nil ||
			code.Device == "" || code.User == "" || code.URI != "https://git.example.com/login/device" {
			t.Fatal("failed to start device authorization")
		}
		if checkBinding && poll(credentials.EgClientID, code.Device)["error"] != "incorrect_client_credentials" {
			t.Fatal("wrong OAuth client could exchange the device code")
		}
		path := "/api/v1/user/device/" + code.User
		rec = admin.do(http.MethodGet, path, nil)
		var pending v1gen.DeviceAuthorization
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &pending) != nil ||
			pending.ClientId != credentials.ErvisioCodeClientID || pending.AppName != "ErvisioCode" ||
			!slices.Equal(pending.Scopes, []string{auth.ScopeErvisioCodeDevice}) {
			t.Fatal("unexpected approval request")
		}
		withoutCSRF := admin.clone()
		withoutCSRF.noCSRF = true
		body := map[string]bool{"approve": true}
		wantHTTPStatus(t, withoutCSRF.do(http.MethodPost, path, body), http.StatusForbidden)
		wantHTTPStatus(t, admin.do(http.MethodPost, path, body), http.StatusNoContent)
		result := poll(credentials.ErvisioCodeClientID, code.Device)
		if result["access_token"] == "" || result["scope"] != auth.ScopeErvisioCodeDevice {
			t.Fatal("device exchange did not return a restricted credential")
		}
		if poll(credentials.ErvisioCodeClientID, code.Device)["error"] != "expired_token" {
			t.Fatal("consumed device code could be exchanged again")
		}
		return result["access_token"]
	}

	wantHTTPStatus(t, admin.do(http.MethodGet, accountPath, nil), http.StatusUnauthorized)
	wantHTTPStatus(t, client.do(http.MethodGet, devicesPath, nil), http.StatusUnauthorized)
	pat, _ := createToken(t, admin, "ordinary", auth.ScopeReadUser)
	wantHTTPStatus(t, client.do(http.MethodGet, accountPath, nil, bearer(pat)), http.StatusForbidden)
	wantHTTPStatus(t, client.do(http.MethodGet, devicesPath, nil, bearer(pat)), http.StatusForbidden)
	online := map[string]bool{"active": true}
	wantHTTPStatus(t, admin.do(http.MethodPut, remotePath, online), http.StatusUnauthorized)
	wantHTTPStatus(t, client.prime().do(http.MethodPut, remotePath, online), http.StatusUnauthorized)
	wantHTTPStatus(t, client.do(http.MethodPut, remotePath, online, bearer(pat)), http.StatusForbidden)

	first, second := enroll(true), enroll(false)
	identity := func(token string) v1gen.ErvisioCodeAccount {
		t.Helper()
		rec := client.do(http.MethodGet, accountPath, nil, bearer(token))
		wantHTTPStatus(t, rec, http.StatusOK)
		var account v1gen.ErvisioCodeAccount
		if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil ||
			account.AccountId == uuid.Nil || account.Device.Id == uuid.Nil || account.Username != "boss" ||
			account.Device.LastUsedAt == nil {
			t.Fatal("missing account or device metadata")
		}
		return account
	}
	a, b := identity(first), identity(second)
	if a.AccountId != b.AccountId || a.Device.Id == b.Device.Id {
		t.Fatal("each credential must have a distinct device ID and the same account ID")
	}
	for _, path := range []string{"/api/v1/user", "/api/v1/meta", "/api/v1/user/tokens"} {
		wantHTTPStatus(t, client.do(http.MethodGet, path, nil, bearer(first)), http.StatusForbidden)
	}
	if _, err := (&auth.Tokens{Store: env.Queries}).Lookup(context.Background(), first); !errors.Is(err, auth.ErrNoToken) {
		t.Fatal("the default, non-v1 token lookup must refuse device credentials")
	}

	rec := admin.do(http.MethodGet, devicesPath, nil)
	wantHTTPStatus(t, rec, http.StatusOK)
	var devices []v1gen.ErvisioCodeDevice
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil || len(devices) != 2 || devices[0].Id != b.Device.Id {
		t.Fatal("list should contain only current device registrations, newest first")
	}
	wantHTTPStatus(t, client.do(http.MethodGet, devicesPath, nil, bearer(first)), http.StatusOK)
	wantHTTPStatus(t, client.do(http.MethodDelete, devicesPath+"/"+b.Device.Id.String(), nil, bearer(first)), http.StatusForbidden)
	wantHTTPStatus(t, admin.do(http.MethodDelete, "/api/v1/user/tokens/"+b.Device.Id.String(), nil), http.StatusNotFound)

	for _, invalid := range []any{
		nil, json.RawMessage(`null`), json.RawMessage(`{}`),
		json.RawMessage(`{"active":null}`), json.RawMessage(`{"active":"true"}`),
		map[string]any{"active": true, "deviceId": b.Device.Id},
	} {
		wantHTTPStatus(t, client.do(http.MethodPut, remotePath, invalid, bearer(first)), http.StatusBadRequest)
	}
	checkPresence := func(want bool) {
		t.Helper()
		rec := admin.do(http.MethodGet, devicesPath, nil)
		wantHTTPStatus(t, rec, http.StatusOK)
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("presence must not be cached")
		}
		var listed []v1gen.ErvisioCodeDevice
		if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 2 {
			t.Fatal("missing device registrations")
		}
		if listed[0].Remote != nil || listed[1].Remote == nil || listed[1].Remote.Online != want {
			t.Fatalf("expected only the reporting device to have presence, online=%t", want)
		}
	}
	wantHTTPStatus(t, client.do(http.MethodPut, remotePath, online, bearer(first)), http.StatusNoContent)
	checkPresence(true)
	wantHTTPStatus(t, client.do(http.MethodPut, remotePath, map[string]bool{"active": false}, bearer(first)), http.StatusNoContent)
	checkPresence(false)

	invite := admin.do(http.MethodPost, "/api/v1/admin/invites", map[string]string{})
	wantHTTPStatus(t, invite, http.StatusCreated)
	var inv struct{ Token string }
	if err := json.Unmarshal(invite.Body.Bytes(), &inv); err != nil || inv.Token == "" {
		t.Fatal("could not read invite")
	}
	other := newBrowser(t, h, "198.51.100.22").prime()
	wantHTTPStatus(t, other.do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": "alice", "email": "alice@example.com", "password": testPassword, "inviteToken": inv.Token,
	}), http.StatusAccepted)
	wantHTTPStatus(t, other.login("alice", testPassword), http.StatusOK)
	rec = other.do(http.MethodGet, devicesPath, nil)
	wantHTTPStatus(t, rec, http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil || len(devices) != 0 {
		t.Fatal("another user must not see these devices")
	}
	wantHTTPStatus(t, other.do(http.MethodDelete, devicesPath+"/"+b.Device.Id.String(), nil), http.StatusNotFound)

	wantHTTPStatus(t, admin.do(http.MethodDelete, devicesPath+"/"+b.Device.Id.String(), nil), http.StatusNoContent)
	wantHTTPStatus(t, client.do(http.MethodGet, accountPath, nil, bearer(second)), http.StatusUnauthorized)
	wantHTTPStatus(t, client.do(http.MethodDelete, devicesPath+"/"+a.Device.Id.String(), nil, bearer(first)), http.StatusNoContent)
	wantHTTPStatus(t, client.do(http.MethodGet, accountPath, nil, bearer(first)), http.StatusUnauthorized)
	wantHTTPStatus(t, client.do(http.MethodPut, remotePath, online, bearer(first)), http.StatusUnauthorized)
}

func acceptOAuthJSON(r *http.Request) {
	r.Header.Set("Accept", "application/json")
}

func readOAuthJSON(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]string {
	t.Helper()
	wantHTTPStatus(t, rec, want)
	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid OAuth response: %v", err)
	}
	return result
}

func wantHTTPStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("unexpected HTTP status: got %d, want %d", rec.Code, want)
	}
}
