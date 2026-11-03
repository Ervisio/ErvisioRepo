# API REST compatibile con GitHub (`/api/v3`)

ErvisioRepo espone un sottoinsieme della API REST di GitHub sotto `/api/v3`,
lo stesso percorso di GitHub Enterprise Server. Script, `go-github`,
`octokit` e `gh api` possono usare un'istanza ErvisioRepo come se fosse un
host GitHub Enterprise. La API nativa resta su `/api/v1`; `/api/v3` è uno
strato di compatibilità sopra gli stessi servizi e gli stessi permessi.

GraphQL è servito su `/api/graphql` (e `/api/v3/graphql`) per il
sottoinsieme usato da `gh`; vedi [GraphQL](#graphql-apigraphql).

## Convenzioni

- **Autenticazione**: `Authorization: token <pat>`, `Bearer <pat>` oppure
  Basic con il token come password. Le richieste anonime vedono solo i
  repository pubblici. Un token non valido riceve `401 Bad credentials`. Gli
  scope del token sono verificati: le letture richiedono `repo:read`, le
  scritture `repo:write`.
- **URL**: ogni campo `url`, `html_url` e `*_url` è costruito da
  `server.public_url`. Gli URL API puntano a `/api/v3`, quelli HTML alla UI web.
- **Id**: i client GitHub si aspettano id numerici. ErvisioRepo assegna a ogni
  oggetto un id stabile a 64 bit al primo uso (tabella `ghapi_ids`, migrazione
  00175). Nei percorsi si accettano sia l'id numerico sia l'UUID nativo.
  `node_id` è una stringa opaca.
- **Paginazione**: `page` (da 1) e `per_page` (predefinito 30, massimo 100),
  con header `Link` (`next`, `prev`, `first`). Non ci sono il link `last` né
  il conteggio totale.
- **Errori**: `{"message": "...", "documentation_url": "...", "status": "..."}`
  con i codici di GitHub (`404` anche per i repository che il chiamante non
  vede, `422` per gli errori di validazione, `409` per i conflitti). È
  l'eccezione documentata alla regola `application/problem+json` del resto
  della API. Un repository rinominato o trasferito risponde `301` verso il
  nuovo percorso.
- **Date**: RFC 3339 in UTC.

## Tabella di compatibilità

Legenda: **sì** supportato; **parziale** supportato con i limiti indicati;
**stub** risposta fissa ma valida; **no** non disponibile (`404`/`405`) o
`501`.

### Meta e utenti

| Endpoint | Stato | Note |
|---|---|---|
| `GET /` | sì | Radice con i modelli di URL. |
| `GET /meta` | stub | `verifiable_password_authentication: false`, liste IP vuote. |
| `GET /rate_limit` | stub | Fisso 5000/5000; non si applica alcun limite. |
| `GET /user` | sì | Utente autenticato. Richiede un token. |
| `GET /user/orgs` | sì | |
| `GET /users/{username}` | sì | Utenti e organizzazioni. |
| `GET /orgs/{org}` | sì | |

### Repository

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{owner}/{repo}` | sì | `permissions` riflette il chiamante. `ssh_url` usa l'host dell'URL pubblico. |
| `GET /user/repos` | sì | Repository del chiamante. |
| `GET /users/{username}/repos`, `GET /orgs/{org}/repos` | sì | |
| `POST /user/repos`, `POST /orgs/{org}/repos` | parziale | `name`, `description`, `private`/`visibility`. `auto_init`, template e gitignore sono ignorati. |
| `PATCH /repos/{owner}/{repo}` | parziale | `name`, `description`, `private`/`visibility`, `default_branch`. |
| `DELETE /repos/{owner}/{repo}` | sì | |

### Contenuti, commit, branch, ref

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/contents/{path}` | sì | File (base64), elenco di directory, symlink. Query `ref`. `Accept: application/vnd.github.raw` restituisce il file grezzo in streaming. I submodule sono elencati come `submodule`. |
| `PUT /repos/{o}/{r}/contents/{path}` | parziale | Creazione (`201`) o aggiornamento con `sha` (`200`), `branch`. `committer` e `author` sono ignorati: l'autore del commit è il proprietario del token. |
| `DELETE /repos/{o}/{r}/contents/{path}` | sì | `sha` obbligatorio. |
| `GET /repos/{o}/{r}/commits` | parziale | `sha` e `path`; `author`, `since`, `until` sono ignorati. |
| `GET /repos/{o}/{r}/commits/{ref}` | parziale | Con `stats` e `files`. Il ref non può contenere `/`: usare lo SHA. Gli utenti `author`/`committer` sono `null`. I media type diff e patch non sono supportati. |
| `GET /repos/{o}/{r}/branches` | parziale | `protected` è sempre `false`; la protezione dei branch si gestisce con `/api/v1`. |
| `GET /repos/{o}/{r}/branches/{branch}` | parziale | Come sopra. |
| `GET /repos/{o}/{r}/tags` | sì | |
| `GET /repos/{o}/{r}/git/ref/{ref}` | sì | |
| `GET /repos/{o}/{r}/git/refs[/{prefix}]` | sì | Una corrispondenza esatta restituisce un oggetto, un prefisso una lista. |
| `GET /repos/{o}/{r}/git/matching-refs/{prefix}` | sì | |
| `POST/PATCH/DELETE /repos/{o}/{r}/git/refs` | no | Usare `git push`. |
| Dati git (`git/blobs`, `git/trees`, `git/commits`, `git/tags`) | no | |

### Pull request

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/pulls` | sì | `state`, `head` (`user:branch`), `base`, `sort=created/updated`. |
| `POST /repos/{o}/{r}/pulls` | parziale | `title`, `body`, `head` (`branch` o `owner:branch`), `head_repo`, `base`, `draft`. La conversione di una issue (`issue`) non è supportata. |
| `GET /repos/{o}/{r}/pulls/{n}` | sì | Con `mergeable`, `mergeable_state`, `commits`, `additions`, `deletions`, `changed_files`. |
| `PATCH /repos/{o}/{r}/pulls/{n}` | sì | `title`, `body`, `state`, `base`, `draft`. |
| `GET .../pulls/{n}/commits` | sì | |
| `GET .../pulls/{n}/files` | parziale | I file non hanno `sha` del blob né `patch`. |
| `GET .../pulls/{n}/merge` | sì | `204` se fusa, `404` altrimenti. |
| `PUT .../pulls/{n}/merge` | sì | `merge_method` merge/squash/rebase, `commit_title`, `commit_message`, `sha`. Si applica la protezione dei branch. |
| `GET/POST .../pulls/{n}/reviews` | parziale | I commenti di una review richiedono `line` (e `side`/`start_line`); il vecchio `position` è rifiutato. |
| `GET/DELETE .../pulls/{n}/reviews/{id}` | sì | L'eliminazione vale solo per le review in sospeso. |
| `POST .../pulls/{n}/reviews/{id}/events` | sì | Invia una review in sospeso. |
| `PUT .../pulls/{n}/reviews/{id}/dismissals` | sì | |
| `GET .../pulls/{n}/reviews/{id}/comments` | sì | |
| `GET/POST .../pulls/{n}/comments` | parziale | Stessa regola su `line`; risposte con `in_reply_to`. |
| `PATCH/DELETE .../pulls/comments/{id}` | sì | In queste risposte `pull_request_url` non contiene il numero. |
| `GET /repos/{o}/{r}/pulls/comments` | no | Elenco su tutto il repository. |
| `GET/POST/DELETE .../pulls/{n}/requested_reviewers` | parziale | Solo utenti; `team_reviewers` è rifiutato. |
| `PUT .../pulls/{n}/update-branch` | no | |

### Issue, label, milestone

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/issues` | parziale | `state`, `labels`, `milestone`, `assignee`, `creator`, `mentioned`, `sort`, `direction`. **Le pull request non sono incluse** (GitHub le include): usare `/pulls`. `since` è ignorato. |
| `GET /repos/{o}/{r}/issues/{n}` | sì | Risponde anche per i numeri delle pull request, con `pull_request`. |
| `POST /repos/{o}/{r}/issues` | sì | `title`, `body`, `labels`, `assignees`/`assignee`, `milestone`. |
| `PATCH /repos/{o}/{r}/issues/{n}` | sì | Anche `state` e `state_reason`. |
| `GET/POST .../issues/{n}/comments` | sì | |
| `GET/PATCH/DELETE .../issues/comments/{id}` | sì | |
| `GET /repos/{o}/{r}/issues/comments` | no | Elenco su tutto il repository. |
| `GET/POST/PUT/DELETE .../issues/{n}/labels`, `DELETE .../labels/{name}` | sì | |
| `POST/DELETE .../issues/{n}/assignees` | sì | |
| `GET /repos/{o}/{r}/assignees`, `GET .../assignees/{user}` | sì | Collaboratori del repository. |
| `GET/POST /repos/{o}/{r}/labels`, `GET/PATCH/DELETE .../labels/{name}` | sì | `new_name` rinomina. |
| `GET/POST /repos/{o}/{r}/milestones`, `GET/PATCH/DELETE .../milestones/{n}` | sì | Il `number` della milestone coincide con il suo `id` numerico. |
| Eventi, timeline, endpoint delle reaction, lock | no | I conteggi delle reaction sono inclusi negli oggetti issue e commento. |

### Release

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/releases` | sì | Le bozze solo per chi può gestire il repository. |
| `POST /repos/{o}/{r}/releases` | parziale | `tag_name`, `target_commitish`, `name`, `body`, `draft`, `prerelease`. `generate_release_notes` è rifiutato; `make_latest` è ignorato. |
| `GET .../releases/latest`, `GET .../releases/tags/{tag}` | sì | |
| `GET/PATCH/DELETE .../releases/{id}` | sì | |
| `GET .../releases/{id}/assets` | sì | |
| `POST {public_url}/api/uploads/repos/{o}/{r}/releases/{id}/assets?name=&label=` | sì | È l'`upload_url` di una release. Corpo grezzo, `Content-Length` obbligatorio (`411`). Accettato anche sotto `/api/v3`. |
| `GET .../releases/assets/{id}` | sì | JSON, oppure il file con `Accept: application/octet-stream` (supporta un singolo `Range`). |
| `PATCH/DELETE .../releases/assets/{id}` | sì | `name`, `label`. |
| `POST .../releases/generate-notes` | no | |

### Stati e check

Gli stati dei commit e i check run sono salvati per commit; l'ultimo stato
di ogni context e l'ultimo run di ogni nome contano come status check
richiesti dalla protezione dei branch (un run passa con `success`, `neutral`
o `skipped`) e rivalutano l'auto-merge. Una check suite raggruppa i run di
un commit per mittente (`app`, lo username di chi chiama): ErvisioRepo non
ha GitHub App, quindi `app.slug` e `app.name` sono quel nome.

| Endpoint | Stato | Note |
|---|---|---|
| `GET .../commits/{ref}/status` | sì | L'ultimo stato di ogni context; `state` è `failure`, `pending` (anche senza stati) o `success`. I check run non sono inclusi, come su GitHub. |
| `GET .../commits/{ref}/statuses`, `GET .../statuses/{ref}` | sì | Dal più recente. |
| `POST .../statuses/{ref}` | sì | `state`, `target_url` (http/https), `description`, `context`. Al massimo 1000 stati per commit e context. Commit sconosciuto: `422`. |
| `POST .../check-runs` | parziale | `name`, `head_sha` (anche un branch o un tag), `status`, `conclusion`, `started_at`, `completed_at`, `details_url`, `external_id`, `output` (`title`, `summary`, `text`, `annotations`; `images` ignorato). `actions` è ignorato. |
| `GET/PATCH .../check-runs/{id}` | sì | `PATCH` mantiene i campi omessi e aggiunge le annotazioni (50 per richiesta, 1000 per run). |
| `GET .../check-runs/{id}/annotations` | sì | |
| `GET .../commits/{ref}/check-runs` | sì | `check_name`, `status`, `filter` (`latest`, predefinito, o `all`); `app_id` è ignorato. |
| `GET .../commits/{ref}/check-suites` | sì | Filtro `app_slug`; `app_id` e `check_name` sono ignorati. |
| `GET .../check-suites/{id}`, `GET .../check-suites/{id}/check-runs` | sì | |
| `POST .../check-suites` | no | `501`: una suite nasce con il suo primo check run. Le ri-richieste e le preferenze delle check suite non sono disponibili. |

### Non disponibili

Ricerca, gist, notifiche, eventi, actions, package, project, deployment,
webhook (usare `/api/v1/.../hooks`), gestione dei collaboratori, fork, team,
app e installazioni.

## GraphQL (`/api/graphql`)

`POST /api/graphql` e `POST /api/v3/graphql` espongono il sottoinsieme della
API GraphQL di GitHub usato da `gh` (`internal/ghapi/graphql`). La richiesta è
`{"query", "variables", "operationName"}`; la risposta è `{"data"}` e, in caso
di errore, `{"errors":[{"type","message","path"}]}` (`NOT_FOUND`,
`FORBIDDEN`, `UNPROCESSABLE`, ...) con i dati parziali, come fa GitHub. Usa
gli stessi servizi e gli stessi controlli dei permessi di `/api/v3`. Gli id
globali sono i valori `node_id` delle risposte REST.

- **Query**: `viewer`, `repository(owner, name)`, `repositoryOwner(login)`,
  `node(id)`, `nodes(ids)`, `search(query, type: ISSUE)` con i qualificatori
  `repo:`, `is:`, `state:`, `author:`, `assignee:`, `mentions:`, `label:`,
  `milestone:`, `base:`, `head:`, `no:`, `sort:` e `review-requested:`
  (`@me` funziona; `type: REPOSITORY` è limitato), `__typename` e una
  introspezione minima `__type`/`__schema` (solo i nomi dei campi, che `gh`
  usa per capire quali funzioni sono disponibili).
- **Oggetti**: `User`, `Organization`, `Repository` (branch, `ref`, label,
  milestone, release, issue, pull request, collaboratori), `Issue`,
  `PullRequest` (commit con lo status check rollup, review, richieste di
  review, file, stato del merge), `IssueComment`, `Label`, `Milestone`,
  `Release`, `Ref`, `Commit`, connection con i cursori `first`, `last` e
  `after` e `totalCount`.
- **Mutation**: `createIssue`, `updateIssue`, `closeIssue`, `reopenIssue`,
  `addComment`, `updateIssueComment`, `deleteIssueComment`,
  `createPullRequest`, `updatePullRequest`, `closePullRequest`,
  `reopenPullRequest`, `mergePullRequest`, `markPullRequestReadyForReview`,
  `convertPullRequestToDraft`, `requestReviews` (solo utenti),
  `addPullRequestReview`, `enablePullRequestAutoMerge`,
  `disablePullRequestAutoMerge`, `addLabelsToLabelable`,
  `removeLabelsFromLabelable`, `addAssigneesToAssignable`,
  `removeAssigneesFromAssignable`.

La lettura richiede un token con `repo:read`, le mutation `repo:write`. Ogni
risposta autenticata sotto `/api` porta l'header `X-OAuth-Scopes` con i nomi
classici di GitHub degli scope del token (`repo:write` diventa `repo`,
`user:read` diventa `read:user, read:org`, `packages:read` diventa
`read:packages`, `actions:write` diventa `workflow`, `admin` diventa
`admin:org`).

## Usare `gh`

`gh` tratta ogni host diverso da github.com come GitHub Enterprise e chiama
`https://<host>/api/v3/` e `https://<host>/api/graphql`. `gh` verifica che il
token abbia gli scope `repo` e `read:org`, quindi serve un token con
`repo:write` e `user:read`.

```sh
gh auth login --hostname git.example.com --with-token < token.txt
gh auth status --hostname git.example.com
gh pr list -R git.example.com/alice/demo
```

Funzionano anche `GH_HOST=git.example.com` e `GH_ENTERPRISE_TOKEN=<token>`
nell'ambiente. In un clone il cui remote punta all'host, `-R` non serve.

| Comando | Funziona | Note |
|---|---|---|
| `gh auth login --hostname`, `gh auth status` | sì | `GET /api/v3/` con `X-OAuth-Scopes`, poi `viewer { login }`. |
| `gh repo view` | sì | Il README non viene mostrato (manca la rotta `/readme`). |
| `gh repo list` | sì | `repositoryOwner { repositories }`. |
| `gh repo clone` | sì | Git puro. |
| `gh repo create`, `fork`, `edit`, `delete` | no | Non implementati via GraphQL. |
| `gh pr list`, `view`, `status` | sì | `status` usa `search` con `author:@me` e `review-requested:@me`. |
| `gh pr create` (anche `--draft`, `--label`, `--assignee`, `--reviewer` utenti) | sì | Team come reviewer e project vengono rifiutati. |
| `gh pr checkout` | sì | Scarica il branch head dal repository base. |
| `gh pr merge` (`--merge`, `--squash`, `--rebase`, `--auto`) | sì | `--delete-branch` fallisce dopo il merge (manca `DELETE /git/refs`). Merge queue non supportata. |
| `gh pr comment`, `close`, `reopen`, `ready`, `edit`, `review` | sì | |
| `gh pr checks` | parziale | Status check rollup dal servizio statuses; nessun link alle esecuzioni dei workflow. |
| `gh pr diff` | no | Il media type diff di `GET /pulls/{number}` non è servito. |
| `gh issue list`, `view`, `create`, `close`, `reopen`, `comment`, `edit` | sì | I project vengono ignorati. |
| `gh issue status` | sì | `search` con `repo:`. |
| `gh release list` | sì | GraphQL `releases`. |
| `gh release view`, `create`, `upload`, `download`, `delete`, `delete-asset` | sì | REST. |
| `gh label list` | sì | GraphQL `labels`. |
| `gh label create`, `edit`, `delete`, `clone` | sì | REST. |
| `gh api <percorso REST>` (anche `--paginate`), `gh api graphql` | sì | |
| `gh run`, `gh workflow`, `gh search`, `gh gist`, `gh codespace`, `gh project` | no | Non implementati (`search` richiede un qualificatore `repo:`). |

Sono attese ricavate dal sorgente di `gh` e dagli endpoint implementati; non
sono state verificate con test automatici.
