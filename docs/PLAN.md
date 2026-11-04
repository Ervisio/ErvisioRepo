# ErvisioRepo — Implementation Plan

Nov 3, 2026 · @Riccardo

## 1. Visione e decisioni

ErvisioRepo è una forgia Git self-hosted e multi-tenant: repository privati, Actions compatibili con i workflow GitHub e un registry OCI. Lo storage è ibrido: il disco locale fa da cache calda (budget di default 50 GB), mentre la copia canonica dei dati vive su storage esterno (Hetzner StorageBox, Hetzner Object Storage, qualsiasi S3). Se il server si rompe non si perde nulla: basta un server nuovo, la configurazione e le chiavi.

Fa parte dell'ecosistema Ervisio. Gira come app standalone (Docker oppure nativa con systemd) e, quando sul server c'è il core Ervisio, si integra come plugin. Per i domini segue lo stesso modello di ErvisioDrive (git.ervisio.app, git.fonlogen.it).

### Decisioni prese

| Area | Decisione | Motivo |
| --- | --- | --- |
| Motore Git | Codice nostro su git CLI (≥ 2.45) + go-git per letture leggere | Controllo totale del tiering; git CLI è l'implementazione di riferimento del protocollo |
| Backend | Go 1.24+, binario unico | Allineato a `ervisiod`; streaming e concorrenza; packaging nfpm/systemd già noto |
| Frontend | React + Vite + TypeScript, SPA incorporata nel binario | Stesso stack del core |
| Database | PostgreSQL 16+ | Relazioni, permessi, code dei job, ricerca full-text |
| Actions | Sintassi YAML GitHub (`.github/workflows` e `.ervisio/workflows`) | Migrazione immediata dei repo esistenti |
| Isolamento job | Docker di default, Firecracker microVM per job non fidati | Multi-tenant pubblico: i fork e gli utenti esterni non devono mai toccare l'host |
| Utenza | Multi-tenant pubblico: organizzazioni, team, quote, hardening | Requisito esplicito |
| Auth | Email+password con 2FA (TOTP, passkey), OIDC, SSO con il core Ervisio, chiavi SSH | Tutte richieste per la v1 |
| Scope v1 | PR e review, issue con label e milestone, mirror e import da GitHub, webhook, API REST | Le altre funzioni vanno in roadmap |

### Obiettivi misurabili della v1

- Perdita di un disco o del server: RPO ≤ 1 push per i repo in modalità `sync` (≤ 60 s in modalità `async`), RTO ≤ 30 minuti per tornare operativi.
- Clone di un repo in cache: prestazioni pari a `git daemon` sullo stesso hardware (±10%).
- Clone di un repo freddo da 1 GB su Object Storage nella stessa regione: primo byte entro 90 s.
- Un workflow GitHub tipico (checkout, setup-node, test, upload-artifact, docker build e push sul registry) gira senza modifiche.
- Il registry supera la suite di conformità OCI Distribution Spec v1.1.

### Non-obiettivi della v1

- Pages, Wiki, Discussions, Projects (le board kanban), Codespaces, Copilot.
- Federazione tra istanze (ForgeFed).
- Runner Windows e macOS: la v1 ha solo runner Linux x86\_64; arm64 va in roadmap.
- Replica attiva-attiva su più server: c'è un solo nodo di controllo, con disaster recovery da storage remoto.

## 2. Stack tecnologico

Un solo modulo Go produce tre binari: `ervisio-repo`, il server; `ervisio-runner`, l'agente che esegue i job; `erepo`, la CLI. La SPA React è incorporata con `embed.FS`. Le dipendenze vanno fissate con le versioni esatte nel `go.mod` della Fase 0. Le versioni indicate qui sono minime.

### Backend (Go)

| Ambito | Libreria / strumento | Note |
| --- | --- | --- |
| HTTP router | `go-chi/chi` v5 | Middleware semplici, nessuna magia |
| API REST | OpenAPI 3.1 scritto a mano in `api/openapi.yaml` + `oapi-codegen` (strict server) | Contratto unico per la SPA, la CLI e il plugin |
| DB | `jackc/pgx` v5 + `sqlc` | SQL esplicito con tipi generati |
| Migrazioni | `pressly/goose` v3, incorporate nel binario | `ervisio-repo migrate up` all'avvio |
| Code di job | `riverqueue/river` (coda su Postgres) | Nessun Redis: hydration, eviction, backup, webhook e mirror girano qui |
| Git | `git` CLI ≥ 2.45 via `os/exec` (argv, mai shell) + `go-git` v5 solo per letture semplici | `cat-file --batch` long-lived per albero, blob e diff |
| SSH | `golang.org/x/crypto/ssh` (server incorporato) | Porta 2222, comandi `git-upload-pack`, `git-receive-pack`, `git-lfs-authenticate` |
| S3 | `aws-sdk-go-v2/service/s3` | Funziona con Hetzner Object Storage, AWS, R2 e MinIO (path-style configurabile) |
| StorageBox | `pkg/sftp` (già usato dal core) + `x/crypto/ssh` | Protocollo primario per StorageBox; WebDAV come alternativa opzionale |
| Cifratura | AES-256-GCM a chunk da 64 KiB con DEK per oggetto, avvolta da KEK (HKDF) | Stesso schema di `crypto-store.ts` di ErvisioDrive, portato in Go |
| Auth | `go-webauthn/webauthn`, `pquerna/otp`, `coreos/go-oidc` v3, `golang.org/x/oauth2`, argon2id | Sessioni con cookie opaco salvato in DB |
| Actions | Parser YAML con `goccy/go-yaml`; motore di espressioni nostro; esecuzione basata su un fork di `nektos/act` (MIT) | Vedi §6: è il punto che fa risparmiare mesi |
| Registry OCI | Implementazione nostra della Distribution Spec v1.1, con `opencontainers/image-spec` per i tipi | Blob sullo storage a livelli |
| Config | TOML (`BurntSushi/toml`, come il core) + override con env `EREPO_*` | Un file `/etc/ervisio-repo/config.toml` |
| Log e metriche | `log/slog` (JSON), `prometheus/client_golang`, OpenTelemetry tracing opzionale | `/metrics` protetto |
| Test | `testing` + `testcontainers-go` (Postgres, MinIO, sshd per SFTP) + test di sicurezza `sec_*_test.go` | Convenzione del core |

### Frontend

| Ambito | Scelta | Note |
| --- | --- | --- |
| Base | React 19 + TypeScript + Vite | Come il core (che usa React 18.3: allineare quando il core passa alla 19) |
| Routing e dati | TanStack Router + TanStack Query | Route tipizzate, cache e invalidazione per le liste di PR e job |
| Client API | `openapi-typescript` + `openapi-fetch`, generati da `api/openapi.yaml` | Nessun tipo scritto a mano |
| UI kit | Componenti nostri sopra le primitive accessibili Radix (dialog, menu, popover, tabs) | Il core non usa librerie UI: Radix porta solo comportamento, lo stile resta nostro |
| Stile | CSS con i token del core Ervisio (`tokens.css`: OLED, hue per sezione, raggi 12/18/20) | Coerenza visiva con il plugin; la UI la definiamo insieme dopo |
| Codice | Shiki (evidenziazione, temi da token), CodeMirror 6 (editor web), diff renderer nostro con virtualizzazione | I diff grandi non devono bloccare il browser |
| Markdown | `react-markdown` + `remark-gfm` + `rehype-sanitize` | Mermaid e math in lazy load |
| Log dei job | `@xterm/xterm` (già nel core) in streaming via SSE | ANSI nativo |
| i18n | `useT(namespace)` con JSON `i18n/{en,it}/<ns>.json` + `check-i18n` | Stessa convenzione del core |
| Test | `node --test` per la logica, Playwright a 360, 390, 768 e 1280 px | Come ErvisioDrive |

### Infrastruttura

- Postgres 16 (container nello stack Docker, oppure pacchetto di sistema nell'installazione nativa).
- Docker Engine ≥ 25 sul nodo runner. Firecracker ≥ 1.7 + `firecracker-containerd` (o un launcher nostro) per i job isolati.
- Caddy come reverse proxy TLS, configurato con lo stesso flusso a snippet di ErvisioDrive.
- Nessun Redis né broker esterno: Postgres fa da DB, coda e pub/sub (`LISTEN/NOTIFY` per lo streaming dei log).

## 3. Architettura

Il sistema ha un nodo di controllo e uno o più nodi runner, e tutto lo stato durevole sta fuori dal server. Proposta per il tuo host Proxmox (Ryzen 7 7700, 64 GB):

- **Una sola VM** (decisione presa): l'attuale VM da 8 vCPU e 16 GB ospita `ervisio-repo`, Postgres, Caddy, il core Ervisio e `ervisio-runner` con Docker e Firecracker. CPU di tipo `host` con nested virtualization attiva per Firecracker.
- **Separazione con cgroup**: server e Postgres stanno in una slice systemd `ervisio-repo.slice` con riserva garantita (`CPUWeight` alto, `MemoryMin=4G`). I job stanno in `ervisio-runner.slice` con un tetto rigido (`CPUQuota=600%`, `MemoryMax=12G`), così un job pesante rallenta solo gli altri job e mai i clone. Nella modalità Docker si ottiene lo stesso con `cpus` e `mem_limit` nel compose.
- **Disco**: almeno 150 GB, di cui 50 GB di cache, circa 40 GB per immagini Docker e workspace dei job (ripuliti a fine job) e il resto per sistema e Postgres.
- **Se serve più margine**: l'host ha 64 GB, quindi portare la VM a 24 GB lascia 16 GB ai job, cioè 4 job paralleli da 4 GB. Il runner resta comunque un processo separato che si può spostare su un'altra VM in seguito senza modifiche, perché comunica col server solo in uscita.

&#91;embedded content: architettura · una VM su Proxmox, 3 livelli di storage\]

Le frecce seguono la direzione delle richieste. Il runner contatta solo il server (nessuna porta in ingresso), il plugin del core parla col server solo tramite il socket e configura Caddy. Il motore tier è l'unico componente che legge e scrive sugli storage.

## 4. Storage ibrido a livelli

Sì, si può fare, e la regola chiave è una: **il disco locale è solo una cache, la copia canonica è sempre remota**. Ogni scrittura (push, oggetto LFS, layer di un'immagine, artifact) è confermata solo quando è sul livello remoto, oppure entro 60 s nella modalità asincrona. Lo sfratto (eviction) di un repo dal disco locale avviene solo se la copia remota è verificata. Un recupero (hydration) riscarica il repo quando serve.

### 4.1 Astrazione BlobStore

Un'unica interfaccia in `internal/storage/blob`, implementata da ogni driver:

```go
type Store interface {
    Name() string
    Caps() Caps // Range, Presign, Multipart, AtomicRename, MaxConns
    Put(ctx context.Context, key string, r io.Reader, size int64, o PutOpts) (Info, error)
    Get(ctx context.Context, key string, rng *Range) (io.ReadCloser, Info, error)
    Stat(ctx context.Context, key string) (Info, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string, fn func(Info) error) error
}

// Facoltative, rilevate con type assertion
type Presigner interface {
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    PresignPut(ctx context.Context, key string, size int64, sha256 string, ttl time.Duration) (string, http.Header, error)
}
```

| Driver | Uso tipico | Presign | Note |
| --- | --- | --- | --- |
| `local` | Cache calda, installazioni piccole | No | Scrittura su file temporaneo + `rename` atomico + `fsync` |
| `s3` | Hetzner Object Storage, AWS S3, R2, MinIO | Sì | Multipart sopra i 64 MiB; endpoint, regione e path-style configurabili |
| `sftp` | Hetzner StorageBox (porta 23, chiave SSH, sub-account) | No | Pool di connessioni limitato da `max_conns`; `rename` atomico; niente presign, quindi i download passano dal server |
| `webdav` | StorageBox o NAS in alternativa (opzionale, Fase 9) | No | Solo se SFTP non è disponibile |

I wrapper si compongono sopra qualunque driver: `encrypted` (AES-256-GCM a chunk, chiave per oggetto, KEK derivata dalla master key), `metered` (metriche e quote), `retry` (backoff esponenziale con jitter).

### 4.2 Target e policy in configurazione

I **target** sono gli storage fisici. Le **policy** dicono, per ogni tipo di dato, dove sta la copia primaria, dove vanno le repliche e quanto resta in cache.

```toml
[[storage.target]]
name = "hot"
kind = "local"
path = "/var/lib/ervisio-repo/hot"
budget = "50GB"          # budget totale della cache calda
high_watermark = 0.90
low_watermark  = 0.75

[[storage.target]]
name = "objstore"
kind = "s3"
endpoint = "https://fsn1.your-objectstorage.com"
bucket = "ervisio-repo"
region = "fsn1"
encrypt = true

[[storage.target]]
name = "box"
kind = "sftp"
host = "uXXXXX.your-storagebox.de"
port = 23
user = "uXXXXX-sub1"
key_file = "/etc/ervisio-repo/storagebox_ed25519"
known_hosts_file = "/etc/ervisio-repo/storagebox_known_hosts"
max_conns = 8
encrypt = true

[storage.policy.git]
primary  = "objstore"
replicas = ["box"]
cache    = "hot"
cache_ttl = "7d"
durability = "sync"      # sync | async

[storage.policy.lfs]
primary = "objstore"
replicas = ["box"]
cache = "hot"
cache_ttl = "7d"
cache_share = 0.25      # quota del budget hot riservata a LFS

[storage.policy.registry]
primary = "objstore"
cache = "hot"
cache_ttl = "3d"

[storage.policy.actions]   # artifact, cache e log dei job
primary = "objstore"
cache_ttl = "1d"
retention = "90d"

[storage.policy.backup]    # WAL e base backup di Postgres
primary = "box"
replicas = ["objstore"]
```

Una configurazione solo locale resta valida: `primary = "hot"`, senza tiering. È utile per sviluppo e installazioni piccole. In quel caso l'avvio mostra un avviso permanente in UI che dice che non c'è protezione dai guasti del disco.

### 4.3 Tiering dei repository Git: il «pack journal»

Un repo Git non si può spezzare in file indipendenti come un drive. Per questo la copia remota è un **journal** di pack immutabili più degli snapshot dei ref, con questo layout di chiavi (`<repo>` è l'UUID del repo, stabile anche dopo un rename):

```
git/<repo>/manifest.json            # generazione corrente, base, segmenti, seq dei ref
git/<repo>/base/<gen>.pack|.idx     # pack completo della generazione
git/<repo>/seg/<seq>-<sha>.pack|.idx  # oggetti nuovi di un singolo push
git/<repo>/refs/<seq>.json          # snapshot completo dei ref dopo il push <seq>
```

**Scrittura (push, merge da UI, sync del mirror).** Tutte le scritture passano dagli hook di Git, che il binario installa e che parlano col server via socket unix.

1. `pre-receive` gira mentre gli oggetti sono ancora nell'area di quarantena di Git. Calcola gli oggetti nuovi con `git rev-list --objects <nuovi tip> --not --all`, li impacchetta con `git pack-objects` (pack autosufficiente, mai thin) e carica il segmento.
2. `reference-transaction` (stato `committed`) scrive `refs/<seq>.json` e aggiorna `manifest.json` con una scrittura condizionale (`If-Match` su S3; su SFTP file temporaneo + `rename`).
3. In modalità `sync` il client riceve l'esito del push solo dopo i passi 1 e 2. In modalità `async` i passi vengono accodati come job River, con RPO tipico sotto i 60 s.
4. Se il push fallisce dopo il passo 1, il segmento resta orfano e la garbage collection lo rimuove dopo 24 h.

**Compattazione.** Quando i segmenti superano 64 oppure il 30% della base, un job crea una nuova generazione: `git repack -ad` locale, upload del nuovo base pack, nuovo manifest. Le due generazioni precedenti restano per 7 giorni come rete di sicurezza.

**Hydration (repo freddo → caldo).**

1. Una richiesta (HTTP, SSH, UI o job) trova il repo nello stato `cold` e prende un lock per repo. Le richieste concorrenti aspettano la stessa operazione (singleflight).
2. Il server scarica il manifest, poi base e segmenti in parallelo. Ogni pack va direttamente in `git index-pack --stdin`, senza file intermedi. I ref arrivano dall'ultimo snapshot con `git update-ref --stdin`.
3. Poi rigenera `commit-graph` e `multi-pack-index` e controlla che i tip dei ref esistano. In modalità `paranoid` esegue anche `git fsck`.
4. Il repo passa allo stato `hot`. Su SSH l'avanzamento appare nel terminale dell'utente (`remote: recupero dallo storage remoto… 42%`); nella UI con una barra via SSE. Su HTTP il client aspetta fino a 10 minuti.

**Eviction (caldo → freddo).** Un job gira ogni 15 minuti e sfratta un repo solo se valgono tutte queste condizioni:

- l'ultimo accesso è più vecchio di `cache_ttl`, oppure l'uso della cache supera `high_watermark` (in quel caso si scende fino a `low_watermark`, in ordine LRU pesato per dimensione);
- la `seq` del manifest remoto è uguale a quella locale ed è verificata;
- il repo non è fissato (`pinned`) e non ha operazioni in corso (lock libero, nessun job Actions che lo sta clonando).

Lo sfratto sposta la cartella in `hot/.trash/` e la cancella dopo 1 ora: un ripensamento costa zero. Gli stati salvati nel DB sono `hot`, `cold`, `hydrating`, `evicting`, `degraded` (manifest illeggibile, intervento manuale).

&#91;embedded content: stati di un repo · 5 stati, 7 transizioni\]

Lo sfratto parte quando il TTL è scaduto o la cache supera il 90%, e solo se la copia remota è verificata; ogni push su un repo caldo aggiunge un segmento al journal.

**Dimensioni e trade-off.** Lo scenario «max 50 GB in locale, 7 giorni, globale illimitato» corrisponde a `budget = "50GB"`, `cache_ttl = "7d"`, primary su Object Storage. Il primo clone di un repo freddo da 2 GB richiede circa 20–40 s a 1 Gbit/s, più il tempo di indicizzazione. Un ref advertisement servito dal manifest senza hydration completa è un'ottimizzazione possibile in Fase 9.

### 4.4 Git LFS: mai solo in locale

Gli oggetti LFS sono indirizzati per contenuto (`lfs/sha256/ab/cd/<oid>`), deduplicati a livello globale e autorizzati per repo tramite la tabella `lfs_objects(repo_id, oid)`.

| Operazione | Target con presign (S3, Object Storage) | Target senza presign (StorageBox) |
| --- | --- | --- |
| Upload | Batch API → URL PUT prefirmato: il client carica direttamente sul bucket, poi l'azione `verify` fa HEAD e registra l'oggetto | Il client carica sul server, che inoltra in streaming a SFTP verificando lo sha256 al volo; la conferma arriva dopo la scrittura remota |
| Download | Dalla cache locale se presente, altrimenti redirect a un URL GET prefirmato (TTL 15 min) | Proxy dal server, con scrittura nella cache LRU locale |
| Con `encrypt = true` | Niente presign: proxy con decifratura e cache | Proxy con decifratura e cache |

C'è anche la Locks API di LFS. La quota LFS si conta per organizzazione.

### 4.5 Backup e disaster recovery

- **Postgres.** Archiviazione continua dei WAL e base backup giornaliero con `wal-g` (target S3 o SSH, quindi anche StorageBox), retention 14 giorni. Nell'installazione Docker gira come sidecar.
- **Repliche.** Un job copia ogni oggetto dal primary ai replica e ne verifica lo sha256. Con Object Storage come primary e StorageBox come replica, più gli snapshot automatici della StorageBox, ci sono tre copie, di cui una con cronologia.
- **Recovery kit.** `ervisio-repo keys export` produce un file cifrato con passphrase (formato age) che contiene master key, configurazione dei target e chiave host SSH. Senza questo kit i dati cifrati sono irrecuperabili, quindi la UI blocca la cifratura finché l'admin non conferma di aver scaricato il kit.
- **Procedura DR.** Server nuovo → installazione → `ervisio-repo restore --kit kit.age --from objstore` → ripristino del DB all'ultimo WAL → tutti i repo segnati `cold` → avvio. I repo tornano caldi alla prima richiesta; `ervisio-repo prewarm --top 20` scalda quelli più usati.
- **Verifica continua.** Un job settimanale scarica a campione segmenti e oggetti e ne controlla lo sha256. Un test di integrazione «chaos» in CI cancella il disco locale e il DB, esegue il restore e confronta ref e `fsck` di tutti i repo.

## 5. Server Git e collaborazione

Il protocollo lo gestisce `git` stesso. ErvisioRepo si occupa di autenticazione, autorizzazione, hook, tiering e di tutto quello che sta sopra: PR, review, issue, mirror.

### 5.1 Trasporti

- **Smart HTTP** su `https://<host>/<owner>/<repo>.git`: `info/refs`, `git-upload-pack`, `git-receive-pack`, protocollo v2 abilitato (`GIT_PROTOCOL`), risposte gzip in streaming, nessun buffering in memoria. Auth: token personale o di job come password Basic, oppure cookie di sessione per la UI.
- **SSH** sulla porta 2222 (configurabile; la 22 resta al sistema). Login con l'utente `git`; la chiave pubblica identifica l'utente o la deploy key. Comandi ammessi: `git-upload-pack`, `git-receive-pack`, `git-upload-archive`, `git-lfs-authenticate`, `git-lfs-transfer`. Chiavi host ed25519 + RSA generate al primo avvio e incluse nel recovery kit.
- **Caddy non fa da proxy per SSH** (lavora a livello HTTP): l'SSH va esposto con un port forward diretto dalla VM. Su Proxmox serve il NAT o il bridge della VM sulla porta 2222 pubblica.
- **Limiti**: dimensione massima di un push (default 2 GiB), file singolo fuori LFS (default 100 MiB, con suggerimento LFS nel messaggio di rifiuto), rate limit per IP e per token, timeout di inattività.

### 5.2 Layout su disco e operazioni

- Repo bare in `hot/git/<aa>/<repo-uuid>.git`: l'UUID rende i rename istantanei, mentre il path pubblico `owner/nome` vive solo nel DB, con redirect dai vecchi nomi.
- Wrapper `internal/git` con `Run(ctx, repoPath, args...)` (argv, ambiente pulito, `GIT_CONFIG_NOSYSTEM=1`, timeout) e un pool di processi `cat-file --batch` per le letture della UI.
- Manutenzione: `git maintenance run --task=...` accodata dopo N push (commit-graph, loose-objects, incremental-repack). Il GC completo coincide con la compattazione del journal (§4.3).
- Repo bloccato sul nodo di controllo: un lock di scrittura per repo (advisory lock Postgres), così le operazioni della UI (merge, edit web) non entrano in conflitto con i push.

### 5.3 Modello di permessi

| Livello | Ruoli | Note |
| --- | --- | --- |
| Istanza | `admin`, `user`, `restricted` | `restricted` vede solo ciò a cui è invitato |
| Organizzazione | `owner`, `member`, `billing` (futuro) | Team con permessi sui repo |
| Repo | `read`, `triage`, `write`, `maintain`, `admin` | Come GitHub, così l'import mappa i permessi uno a uno |
| Token | Scope fini (`repo:read`, `repo:write`, `packages:write`, `actions:write`, …), scadenza obbligatoria, limitabili a repo o organizzazioni | Prefisso `erp_` (personali), `erj_` (job), `erd_` (deploy). In DB solo l'hash SHA-256, come ErvisioDrive |

**Branch protection**: pattern glob, review obbligatorie (N approvazioni, CODEOWNERS, invalidazione su nuovo push), status check obbligatori (Actions), storia lineare, divieto di force-push e di delete, firma dei commit obbligatoria (GPG e SSH). I controlli sono in `pre-receive`, quindi valgono anche per i push SSH, non solo per la UI. Ci sono anche le **regole sui tag**.

### 5.4 Pull request e review

- PR dallo stesso repo o da un fork. I fork condividono gli oggetti tramite `objects/info/alternates` verso un repo «pool» per rete di fork, come fa GitHub, e il journal remoto del pool è condiviso.
- Ref nascosti `refs/pull/<n>/head` e `refs/pull/<n>/merge`. La merge commit di prova si ricalcola a ogni push, in un worktree temporaneo con `git merge-tree --write-tree` (senza checkout, quindi veloce).
- Diff: `git diff --patch --find-renames` paginato per file, rilevamento dei file generati e vendor (pattern come `linguist-generated`), word-diff opzionale.
- Review: commenti inline su riga o range, suggested changes applicabili con un clic, thread risolvibili, stati `approved`, `changes_requested`, `commented`. I commenti restano ancorati anche dopo un force-push, tramite la mappatura delle righe sul nuovo diff.
- Merge: `merge commit`, `squash`, `rebase`, con auto-merge quando i check passano, e l'opzione di cancellare il branch.
- Integrazione con Actions: gli eventi `pull_request`, `pull_request_target` e `pull_request_review`; i check run sono mostrati nella PR.

### 5.5 Issue

Markdown con allegati (salvati sullo storage a livelli), label, milestone, assegnatari, menzioni con notifiche, riferimenti incrociati (`#123`, `owner/repo#45`, SHA dei commit), chiusura automatica da commit e PR (`fixes #12`), template in `.github/ISSUE_TEMPLATE`, reazioni e ricerca full-text (`tsvector` di Postgres). Notifiche in-app più email tramite SMTP configurabile.

### 5.6 Import, backup e sync con GitHub

GitHub diventa una destinazione in più per il codice: una copia di sicurezza esterna o un secondo remote sempre allineato. Si sincronizzano solo branch e tag. LFS, issue, PR, release e Actions restano su ErvisioRepo.

**Connessione.** Il metodo consigliato è una GitHub App installata sull'organizzazione o sull'account GitHub: ha permessi fini (`contents: read/write`, `metadata`, `administration` solo se deve creare i repo), token brevi e webhook firmati. In alternativa si può usare un token fine-grained. Le credenziali sono cifrate in DB come i segreti delle Actions. Una connessione appartiene a un utente o a un'organizzazione di ErvisioRepo e si riusa su più repo.

| Modalità | Direzione | Quando parte | Uso tipico |
| --- | --- | --- | --- |
| Import una tantum | GitHub → ErvisioRepo | Manuale | Migrazione di repo o di un'intera organizzazione, con issue, PR, label, milestone e release |
| Pull mirror | GitHub → ErvisioRepo, repo in sola lettura | Webhook `push` della GitHub App, più un fetch periodico di sicurezza (minimo 10 minuti) | Tenere una copia locale di progetti che vivono su GitHub |
| Backup | ErvisioRepo → GitHub, unidirezionale | Dopo ogni push (raggruppati in 30 s) e con un controllo notturno | Copia di sicurezza fuori da Hetzner |
| Sync bidirezionale | In entrambe le direzioni, entrambi scrivibili | Push su ErvisioRepo e webhook `push` da GitHub | Migrazione graduale o collaboratori che restano su GitHub |

**Backup verso GitHub.**

- Per ogni repo, oppure per un'intera organizzazione con una regola (ad esempio tutti i repo di `fonlogen` verso l'organizzazione GitHub `fonlogen-backup`). Se il repo su GitHub non esiste, viene creato privato.
- Si spingono solo `refs/heads/*` e `refs/tags/*`. I ref interni (`refs/pull/*`, `refs/ervisio/*`) non escono mai. La cancellazione di un branch si propaga solo se l'opzione `prune` è attiva; di default resta su GitHub, che così fa anche da cestino.
- **LFS escluso**: il push parte dal server senza hook LFS, quindi su GitHub arrivano solo i file puntatore. La UI lo segnala sul repo, così nessuno scambia la copia GitHub per un backup completo, che resta sullo storage a livelli.
- I push rifiutati da GitHub (ad esempio file oltre il limite di GitHub fuori da LFS, o regole di protezione dei branch su GitHub) diventano un errore visibile sul repo, con notifica e nuovo tentativo con backoff. Il backup non blocca mai i push degli utenti su ErvisioRepo.
- Pagina «Backup GitHub» con lo stato per repo: ultimo SHA spinto per ogni ref, ritardo, errori.

**Sync bidirezionale.**

1. Gli aggiornamenti da GitHub arrivano col webhook `push`. Il server fa fetch in un namespace di appoggio (`refs/sync/github/*`), poi applica ogni ref con il normale percorso di scrittura (hook, journal, branch protection, trigger delle Actions con `event: push`).
2. Si applica solo il **fast-forward**. Se un ref è divergente (commit diversi su entrambi i lati), non si sovrascrive nulla: il ref va nello stato `diverged`, il commit di GitHub viene salvato in `sync-conflict/github/<branch>` e il maintainer sceglie dalla UI fra tenere la versione di ErvisioRepo, quella di GitHub o fare un merge.
3. Contro i cicli: ogni operazione di sync porta un'origine e si salta quando lo SHA è già uguale sull'altro lato.
4. I branch protetti su ErvisioRepo accettano aggiornamenti da GitHub solo se l'opzione «fidati di GitHub per questo branch» è attiva; altrimenti l'aggiornamento diventa un conflitto da approvare.
5. Un lock per repo serializza la sync con i push locali, così due aggiornamenti contemporanei non si scavalcano.

**Task aggiuntivi in Fase 5**: client GitHub App (JWT, token di installazione, verifica della firma dei webhook), modello `sync_links` (repo, connessione, modalità, opzioni, stato per ref), worker River per push e fetch, UI di stato e dei conflitti, test con un server Git finto e con un repo GitHub reale di prova.

**Accettazione aggiuntiva**: un push su ErvisioRepo appare su GitHub entro 60 s; un push su GitHub appare su ErvisioRepo entro 60 s in modalità sync; un branch divergente non perde mai commit su nessuno dei due lati (test `sec_sync_*`).

### 5.7 Webhook e API

- **API REST v1** (`/api/v1/...`) definita in OpenAPI e completa per tutte le funzioni della UI: la SPA usa solo l'API pubblica.
- **Sottoinsieme compatibile con GitHub REST** (`/api/v3/...`) per i casi che contano davvero: repo, contenuti, PR, issue, release, status e check, così che `gh` (con `GH_HOST`), Renovate e Terraform possano funzionare. È una compatibilità dichiarata endpoint per endpoint, con test di contratto.
- **Webhook** in uscita con payload in formato GitHub (`X-GitHub-Event`, `X-Hub-Signature-256`) e anche in formato Ervisio; consegna con retry esponenziale, cronologia delle consegne e redelivery dalla UI. Le URL verso IP privati sono bloccate di default (protezione SSRF), a meno che l'admin non le consenta.

## 6. Ervisio Actions

I workflow GitHub girano senza modifiche. Il lavoro è diviso in due: il **server** decide *cosa* eseguire (trigger, grafo dei job, matrix, concurrency, permessi), mentre il **runner** (`ervisio-runner`) decide *come* eseguirlo. Per l'esecuzione degli step il runner usa come libreria il fork di `nektos/act` mantenuto da Gitea per `act_runner` (licenza MIT), su cui lavora la community Gitea e Forgejo da anni. È l'unico componente non scritto da zero ed è la scelta che fa risparmiare più tempo.

### 6.1 Server: orchestrazione

- **Rilevamento**: a ogni evento si leggono `.ervisio/workflows/*.yml` e `.github/workflows/*.yml` dal commit interessato (`.ervisio` ha la precedenza a parità di nome file).
- **Trigger supportati in v1**: `push`, `pull_request`, `pull_request_target`, `pull_request_review`, `workflow_dispatch` (con input dalla UI), `workflow_call` (workflow riutilizzabili, anche cross-repo), `schedule` (cron, minimo 5 minuti), `release`, `issues`, `issue_comment`, `create`, `delete`, `repository_dispatch`. Filtri `branches`, `tags`, `paths`, `types`.
- **Pianificazione**: espansione della matrix (massimo 256 job, `include`/`exclude`, `fail-fast`, `max-parallel`), grafo `needs`, `if` a livello di job, `concurrency` con `cancel-in-progress`, `environment` con approvazione manuale, `timeout-minutes` (limite massimo di istanza).
- **Espressioni** `${{ }}`: riuso del parser di act (`pkg/exprparser`) lato server, con test di parità rispetto al runner.
- **Token del job**: `erj_…` valido per la durata del job, limitato al repo e ai `permissions:` del workflow (default in sola lettura). Vale per git, API e registry, ed è esposto come `GITHUB_TOKEN` e `ERVISIO_TOKEN`.
- **Contesti**: `github.*` popolato con valori compatibili (`server_url`, `api_url` puntato a `/api/v3`, `repository`, `sha`, `ref`, `event`), più `ervisio.*` come alias.
- **Stato**: check run e commit status su PR e commit, badge SVG, notifiche in caso di fallimento.

### 6.2 Protocollo runner

- Il runner si registra con un token di registrazione (di istanza, di organizzazione o di repo) e riceve credenziali proprie.
- Comunicazione **ConnectRPC su HTTPS** in sola uscita dal runner: `FetchTask` in long-poll, `UpdateTask`, `UpdateLog` (chunk compressi), `Heartbeat`. Nessuna porta in ingresso sul runner, quindi i runner possono stare in altre VM o su altre macchine.
- **Etichette**: `runs-on: ubuntu-latest` viene mappato a un'immagine tramite configurazione del runner, ad esempio `ubuntu-latest=registry.ervisio.app/runner-images/ubuntu:24.04-full`. Le immagini runner si costruiscono con un repo dedicato e si ospitano sul nostro registry.
- Le **action remote** (`uses: actions/checkout@v4`) si risolvono tramite un proxy-cache: al primo uso il server crea un mirror di sola lettura in un'organizzazione interna `_actions`, fissato allo SHA risolto. L'admin può disattivare il fetch esterno (modalità air-gapped) e consentire solo i mirror approvati.

### 6.3 Backend di esecuzione e isolamento

| Backend | Isolamento | Quando | Requisiti |
| --- | --- | --- | --- |
| `docker` | Container per job, rete dedicata, utente non root, `no-new-privileges`, seccomp di default, limiti cgroup su CPU, memoria e PID | Repo e utenti fidati (organizzazioni della stessa istanza) | Docker Engine ≥ 25 sulla VM runner |
| `docker+gvisor` (Fase 9) | Kernel utente `runsc` | Via di mezzo senza KVM | gVisor installato |
| `microvm` | Una microVM Firecracker per job, con kernel e rootfs propri e Docker dentro la VM per `container:` e `services:` | PR da fork, organizzazioni non verificate, istanza pubblica | KVM nella VM: su Proxmox CPU di tipo `host` e nested virtualization attiva sull'host (`kvm_amd nested=1`) |

Una **policy di fiducia** sceglie il backend: `trusted` va su `docker`, `untrusted` su `microvm`, e un job non fidato non può mai finire su `docker`. Sono non fidati: le PR da fork, gli utenti con account più recente di N giorni e le organizzazioni non verificate dall'admin. I segreti non vengono mai passati alle PR da fork, tranne che con `pull_request_target` e solo dopo l'approvazione di un maintainer per i contributor esterni.

I dettagli della microVM: rootfs ext4 copy-on-write per job, rete tap + NAT con firewall di egress opzionale (blocco degli IP privati e della rete di Proxmox), snapshot della VM già avviata per partire in meno di 1 s, distruzione completa a fine job.

**Capacità sulla VM attuale (8 core, 16 GB)**: con il server sulla stessa VM restano circa 6 core e 12 GB per i job, quindi 3 job da 2 vCPU e 4 GB, oppure 2 microVM da 2 vCPU e 4 GB più un job Docker. Server e runner condividono la stessa VM, con i limiti cgroup del §3 a proteggere il server.

### 6.4 Servizi accessori per la compatibilità

- **Cache** (`actions/cache` v4): implementazione del servizio di cache Twirp v2 che il toolkit usa dal 2025, sullo storage a livelli. Chiavi con scope per repo e branch come su GitHub (un branch legge la propria cache e quella del branch di default), limite di 10 GB per repo con eviction LRU.
- **Artifact** (`actions/upload-artifact` e `download-artifact` v4): servizio artifact v4, con upload a blocchi sullo storage a livelli, retention configurabile (default 90 giorni) e download zip dalla UI. Gitea ha già implementato questo protocollo: va usato come riferimento e verificato con test di integrazione sulle versioni correnti delle action ufficiali.
- **Log**: i chunk vanno in Postgres mentre il job gira (streaming via SSE nella UI, con `LISTEN/NOTIFY`); a fine job diventano un oggetto compresso sullo storage e le righe in Postgres vengono cancellate. I segreti sono mascherati sia nel runner sia nel server.
- **Segreti e variabili** a livello di istanza, organizzazione, repo ed environment. Sono cifrati con envelope (DEK per segreto, KEK dalla master key) e non vengono mai restituiti dall'API dopo la scrittura.
- **OIDC per i job** (`id-token: write`), cioè ErvisioRepo come identity provider per deploy senza segreti: Fase 9.

## 7. Registry OCI (sostituto di GHCR)

Il registry implementa la OCI Distribution Spec v1.1 sotto `https://<host>/v2/`, sullo stesso dominio della forgia: `docker login git.ervisio.app` e poi `git.ervisio.app/<org>/<immagine>:tag`. Un sottodominio dedicato (`cr.ervisio.app`) è opzionale e si configura con lo stesso flusso Caddy.

- **Auth**: Docker token auth (`WWW-Authenticate: Bearer realm=.../v2/token`) che emette JWT brevi con scope `repository:<nome>:pull,push`. Si accettano token personali, token dei job (`permissions: packages: write`) e accesso anonimo per i pacchetti pubblici.
- **Blob**: content-addressed in `oci/blobs/sha256/<aa>/<digest>`, deduplicati a livello globale, con cache locale (TTL 3 giorni). Il download usa un redirect 307 a URL prefirmati quando il target lo permette e la cifratura è spenta; altrimenti passa in streaming dal server.
- **Upload**: monolitico e a chunk (`PATCH` con `Content-Range`), sessioni di upload salvate in DB e ripristinabili, verifica del digest prima del commit, cross-repo mount (`?mount=&from=`) senza copie.
- **Manifest**: image manifest e index (multi-arch), referrers API (firme cosign, SBOM, attestazioni), tag immutabili opzionali per pattern.
- **Collegamento ai repo**: un pacchetto appartiene a un'organizzazione o a un utente e si può collegare a un repo (label `org.opencontainers.image.source`), da cui eredita la visibilità e i permessi. Pagina pacchetto nella UI con tag, dimensioni, comando di pull e cronologia.
- **Retention e GC**: policy per pacchetto (tieni gli ultimi N tag, cancella le immagini senza tag più vecchie di X giorni). La garbage collection dei blob non referenziati è un mark-and-sweep in due fasi con 48 h di grazia, così non cancella un upload in corso.
- **Conformità**: la suite ufficiale di conformità OCI gira in CI, insieme a test end-to-end con `docker`, `podman`, `buildx` (multi-arch) e `oras`.
- **Roadmap**: registry npm, Maven, PyPI, Cargo e Helm (OCI per Helm è già coperto in v1).

## 8. Identità, multi-tenant e sicurezza

Per un'istanza pubblica la sicurezza è un requisito della v1, non una rifinitura: ogni fase della roadmap ha i propri test `sec_*`.

### 8.1 Autenticazione

- **Locale**: email + password con hash argon2id, verifica email, reset via email, password controllate contro le liste di password compromesse (k-anonymity HIBP opzionale).
- **2FA**: TOTP e passkey WebAuthn (anche come primo fattore senza password), codici di recupero, 2FA obbligatoria configurabile per istanza o organizzazione.
- **OIDC**: provider generici configurabili (Google, Cognito, Keycloak, Authentik, GitHub OAuth), con PKCE e collegamento a un account esistente. Il link è legato a `issuer#sub`, mai alla sola email, come in ErvisioDrive.
- **SSO con il core Ervisio**: oggi il core non ha un'identità condivisa con i plugin, quindi serve una modifica al core (§9.3). Finché non c'è, il login è separato.
- **Sessioni**: cookie opaco `HttpOnly` + `Secure` + `SameSite=Lax`, salvato in DB, revocabile dalla UI; durata 14 giorni con rotazione; protezione CSRF con double-submit token per tutte le mutazioni via cookie.
- **Chiavi SSH** (ed25519, ecdsa, rsa ≥ 3072), firme dei commit verificate (GPG e SSH), deploy key per repo con permesso di sola lettura o di scrittura.

### 8.2 Multi-tenant

- Utenti, organizzazioni e team; visibilità dei repo `private`, `internal` (visibile agli utenti dell'istanza) e `public`.
- Registrazione aperta, su invito oppure chiusa; dominio email consentito per organizzazione; verifica delle organizzazioni da parte dell'admin (sblocca i runner fidati, §6.3).
- **Quote** per utente e organizzazione: spazio git, LFS, pacchetti, artifact e minuti di Actions al mese. Si applicano al push (rifiuto chiaro), all'upload e all'accodamento dei job.
- **Anti-abuso**: rate limit per IP, token e utente (token bucket in memoria più contatori Postgres), CAPTCHA opzionale alla registrazione (provider self-hosted come ALTCHA), blocco dei repo segnalati, log di audit esportabile.

### 8.3 Hardening

- Nessun comando di sistema passa da shell: tutto è argv con validazione (regola del core Ervisio).
- Path traversal: tutti i path dei repo derivano dall'UUID, mai dall'input; per i file si usa `os.Root`.
- Rendering: il markdown è sanificato, l'HTML degli utenti non viene mai servito sul dominio principale; i file raw sono serviti con `Content-Disposition` e `Content-Security-Policy: sandbox`, ed è previsto un dominio separato opzionale per i contenuti degli utenti.
- SSRF: webhook, mirror e import bloccano gli IP privati, link-local e i metadata endpoint, salvo allowlist dell'admin.
- Segreti cifrati con envelope encryption; la master key arriva da file con permessi 0600 o da una variabile d'ambiente, mai dal DB.
- CSP rigida sulla SPA, header di sicurezza standard, cookie con prefisso `__Host-`.
- Dipendenze controllate con `govulncheck` e `npm audit` in CI, SBOM delle release e immagini firmate con cosign.
- Il log di audit registra login, permessi, token, segreti, branch protection, eliminazioni e azioni admin, con retention configurabile.

## 9. Integrazione con l'ecosistema Ervisio

ErvisioRepo segue il modello di ErvisioDrive. È un'app standalone con login, DB e `/api/health` propri. Il plugin è una console admin che gira nell'iframe del core e parla con l'app tramite un'API admin su socket unix protetta da un segreto per processo. La differenza rispetto a Drive è che ErvisioRepo si installa **anche in modo nativo**, sul modello del packaging del core.

### 9.1 Modalità di installazione

| Modalità | Cosa installa | Comando | Note |
| --- | --- | --- | --- |
| Docker Compose | Stack `ervisiorepo`: `postgres`, `app`, `walg` (backup), `runner` (opzionale) | `./setup.sh` (stessi flag di Drive: `--non-interactive --admin-email --url --ervisio yes\|no`) | Rete `private` (Postgres) + `edge` chiamata `ervisiorepo_edge` (solo `app`, per un Caddy in container). Socket montato in `${ERVISIO_SOCKET_DIR}:/run/ervisio-repo` |
| Nativa (.deb/.rpm) | Binari in `/usr/lib/ervisio-repo/versions/<v>` con symlink `current`, unit systemd `ervisio-repo.service` e `ervisio-runner.service`, utente di sistema `ervisio-repo` | `curl … \| sh` (come `install.sh` del core) oppure `apt install ./ervisio-repo_X.Y.Z_amd64.deb` | Postgres dal pacchetto di sistema (dipendenza consigliata) oppure esterno; config in `/etc/ervisio-repo/config.toml`, dati in `/var/lib/ervisio-repo` |
| Solo runner | `ervisio-runner` su un'altra VM o macchina | `ervisio-runner register --url … --token …` | Per separare il calcolo dal nodo di controllo |

Le release sono firmate (SHA256SUMS con firma ed25519, come il core). Le immagini Docker sono multi-arch e firmate con cosign, pubblicate su GHCR finché il registry di ErvisioRepo non le può ospitare (dogfooding dalla Fase 7).

### 9.2 Plugin per il core

- Cartella `plugin/` nel monorepo, costruita con `@ervisio/plugin-sdk` v0.3.x (contract v3), id `repo`, `minCore` pari alla versione del core che porta il supporto SSO (§9.3).
- **Pagine**: «Repo», cioè la console admin con stato del servizio, uso dello storage per livello (hot, Object Storage, StorageBox), stato della cache e dei repo caldi, runner e job in coda, dominio Caddy, utenti admin, installazione e aggiornamento. **Widget** «usage» nell'Overview: spazio locale e remoto, job in esecuzione.
- **Capabilities** (sul modello di Drive):
  - `http` `repo` su `/run/ervisio-repo/api.sock` con `admin: true`, solo i path `/api/plugin/*`;
  - `http` `docker` su `/var/run/docker.sock` con `adminUnlessGroup: "docker"`, ristretto a list, inspect, exec, start e `networks/ervisiorepo_edge/connect`;
  - comandi `install`, `update`, `install-state`, `install-log` tramite `systemd-run` (sia per Docker sia per i pacchetti nativi), più `service-show`, `validate`, `reload` per Caddy;
  - file in lettura e scrittura su `/etc/caddy` (admin) e `~/.config/ervisio/plugins/repo`.
- **API admin su socket** (`/api/plugin/...`): `status` (versione, URL pubblico, upstream `{host, port, container, containerPort, network}`), `settings`, `storage` (target, test di connessione, uso), `cache` (pin e unpin, prewarm, evict), `runners`, `PUT public-url`, `sso/pair`. Il segreto è un header `x-ervisio-plugin` di 32 byte generato a ogni avvio e confrontato in tempo costante; senza segreto la risposta è 404.
- Fallback senza integrazione: individuare il container con le label Compose e usare `docker exec`, come `locate.ts` di Drive.
- Registrazione nel catalogo `Ervisio/plugins` con `trust: team`. La release usa il workflow riutilizzabile `plugin-release.yml` con `working-directory: plugin` e `sync-root-version: true`.

### 9.3 SSO con il core (richiede una modifica al core)

Oggi i plugin non vedono l'identità dell'utente del core. La proposta, da realizzare in `ervisio` e `plugin-sdk`:

1. Il core aggiunge `sdk.identity.assertion(audience)`, che restituisce un JWT firmato ed25519 con `sub` (utente di sistema), `name`, `groups`, `aud: "repo"`, `iat`, `exp` di 60 s, e un `nonce`.
2. Al primo collegamento, `sso/pair` passa a ErvisioRepo la chiave pubblica del core tramite il socket. Il canale è fidato, quindi non serve uno scambio manuale.
3. Il bottone «Apri ErvisioRepo» nella pagina del plugin chiama `sdk.openExternal('https://git…/auth/ervisio?assertion=…')`. ErvisioRepo verifica firma, audience, scadenza e nonce (monouso), poi crea la sessione dell'account collegato o propone il collegamento al primo accesso.
4. In seguito il core potrà diventare un provider OIDC completo, e ErvisioRepo lo userà come un qualsiasi provider OIDC (§8.1).

### 9.4 Dominio con Caddy

Si riusa il flusso di ErvisioDrive (`caddy-core.ts` e `caddy.ts`, licenza MIT inclusa), in due forme: **TypeScript nel plugin**, copiato da Drive, e **un port in Go** in `internal/caddy` per le installazioni native senza core (`ervisio-repo caddy setup --host git.fonlogen.it`).

1. Rilevamento di Caddy di sistema (`caddy.service`) o in container; i casi `json`, `labels`, `stopped` e `readonly` non sono supportati e vengono segnalati.
2. Scrittura di `ervisio-repo.caddy` accanto al Caddyfile più una riga `import` (oppure un blocco inline tra commenti marker, se la cartella del container non è montata).
3. `caddy validate`, poi reload; in caso di errore si ripristinano i file precedenti.
4. Per Caddy in container: `POST /networks/ervisiorepo_edge/connect`. Infine `PUT /api/plugin/public-url`.

Rispetto a Drive, il blocco generato ha bisogno di impostazioni specifiche per Git (push grandi, clone lunghi, log in streaming):

```
# Managed by the ErvisioRepo plugin
git.ervisio.app {
	request_body {
		max_size 10GB
	}
	reverse_proxy 127.0.0.1:3400 {
		flush_interval -1
		transport http {
			read_timeout 0
			write_timeout 0
		}
	}
	encode {
		zstd
		gzip
		match {
			not header Content-Type application/x-git-*
		}
	}
}
```

Più hostname sulla stessa istanza (ad esempio `git.ervisio.app` e `git.fonlogen.it`) sono supportati in due modi: come **alias** di una stessa istanza, con un URL canonico, oppure come **dominio di organizzazione**, dove `git.fonlogen.it` apre direttamente l'organizzazione Fonlogen con il suo branding. La scelta tra i due è in §12.

## 10. Struttura, dati, API e convenzioni

Un unico repository `Ervisio/ErvisioRepo` (licenza MIT, Copyright Fonlogen, come il resto dell'ecosistema) che contiene server, runner, CLI, SPA, plugin e packaging.

### 10.1 Albero del repository

```
ErvisioRepo/
├── CLAUDE.md                 # regole per gli agenti (§10.5)
├── Makefile                  # build, dev, test, lint, dist, generate
├── go.mod
├── api/
│   ├── openapi.yaml          # contratto REST v1 (fonte unica)
│   ├── github-compat.yaml    # sottoinsieme /api/v3 dichiarato
│   └── runner/v1/*.proto     # protocollo ConnectRPC del runner
├── cmd/
│   ├── ervisio-repo/         # server: serve, migrate, restore, keys, caddy, prewarm
│   ├── ervisio-runner/       # agente runner
│   └── erepo/                # CLI utente e admin
├── internal/
│   ├── config/               # TOML + env, validazione
│   ├── db/                   # migrazioni goose, query sqlc, repository
│   ├── storage/
│   │   ├── blob/             # interfaccia + local, s3, sftp, webdav
│   │   ├── crypt/            # wrapper di cifratura a chunk
│   │   ├── tier/             # policy, cache, hydration, eviction, repliche
│   │   └── journal/          # pack journal git (§4.3)
│   ├── git/                  # wrapper CLI, cat-file pool, diff, merge-tree
│   ├── githttp/              # smart HTTP
│   ├── gitssh/               # server SSH
│   ├── hooks/                # pre-receive, reference-transaction, post-receive
│   ├── lfs/                  # batch API, locks, transfer
│   ├── registry/             # OCI distribution
│   ├── actions/
│   │   ├── workflow/         # parsing, trigger, matrix, espressioni
│   │   ├── scheduler/        # code, concurrency, needs
│   │   ├── runnerapi/        # server ConnectRPC
│   │   ├── cache/            # servizio cache Twirp v2
│   │   └── artifacts/        # servizio artifact v4
│   ├── runner/
│   │   ├── docker/           # backend Docker
│   │   └── microvm/          # backend Firecracker
│   ├── auth/                 # sessioni, password, totp, webauthn, oidc, ervisio-sso, token
│   ├── authz/                # ruoli, permessi, branch protection
│   ├── org/  repo/  pr/  issue/  release/  mirror/  importer/  webhook/  notify/
│   ├── pluginapi/            # API admin sul socket unix
│   ├── caddy/                # port Go del flusso Caddy di Drive
│   ├── jobs/                 # worker River
│   ├── httpapi/              # handler REST v1 + v3 compat + SSE
│   └── sec/                  # test di sicurezza trasversali
├── web/                      # SPA React + Vite (src/routes, src/ui, src/i18n/{en,it})
├── plugin/                   # plugin Ervisio (manifest.json, src, test)
├── deploy/
│   ├── docker/               # Dockerfile, compose.yaml, setup.sh, setup.bat
│   └── runner-images/        # immagini ubuntu per runs-on
├── packaging/                # nfpm, unit systemd, install.sh, AUR
├── test/
│   ├── e2e/                  # git, lfs, docker, act-compat, playwright
│   └── conformance/          # OCI conformance
└── docs/                     # architecture, storage, actions, api, admin, it/
```

### 10.2 Schema dati (tabelle principali)

| Dominio | Tabelle |
| --- | --- |
| Identità | `users`, `emails`, `passwords`, `totp_secrets`, `webauthn_credentials`, `oidc_links`, `ssh_keys`, `gpg_keys`, `sessions`, `tokens` (hash, scope, scadenza) |
| Organizzazioni | `orgs`, `org_members`, `teams`, `team_members`, `team_repos`, `quotas`, `usage_counters` |
| Repo | `repos` (uuid, owner, name, visibility, default\_branch, fork\_of, pool\_id, **storage\_state**, **last\_access\_at**, **local\_bytes**, **remote\_seq**, **pinned**), `repo_collaborators`, `repo_redirects`, `branch_protections`, `tag_rules`, `deploy_keys`, `mirrors` |
| Storage | `storage_objects` (target, key, size, sha256, replicas\_ok, verified\_at), `lfs_objects`, `lfs_locks`, `upload_sessions` |
| Collaborazione | `issues` e `pulls` (numerazione condivisa per repo), `comments`, `review_threads`, `reviews`, `labels`, `milestones`, `reactions`, `mentions`, `notifications`, `releases`, `release_assets` |
| Actions | `workflows`, `workflow_runs`, `jobs`, `steps`, `job_logs` (temporanee), `runners`, `runner_labels`, `secrets`, `variables`, `environments`, `caches`, `artifacts`, `check_runs`, `commit_statuses` |
| Registry | `packages`, `package_versions`, `oci_manifests`, `oci_blobs`, `oci_blob_links`, `retention_policies` |
| Sistema | `webhooks`, `webhook_deliveries`, `audit_log`, `settings`, `river_*` (coda) |

Tutte le chiavi primarie sono UUID v7 (ordinabili nel tempo). I numeri pubblici (#issue, #PR) sono contatori per repo.

### 10.3 Superficie HTTP

| Prefisso | Contenuto |
| --- | --- |
| `/` | SPA (fallback `index.html`) |
| `/<owner>/<repo>.git/...` | Smart HTTP + LFS (`info/lfs/...`) |
| `/v2/...` | Registry OCI |
| `/api/v1/...` | REST Ervisio (OpenAPI) |
| `/api/v3/...` | Sottoinsieme compatibile con GitHub |
| `/api/events/...` | SSE (log dei job, avanzamento hydration, notifiche) |
| `/_runner/...` | ConnectRPC del runner |
| `/_apis/...` | Servizi cache e artifact per le action (percorsi richiesti dal toolkit) |
| `/auth/...` | Login, OIDC callback, SSO Ervisio, WebAuthn |
| `/healthz`, `/readyz`, `/metrics` | Operatività (`/metrics` solo da localhost o con token) |

Sul socket unix separato: `/api/plugin/*`.

### 10.4 Configurazione minima

```toml
[server]
public_url = "https://git.ervisio.app"
listen = "127.0.0.1:3400"
ssh_listen = ":2222"
plugin_socket = "/run/ervisio-repo/api.sock"

[database]
url = "postgres://ervisio_repo@/ervisio_repo?host=/run/postgresql"

[security]
master_key_file = "/etc/ervisio-repo/master.key"
registration = "invite"     # open | invite | closed

[actions]
enabled = true
default_actions_url = "github"  # github | mirror-only
```

### 10.5 Regole per Claude Code (contenuto di `CLAUDE.md`)

Riprendono `SECTION-AGENT-BRIEF.md` del core e le adattano a un monorepo Go + React.

1. Si lavora **una fase alla volta** (§11). Ogni fase si chiude con build, lint e test verdi, `CHANGELOG.md` aggiornato e un breve report finale.
2. Il contratto viene prima: ogni nuovo endpoint parte da `api/openapi.yaml`, poi `make generate`, poi l'implementazione. Lo stesso vale per i `.proto` del runner.
3. Nessun comando di sistema via shell: si usa `internal/sys.Run(argv)` con validazione dei parametri. Per git si passa solo da `internal/git`.
4. Nessun accesso diretto al filesystem per i dati: tutto passa da `internal/storage`. L'unica eccezione è `internal/git` sul path restituito da `tier.Acquire(repoID)`, che garantisce che il repo sia caldo e bloccato.
5. SQL solo in `internal/db/queries/*.sql` (sqlc), migrazioni goose solo in avanti, con test di migrazione.
6. Ogni pacchetto ha test unitari; i parser (workflow, espressioni, Caddyfile, path git) hanno anche test fuzz. Test di sicurezza `sec_*_test.go` per authz, path, SSRF e segreti.
7. Test di integrazione con testcontainers: Postgres, MinIO (S3), `atmoz/sftp` (simula la StorageBox), Docker-in-Docker per i runner.
8. Ogni stringa UI va in `web/src/i18n/{en,it}/<ns>.json` (`make check-i18n`). La UI segue i token del core; screenshot Playwright a 1280×800 e 390×844 per ogni pagina nuova.
9. Non si modificano `go.mod` né `package.json` senza dirlo nel report; niente dipendenze nuove se la standard library basta.
10. Errori Go con `fmt.Errorf("…: %w", err)`; nella REST errori in formato `application/problem+json`; log strutturati con `slog` senza segreti né token.
11. Si fa commit alla fine di ogni task con messaggio in formato Conventional Commits; mai `--no-verify`, mai force-push su `main`.

## 11. Roadmap di implementazione

Le fasi sono 11 numerate (0–10) più la Fase UI, in ordine di dipendenza. Ognuna è una sessione di lavoro autonoma per Claude Code: si passa alla successiva solo quando i criteri di accettazione sono verdi in CI. Nelle fasi segnate «con te» Claude Code prepara le proposte e si ferma per le tue scelte.

&#91;embedded content: roadmap · 3 tappe, 12 fasi, 3 gate\]

La Fase UI è l'unica che richiede una sessione con te; ogni gate si supera solo con i criteri di accettazione delle sue fasi verdi in CI.

### Fase 0 — Fondamenta

- [x] Scaffold del monorepo come in §10.1, `Makefile`, `CLAUDE.md`, licenza, `CHANGELOG.md`, README in inglese e italiano.
- [x] Config TOML + env con validazione e messaggi chiari; `ervisio-repo serve` con `/healthz` e `/readyz`.
- [x] Postgres: pool pgx, goose incorporato, sqlc, River avviato.
- [x] Pipeline OpenAPI: `api/openapi.yaml` → server Go (oapi-codegen strict) e client TS (`openapi-typescript`).
- [x] SPA vuota (Vite, TanStack Router e Query, token del core, i18n en/it) incorporata nel binario.
- [x] Ambiente di sviluppo `deploy/docker/compose.dev.yaml`: Postgres, MinIO, `atmoz/sftp`, Mailpit.
- [x] CI GitHub Actions (finché non c'è il dogfooding): `go vet`, `gofmt`, `golangci-lint`, `govulncheck`, test Go con `-race`, ESLint, `node --test`, build.

**Accettazione**: `make dev` avvia tutto in locale; la CI è verde; un endpoint d'esempio è generato da OpenAPI e chiamato dalla SPA con tipi generati.

### Fase 1 — Livello di storage

- [x] Interfaccia `blob.Store` e driver `local`, `s3`, `sftp` (§4.1).
- [x] Una suite di test di conformità comune che ogni driver deve passare: put e get, range, oggetti da 0 byte e da 5 GiB, sovrascrittura, list paginato, errori di rete simulati, scritture concorrenti.
- [x] Wrapper `crypt` (AES-256-GCM a chunk, range su dati cifrati), `retry`, `metered`.
- [x] Motore `tier` per oggetti indirizzati per contenuto: policy, cache LRU con budget e TTL, scrittura primary-first, job di replica e di verifica, tabella `storage_objects`.
- [x] Comando `ervisio-repo storage test <target>` e pagina admin con l'esito del test di connessione.

**Accettazione**: la suite di conformità passa su MinIO, SFTP e local; un oggetto cancellato dalla cache torna dal primary in modo trasparente; un'interruzione di rete a metà upload non lascia oggetti corrotti.

### Fase 2 — Identità e organizzazioni

- [x] Registrazione e login locale, verifica email, reset, argon2id, sessioni in DB, CSRF.
- [x] TOTP, WebAuthn (passkey), codici di recupero, 2FA obbligatoria per policy.
- [x] Provider OIDC configurabili e collegamento degli account.
- [x] Token personali con scope e scadenza, chiavi SSH e GPG.
- [x] Organizzazioni, team, ruoli (§5.3), inviti, quote di base, log di audit.
- [x] UI: login, registrazione, impostazioni account, pagina organizzazione (provvisorie, si rifiniscono dopo la Fase UI).

**Accettazione**: i test `sec_auth_*` coprono brute force, riuso delle sessioni, CSRF, enumerazione degli utenti e takeover OIDC via email; il login funziona con un IdP OIDC di test (Dex in testcontainers).

### Fase 3 — Motore Git e tiering

- [x] CRUD dei repo, layout per UUID, redirect dopo rename e transfer.
- [x] `internal/git` (argv, ambiente pulito, pool `cat-file`).
- [x] Smart HTTP (protocollo v2) e server SSH sulla porta 2222.
- [x] Hook installati dal binario, che parlano col server via socket.
- [x] Pack journal: segmenti in `pre-receive`, manifest in `reference-transaction`, modalità `sync` e `async`, compattazione (§4.3).
- [x] Stati del repo, hydration con singleflight e avanzamento (SSH stderr e SSE), eviction con watermark, cestino, pin.
- [x] Backup Postgres con wal-g, recovery kit, `ervisio-repo restore`, `prewarm`.
- [ ] Test «chaos»: 50 repo con storia reale, si cancellano disco e DB, si ripristina e si confrontano ref e `fsck`. (non scritto: niente test per decisione dell'utente)

**Accettazione**: clone, push e fetch funzionano su HTTP e SSH con git 2.30 e con la versione più recente; un repo sfrattato torna con un clone; il test chaos è verde in CI; il kill del processo a metà push non lascia mai un manifest che punta a segmenti mancanti.

### Fase UI — Design system (con te)

- [x] Proposta di 2–3 direzioni visive coerenti con i token del core (OLED, hue per sezione) per le schermate chiave: home del repo, vista file, diff della PR, run delle Actions, pagina pacchetto.
- [x] Scelta insieme, poi kit di componenti in `web/src/ui` (Button, Input, Select, Dialog, Menu, Tabs, Table, Badge, Toast, EmptyState) e pagina di catalogo dei componenti.

**Accettazione**: kit approvato da te; screenshot di riferimento a 1280 e 390 px salvati per i test visivi.

### Fase 4 — Navigazione del codice e LFS

- [x] Vista del repo: albero, file con evidenziazione Shiki, README, blame, cronologia, commit con diff, branch, tag, confronto tra ref, download di archivi.
- [x] Ricerca nel repo (`git grep` sul ref di default) e ricerca globale per nome.
- [x] Edit dal web (CodeMirror) con commit diretto o su un nuovo branch.
- [x] Git LFS: batch API, upload e download prefirmati o in proxy, locks, quote (§4.4).
- [x] Release con asset sullo storage a livelli.

**Accettazione**: `git lfs push` e `pull` di 5 GB funzionano su Object Storage (presign) e StorageBox (proxy); un diff da 10.000 righe resta fluido nel browser.

### Fase 5 — Collaborazione, API e migrazione

- [x] PR con fork e pool di oggetti, `merge-tree`, review inline, suggested changes, tre strategie di merge, auto-merge.
- [x] Branch protection e regole sui tag applicate in `pre-receive`, CODEOWNERS.
- [x] Issue, label, milestone, menzioni, riferimenti incrociati, chiusura automatica, template, ricerca full-text.
- [x] Notifiche in-app ed email.
- [x] Webhook in formato GitHub e Ervisio, con cronologia delle consegne.
- [x] Completamento di `/api/v1` e del sottoinsieme `/api/v3`; i test di contratto con `gh` e `go-github` li fa l'utente.
- [x] Import da GitHub (repo e organizzazioni con issue, PR, release), pull mirror, backup e sync bidirezionale con GitHub tramite GitHub App (§5.6).

**Accettazione**: import completo di un'organizzazione GitHub reale di prova; `gh pr list`, `gh issue create` e `gh release view` funzionano con `GH_HOST`; nessuna regola di branch protection aggirabile via SSH (test `sec_protect_*`).

### Fase 6 — Actions (backend Docker)

- [x] Parser dei workflow, trigger, filtri, matrix, `needs`, `concurrency`, `environment` con approvazione, workflow riutilizzabili.
- [x] Scheduler su River, protocollo ConnectRPC, registrazione dei runner, etichette.
- [x] `ervisio-runner` con il fork di act e backend Docker con limiti cgroup.
- [x] Token del job, segreti, variabili, mascheramento, log in streaming e archiviazione.
- [x] Servizi cache v2 e artifact v4.
- [ ] Proxy-cache delle action remote (`_actions`): non costruito, serve un mirror git sul server.
- [x] Check run nelle PR, badge, UI dei run con log xterm, re-run di job falliti. (log con rendering testuale, senza xterm)

**Accettazione**: una suite di workflow reali (Node, Go, Python, Docker build e push, matrix, servizi Postgres) gira senza modifiche e dà lo stesso esito che su GitHub; le PR da fork non vedono mai i segreti.

### Fase 7 — Registry OCI e dogfooding (rinviata)

> Rinviata su decisione dell'utente (2026-11-03): non serve per ora.


- [ ] Distribution Spec v1.1 (§7), token auth, upload a chunk, mount, referrers, retention e GC.
- [ ] Pagine dei pacchetti nella UI e collegamento ai repo.
- [ ] Migrazione del repo ErvisioRepo stesso su un'istanza ErvisioRepo, con la sua CI su Ervisio Actions e le immagini sul suo registry.

**Accettazione**: la suite di conformità OCI è verde; `docker buildx build --push` multi-arch da un job Actions funziona; la release di ErvisioRepo si costruisce su ErvisioRepo.

### Fase 8 — Integrazione Ervisio e installazione

> Ambito ridotto su decisione dell'utente (2026-11-03). Le distribuzioni sono due: **standalone** (binario nativo) e **Docker**. Il plugin Ervisio non è una terza distribuzione: automatizza l'installazione di una delle due, scelta dall'utente, e offre una configurazione grafica nelle impostazioni di Ervisio. Niente SSO col core: Ervisio usa il login con gli account Linux, che non si adatta.

- [x] Docker: Dockerfile multi-stage, `compose.yaml`, `setup.sh` e `setup.bat`.
- [x] Standalone: pacchetti nativi nfpm (.deb e .rpm), unit systemd, `install.sh`, release firmate.
- [x] Socket admin `/api/plugin/*` con segreto per processo, usato dal plugin per leggere e scrivere la configurazione.
- [x] Plugin in `plugin/`: installazione automatica in modalità standalone o Docker (a scelta dell'utente) e pagina di configurazione grafica nelle impostazioni di Ervisio.
- [x] Flusso Caddy in TS (dal plugin) e in Go (`ervisio-repo caddy setup`), con i parametri Git del §9.4.
- [x] Voce nel catalogo `Ervisio/plugins`.
- ~~SSO col core~~ (tolto).

**Accettazione**: ErvisioRepo si installa standalone e con Docker senza il plugin; dal pannello Ervisio il plugin installa una delle due modalità, configura `git.<dominio>` con TLS e permette di modificare la configurazione dalle impostazioni.

### Fase 9 — Isolamento e hardening per l'istanza pubblica (rinviata)

> Rinviata su decisione dell'utente (2026-11-03).


- [ ] Backend `microvm` con Firecracker: immagini kernel e rootfs, rete tap con egress filtrato, snapshot per avvio rapido, pulizia garantita.
- [ ] Policy di fiducia (§6.3) e approvazione dei workflow per i contributor esterni.
- [ ] Quote applicate ovunque, rate limit, CAPTCHA, segnalazioni, strumenti di moderazione admin.
- [ ] Dominio separato opzionale per i contenuti degli utenti, revisione di CSP e header.
- [ ] Revisione di sicurezza completa: un agente separato senza contesto di implementazione rilegge authz, storage, hook e runner; poi fuzzing esteso.

**Accettazione**: un job ostile (fork bomb, accesso a `/var/run/docker.sock`, scansione della rete Proxmox, metadata endpoint) non esce dalla microVM e non tocca altri job; i test `sec_*` sono tutti verdi.

### Fase 10 — Beta e v1.0

- ~~Documentazione per admin e utenti, guida alla migrazione da GitHub~~ (tolte su decisione dell'utente, 2026-11-03).
- [x] Guida al DR (en e it).
- ~~Test di carico ed esercitazione DR su Hetzner~~: li esegue l'utente (2026-11-03), non fanno parte del lavoro degli agenti.
- [ ] Release v1.0.0 firmata, pacchetti, immagini e plugin nel catalogo. (v1.0.0 il 2026-11-04 con i pacchetti firmati; v1.0.2 lo stesso giorno con l'immagine `ghcr.io/ervisio/ervisio-repo` multi-arch e il plugin `repo-1.0.2.tar.gz`; registrazione nel catalogo aperta come Ervisio/plugins#41, in attesa di review; il package ghcr va reso pubblico)

**Accettazione**: obiettivi del §1 misurati e rispettati; l'esercitazione DR è documentata con i tempi reali.

### Dopo la v1

Driver WebDAV, `docker+gvisor`, OIDC per i job, runner arm64, ref advertisement dal manifest senza hydration completa, backend runner con VM effimere via API Proxmox, registry npm, Maven, PyPI e Cargo, Wiki e Pages, federazione.

## 12. Rischi, questioni aperte e prossimi passi

### Rischi principali

| Rischio | Impatto | Mitigazione |
| --- | --- | --- |
| Compatibilità con le action ufficiali (cache v2, artifact v4) che cambiano protocollo senza preavviso | Workflow rotti dopo un aggiornamento delle action | Test notturni contro le ultime versioni di `actions/*`; seguire il fork di act di Gitea, che affronta lo stesso problema |
| Hydration lenta di repo molto grandi | Primo clone lento dopo 7 giorni di inattività | Pin dei repo critici, `prewarm` pianificato, compattazione per avere un solo pack da scaricare, ref advertisement dal manifest (dopo la v1) |
| Perdita del recovery kit con la cifratura attiva | Dati remoti irrecuperabili | Blocco in UI finché il kit non viene scaricato, promemoria periodico, test di decifratura del kit dalla UI |
| Firecracker dentro una VM Proxmox (nested virtualization) | Prestazioni ridotte o funzione non disponibile | Verifica nella Fase 0 su VM reale; in alternativa backend Docker + gVisor per i job non fidati finché non c'è |
| Abuso di un'istanza pubblica (crypto mining nelle Actions, hosting di malware) | Ban dell'IP da parte di Hetzner, costi | Minuti di Actions limitati per default per i nuovi account, egress filtrato, moderazione, registrazione su invito al lancio |
| Limiti di connessioni SFTP della StorageBox | Hydration e repliche rallentate | `max_conns` configurabile, preferire Object Storage come primary e StorageBox come replica |
| Scope molto ampio | Tempi lunghi prima di poter usare il sistema | Fasi 0–4 utilizzabili da subito come «Git privato con backup»; Actions e registry si aggiungono dopo |

### Da verificare all'inizio della Fase 0

- Endpoint, presign, scritture condizionali (`If-Match`) e limiti di Hetzner Object Storage, sulla documentazione aggiornata di Hetzner.
- Limite di connessioni SFTP concorrenti e politica degli snapshot della StorageBox in uso.
- Supporto della nested virtualization sull'host Proxmox (Ryzen 7 7700) e nella VM.
- Stato e licenza attuali del fork di act di Gitea e dei protocolli cache e artifact.

### Decisioni ancora aperte

- **Domini multipli**: alias della stessa istanza oppure dominio per organizzazione (§9.4)?
- **Topologia**: decisa, una sola VM con server e runner separati da cgroup (§3).
- **Categoria nel catalogo plugin**: le categorie attuali non includono lo sviluppo software, quindi il plugin è registrato in «Web servers»; si può aggiungere una categoria «Development» al registro `Ervisio/plugins` e spostarlo lì.
- **Direzione visiva** della UI: da decidere insieme nella Fase UI.

### Prossimi passi

1. Rivedere questo piano e chiudere le decisioni aperte (anche con commenti nel documento).
2. Creare il repository `Ervisio/ErvisioRepo` e salvarvi questo piano come `docs/PLAN.md`, con il `CLAUDE.md` del §10.5.
3. Avviare Claude Code sulla Fase 0 con il prompt: «Implementa la Fase 0 di `docs/PLAN.md` seguendo `CLAUDE.md`; fermati ai criteri di accettazione e riporta l'esito.»
4. Prima della Fase 4, sessione insieme sulla UI (Fase UI).
