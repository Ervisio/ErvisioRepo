# Handoff: stato del lavoro (2026-11-04)

Le Fasi 0-6, la Fase 8 ridotta e la guida al DR della Fase 10 sono completate. Tutto è committato e pushato su `main`; build, vet, lint, codegen e controlli web sono verdi. Le release v1.0.0, v1.0.2 e v1.0.3 sono pubblicate su GitHub (pacchetti e SHA256SUMS firmati; dalla v1.0.2 anche il plugin `repo-<ver>.tar.gz`); l'immagine pubblica `ghcr.io/ervisio/ervisio-repo:1.0.3`/`latest` è su ghcr e il plugin `repo` 1.0.3 è nel catalogo (categoria «Web servers»). La v1.0.1 è solo un tag, mai pubblicata; la v1.0.2 non parte senza `[storage.policy.actions]` nel config. Fase 7 (OCI) e Fase 9 (hardening) restano rinviate.

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

- Fasi 0-5 e Fase UI (dettaglio nel `CHANGELOG.md`, sezione Unreleased). Ultima migrazione: `00210_actions_results`.
- **Fase 6, Actions:** contratto runner ConnectRPC, `ervisio-runner` su act con backend Docker, server Actions (parser, trigger, matrice, `needs`, ambienti con approvazione, workflow riutilizzabili, segreti sigillati, log), REST API, servizi cache v2 e artifact v4, UI dei run, dei log e delle impostazioni.
- **Fase 8 ridotta:** Dockerfile, compose, `setup.sh` e `setup.bat`; pacchetti nfpm, systemd, `install.sh`; socket `/api/plugin/*`; plugin in `plugin/`; Caddy in Go e nel plugin; `plugin/catalog-entry.json`.
- **Fase 10:** guida al DR in `docs/dr.md` e `docs/dr.it.md`.
- `docs/PLAN.md` §11 ha le caselle aggiornate.

## Cosa resta

1. **Fase 10, chiusura:** la chiave privata ed25519 è in `~/.ervisio-release/release.pem` sulla macchina del maintainer, fuori dal repo. Il catalogo sincronizza le nuove release da solo ogni 15 minuti (workflow «Sync plugin releases» di Ervisio/plugins, lanciabile a mano). A ogni release vanno alzati insieme il default `ERVISIO_VERSION` in `deploy/docker/compose.yaml` e la versione del plugin (`package.json`, `package-lock.json`, `plugin/manifest.json`, `catalog-entry.json`), che deve coincidere con il tag; gli asset sono costruiti in locale perché la CI è disabilitata.
2. **Punti aperti:**
   - `docs/dr.md` contiene vari `TODO: confirm` (credenziali dei target fuori dal kit, owner e modo di `recovery-kit import`, entrypoint Docker e `restore`, runner dopo un restore, ri-replica su un nuovo target, evict di un singolo repo, chiavi di firma lato server).
3. **Lacune note della Fase 6:**
   - `actions/upload-artifact@v4` e `download-artifact@v4` ufficiali rifiutano host diversi da github.com: usare un fork compatibile (per esempio `christopherhx/gitea-upload-artifact`);
   - il proxy-cache `_actions` non è costruito (serve un mirror git sul server);
   - nessun download con `Range`;
   - le impostazioni Actions richiedono un riavvio;
   - nella UI non ci sono segreti per ambiente;
   - il test chaos della Fase 3 non è scritto (niente test per decisione dell'utente).
4. Test di carico, esercitazione DR e contratti con `gh`/`go-github` li fa l'utente.

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
