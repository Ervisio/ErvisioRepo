# GitHub compatible REST API (`/api/v3`)

ErvisioRepo serves a subset of the GitHub REST API under `/api/v3`, the
path GitHub Enterprise Server uses. Scripts, `go-github`, `octokit` and
`gh api` can point at an ErvisioRepo instance as if it were a GitHub
Enterprise host. The native API stays at `/api/v1`; `/api/v3` is a
compatibility layer over the same services and the same permissions.

GraphQL is **not** implemented: `POST /api/graphql` answers `501` with a
GitHub-shaped error.

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
| `GET /repos/{o}/{r}/commits` | partial | `sha` and `path`; `author`, `since`, `until` are ignored. |
| `GET /repos/{o}/{r}/commits/{ref}` | partial | With `stats` and `files`. The ref cannot contain `/`; use the SHA. `author`/`committer` users are `null`. Diff and patch media types are not supported. |
| `GET /repos/{o}/{r}/branches` | partial | `protected` is always `false`; branch protection is managed with `/api/v1`. |
| `GET /repos/{o}/{r}/branches/{branch}` | partial | As above. |
| `GET /repos/{o}/{r}/tags` | yes | |
| `GET /repos/{o}/{r}/git/ref/{ref}` | yes | |
| `GET /repos/{o}/{r}/git/refs[/{prefix}]` | yes | Exact match answers an object, a prefix a list. |
| `GET /repos/{o}/{r}/git/matching-refs/{prefix}` | yes | |
| `POST/PATCH/DELETE /repos/{o}/{r}/git/refs` | no | Use `git push`. |
| Git data (`git/blobs`, `git/trees`, `git/commits`, `git/tags`) | no | |

### Pull requests

| Endpoint | Status | Notes |
|---|---|---|
| `GET /repos/{o}/{r}/pulls` | yes | `state`, `head` (`user:branch`), `base`, `sort=created/updated`. |
| `POST /repos/{o}/{r}/pulls` | partial | `title`, `body`, `head` (`branch` or `owner:branch`), `head_repo`, `base`, `draft`. Converting an issue (`issue`) is not supported. |
| `GET /repos/{o}/{r}/pulls/{n}` | yes | With `mergeable`, `mergeable_state`, `commits`, `additions`, `deletions`, `changed_files`. |
| `PATCH /repos/{o}/{r}/pulls/{n}` | yes | `title`, `body`, `state`, `base`, `draft`. |
| `GET .../pulls/{n}/commits` | yes | |
| `GET .../pulls/{n}/files` | partial | Files have no blob `sha` and no `patch`. |
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
| `PUT .../pulls/{n}/update-branch` | no | |

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

ErvisioRepo has no CI integration yet, so these endpoints answer empty
results and clients see "no checks".

| Endpoint | Status | Notes |
|---|---|---|
| `GET .../commits/{ref}/status` | stub | `state: pending`, no statuses. |
| `GET .../commits/{ref}/statuses`, `GET .../statuses/{sha}` | stub | Empty list. |
| `POST .../statuses/{sha}` | no | `501`. |
| `GET .../commits/{ref}/check-runs`, `.../check-suites` | stub | `total_count: 0`. |
| `POST .../check-runs`, `POST .../check-suites` | no | `501`. |

### Not available

Search, gists, notifications, events, actions, packages, projects,
deployments, webhooks (use `/api/v1/.../hooks`), collaborators management,
forks, teams, apps and installations.

## Using `gh`

`gh` treats any host other than github.com as GitHub Enterprise and calls
`https://<host>/api/v3/` and `https://<host>/api/graphql`. Most `gh`
subcommands rely on GraphQL, so only a part of them works. `gh auth login`
reads the user name over GraphQL and fails; set the token in the environment
instead:

```sh
export GH_HOST=git.example.com
export GH_ENTERPRISE_TOKEN=<access token>
gh api repos/alice/demo/pulls
```

Pass `-R git.example.com/owner/repo` to skip the repository lookup from git
remotes, which can use GraphQL.

| Command | Expected to work | Why |
|---|---|---|
| `gh api <REST path>` (also `--paginate`) | yes | Plain REST with `Link` pagination. |
| `gh release view`, `upload`, `download`, `delete`, `delete-asset` | yes | REST. |
| `gh release create` | partial | REST, but some versions check the tag over GraphQL first. |
| `gh release list` | no | GraphQL. |
| `gh label create`, `edit`, `delete` | yes | REST. |
| `gh label list`, `clone` | no | GraphQL. |
| `gh auth login`, `gh auth status` | no | GraphQL. |
| `gh repo view/create/clone/fork/list` | no | GraphQL (`gh repo clone` works with a full git URL, it is plain git). |
| `gh pr ...`, `gh issue ...` (all) | no | GraphQL. Use `gh api` with the endpoints above. |
| `gh run`, `gh workflow`, `gh search`, `gh gist`, `gh codespace` | no | Not implemented. |

These are expectations from the `gh` source and the endpoints implemented;
they have not been exercised in an automated test.
