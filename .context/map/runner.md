# runner
Purpose: ervisio-runner, an outbound-only daemon that long-polls the server for Actions tasks and executes each one with the act library from gitea.com/gitea/runner, streaming logs and state back.
Entry points: cmd/ervisio-runner/main.go:dispatch (subcommands register, daemon), internal/runner/daemon.go:Daemon.Run, internal/runner/job.go:executor.execute, internal/runner/report/reporter.go:Reporter, internal/runner/register.go:Register, internal/runner/config/labels.go:ParseLabel, api/runner/v1/runner.proto
Flow: register (errt_ token in body) → State file {id,credential,labels,ephemeral} → daemon: declare labels → pollLoop (capacity slots, FetchTask long-poll, backoff on error) → start → executor.execute: generateWorkflow (single-job payload plus finished placeholders for needs) → PlanJob → PickPlatform(label, runs-on) → act runner with GithubContext built from task.Context → Reporter (logrus hook) batches UpdateLog/UpdateTask → Close sends final result. Heartbeat loop runs separately and carries running ids; the reply lists cancel_task_ids.
Invariants:
- Runner only dials out; all calls are ConnectRPC under APIPath /api/actions/runner (client.go). Only Register uses the registration token; everything else uses "Bearer erun_" from State.
- Job token (erj_) arrives inside Task, is exposed as GITHUB_TOKEN / ERVISIO_TOKEN secrets, never used on the runner service.
- Every secret value is masked by the Reporter replacer before upload; keep new secrets (e.g. ACTIONS_RUNTIME_TOKEN) in the secrets map, not plain env.
- A payload must hold exactly one job; task id and repository are regex-validated before use (path safety).
- Label wire form is name:scheme://image or name:host; host backend runs only if cfg.Host.Enabled, else the job fails.
- Ephemeral runner takes one task then stops fetching.
- Jobs run on a context detached from the daemon ctx; they are cancelled only after shutdown_timeout grace.
- Output limits: key 255 bytes, value 1 MiB (report.maxOutput*), also enforced by the server.
Gotchas:
- act hardcodes GITHUB_API_URL=<server_url>/api/v1 and blanks graphql_url, so the server must alias /api/v1 to /api/v3 (the GitHub-compatible API); without it actions calling the API 404.
- go.mod marks gitea.com/gitea/runner, actions-proto-go and others as // indirect although they are imported directly; do not "fix" with go mod tidy without noting it in the report (CLAUDE.md rule 9).
- job.go, workflow.go and report/ are ported from gitea/runner (MIT); keep the license header and upstream behaviours (e.g. isJobStepEntry log routing) when editing.
- Generated protobuf lives in internal/runnerapi/runnerv1 (package path in the proto go_package); regenerate with make generate (buf), never hand-edit.
- act logging goes through logrus; configureLogrus in daemon.go silences/redirects it, and nullLogger stops act writing job logs elsewhere.
- DefaultActionInstance is forced to https://github.com so `uses: owner/action` resolves from GitHub unless the workflow overrides it.
Related: map/deploy.md (runner images, packaging/systemd), server-side Actions code is not mapped here.
