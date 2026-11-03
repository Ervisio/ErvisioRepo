# GitHub compatible REST API (`/api/v3`)

ErvisioRepo serves a subset of the GitHub REST API under `/api/v3`, the
path GitHub Enterprise Server uses. Scripts, `go-github`, `octokit` and
`gh api` can point at an ErvisioRepo instance as if it were a GitHub
Enterprise host. The native API stays at `/api/v1`; `/api/v3` is a
compatibility layer over the same services and the same permissions.

GraphQL is served at `/api/graphql` (and `/api/v3/graphql`) for the subset
that `gh` uses; see [GraphQL](#graphql-apigraphql).

## Conventions

- **Authentication**: `Authorization: token <pat>`, `Bearer <pat>`, or Basic
  with the access token as password. Anonymous requests see public
  repositories only. An invalid token is refused with `401 Bad credentials`.
  Token scopes are checked: reads need `repo:read`, writes need `repo:write`.
- **URLs**: every `url`, `html_url` and `*_url` field is built from
  `server.public_url`. API URLs point at `/api/v3`, HTML URLs at the web UI.
- **Ids**: GitHub clients expect numeric ids. ErvisioRepo gives every object a
  stable 64-bit id on first use (table `ghapi_ids`, migration 00175). Both the
  numeric id and the native UUID are accepted in paths. `node_id` is an opaque
  string.
- **Pagination**: `page` (from 1) and `per_page` (default 30, max 100), with a
  `Link` header (`next`, `prev`, `first`). There is no `last` link and no
  total count.
- **Errors**: `{"message": "...", "documentation_url": "...", "status": "..."}`
  with the GitHub status codes (`404` also for repositories the caller cannot
  see, `422` for validation errors, `409` for conflicts). This is the
  documented exception to the `application/problem+json` rule of the rest of
  the API. A renamed or transferred repository answers `301` to the new path.
- **Timestamps**: RFC 3339 in UTC.

## Compatibility table

Legend: **yes** supported; **partial** supported with the limits listed;
**stub** answers a fixed, valid response; **no** not available (`404`/`405`)
or `501`.

### Meta and users

| Endpoint | Status | Notes |
|---|---|---|
| `GET /` | yes | Root with the URL templates. |
| `GET /meta` | stub | `verifiable_password_authentication: false`, empty IP lists. |
| `GET /rate_limit` | stub | Fixed 5000/5000; no rate limiting is applied. |
| `GET /user` | yes | Signed-in user. Needs a token. |
| `GET /user/orgs` | yes | |
| `GET /users/{username}` | yes | Users and organizations. |
| `GET /orgs/{org}` | yes | |

### Repositories

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{owner}/{repo}` | yes | `permissions` reflects the caller. `ssh_url` uses the host of the public URL. |
| `GET /user/repos` | yes | Repositories of the caller. |
| `GET /users/{username}/repos`, `GET /orgs/{org}/repos` | yes | |
| `POST /user/repos`, `POST /orgs/{org}/repos` | partial | `name`, `description`, `private`/`visibility`. `auto_init`, templates and gitignore are ignored. |
| `PATCH /repos/{owner}/{repo}` | partial | `name`, `description`, `private`/`visibility`, `default_branch`. |
| `DELETE /repos/{owner}/{repo}` | yes | |

### Contents, commits, branches, refs

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{o}/{r}/contents/{path}` | yes | File (base64), directory listing, symlink. `ref` query. `Accept: application/vnd.github.raw` streams the raw file. Submodules are listed as `submodule`. |
| `PUT /repos/{o}/{r}/contents/{path}` | partial | Create (`201`) or update with `sha` (`200`), `branch`. `committer` and `author` are ignored: the commit is authored by the token owner. |
| `DELETE /repos/{o}/{r}/contents/{path}` | yes | `sha` required. |
| `GET /repos/{o}/{r}/readme[/{dir}]` | partial | The README of the directory (the one the code browser shows: first of `README.md`, `.markdown`, `.mdown`, `.rst`, `.adoc`, `.txt`, plain `README`, case-insensitive), answered like a contents file. `ref` query. `Accept: application/vnd.github.raw` streams it; `application/vnd.github.html` is not supported (JSON is returned). `404` when there is none. |
| `GET /repos/{o}/{r}/commits` | partial | `sha` and `path`; `author`, `since`, `until` are ignored. |
| `GET /repos/{o}/{r}/commits/{ref}` | partial | With `stats` and `files`. The ref may contain `/` (`feature/x`); a ref whose last segment is `status`, `statuses`, `check-runs` or `check-suites` selects that sub-resource, as on GitHub. `author`/`committer` users are `null`. Diff and patch media types are not supported. |
| `GET /repos/{o}/{r}/branches` | partial | `protected` is always `false`; branch protection is managed with `/api/v1`. |
| `GET /repos/{o}/{r}/branches/{branch}` | partial | As above. |
| `GET /repos/{o}/{r}/tags` | yes | |
| `GET /repos/{o}/{r}/git/ref/{ref}` | yes | |
| `GET /repos/{o}/{r}/git/refs[/{prefix}]` | yes | Exact match answers an object, a prefix a list. |
| `GET /repos/{o}/{r}/git/matching-refs/{prefix}` | yes | |
| `POST /repos/{o}/{r}/git/refs` | partial | `ref` and `sha` (full id); `201` with the ref. Only `refs/heads/` and `refs/tags/` (a tag may point to an annotated tag object). Existing ref or unknown object: `422`. |
| `PATCH /repos/{o}/{r}/git/refs/{ref}` | yes | `sha` and `force`. Not a fast-forward without `force`: `422 Update is not a fast forward`. |
| `DELETE /repos/{o}/{r}/git/refs/{ref}` | partial | `204`. Missing ref: `422`. The default branch cannot be deleted. |
| Ref writes | | Need push permission and the `repo:write` scope; they run through the same hooks as a push, so branch and tag protection, the journal, events and webhooks apply. A refused update answers `422`. |
| Git data (`git/blobs`, `git/trees`, `git/commits`, `git/tags`) | no | |

### Pull requests

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{o}/{r}/pulls` | yes | `state`, `head` (`user:branch`), `base`, `sort=created/updated`. |
| `POST /repos/{o}/{r}/pulls` | partial | `title`, `body`, `head` (`branch` or `owner:branch`), `head_repo`, `base`, `draft`. Converting an issue (`issue`) is not supported. |
| `GET /repos/{o}/{r}/pulls/{n}` | yes | With `mergeable`, `mergeable_state`, `commits`, `additions`, `deletions`, `changed_files`, `merged_by`. `Accept: application/vnd.github.diff` answers the unified diff from the merge base, `application/vnd.github.patch` the commits as a `format-patch` series (merge commits left out); `406` when the diff is over the limits or the series has more than 250 commits. |
| `PATCH /repos/{o}/{r}/pulls/{n}` | yes | `title`, `body`, `state`, `base`, `draft`. |
| `GET .../pulls/{n}/commits` | yes | |
| `GET .../pulls/{n}/files` | yes | Blob `sha` and `patch` from the merge base diff; `patch` is missing for binary files and over the diff limits. |
| `GET .../pulls/{n}/merge` | yes | `204` merged, `404` not merged. |
| `PUT .../pulls/{n}/merge` | yes | `merge_method` merge/squash/rebase, `commit_title`, `commit_message`, `sha`. Branch protection applies. |
| `GET/POST .../pulls/{n}/reviews` | partial | Comments in a review need `line` (and `side`/`start_line`); the legacy `position` is refused. |
| `GET/DELETE .../pulls/{n}/reviews/{id}` | yes | Delete works on pending reviews only. |
| `POST .../pulls/{n}/reviews/{id}/events` | yes | Submit a pending review. |
| `PUT .../pulls/{n}/reviews/{id}/dismissals` | yes | |
| `GET .../pulls/{n}/reviews/{id}/comments` | yes | |
| `GET/POST .../pulls/{n}/comments` | partial | Same `line` rule; replies with `in_reply_to`. |
| `PATCH/DELETE .../pulls/comments/{id}` | yes | `pull_request_url` has no number in these answers. |
| `GET /repos/{o}/{r}/pulls/comments` | no | Repository-wide listing. |
| `GET/POST/DELETE .../pulls/{n}/requested_reviewers` | partial | Users only; `team_reviewers` is refused. |
| `PUT .../pulls/{n}/update-branch` | yes | Merges the base branch into the head branch as the caller (write access to the head repository, branch protection of the head applies); `expected_head_sha` is checked. Answers `202` when done, `422` when already up to date, conflicting or the head moved. |

### Issues, labels, milestones

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{o}/{r}/issues` | partial | `state`, `labels`, `milestone`, `assignee`, `creator`, `mentioned`, `sort`, `direction`. **Pull requests are not included** (GitHub includes them); use `/pulls`. `since` is ignored. |
| `GET /repos/{o}/{r}/issues/{n}` | yes | Also answers pull request numbers, with `pull_request`. |
| `POST /repos/{o}/{r}/issues` | yes | `title`, `body`, `labels`, `assignees`/`assignee`, `milestone`. |
| `PATCH /repos/{o}/{r}/issues/{n}` | yes | Also `state` and `state_reason`. |
| `GET/POST .../issues/{n}/comments` | yes | |
| `GET/PATCH/DELETE .../issues/comments/{id}` | yes | |
| `GET /repos/{o}/{r}/issues/comments` | no | Repository-wide listing. |
| `GET/POST/PUT/DELETE .../issues/{n}/labels`, `DELETE .../labels/{name}` | yes | |
| `POST/DELETE .../issues/{n}/assignees` | yes | |
| `GET /repos/{o}/{r}/assignees`, `GET .../assignees/{user}` | yes | Collaborators of the repository. |
| `GET/POST /repos/{o}/{r}/labels`, `GET/PATCH/DELETE .../labels/{name}` | yes | `new_name` renames. |
| `GET/POST /repos/{o}/{r}/milestones`, `GET/PATCH/DELETE .../milestones/{n}` | yes | The milestone `number` equals its numeric `id`. |
| Issue events, timeline, reactions endpoints, lock | no | Reaction counts are included in issue and comment objects. |

### Releases

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{o}/{r}/releases` | yes | Drafts only for users who can manage the repository. |
| `POST /repos/{o}/{r}/releases` | partial | `tag_name`, `target_commitish`, `name`, `body`, `draft`, `prerelease`. `generate_release_notes` is refused; `make_latest` is ignored. |
| `GET .../releases/latest`, `GET .../releases/tags/{tag}` | yes | |
| `GET/PATCH/DELETE .../releases/{id}` | yes | |
| `GET .../releases/{id}/assets` | yes | |
| `POST {public_url}/api/uploads/repos/{o}/{r}/releases/{id}/assets?name=&label=` | yes | The `upload_url` of a release. Raw body, `Content-Length` required (`411`). Also accepted under `/api/v3`. |
| `GET .../releases/assets/{id}` | yes | JSON, or the file with `Accept: application/octet-stream` (single `Range` supported). |
| `PATCH/DELETE .../releases/assets/{id}` | yes | `name`, `label`. |
| `POST .../releases/generate-notes` | no | |

### Statuses and checks

Commit statuses and check runs are stored per commit; the latest status of
each context and the latest run of each name count as required status
checks of branch protection (runs pass with `success`, `neutral` or
`skipped`) and re-evaluate auto-merge. A check suite groups the runs of a
commit by reporter (`app`, the caller's username): ErvisioRepo has no GitHub
Apps, so `app.slug` and `app.name` are that name.

| Endpoint | Status | Notes |
|---|---|---|
| `GET .../commits/{ref}/status` | yes | Latest status of each context; `state` is `failure`, `pending` (also with no statuses) or `success`. Check runs are not included, as on GitHub. |
| `GET .../commits/{ref}/statuses`, `GET .../statuses/{ref}` | yes | Newest first. |
| `POST .../statuses/{ref}` | yes | `state`, `target_url` (http/https), `description`, `context`. At most 1000 statuses per commit and context. Unknown commit: `422`. |
| `POST .../check-runs` | partial | `name`, `head_sha` (also a branch or tag), `status`, `conclusion`, `started_at`, `completed_at`, `details_url`, `external_id`, `output` (`title`, `summary`, `text`, `annotations`; `images` ignored). `actions` is ignored. |
| `GET/PATCH .../check-runs/{id}` | yes | `PATCH` keeps omitted fields and appends annotations (50 per request, 1000 per run). |
| `GET .../check-runs/{id}/annotations` | yes | |
| `GET .../commits/{ref}/check-runs` | yes | `check_name`, `status`, `filter` (`latest`, default, or `all`); `app_id` is ignored. |
| `GET .../commits/{ref}/check-suites` | yes | `app_slug` filter; `app_id` and `check_name` are ignored. |
| `GET .../check-suites/{id}`, `GET .../check-suites/{id}/check-runs` | yes | |
| `POST .../check-suites` | no | `501`: a suite is created with its first check run. Re-requests and the check suite preferences are not available. |

### Not available

Search, gists, notifications, events, actions, packages, projects,
deployments, webhooks (use `/api/v1/.../hooks`), collaborators management,
forks, teams, apps and installations.

## GraphQL (`/api/graphql`)

`POST /api/graphql` and `POST /api/v3/graphql` serve the subset of the GitHub
GraphQL API that `gh` uses (`internal/ghapi/graphql`). The request is
`{"query", "variables", "operationName"}`; the answer is `{"data"}` and, when
something failed, `{"errors":[{"type","message","path"}]}` (`NOT_FOUND`,
`FORBIDDEN`, `UNPROCESSABLE`, ...) with partial data, as GitHub does. It uses
the same services and permission checks as `/api/v3`. Global ids are the
`node_id` values of the REST answers.

- **Queries**: `viewer`, `repository(owner, name)`, `repositoryOwner(login)`,
  `node(id)`, `nodes(ids)`, `search(query, type: ISSUE)` with the
  `repo:`, `is:`, `state:`, `author:`, `assignee:`, `mentions:`, `label:`,
  `milestone:`, `base:`, `head:`, `no:`, `sort:` and
  `review-requested:` qualifiers (`@me` works; `type: REPOSITORY` is limited),
  `__typename` and a minimal
  `__type`/`__schema` introspection (field names only, used by `gh` for
  feature detection).
- **Objects**: `User`, `Organization`, `Repository` (branches, `ref`, labels,
  milestones, releases, issues, pull requests, collaborators), `Issue`,
  `PullRequest` (commits with status check rollup, reviews, review requests,
  files, merge state), `IssueComment`, `Label`, `Milestone`, `Release`, `Ref`,
  `Commit`, connections with `first`, `last` and `after` cursors and
  `totalCount`.
- **Mutations**: `createIssue`, `updateIssue`, `closeIssue`, `reopenIssue`,
  `addComment`, `updateIssueComment`, `deleteIssueComment`,
  `createPullRequest`, `updatePullRequest`, `closePullRequest`,
  `reopenPullRequest`, `mergePullRequest`, `markPullRequestReadyForReview`,
  `convertPullRequestToDraft`, `requestReviews` (users only),
  `addPullRequestReview`, `enablePullRequestAutoMerge`,
  `disablePullRequestAutoMerge`, `addLabelsToLabelable`,
  `removeLabelsFromLabelable`, `addAssigneesToAssignable`,
  `removeAssigneesFromAssignable`.

Reading needs a token with `repo:read`, mutations need `repo:write`. Every
authenticated answer under `/api` carries `X-OAuth-Scopes` with the classic
GitHub names of the token scopes (`repo:write` is `repo`, `user:read` is
`read:user, read:org`, `packages:read` is `read:packages`, `actions:write` is
`workflow`, `admin` is `admin:org`).

## Using `gh`

`gh` treats any host other than github.com as GitHub Enterprise and calls
`https://<host>/api/v3/` and `https://<host>/api/graphql`. `gh` checks that
the token has the `repo` and `read:org` scopes, so use a token with
`repo:write` and `user:read`.

```sh
gh auth login --hostname git.example.com --with-token < token.txt
gh auth status --hostname git.example.com
gh pr list -R git.example.com/alice/demo
```

`GH_HOST=git.example.com` and `GH_ENTERPRISE_TOKEN=<token>` in the
environment work as well. Inside a clone whose remote points at the host,
`-R` is not needed.

| Command | Expected to work | Notes |
|---|---|---|
| `gh auth login --hostname`, `gh auth status` | yes | `GET /api/v3/` with `X-OAuth-Scopes`, then `viewer { login }`. |
| `gh repo view` | yes | |
| `gh repo list` | yes | `repositoryOwner { repositories }`. |
| `gh repo clone` | yes | Plain git. |
| `gh repo create`, `fork`, `edit`, `delete` | no | Not implemented over GraphQL. |
| `gh pr list`, `view`, `status` | yes | `status` uses `search` with `author:@me` and `review-requested:@me`. |
| `gh pr create` (also `--draft`, `--label`, `--assignee`, `--reviewer` users) | yes | Team reviewers and projects are refused. |
| `gh pr checkout` | yes | Fetches the head branch from the base repository. |
| `gh pr merge` (`--merge`, `--squash`, `--rebase`, `--auto`) | yes | `--delete-branch` deletes the head branch with `DELETE /git/refs` (subject to branch protection). Merge queue not supported. |
| `gh pr comment`, `close`, `reopen`, `ready`, `edit`, `review` | yes | |
| `gh pr checks` | partial | Status check rollup from the statuses service; no workflow run links. |
| `gh pr diff` (also `--patch`, `--name-only`) | yes | Diff and patch media types of `GET /pulls/{number}`. |
| `gh issue list`, `view`, `create`, `close`, `reopen`, `comment`, `edit` | yes | Projects are ignored. |
| `gh issue status` | yes | `search` with `repo:`. |
| `gh release list` | yes | GraphQL `releases`. |
| `gh release view`, `create`, `upload`, `download`, `delete`, `delete-asset` | yes | REST. |
| `gh label list` | yes | GraphQL `labels`. |
| `gh label create`, `edit`, `delete`, `clone` | yes | REST. |
| `gh api <REST path>` (also `--paginate`), `gh api graphql` | yes | |
| `gh run`, `gh workflow`, `gh search`, `gh gist`, `gh codespace`, `gh project` | no | Not implemented (`search` needs a `repo:` qualifier). |

These are expectations from the `gh` source and the endpoints implemented;
they have not been exercised in an automated test.
