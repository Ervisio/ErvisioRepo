# Ervisio Repo

Forge Git self-hosted con CI integrata (compatibile con Actions), registry OCI, LFS e storage a livelli. Un solo binario Go, un database PostgreSQL, una SPA React incorporata.

Parte dell'ecosistema Ervisio. Licenza MIT.

## Stato

Completate la fase 0 (fondamenta) e la fase 1 (livello di storage): configurazione, server HTTP, database, pipeline OpenAPI, SPA con il kit UI, ambiente di sviluppo, CI e storage a livelli su disco locale, S3 e SFTP. Le funzioni Git non ci sono ancora. La roadmap è in [docs/PLAN.md](docs/PLAN.md).

## Requisiti

- Go (versione in `go.mod`)
- Node 24
- Docker con Compose (servizi di sviluppo e test di integrazione)
- GNU make

## Avvio rapido

```sh
make tools   # sqlc e golangci-lint alle versioni fissate
make dev     # servizi, server Go su :3400, Vite su :5173
```

Apri <http://localhost:5173>. Mailpit è su <http://localhost:8025>, la console MinIO su <http://localhost:9001>. Per fermare i servizi: `make dev-down`.

Altri target: `make help`.

## Storage

Le destinazioni (`local`, `s3`, `sftp`) e le policy per tipo di dato si configurano nella sezione `[storage]`; un esempio completo è in `docs/PLAN.md`, sezione 4. Note:

- Le destinazioni `sftp` richiedono `known_hosts_file` (percorso assoluto, la chiave dell'host è sempre verificata); `root` imposta la directory remota di base.
- Le destinazioni `s3` accettano un `prefix` opzionale (che termina con `/`). Le credenziali vengono da `credentials_file` o dalle variabili d'ambiente standard `AWS_*`.
- Le destinazioni con `encrypt = true` richiedono `security.master_key_file`: 32 byte in hex o base64, permessi `0600`.
- I file di chiavi, credenziali e known_hosts non devono essere leggibili dal gruppo o da altri.

Per provare una destinazione da capo a fondo (scrittura, stat, lettura, lettura parziale, elenco, cancellazione):

```sh
ervisio-repo storage test <destinazione> --config /etc/ervisio-repo/config.toml
```

Lo stesso test, con lo stato di policy e cache, è nella pagina di amministrazione `/admin/storage`. Serve un account amministratore dell'istanza.


## Account e organizzazioni

- Il primo account registrato diventa amministratore dell'istanza. `[security] registration` vale `open`, `invite` (solo inviti dell'amministratore) o `closed`; la posta passa dalla sezione `[mail]` (Mailpit in sviluppo).
- Autenticazione a due fattori: TOTP, passkey e codici di recupero in *Impostazioni, Sicurezza*. `[auth] require_2fa = "off" | "admins" | "all"`; anche il proprietario di un'organizzazione può richiederla ai membri.
- Accesso OIDC: un blocco `[[auth.oidc]]` per provider (`name`, `display_name`, `issuer`, `client_id`, `client_secret_file`); l'URL di callback da registrare presso il provider è `<public_url>/auth/oidc/<name>/callback`. Gli account si collegano solo da una sessione già autenticata.
- I token di accesso personali (`erp_…`, sempre con scadenza) si inviano come `Authorization: Bearer`.
- Gli inviti a un'organizzazione arrivano per email come link `/org-invite?token=`.

## Struttura

| Percorso | Contenuto |
| --- | --- |
| `api/` | Contratto OpenAPI e protocollo del runner |
| `cmd/` | `ervisio-repo` (server), `ervisio-runner`, `erepo` (CLI) |
| `internal/` | Pacchetti del server |
| `web/` | SPA React (Vite) |
| `plugin/` | Plugin Ervisio |
| `deploy/` | File di deploy, sviluppo e produzione |
| `packaging/` | Pacchetti di distribuzione |
| `test/` | Test end-to-end e di conformità |
| `docs/` | Documentazione di progetto e operativa |

Le regole per agenti e contributori sono in [CLAUDE.md](CLAUDE.md).
