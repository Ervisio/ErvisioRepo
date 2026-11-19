package credentials

import (
	"errors"
	"slices"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
)

func TestErvisioCodeDeviceScope(t *testing.T) {
	for _, scope := range []string{"", "repo", "workflow", "ervisiocode:device repo", "ervisiocode:device,workflow", "unknown"} {
		if _, err := requestedDeviceScopes(ErvisioCodeClientID, scope); !errors.Is(err, ErrErvisioCodeScope) {
			t.Errorf("scope %q: got %v, want invalid_scope", scope, err)
		}
	}
	scopes, err := requestedDeviceScopes(ErvisioCodeClientID, auth.ScopeErvisioCodeDevice)
	if err != nil || !slices.Equal(scopes, []string{auth.ScopeErvisioCodeDevice}) {
		t.Fatal("the dedicated device scope should be accepted")
	}
	if auth.ValidScope(auth.ScopeErvisioCodeDevice) {
		t.Fatal("ordinary personal tokens must not be allowed to request the reserved scope")
	}
	for _, client := range []string{EgClientID, GitHubCLIClientID} {
		scopes, err := requestedDeviceScopes(client, "")
		if err != nil || !slices.Equal(scopes, auth.DefaultGitHubScopes) {
			t.Errorf("legacy defaults changed for %s", client)
		}
	}
}
