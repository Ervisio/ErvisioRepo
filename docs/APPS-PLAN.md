# App e bot — piano

Stato: proposta (2026-11-16). Estende `docs/PLAN.md` §11 («Dopo la v1»).

Scelte fatte:
- Tutti e tre i tipi di App nella prima tornata: bot di sistema, OAuth App e App stile GitHub Apps.
- Claude come App integrata che lavora sui runner Actions.
- Le App le registrano utenti e org; l'admin dell'istanza può limitarlo.
- Un elenco semplice delle App pubbliche su `/apps`.

## Obiettivi

1. Ogni azione automatica ha un autore riconoscibile, `nome[bot]` con il badge «bot», invece di un autore vuoto.
2. App di terze parti scritte per GitHub (Probot, Octokit, Renovate, Dependabot-like, claude-code-action) funzionano puntando a Ervisio, senza modifiche: stessi endpoint, stessi JWT, stessi token, stessi payload webhook.
3. Claude si installa su un repo o un'org e risponde a `@claude` in issue e PR. Rivede le PR e apre PR con le modifiche.

## Concetti

| Concetto | Cos'è | Come autentica |
|---|---|---|
| Bot di sistema | Account interno non loggabile (`ervisio-sync[bot]`, `ervisio-actions[bot]`) | Nessun login: lo usa il server come autore |
| OAuth App | Client che agisce **per conto di un utente** | `client_id`/`client_secret`; web flow con PKCE e device flow; token utente con scope |
| App (stile GitHub App) | Integrazione con **identità propria** (`slug[bot]`), installata su account e repo | Chiave privata RS256 → JWT di 10 min → token di installazione di 1 h, con permessi granulari |
| Installazione | Collegamento App ↔ utente/org, con tutti i repo o una selezione | — |
| Token user-to-server | Token di un'App che agisce per un utente (permessi App ∩ permessi utente) | OAuth web/device flow dell'App |

## Fase A0 — Bot di sistema (fatto)

Dati:
- `namespaces.kind` accetta `bot`;
- `users.is_bot` (o una tabella `bots`) con `app_id` facoltativo;
- i bot non hanno password, email, sessioni o 2FA, e il login è impossibile.

Bot di sistema creati dalla migrazione:
- `ervisio-sync[bot]`: autore dei push e degli import della sincronizzazione GitHub. Sostituisce l'attore vuoto e la regola «niente attore = sistema» in `actions/trigger.go`, che diventa «attore = bot di sistema».
- `ervisio-actions[bot]`: autore di quello che fanno i workflow con il token del job (commenti, push, release), come `github-actions[bot]`.

API e UI:
- REST v3 `"type": "Bot"` e `login` con `[bot]`;
- GraphQL `Bot` (implements `Actor`);
- v1 `UserProfile.kind`;
- badge «bot» su attività, timeline, commenti, autori dei run e intestazione profilo;
- pagina profilo del bot con descrizione e link all'App.

Commit: email noreply dei bot (`<id>+<login>@users.noreply.<host>`), mappata sull'account bot nelle liste commit.

Retro-compatibilità: le righe esistenti con attore nullo restano tali, e la UI continua a mostrarle come ErvisioSync.

## Fase A1 — OAuth App (fatto)

- **Registrazione**: in Impostazioni › Sviluppatore › OAuth App, per utenti e org. Campi:
  - nome, homepage, descrizione, logo (con la stessa pipeline degli avatar);
  - URL di callback (più d'uno);
  - device flow sì/no.
  
  Il `client_id` è generato. I `client_secret` possono essere più d'uno, sono mostrati una volta sola, salvati come hash e revocabili.
- **Flusso web**: `GET /login/oauth/authorize` (`client_id`, `redirect_uri`, `scope`, `state`, PKCE `code_challenge` S256) → pagina di consenso → `code` monouso di 10 min → `POST /login/oauth/access_token` (`code` + `client_secret` o `code_verifier`). Wire format GitHub (form o JSON).
- **Device flow**: riusa `internal/credentials/device.go`. I client built-in (`eg`, `gh`) diventano righe di sistema della stessa tabella.
- **Token**: token utente con scope mappati (`internal/auth/ghscopes.go`), legati al grant (utente, app).
- **Utente**: Impostazioni › Applicazioni autorizzate, con elenco, scope, ultimo uso e «Revoca» (revoca tutti i token del grant).
- **Org**: politica «accesso OAuth App» con approvazione dell'org per le App esterne (rinviata).
- **API GitHub-compatibili**: `/applications/{client_id}/token` (check/reset/delete) e `/applications/{client_id}/grant`.

## Fase A2 — App (stile GitHub App) (fatto)

### Registrazione

In Impostazioni › Sviluppatore › App (utente o org). Campi:
- nome, slug, descrizione, homepage e logo;
- visibilità: privata (installabile solo dal proprietario) o pubblica (compare in `/apps`);
- **permessi**: matrice con livelli none/read/write;
  - sul repo: `contents`, `metadata` (sempre read), `issues`, `pull_requests`, `checks`, `statuses`, `actions`, `secrets`, `variables`, `administration`, `workflows`, `deployments`, `pages` (riservato);
  - sull'org: `members`, `administration`;
  - sull'utente: `email`;
- **eventi** a cui iscriversi, limitati dai permessi;
- **webhook**: URL, secret, attivo sì/no;
- **chiavi private**: genera, scarica il PEM una sola volta e revoca. Si possono averne più d'una per la rotazione;
- facoltativi: URL di setup post-installazione e callback per i token user-to-server.

Alla creazione nasce l'utente bot `slug[bot]`.

### Installazione

- Pagina `/apps/{slug}/installations/new`: si sceglie l'account (utente o org di cui si è owner) e «tutti i repo» o una selezione, poi si accettano i permessi.
- Quando l'App chiede permessi nuovi, l'installazione resta ai vecchi finché l'owner non approva, come su GitHub.
- Sospendi e disinstalla da Impostazioni org/utente › App installate.
- Eventi webhook: `installation`, `installation_repositories`, `github_app_authorization`.

### Autenticazione e autorizzazione

- **JWT dell'App** (RS256, `iss` = app id, durata ≤ 10 min, tolleranza di 60 s sull'orologio) per gli endpoint `/app/*`.
- **Token di installazione** (`erps_…`, 1 h): `POST /app/installations/{id}/access_tokens` con `repositories` e `permissions` facoltativi, purché restringano e non allarghino.
- Nuovo tipo di principal nel middleware di auth, `installation`. `authz` lo valuta con la matrice permessi × repo dell'installazione, al posto dei ruoli utente. Tutti i punti che oggi chiamano `authz.Allowed` passano per una funzione unica che gestisce anche questo caso.
- **Rate limit** per installazione.
- **Audit log** con attore `slug[bot]` e app id.

### API GitHub-compatibili (`/api/v3`)

- `GET /app`, `GET /app/installations`, `GET|DELETE /app/installations/{id}`;
- `POST /app/installations/{id}/access_tokens`;
- `GET /repos/{o}/{r}/installation`, `/orgs/{org}/installation`, `/users/{u}/installation`;
- `GET /installation/repositories`, `DELETE /installation/token`;
- `GET /user/installations` e `/user/installations/{id}/repositories` (token utente);
- `GET /apps/{slug}`.
- Check run e check suite collegati all'App (`app` nel payload), richiesti dalle protezioni dei rami per nome **e** App.

### Webhook

Riusa `internal/webhooks` (consegne, ritentativi, firma `X-Hub-Signature-256`). Aggiunge:
- `installation` nel payload;
- `X-GitHub-Hook-Installation-Target-Type/ID`;
- la pagina «Consegne recenti» dell'App con «Riconsegna».

## Fase A3 — App Claude

App **di sistema** `claude` (bot `claude[bot]`), installabile come le altre.

- **Configurazione**, a livello istanza (admin) e/o org, dove l'org prevale:
  - chiave API Anthropic, salvata come secret sigillato e mai mostrata;
  - modello (default `claude-sonnet-5-5`, opzione `claude-opus-5-5`);
  - frase d'innesco (default `@claude`);
  - chi può invocarlo (default: chi ha permesso di scrittura);
  - limiti: turni massimi, tempo massimo del job, tetto di spesa giornaliero per org;
  - review automatica delle PR aperte (sì/no);
  - label del runner (default `ubuntu-latest`).
- **Innesco**: menzione in issue, commento di PR o review; assegnazione dell'issue a `claude[bot]`; PR aperta, se è attiva la review automatica.
- **Esecuzione**:
  1. Il server mette in coda un **job Actions sintetico**: nessun file nel repo, workflow generato dal server e visibile come run «Claude» nella pagina Actions.
  2. Il job gira su un runner Ervisio con Claude Code (immagine con `claude` CLI). Riceve un token di installazione con i permessi dell'App e la chiave API come secret mascherato.
  3. Un commento di avanzamento viene aggiornato mentre lavora, con link al run.
  4. Le modifiche vanno su un ramo `claude/<issue>-<slug>`, con una PR aperta o aggiornata. Le review sono commenti di review sulle righe. Commit firmati come `claude[bot]`.
- **Sicurezza**:
  - nei repo pubblici un fork non attiva Claude;
  - nessun secret del repo arriva nel job oltre alla chiave Anthropic;
  - la PR resta da approvare a mano, e le protezioni dei rami valgono anche per `claude[bot]`;
  - log del run conservati come gli altri.
- **Compatibilità**: anche la action ufficiale `anthropics/claude-code-action` deve funzionare nei workflow degli utenti (variabili `GITHUB_API_URL`/`GITHUB_SERVER_URL`, token del job, endpoint usati dalla action). È un controllo da fare con la action reale.

## Fase A4 — Elenco `/apps` e rifiniture

- `/apps`: App pubbliche dell'istanza, con ricerca, categorie semplici e pulsante «Installa». Ogni pagina App mostra descrizione, permessi richiesti, eventi, sviluppatore e numero di installazioni.
- Impostazione admin: «chi può registrare App» (tutti / solo org / solo admin) e «App pubbliche consentite».
- `eg`:
  - `eg api` con `--app`?;
  - i comandi `eg app list|view|install` vanno decisi dopo, perché `gh` non li ha.

## UI da passare in galleria (Variant Studio)

1. Form di registrazione App/OAuth App (matrice permessi, eventi, chiavi).
2. Flusso di installazione (scelta account e repo, consenso permessi).
3. Elenco `/apps` e pagina della singola App.
4. Configurazione dell'App Claude e commento di avanzamento in issue/PR.

## Ordine e dipendenze

A0 → A1 → A2 → A3 → A4.
- A1 e A2 condividono la tabella client, le chiavi e la pagina sviluppatore.
- A3 dipende da A2 (installazioni e token) e dal motore Actions.
- Ogni fase si chiude con build, `go vet`, lint e typecheck verdi, CHANGELOG e release minore.

## Decisioni ancora aperte

- Prefissi dei token (deciso in A2): `erps_` per le installazioni; i token user-to-server restano `erp_`, legati al grant dell'utente come quelli delle app OAuth.

- Immagine del runner per Claude: decisa, immagine nostra `ghcr.io/ervisio/claude-runner` con Claude Code preinstallato.
- Tetto di spesa: misurato dai token riportati dall'API Anthropic e salvato per org.
- Dopo la A2: approvazione delle OAuth App da parte delle org (A1) e marketplace con pagamenti (escluso).
