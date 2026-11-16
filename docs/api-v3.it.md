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
| `GET /` | sì | Radice con i modelli di URL; `current_user_authorizations_html_url` è la pagina dei token (`/settings?section=tokens`). |
| `GET /meta` | stub | `verifiable_password_authentication: false`, liste IP vuote. |
| `GET /rate_limit` | stub | Fisso 5000/5000; non si applica alcun limite. |
| `GET /user` | sì | Utente autenticato. Richiede un token. |
| `GET /user/orgs` | sì | |
| `GET /users/{username}` | sì | Utenti e organizzazioni. |
| `GET /orgs/{org}` | sì | |

### Repository

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{owner}/{repo}` | sì | `permissions` riflette il chiamante. `ssh_url` usa l'host dell'URL pubblico. `fork`, `forks_count` e, su un fork, `parent` e `source`. |
| `GET /user/repos` | sì | Repository del chiamante. |
| `GET /users/{username}/repos`, `GET /orgs/{org}/repos` | sì | |
| `POST /user/repos`, `POST /orgs/{org}/repos` | parziale | `name`, `description`, `private`/`visibility`, `default_branch`, `auto_init`, `gitignore_template`, `license_template` (un unico "Initial commit" con README.md, .gitignore e LICENSE). `is_template`, `homepage` e le opzioni delle funzionalità sono ignorati. |
| `PATCH /repos/{owner}/{repo}` | parziale | `name`, `description`, `private`/`visibility`, `default_branch`. I campi che il server non gestisce (`homepage`, `has_*`, `allow_*`, `delete_branch_on_merge`, `archived`) sono ignorati, come GitHub ignora i campi sconosciuti. |
| `DELETE /repos/{owner}/{repo}` | sì | |
| `GET /repos/{owner}/{repo}/forks` | sì | I fork che il chiamante può leggere. `sort` è ignorato. |
| `POST /repos/{owner}/{repo}/forks` | sì | `organization`, `name`; `default_branch_only` è ignorato (si copiano tutti i branch). Risponde `202` con il fork, quello esistente se c'era già. |
| `POST /repos/{owner}/{repo}/merge-upstream` | parziale | `branch` (predefinito: il branch di default). Solo fast-forward: un branch che diverge dal padre risponde `409`. |
| `GET /gitignore/templates`, `GET /gitignore/templates/{name}` | sì | Template inclusi: C, Go, Java, macOS, Node, Python, Rust, Windows. |
| `GET /licenses`, `GET /licenses/{license}` | sì | Inclusi: `mit`, `apache-2.0`, `gpl-3.0`, `bsd-3-clause`, `mpl-2.0`, `unlicense`. |
| `GET/PUT /repos/{owner}/{repo}/topics`, `POST .../generate` | no | Topic e repository template non sono gestiti. |

### Ricerca

| Endpoint | Stato | Note |
|---|---|---|
| `GET /search/repositories` | parziale | Testo su nome e descrizione, `user:`/`org:`/`repo:`, `is:public`/`is:private`. `language:`, `stars:`, `topic:` non danno risultati. `total_count` conta al massimo 1000 risultati. |
| `GET /search/issues` | parziale | Richiede `repo:owner/name`. `is:issue`/`is:pr`, `is:open`/`closed`/`merged`, `state:`, `author:`, `assignee:`, `mentions:`, `label:`, `milestone:`, `base:`, `head:`, `review-requested:`, `no:`, `sort:` (`@me` funziona), come la ricerca GraphQL. |
| `GET /search/code` | parziale | Richiede `repo:owner/name`; `path:` limita la cartella. git grep (senza maiuscole, stringa fissa) sul branch di default, un elemento per file; `sha` è il commit cercato. |
| `GET /search/users` | parziale | Prefisso del nome utente o del nome visualizzato, nella directory utenti visibile al chiamante; i qualificatori sono ignorati. |

### Contenuti, commit, branch, ref

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/contents/{path}` | sì | File (base64), elenco di directory, symlink. Query `ref`. `Accept: application/vnd.github.raw` restituisce il file grezzo in streaming. I submodule sono elencati come `submodule`. |
| `PUT /repos/{o}/{r}/contents/{path}` | parziale | Creazione (`201`) o aggiornamento con `sha` (`200`), `branch`. `committer` e `author` sono ignorati: l'autore del commit è il proprietario del token. |
| `DELETE /repos/{o}/{r}/contents/{path}` | sì | `sha` obbligatorio. |
| `GET /repos/{o}/{r}/readme[/{dir}]` | parziale | Il README della directory (quello mostrato dal browser del codice: il primo tra `README.md`, `.markdown`, `.mdown`, `.rst`, `.adoc`, `.txt` e `README` semplice, senza distinzione tra maiuscole e minuscole), restituito come un file di contents. Query `ref`. `Accept: application/vnd.github.raw` lo restituisce in streaming; `application/vnd.github.html` non è supportato (si riceve il JSON). `404` se non c'è. |
| `GET /repos/{o}/{r}/commits` | parziale | `sha` e `path`; `author`, `since`, `until` sono ignorati. |
| `GET /repos/{o}/{r}/commits/{ref}` | parziale | Con `stats` e `files`. Il ref può contenere `/` (`feature/x`); un ref il cui ultimo segmento è `status`, `statuses`, `check-runs` o `check-suites` seleziona quella sotto-risorsa, come su GitHub. Gli utenti `author`/`committer` sono `null`. I media type diff e patch non sono supportati. |
| `GET /repos/{o}/{r}/branches` | parziale | `protected` è sempre `false`; la protezione dei branch si gestisce con `/api/v1`. |
| `GET /repos/{o}/{r}/branches/{branch}` | parziale | Come sopra. |
| `GET /repos/{o}/{r}/tags` | sì | |
| `GET /repos/{o}/{r}/git/ref/{ref}` | sì | |
| `GET /repos/{o}/{r}/git/refs[/{prefix}]` | sì | Una corrispondenza esatta restituisce un oggetto, un prefisso una lista. |
| `GET /repos/{o}/{r}/git/matching-refs/{prefix}` | sì | |
| `POST /repos/{o}/{r}/git/refs` | parziale | `ref` e `sha` (id completo); `201` con il ref. Solo `refs/heads/` e `refs/tags/` (un tag può puntare a un oggetto tag annotato). Ref esistente od oggetto sconosciuto: `422`. |
| `PATCH /repos/{o}/{r}/git/refs/{ref}` | sì | `sha` e `force`. Senza `force` un aggiornamento non fast-forward risponde `422 Update is not a fast forward`. |
| `DELETE /repos/{o}/{r}/git/refs/{ref}` | parziale | `204`. Ref inesistente: `422`. Il branch predefinito non si può eliminare. |
| Scritture dei ref | | Richiedono il permesso di push e lo scope `repo:write`; passano dagli stessi hook di un push, quindi valgono la protezione di branch e tag, il journal, gli eventi e i webhook. Un aggiornamento rifiutato risponde `422`. |
| Dati git (`git/blobs`, `git/trees`, `git/commits`, `git/tags`) | no | |

### Pull request

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/pulls` | sì | `state`, `head` (`user:branch`), `base`, `sort=created/updated`. |
| `POST /repos/{o}/{r}/pulls` | parziale | `title`, `body`, `head` (`branch` o `owner:branch`), `head_repo`, `base`, `draft`. La conversione di una issue (`issue`) non è supportata. |
| `GET /repos/{o}/{r}/pulls/{n}` | sì | Con `mergeable`, `mergeable_state`, `commits`, `additions`, `deletions`, `changed_files`, `merged_by`. Con `Accept: application/vnd.github.diff` risponde il diff unificato dalla merge base, con `application/vnd.github.patch` i commit come serie `format-patch` (senza i merge commit); `406` se il diff supera i limiti o la serie ha più di 250 commit. |
| `PATCH /repos/{o}/{r}/pulls/{n}` | sì | `title`, `body`, `state`, `base`, `draft`. |
| `GET .../pulls/{n}/commits` | sì | |
| `GET .../pulls/{n}/files` | sì | `sha` del blob e `patch` dal diff dalla merge base; `patch` manca per i file binari e oltre i limiti del diff. |
| `GET .../pulls/{n}/merge` | sì | `204` se fusa, `404` altrimenti. |
| `PUT .../pulls/{n}/merge` | sì | `merge_method` merge/squash/rebase, `commit_title`, `commit_message`, `sha`. Si applica la protezione dei branch; un admin del repository supera i requisiti di review e check di una regola con "admins bypass" (l'auto-merge non supera mai). |
| `GET/POST .../pulls/{n}/reviews` | parziale | I commenti di una review richiedono `line` (e `side`/`start_line`); il vecchio `position` è rifiutato. |
| `GET/DELETE .../pulls/{n}/reviews/{id}` | sì | L'eliminazione vale solo per le review in sospeso. |
| `POST .../pulls/{n}/reviews/{id}/events` | sì | Invia una review in sospeso. |
| `PUT .../pulls/{n}/reviews/{id}/dismissals` | sì | |
| `GET .../pulls/{n}/reviews/{id}/comments` | sì | |
| `GET/POST .../pulls/{n}/comments` | parziale | Stessa regola su `line`; risposte con `in_reply_to`. |
| `PATCH/DELETE .../pulls/comments/{id}` | sì | In queste risposte `pull_request_url` non contiene il numero. |
| `GET /repos/{o}/{r}/pulls/comments` | no | Elenco su tutto il repository. |
| `GET/POST/DELETE .../pulls/{n}/requested_reviewers` | parziale | Solo utenti; `team_reviewers` è rifiutato. |
| `PUT .../pulls/{n}/update-branch` | sì | Unisce il branch base nel branch head come il chiamante (serve la scrittura sul repository head, si applica la protezione del branch head); `expected_head_sha` viene verificato. Risponde `202` a operazione fatta, `422` se è già aggiornato, in conflitto o se l'head si è spostato. |

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
| `PUT/DELETE .../issues/{n}/lock` | sì | `lock_reason` `off-topic`, `too heated`, `resolved` o `spam`. Richiede il permesso di triage; vale anche per i numeri delle pull request. `204`. |
| Eventi, timeline, endpoint delle reaction | no | I conteggi delle reaction sono inclusi negli oggetti issue e commento. |

### Release

| Endpoint | Stato | Note |
|---|---|---|
| `GET /repos/{o}/{r}/releases` | sì | Le bozze solo per chi può gestire il repository. |
| `POST /repos/{o}/{r}/releases` | parziale | `tag_name`, `target_commitish`, `name`, `body`, `draft`, `prerelease`. `generate_release_notes: true` riempie `name` e `body` vuoti con le note generate; `make_latest` (`true`, `false`, `legacy`) fissa o esclude la release da "latest". |
| `GET .../releases/latest`, `GET .../releases/tags/{tag}` | sì | `latest` rispetta `make_latest`: una release fissata vince, quelle con `make_latest: false` sono saltate, altrimenti la più recente pubblicata non prerelease. |
| `GET/PATCH/DELETE .../releases/{id}` | sì | |
| `GET .../releases/{id}/assets` | sì | |
| `POST {public_url}/api/uploads/repos/{o}/{r}/releases/{id}/assets?name=&label=` | sì | È l'`upload_url` di una release. Corpo grezzo, `Content-Length` obbligatorio (`411`). Accettato anche sotto `/api/v3`. |
| `GET .../releases/assets/{id}` | sì | JSON, oppure il file con `Accept: application/octet-stream` (supporta un singolo `Range`). |
| `PATCH/DELETE .../releases/assets/{id}` | sì | `name`, `label`. |
| `POST .../releases/generate-notes` | parziale | `tag_name`, `target_commitish`, `previous_tag_name` (default: il tag precedente per data) rispondono `{name, body}`: elenco "What's Changed" (titolo della pull request, `@autore`, link) e riga "Full Changelog" di confronto. `configuration_file_path` è ignorato. |
| `PATCH .../releases/{id}` `make_latest` | sì | Come in creazione. |

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

### Actions

Esecuzioni dei workflow di Ervisio Actions. Le esecuzioni importate da
GitHub (sincronizzazione GitHub) sono elencate in sola lettura con
`"external": true`: il loro `html_url` è l'esecuzione su GitHub e qui non
hanno job, log né artifact. L'`id` di un workflow deriva dal repository e
dal percorso del workflow; al suo posto funziona anche il nome del file.

| Endpoint | Stato | Note |
|---|---|---|
| `GET .../actions/workflows`, `GET .../actions/workflows/{id}` | sì | Workflow del branch di default e delle esecuzioni passate; `state` è sempre `active`. |
| `PUT .../actions/workflows/{id}/enable`, `/disable` | no | I workflow non si possono disattivare (`404`). |
| `POST .../actions/workflows/{id}/dispatches` | sì | `ref` (predefinito: il branch di default), `inputs`. `204`. |
| `GET .../actions/runs`, `GET .../actions/workflows/{id}/runs` | sì | `branch`, `event`, `status` (stato o esito), `actor`, `head_sha`, `created` (`>=`, `<`, `a..b`, una data). `total_count` è esatto. |
| `GET .../actions/runs/{id}`, `.../attempts/{n}` | sì | |
| `GET .../actions/runs/{id}/jobs`, `.../attempts/{n}/jobs` | sì | Con gli step (`number` da 1). I job in attesa dei loro `needs` sono `pending`. |
| `POST .../actions/runs/{id}/rerun`, `/cancel` | sì | `rerun` richiede un'esecuzione conclusa, `cancel` una in corso (altrimenti `409`). |
| `POST .../actions/runs/{id}/rerun-failed-jobs` | no | `422`: si può rieseguire solo l'intera esecuzione. |
| `DELETE .../actions/runs/{id}` | no | Le esecuzioni non si possono eliminare. |
| `GET .../actions/runs/{id}/logs` | sì | Zip dell'ultimo tentativo: `<n>_<job>.txt` e `<job>/<step>_<nome>.txt`, come GitHub. Servito direttamente (senza redirect). |
| `GET .../actions/jobs/{id}`, `GET .../actions/jobs/{id}/logs` | sì | Il log è testo semplice, servito direttamente. |
| `GET .../actions/artifacts`, `GET .../actions/runs/{id}/artifacts` | sì | Filtro `name`. Vuoto se non è configurata una policy di storage per actions. |
| `GET/DELETE .../actions/artifacts/{id}`, `GET .../actions/artifacts/{id}/zip` | sì | `zip` reindirizza (`302`) a un URL di download firmato valido un'ora. |
| `GET .../actions/secrets/public-key` | sì | Chiave Curve25519 derivata da `security.master_key_file`; senza, `503`. |
| `GET/PUT/DELETE .../actions/secrets[/{name}]` | sì | `PUT` accetta `encrypted_value` (sealed box libsodium per la chiave pubblica) e `key_id`. Richiede il permesso sulle impostazioni del repository. |
| `GET/POST .../actions/variables`, `GET/PATCH/DELETE .../actions/variables/{name}` | sì | `PATCH` può rinominare. |

### Non disponibili

Ricerca nei commit, gist, notifiche, eventi, package, project, deployment,
webhook (usare `/api/v1/.../hooks`), gestione dei collaboratori, team, app e
installazioni.

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
  `removeAssigneesFromAssignable`, `lockLockable` e `unlockLockable` (con `LockReason`), `deleteIssue` (solo admin del repository).
- **Check richiesti**: `CheckRun.isRequired` e `StatusContext.isRequired` accettano `pullRequestNumber` e sono true quando il contesto è un controllo di stato richiesto dalla regola di protezione del branch base della pull request.

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
| `gh repo view` | sì | |
| `gh repo list` | sì | `repositoryOwner { repositories }`. |
| `gh repo clone` | sì | Git puro. |
| `gh repo create`, `edit`, `delete` | no | Non implementati via GraphQL. |
| `gh repo fork`, `gh repo sync` | sì | REST `POST /forks` e `POST /merge-upstream` (solo fast-forward). |
| `gh pr list`, `view`, `status` | sì | `status` usa `search` con `author:@me` e `review-requested:@me`. |
| `gh pr create` (anche `--draft`, `--label`, `--assignee`, `--reviewer` utenti) | sì | Team come reviewer e project vengono rifiutati. |
| `gh pr checkout` | sì | Scarica il branch head dal repository base. |
| `gh pr merge` (`--merge`, `--squash`, `--rebase`, `--auto`) | sì | `--delete-branch` elimina il branch head con `DELETE /git/refs` (soggetto alla protezione dei branch). Merge queue non supportata. |
| `gh pr comment`, `close`, `reopen`, `ready`, `edit`, `review` | sì | |
| `gh pr checks` | parziale | Status check rollup dal servizio statuses; nessun link alle esecuzioni dei workflow. |
| `gh pr diff` (anche `--patch`, `--name-only`) | sì | Media type diff e patch di `GET /pulls/{number}`. |
| `gh issue list`, `view`, `create`, `close`, `reopen`, `comment`, `edit` | sì | I project vengono ignorati. |
| `gh issue status` | sì | `search` con `repo:`. |
| `gh release list` | sì | GraphQL `releases`. |
| `gh release view`, `create`, `upload`, `download`, `delete`, `delete-asset` | sì | REST. |
| `gh label list` | sì | GraphQL `labels`. |
| `gh label create`, `edit`, `delete`, `clone` | sì | REST. |
| `gh api <percorso REST>` (anche `--paginate`), `gh api graphql` | sì | |
| `gh search repos`, `issues`, `prs`, `code` | parziale | REST `/search/*`; `issues`, `prs` e `code` richiedono `--repo`. `gh search commits` non è disponibile. |
| `gh run list`, `view` (anche `--log`), `watch`, `rerun`, `cancel`, `download` | sì | REST `/actions/*`. `rerun --failed` è rifiutato (`422`). Le esecuzioni importate da GitHub mostrano solo il link a GitHub. |
| `gh workflow list`, `view`, `run` | sì | `enable`/`disable` non sono disponibili. |
| `gh secret list`, `set`, `delete`; `gh variable list`, `get`, `set`, `delete` | sì | Solo a livello di repository. |
| `gh gist`, `gh codespace`, `gh project` | no | Non implementati. |

Sono attese ricavate dal sorgente di `gh` e dagli endpoint implementati; non
sono state verificate con test automatici.

## Device flow OAuth (`eg auth login`, `gh auth login`)

Il server implementa l'OAuth 2.0 Device Authorization Grant
([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)) con gli endpoint e il
formato di GitHub, così i client a riga di comando possono accedere passando
dal browser:

1. `POST /login/device/code` con `client_id` e uno `scope` facoltativo
   (scope GitHub separati da spazi o virgole). La risposta contiene
   `device_code`, `user_code` (`XXXX-XXXX`), `verification_uri`
   (`https://<host>/login/device`), `expires_in` (900) e `interval` (5). Un
   campo non standard `device_name` dà il nome al token (per esempio il nome
   host della macchina).
2. L'utente apre `/login/device` (con l'accesso fatto, o dopo averlo fatto),
   inserisce il codice (`?user_code=` lo precompila), controlla app e scope e
   sceglie Autorizza o Annulla.
3. Il client interroga `POST /login/oauth/access_token` con `client_id`,
   `device_code` e `grant_type=urn:ietf:params:oauth:grant-type:device_code`
   non più spesso di `interval`. Riceve `access_token`, `token_type=bearer` e
   `scope`, oppure uno degli errori `authorization_pending`, `slow_down`
   (l'intervallo cresce di 5 secondi, il nuovo valore è in `interval`),
   `expired_token`, `access_denied`, `incorrect_client_credentials`,
   `incorrect_device_code` o `unsupported_grant_type`. Come su GitHub questi
   errori arrivano con HTTP 200.

Entrambi gli endpoint accettano corpi form-encoded o JSON e rispondono in
JSON quando l'header `Accept` contiene `application/json`, altrimenti
form-encoded. I device code sono salvati come hash e durano 15 minuti; i
codici sbagliati nella pagina di approvazione sono limitati per account e IP
client, e ogni IP client può chiedere 60 device code all'ora.

Client integrati (le app OAuth registrate arriveranno in seguito):

| Client | `client_id` |
|---|---|
| `eg`, la CLI di ErvisioRepo | `Ov23liErvisioEgCli01` |
| GitHub CLI (`gh auth login --hostname <host>`) | `178c6fc778ccc68e1d6a` |

Il dispositivo riceve un token di accesso personale, elencato in
Impostazioni > Token come `eg on <dispositivo o host del server> (device)`
(con `#2`, `#3`... se il nome è già usato) e revocabile da lì. Dura quanto la
durata massima dei token (`auth.token_max_ttl`). Gli scope GitHub diventano
scope del token così; uno `scope` vuoto vale i default di gh
`repo read:org workflow`, e `user:read` è sempre concesso:

| Scope GitHub | Scope del token |
|---|---|
| `repo`, `public_repo`, `repo:status`, `repo_deployment`, `delete_repo`, `admin:repo_hook`, `write:repo_hook` | `repo:write` |
| `read:repo_hook` | `repo:read` |
| `workflow` | `actions:write` |
| `read:org`, `read:user`, `user:email`, `read:public_key`, `read:gpg_key`, `read:discussion` | `user:read` |
| `user`, `user:follow`, `write:org`, `admin:org`, `admin:org_hook`, `write:public_key`, `admin:public_key`, `write:gpg_key`, `admin:gpg_key`, `notifications`, `write:discussion` | `user:write` |
| `read:packages` | `packages:read` |
| `write:packages`, `delete:packages` | `packages:write` |
| `gist`, `codespace`, `project`, altri | nessuno |

Nessuno scope GitHub concede lo scope `admin` dell'istanza. Con gli scope di
default `GET /api/v3/user` risponde `X-OAuth-Scopes: workflow, repo, read:user,
read:org`, che basta a `gh auth status`. L'API accetta il token come
`Authorization: token <t>` (quello che manda gh) o `Bearer <t>`.
