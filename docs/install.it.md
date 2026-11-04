# Installare Ervisio Repo

Due modi supportati: pacchetto Linux nativo oppure Docker Compose. Versione inglese: [install.md](install.md).

Il primo account che registri diventa l'amministratore dell'istanza. Entrambe le installazioni partono con registrazione solo su invito, che non blocca il primo account.

## Nativa (Debian, Ubuntu, famiglia RHEL)

Requisiti: systemd, `git` e PostgreSQL 14 o successivo (locale o remoto).

```sh
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# una release specifica:  ... | sudo sh -s -- --version 1.2.3
```

Lo script riconosce distribuzione e architettura (amd64, arm64), scarica il `.deb` o `.rpm`, verifica `SHA256SUMS` e la sua firma ed25519 (serve openssl 3) e installa. Puoi anche installare a mano un pacchetto dalla pagina delle release.

Cosa configura il pacchetto:

- Binari in `/usr/lib/ervisio-repo/versions/<versione>`, un link simbolico `current` e i link in `/usr/bin` (`ervisio-repo`, `ervisio-runner`, `erepo`).
- Utente di sistema `ervisio-repo`, dati in `/var/lib/ervisio-repo`, configurazione in `/etc/ervisio-repo/config.toml` (conservata negli aggiornamenti) e una `master.key` generata accanto. Fanne una copia: senza la chiave i segreti salvati non sono recuperabili.
- Unit protette `ervisio-repo.service` e `ervisio-runner.service` con slice cgroup dedicate. Il runner parte solo quando esiste `/etc/ervisio-repo/runner.toml`.
- Con un PostgreSQL locale attivo, vengono creati un ruolo e un database `ervisio_repo` per l'utente `ervisio-repo` (autenticazione peer).

Poi:

```sh
sudo nano /etc/ervisio-repo/config.toml        # imposta server.public_url
sudo runuser -u ervisio-repo -- ervisio-repo migrate up --config /etc/ervisio-repo/config.toml
sudo systemctl enable --now ervisio-repo
```

Apri l'URL pubblico e registrati. Rimuovere il pacchetto conserva dati, configurazione e master key.

## Docker Compose

Requisiti: Docker Engine 24+ (o Docker Desktop) con il plugin Compose.

```sh
cd deploy/docker
./setup.sh                      # Windows: setup.bat
./setup.sh --non-interactive --url https://git.example.com --admin-email me@example.com --ervisio no
```

`setup.sh` scrive `.env`, `config/config.toml` e `config/master.key`, costruisce l'immagine e avvia lo stack `ervisiorepo` (`postgres` e `app`). Rilanciarlo conserva segreti e configurazione. `--ervisio yes` abilita il socket di amministrazione usato dal plugin Caddy, condiviso in `${ERVISIO_SOCKET_DIR}` (montato in `/run/ervisio-repo`).

L'app ascolta su `127.0.0.1:3400` (web e API) e `127.0.0.1:2222` (SSH); cambia `BIND_ADDRESS`, `APP_PORT` e `SSH_PORT` in `.env`. Le reti sono due: `private` (Postgres, interna) e `ervisiorepo_edge`, a cui si unisce solo `app` (alias `ervisiorepo-app`) perché un Caddy in container lo raggiunga.

Profili opzionali:

```sh
docker compose --profile runner up -d    # runner CI, monta /var/run/docker.sock
docker compose --profile backup up -d    # sidecar per l'archiviazione dei WAL (vedi docs/backup.it.md)
```

Il runner può avviare container sull'host tramite il socket Docker: abilitalo solo su macchine di cui ti fidi.

## Pubblicare dietro Caddy

```sh
sudo ervisio-repo caddy setup --host git.example.com
ervisio-repo caddy status
ervisio-repo caddy remove
```

`caddy` cerca un Caddy installato come servizio di sistema o in esecuzione in un container Docker, scrive un blocco gestito (`ervisio-repo.caddy` accanto al Caddyfile, importato da questo) con upload grandi, nessun timeout sugli stream Git e compressione che salta i tipi di contenuto Git, lo valida e ricarica Caddy. Se validazione o ricarica falliscono, ripristina la configurazione precedente. Opzioni utili:

- `--dry-run`: mostra la modifica senza scrivere.
- `--instance system|<container>`: sceglie quando girano più istanze Caddy.
- `--upstream` (predefinito `127.0.0.1:3400`), `--container-upstream` (predefinito `ervisiorepo-app:3400`), `--network` (predefinito `ervisiorepo_edge`): come Caddy raggiunge il server. Un Caddy in container viene collegato da solo alla rete edge.

Se non trova un Caddy utilizzabile, il comando stampa lo snippet da incollare a mano. Poi imposta `server.public_url` sul nuovo indirizzo `https://` e riavvia il servizio.
