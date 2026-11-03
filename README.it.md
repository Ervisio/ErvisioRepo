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

Lo stesso test, con lo stato di policy e cache, è nella pagina di amministrazione `/admin/storage`. Finché non arriva l'autenticazione (fase 2), l'API di amministrazione risponde solo a richieste dirette da loopback.

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
