# Handoff: stato del lavoro (2026-11-03)

La Fase 5 è chiusa. Tutto è committato e pushato su `main`, e build, vet, lint, codegen e controlli web sono verdi. Il lavoro si è fermato qui su richiesta dell'utente; la prossima sessione riparte dalla **Fase 6, Actions**.

## Obiettivo e ambito (deciso dall'utente il 2026-11-03)

Le distribuzioni sono due:

- **standalone**: binario nativo con .deb/.rpm, systemd e `install.sh`;
- **Docker**: compose con `setup.sh` e `setup.bat`.

Il **plugin Ervisio** non è una terza distribuzione. Fa due cose: installa una delle due modalità, scelta dall'utente, e offre una configurazione grafica nelle impostazioni di Ervisio.

Escluso dal lavoro degli agenti:

- **SSO col core:** escluso, perché Ervisio usa il login con gli account Linux.
- **Fase 7 (OCI) e Fase 9 (hardening):** rinviate.
- **Fase 10:** niente documentazione utente/admin e niente guida alla migrazione. I test di carico e l'esercitazione DR li fa l'utente sul suo server.

Restano da fare: la Fase 6, la Fase 8 ridotta, e della Fase 10 la guida al DR e la release v1.0. I dettagli sono in `docs/PLAN.md` §11, già aggiornato.

## Regole operative (oltre a CLAUDE.md)

- **Agenti:** solo subagent normali (Agent), niente Workflow. Sonnet per il lavoro meccanico, opus per quello difficile. Due agenti in parallelo vanno bene se lavorano su aree separate: entrambi modificano `api/openapi.yaml`, quindi devono rileggerlo prima di ogni modifica e rigenerare subito dopo.
- **Niente test e niente CI:** i test li fa l'utente.
- **Verifiche ammesse:** build, vet, lint, check-generate e, in `web/`, `npx tsc --noEmit -p .`, `npx eslint .`, `npm run -s check-i18n` e `npm run build`.
- **Commit:** Conventional Commits su `main`, poi push. Mai `--no-verify`, mai force push. Le righe di attribuzione sono quelle indicate dalla sessione.
- **Domande all'utente:** solo con AskUserQuestion; per il resto si va avanti.
- **Dipendenze:** ogni modifica a go.mod o package.json va segnalata nel report.
- **UI:** la direzione è decisa in `design/DESIGN.md`. Console è il default; Foglio e Corsie si attivano via `data-layout`. Le schermate nuove (per esempio i run delle Actions) passano da un round di galleria variant-studio (`--project design`) che parte da Console.
  - Per issue e notifiche è stata approvata la variante A, "righe dense"; B e C restano selezionabili. Il round è in `design/wip/002-issue-e-notifiche/DECISION.md`.

### Ambiente Windows

- `make` non c'è; build, vet e lint vanno lanciati con `GOOS=linux`:
  - `GOOS=linux go build ./... && GOOS=linux go vet ./...`
  - lint: `GOOS=linux "$(go env GOPATH)/bin/golangci-lint.exe" run ./...`. La versione è la v2.15.0, installata per Windows con `go install`. Restano 3 errori gofmt in `internal/storage/blob`, dovuti ai CRLF di `core.autocrlf`: sono rumore.
- I generatori vanno lanciati **senza** `GOOS=linux`. Il check-generate a mano:
  ```sh
  git add -A
  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2 -config api/oapi-codegen.yaml api/openapi.yaml
  CGO_ENABLED=0 go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate -f internal/db/sqlc.yaml
  npm --prefix web run gen:api
  git diff --exit-code
  ```

## Fatto

- Fasi 0–4 e Fase UI.
- **Fase 5:**
  - eventi (outbox, dispatcher River, bus);
  - issue, protezione dei branch e regole sui tag, CODEOWNERS;
  - pull request con fork e pool, merge-tree, review inline, suggested change, tre strategie di merge, auto-merge, update-branch;
  - notifiche in-app ed email, webhook github ed ervisio;
  - commit status e check run;
  - `/api/v1`, `/api/v3` compatibile con GitHub (`docs/api-v3.md`) e un sottoinsieme GraphQL per `gh`;
  - import da GitHub di issue, PR, label, milestone e release;
  - UI di issue, PR, notifiche, webhook, regole e GitHub.
  - Il dettaglio di ogni blocco è nel `CHANGELOG.md`, sezione Unreleased.
- Ultima migrazione: `00190_fork_networks`.

## Lacune note della Fase 5 (non bloccanti)

- **Pool dei fork:**
  - non c'è un journal remoto condiviso, quindi un fork idratato dal journal torna una copia completa;
  - il pool non conta nel budget hot e non viene mai compattato;
  - i fork privati e i fork creati prima della migrazione 00190 restano copie complete.
- **PR:** i commenti inline pubblicati dentro una review non registrano riferimenti incrociati. Il picker dei reviewer non usa ancora `GET /search/users`.
- **Webhook:** la lista `commits` nei payload push è vuota.
- **Importer:**
  - non importa timeline, merged-by, risoluzione dei thread, richieste di review e cronologia delle modifiche;
  - le immagini nei body puntano ancora a GitHub;
  - i repo fork non vengono importati.
- **/api/v3:**
  - il campo `protected` dei branch è sempre false;
  - la lista issue esclude le PR;
  - POST check-suites risponde 501.
  - Le altre limitazioni su readme e git refs sono nel CHANGELOG.
- **GraphQL:**
  - `search` richiede il qualificatore `repo:`;
  - l'introspection è minima.
- **Web:** il chunk principale è di circa 850 kB. La UI non è mai stata aperta in un browser.

## Prossimi passi

1. **Fase 6, Actions** (`docs/PLAN.md` §6 e §11):
   - parser dei workflow e trigger;
   - scheduler su River con ConnectRPC e i `.proto` del runner, contratto prima;
   - `ervisio-runner` con il fork di act e backend Docker;
   - segreti e log in streaming;
   - cache v2 e artifact v4;
   - check run nelle PR (riusare `internal/statuses`);
   - UI dei run, con un round di galleria prima.
2. **Fase 8 ridotta:** Docker, standalone, socket admin, plugin (installazione più configurazione grafica), Caddy, catalogo.
3. **Fase 10:** guida al DR (en e it) e release v1.0.

## Punti aperti noti

- La creazione dei tag delle release (`tagOnDisk`) non passa da una sessione hook, quindi salta le regole sui tag.
- I commit creati dal server non sono firmati.
- Le scritture della sync GitHub non contano nella quota.
- Mancano in OpenAPI lo stato 502 di ghsync e l'idratazione via SSE.
- Gli allegati non hanno il redirect "moved" e non rispondono a HEAD.
- L'URL di clone SSH è provvisorio.
- `web/tests/visual/auth.spec.ts` cita ancora il billing.
- `require_local_2fa`, `security.user_directory` e le sezioni di configurazione `[git]` e `[ssh]` non sono documentati.
- Mancano le deploy key e l'aggiornamento di `last_used` per le chiavi SSH.
- L'avanzamento dell'idratazione ha la lingua fissa su "en".
