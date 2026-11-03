# ErvisioRepo — regole per gli agenti

Il piano completo è in `docs/PLAN.md`. Queste regole vengono dal §10.5.

1. Si lavora **una fase alla volta** (`docs/PLAN.md` §11). Ogni fase si chiude con build, `go vet`, lint e typecheck verdi (controlli di sintassi ed errori logici), `CHANGELOG.md` aggiornato e un breve report finale.
2. Il contratto viene prima: ogni nuovo endpoint parte da `api/openapi.yaml`, poi `make generate`, poi l'implementazione. Lo stesso vale per i `.proto` del runner.
3. Nessun comando di sistema via shell: si usa `internal/sys.Run(argv)` con validazione dei parametri. Per git si passa solo da `internal/git`.
4. Nessun accesso diretto al filesystem per i dati: tutto passa da `internal/storage`. L'unica eccezione è `internal/git` sul path restituito da `tier.Acquire(repoID)`, che garantisce che il repo sia caldo e bloccato.
5. SQL solo in `internal/db/queries/*.sql` (sqlc), migrazioni goose solo in avanti.
6. **Niente test**: per decisione dell'utente (2026-11-03) non si scrivono né si eseguono test nuovi e non si lancia la CI. Le verifiche sono solo `make build`, `go vet ./...`, `make lint`, typecheck/lint web e `make check-generate`. I test esistenti restano nel repo ma non vanno estesi né eseguiti.
7. La CI GitHub è disabilitata: non va riattivata né lanciata.
8. Ogni stringa UI va in `web/src/i18n/{en,it}/<ns>.json` (`make check-i18n`). La UI segue i token del core (niente nuovi screenshot Playwright).
9. Non si modificano `go.mod` né `package.json` senza dirlo nel report; niente dipendenze nuove se la standard library basta.
10. Errori Go con `fmt.Errorf("…: %w", err)`; nella REST errori in formato `application/problem+json`; log strutturati con `slog` senza segreti né token.
11. Si fa commit alla fine di ogni task con messaggio in formato Conventional Commits, direttamente su `main`; mai `--no-verify`, mai force-push su `main`.

## Convenzioni del codice

- Codice, identificatori, commenti e messaggi di errore/log in inglese; documentazione utente in inglese e italiano.
- Modulo Go: `github.com/Ervisio/ErvisioRepo`. Comandi in `cmd/`, tutto il resto in `internal/`.
- CLI con la sola libreria standard (`flag`), sottocomandi in `cmd/<bin>/`.
- Codice generato (oapi-codegen, sqlc, openapi-typescript) è versionato e rigenerato con `make generate`; la CI verifica che non ci siano differenze.
