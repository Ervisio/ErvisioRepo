# gitea.com/gitea/runner v1.0.8, patched for ErvisioRepo

Copy of the `gitea.com/gitea/runner` module at v1.0.8 (MIT, see `LICENSE`
and `act/LICENSE`), used through `replace gitea.com/gitea/runner =>
./third_party/gitea-runner` in the root `go.mod`.

Only the `act/` tree is kept, since ErvisioRepo imports nothing else. Removed:
the runner daemon (`main.go`, `internal/`), examples, scripts, tooling and CI
files, `*_test.go` and `testdata/`. `go.mod` and `go.sum` are unchanged.

## Changes

All code changes are marked `Ervisio patch`; new code is in
`act/runner/ervisio.go`.

1. `act/runner/run_context.go` (`GetGithubContext`, `withGithubEnv`): the
   "Adapt to Gitea" block now sets `GITHUB_API_URL` to `<instance>/api/v3`
   and `GITHUB_GRAPHQL_URL` to `<instance>/api/graphql`, ErvisioRepo's
   GitHub-compatible APIs. Upstream points at Gitea's `/api/v1` and blanks
   GraphQL, so octokit, `gh` and similar clients hit the wrong API.
   `withGithubEnv` also honours these three variables from the runner config
   env, as `GetGithubContext` already did.
2. `act/runner/action.go` (`setupActionEnv`): `withGithubEnv` runs again
   after the step's `env:` is merged, so a step could not override
   `GITHUB_SERVER_URL`, `GITHUB_API_URL` or `GITHUB_GRAPHQL_URL`. The values
   the step sets explicitly are now restored afterwards.
3. Same place: for `actions/upload-artifact` and `actions/download-artifact`
   (any ref or host) `GITHUB_SERVER_URL` is set to `https://github.com`
   unless the step sets it. `@actions/artifact` v2 (v4+ of those actions)
   throws `GHESNotSupportedError` when `isGhes()` sees another host; the
   transfer itself uses `ACTIONS_RESULTS_URL` and `ACTIONS_RUNTIME_TOKEN`,
   which point at ErvisioRepo.

## Updating

Copy the new release over this directory, apply the same trimming, re-apply
the changes above and update the version here.
