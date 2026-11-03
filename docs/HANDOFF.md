# Handoff: stato del lavoro (2026-11-03)

Il lavoro si è fermato su richiesta dell'utente a metà della Fase 5, wave 2. **Il commit WIP non compila**: `internal/httpapi` non implementa ancora i metodi generati da OpenAPI per pull request, webhook e notifiche (per esempio `CountNotifications`).

## Obiettivo generale

Finire l'app in modo che sia deployabile in tre modi:

- standalone;
- come plugin Ervisio;
- con Docker.

Si seguono le fasi di `docs/PLAN.md` §11.

## Regole operative (oltre a CLAUDE.md)

- **Agenti:** solo subagent normali (Agent), niente Workflow. Sonnet per il lavoro meccanico, opus per quello difficile. Risposte brevi, qualità prima di tutto.
- **Niente test e niente CI:** i test non si scrivono, non si eseguono e la CI non si lancia.
- **Verifiche ammesse:**
  - `go build ./...`
  - `go vet ./...`
  - `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...`
  - `make build`
  - `git add -A && make check-generate`. Il target confronta con l'indice git, quindi prima va fatto `git add`.
  - In `web/`: `npx tsc --noEmit -p .`, `npx eslint .`, `npm run -s check-i18n`.
- **Commit:** Conventional Commits su `main`, poi push. Mai `--no-verify`, mai force push. Le righe di attribuzione sono quelle indicate dalla sessione.
- **Domande all'utente:** solo con AskUserQuestion; per il resto si va avanti su tutto.
- **Dipendenze:** ogni modifica a go.mod o package.json va segnalata nel report.
- **UI:** la direzione è già decisa in `design/DESIGN.md`. Console è il default; Foglio e Corsie si attivano via `data-layout`. Le schermate che mancano nei mock passano da un round di galleria (variant-studio, `--project design`) che parte da Console. Non si ripropongono direzioni generali.

## Fatto

- Fasi 0–4 complete e pushate.
- **Fase 5, wave 1** (commit e195da3):
  - `internal/events`: outbox più dispatcher River; `KindPush` viene emesso da `events.PushHooks`.
  - `internal/issues`.
  - `internal/protection`.
  - `internal/ghsync`: il backup dopo un push ora parte dal bus (`bus.Register("ghsync.push", …)` in `cmd/ervisio-repo/serve.go`). Il fallback in `Hooks.Committed` è stato rimosso.
  - Migrazioni 00120–00140.
- In `internal/events/event.go` sono già dichiarati questi kind: `KindPull`, `KindPullReview`, `KindPullReviewComment`, `KindRelease`, `KindStatus`.

## In corso (WIP in questo commit, interrotto)

### 1. Pull request

Spec in `docs/PLAN.md` §5.4. Parti già scritte:

- migrazione `00150_pulls.sql`;
- `internal/db/queries/pulls.sql` e il codice dbgen;
- `internal/gitbrowse/pulls.go`;
- sezione PR in `api/openapi.yaml`.

Mancano:

- `internal/git/merge.go`, con le funzioni merge-tree `--write-tree` e commit-tree;
- il package `internal/pulls`;
- `internal/httpapi/pullsapi.go`;
- `internal/events/pulls.go`, con `PullPayload`, `PullReviewPayload` e `PullReviewCommentPayload`;
- `ReviewSource` per protection, collegato via `Engine.SetSources`;
- fork con pool e `alternates`;
- i ref `refs/pull/<n>/head|merge`;
- le review inline e le suggested change;
- le strategie di merge merge, squash e rebase;
- l'auto-merge;
- `CloseFromText` al merge;
- l'entry nel CHANGELOG.

Da riusare: `issues.NextNumber`, `protection.ProtocolMerge` e la sessione hook come in gitbrowse.

### 2. Webhook e notifiche

Spec in `docs/PLAN.md` §5.7 e §5.5. Parti già scritte:

- migrazione `00160_webhooks_notifications.sql`;
- le query `webhooks.sql` e `notifications.sql`;
- la configurazione `[webhooks]` (`allow_private`, allowlist CIDR) in `internal/config` e in `deploy/dev/config.toml`;
- le sezioni OpenAPI.

Mancano:

- i package `internal/webhooks` e `internal/notify`;
- `internal/httpapi/webhooksapi.go` e `notificationsapi.go`;
- la registrazione degli handler sul bus in `serve.go`.

Requisiti da rispettare:

- formati `github` (`X-GitHub-Event`, `X-Hub-Signature-256`) ed `ervisio`;
- retry esponenziale con River;
- cronologia delle consegne, redelivery e ping;
- protezione SSRF al dial, con `net.Dialer.Control`;
- email con `internal/mail`.

### 3. Galleria UI Fase 5

Round `002-issue-e-notifiche` in `design/.variant-studio` (ignorato da git). Una copia è in `design/wip/002-issue-e-notifiche/`.

- Tre varianti Console: a righe dense, b colonna filtri, c split view.
- Stati: lista issue, dettaglio issue, notifiche, impostazioni repo.
- La variante c potrebbe essere incompleta.
- Prima di `present`, va completato `round.json` con label, note e `states`.

Per presentare il round:

```sh
node ~/.claude/skills/variant-studio/scripts/studio.mjs present --project design
```

## Prossimi passi

1. Finire le pull request e i webhook con le notifiche, fino ad avere build, vet e lint verdi. Poi commit.
2. **API:** completare `/api/v1` e il sottoinsieme compatibile `/api/v3` (repo, contenuti, PR, issue, release, status e check).
3. **Importer GitHub** via `ghsync.Importer`: issue, PR, label, milestone e release.
4. **UI della Fase 5:** issue, PR, protezione dei branch, sync GitHub, notifiche, webhook. La UI parte dalla variante approvata in galleria.
5. Spuntare la Fase 5 nel PLAN, poi:
   - Fase 6, Actions;
   - Fase 7, OCI;
   - Fase 8, plugin Ervisio, Docker, compose, setup.sh e setup.bat, nfpm, systemd, install.sh, SSO;
   - Fase 9, hardening;
   - Fase 10, documentazione e release.

## Punti aperti noti

- La creazione di tag delle release (`tagOnDisk`) non passa da una sessione hook, quindi salta le regole sui tag.
- I commit creati dal server non sono firmati.
- Le scritture della sync GitHub non contano nella quota.
- Lo stato 502 di ghsync manca in OpenAPI.
- Gli allegati non hanno il redirect "moved" e non rispondono a HEAD.
- L'idratazione via SSE manca in OpenAPI.
- L'URL di clone SSH è provvisorio.
- `web/tests/visual/auth.spec.ts` cita ancora il billing.
- `require_local_2fa` e le sezioni di configurazione `[git]` e `[ssh]` non sono documentate.
- Mancano le deploy key e l'aggiornamento di `last_used` per le chiavi SSH.
- L'avanzamento dell'idratazione ha la lingua fissa su "en".
