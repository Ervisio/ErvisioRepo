package runner

import "strings"

// Ervisio patches (see third_party/gitea-runner/PATCHES.md).

const (
	// ervisioAPIPath is ErvisioRepo's GitHub-compatible REST API.
	ervisioAPIPath = "/api/v3"
	// ervisioGraphQLPath is ErvisioRepo's GitHub-compatible GraphQL API.
	ervisioGraphQLPath = "/api/graphql"
)

// serverEnvKeys are the variables withGithubEnv forces on every step and
// that a step's own env: may override.
var serverEnvKeys = []string{"GITHUB_SERVER_URL", "GITHUB_API_URL", "GITHUB_GRAPHQL_URL"}

// githubArtifactActions are the actions/* repos built on @actions/artifact.
var githubArtifactActions = map[string]bool{
	"upload-artifact":   true,
	"download-artifact": true,
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// keepStepServerEnv returns the server URL variables the step sets in its
// own env:, with the values already interpolated by setupEnv.
func keepStepServerEnv(step actionStep) map[string]string {
	declared := step.getStepModel().Environment()
	env := *step.getEnv()
	kept := map[string]string{}
	for _, k := range serverEnvKeys {
		if _, ok := declared[k]; ok {
			kept[k] = env[k]
		}
	}
	return kept
}

// applyServerEnv restores the step's explicit server URLs after
// withGithubEnv and points the artifact actions at github.com.
//
// @actions/artifact v2 (upload-artifact / download-artifact v4+) calls
// isGhes() on GITHUB_SERVER_URL and throws GHESNotSupportedError for any
// host other than github.com, *.ghe.com or *.localhost. It never uses that
// URL afterwards: the twirp calls go to ACTIONS_RESULTS_URL with
// ACTIONS_RUNTIME_TOKEN, both served by ErvisioRepo, so claiming
// github.com only gets past the check.
func applyServerEnv(env, kept map[string]string, ra *remoteAction) {
	if ra != nil && strings.EqualFold(ra.Org, "actions") && githubArtifactActions[strings.ToLower(ra.Repo)] {
		env["GITHUB_SERVER_URL"] = "https://github.com"
	}
	for k, v := range kept {
		env[k] = v
	}
}
