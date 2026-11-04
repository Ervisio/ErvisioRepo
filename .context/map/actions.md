# actions
Purpose: Actions server (internal/actions). Turns repo events and manual triggers into workflow runs (planned from YAML), queues jobs for runners, receives runner reports and logs, serves the REST/UI read and control API, and archives logs.
Entry points:
- internal/actions/service.go: New(Options), Wire(Deps), VerifyRuntimeToken, PublicURL, errors (ErrNotFound/ErrForbidden/ErrDisabled/ErrNotWired/ErrConflict, ValidationError).
- Queries/control (api.go, all take who auth.Principal): ListWorkflows, ListRuns, GetRun, RunWorkflow, ListJobs, GetJob, ReadLog, WaitLog, RawLog, CancelRun, CancelJob, RerunRun, ApproveRun, ApproveDeployment, Badge.
- Triggers (trigger.go): Dispatch (workflow_dispatch), RepositoryDispatch, EventHandler (bus handler, events.KindX list in EventKinds), Listen (notification loop, started by wireActions).
- Secrets/vars/envs/runners (secrets.go, runnersvc.go): Scope builders InstanceScope/OwnerScope/RepoScope; Set/List/Delete Secret, Variable; ListEnvironments/PutEnvironment/DeleteEnvironment; Runners() *RunnerService; ListRunners, ListRepoRunners, DeleteRunner, CreateRegistrationToken, LookupJobToken (auth.JobTokenLookup).
- RunnerHandler() (http.go): ConnectRPC RunnerService under /api/actions/runner/. Long polls: no request timeout.
- JobsOption() (workers.go): river workers and periodic jobs (report, archive, sweep, schedule).
- Wiring: cmd/ervisio-repo/actions.go setupActions (nil when actions.enabled=false, uses security.master_key_file), wireActions (Wire, bus.Register("actions.trigger", EventHandler, EventKinds...), go Listen). serve.go passes JobsOption into jobs, RunnerHandler to httpapi Deps.ActionsRunner, and LookupJobToken into auth stores.
Data flow: event/dispatch → trigger.go builds event (fork flag, NeedsApproval) → state.go start → plan.go createRun parses workflow YAML (expressions via expr.go, matrix/needs/reusable calls expandCall), inserts job rows (concurrency, permissions, timeout) → advance/advanceOne (state.go) admits ready jobs → runner FetchTask/claim (ClaimActionsJob matches runner labels and scope; job token erj_ stored as hash) → Task payload with secrets/variables/runtime JWT → UpdateTask/UpdateLog/Heartbeat (runnersvc.go) → river report job → completeJob → archiveLog (gzip to storage class "actions", logs.go) → readLog serves DB rows or archive.
Token kinds:
- errt_ registration token (hash stored, TTL actions.registration_token_ttl, default 1h), used only by Register.
- erun_ runner credential (hash stored), all later runner calls; Ephemeral runner stops after one job.
- erj_ job token (auth.JobTokenPrefix), hash stored per job; grants a repo read/write token derived from workflow permissions; fork PRs downgrade write to read.
- Runtime JWT (tokens.go signRuntimeToken): HMAC-SHA256 with key HKDF(master key), scp "Actions.Results:<runID>:<jobID>", ttl = job timeout + runtimeTokenGrace (1h). Without master key the key is random per process (tokens die on restart).
Invariants:
- Repo-level reads use repoFor(ActRead); mutating controls check authz.Allowed; manageRepo needs ActSettings; grants (who.Grant != nil) are rejected for settings and scope management; instance scope needs admin.
- Deployment approval: canApprove (admins, collaborators, listed reviewers, or settings holders when no reviewers). ApproveRun: writers approve fork PR runs with NeedsApproval.
- Secrets are write-only in the API: Secret struct has name/scope/env/times, no value. Values are sealed (sealer: per-secret AES-256-GCM DEK, wrapped by KEK from HKDF of master key; AAD = scope, scope id, env, name).
- Secrets and variables are resolved per job by scope rank (repo/env/owner) and passed to runners; fork pull_request runs get no secrets (secrets() returns empty when run.Fork).
- pull_request_target runs the base ref and its workflow (from base commit), not the fork's; pull_request on a fork needs approval for outside contributors.
- Events without RepoID or ActorID never start workflows (no loops from Actions' own events).
- Secrets masked in logs by per-job replacer (masks) before storage; job log rows capped at maxLogRowBytes (64 KiB).
- Runner heartbeat/offline: runners silent for runner_offline_after (default 2m) marked offline; jobs whose runner vanished after twice that are failed by sweep (appendServerLog + completeJob failure).
- Jobs timeout: min(timeout-minutes, max_job_timeout (default 6h)); jobs with no timeout use max.
- Max jobs per run: actions.max_jobs_per_run (default 512), matrix included.
- Archive: completed job logs moved to storage after completion; sweep archives lost-job logs after 15 min.
Config (internal/config/config.go, section [actions]): enabled (default true), default_actions_url ("github" | "mirror-only", default "github"), max_job_timeout (6h), runner_offline_after (2m), registration_token_ttl (1h), max_jobs_per_run (512).
Gotchas:
- act hardcodes GITHUB_API_URL=<server>/api/v1; httpapi/router.go jobAPIAlias rewrites /api/v1/ requests carrying an erj_ token to /api/v3 (ghapi.Prefix). Other /api/v1 traffic is untouched. Do not remove the alias.
- Without security.master_key_file: secrets cannot be stored (ErrNoMasterKey; secrets() skips sealed secrets), and runtime tokens use a random key.
- Actions are a separate river-driven pipeline: do not start jobs or run plan logic from HTTP handlers; enqueue via s.enqueue.
- Fork/permission checks happen in service methods, not in the router; a new endpoint must call repoFor or manageRepo.
- Events are dispatched by bus registration in serve.go (actions.trigger): a new event kind must be added to actions.EventKinds or it never triggers workflows.
- sqlc queries live in internal/db/queries/actions.sql (CLAUDE.md rule 5); new SQL goes there.
Related: map/runner.md (ervisio-runner client side, act library, reporter), map/repo-core.md (events bus, jobs, storage).
