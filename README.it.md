# Ervisio Repo

Forge Git self-hosted con CI integrata (compatibile con Actions), registry OCI, LFS e storage a livelli. Un solo binario Go, un database PostgreSQL, una SPA React incorporata.

Parte dell'ecosistema Ervisio. Licenza MIT.

## Stato

Fase 0 (fondamenta): configurazione, server HTTP, database, pipeline OpenAPI, SPA vuota, ambiente di sviluppo, CI. Le funzioni Git non ci sono ancora. La roadmap è in [docs/PLAN.md](docs/PLAN.md).

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
