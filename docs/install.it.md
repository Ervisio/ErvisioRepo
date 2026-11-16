# Installare Ervisio Repo

Due modi supportati: pacchetto Linux nativo oppure Docker Compose. Versione inglese: [install.md](install.md).

Il primo account che registri diventa l'amministratore dell'istanza. Entrambe le installazioni partono con registrazione solo su invito, che non blocca il primo account.

## Nativa (Debian, Ubuntu, famiglia RHEL)

Requisiti: systemd, `git` e PostgreSQL 14 o successivo (locale o remoto).

```sh
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# una release specifica:  ... | sudo sh -s -- --version 1.2.3
```

Lo script riconosce distribuzione e architettura (per ora solo amd64), scarica il `.deb` o `.rpm`, verifica `SHA256SUMS` e la sua firma ed25519 (serve openssl 3) e installa. Puoi anche installare a mano un pacchetto dalla pagina delle release.

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

## Storage esterno dall'admin web

Oltre a `[[storage.target]]` in `config.toml`, un amministratore dell'istanza può aggiungere destinazioni S3 compatibili e SFTP in **Admin > Storage** e cambiare dove viene salvato ogni tipo di dati (`git`, `lfs`, `actions`, ...).

- **Credenziali.** Chiave di accesso e chiave segreta S3, chiave privata SFTP (con passphrase facoltativa) o password, e righe degli host noti si scrivono nel modulo e vengono cifrate nel database con la chiave master (`security.master_key_file`). Non vengono mai mostrate di nuovo: la pagina dice solo se ognuna è salvata, e un campo vuoto mantiene il valore salvato. Senza chiave master le credenziali non si possono salvare. Non perdere la chiave: senza di essa le credenziali salvate non sono leggibili. Una destinazione SFTP accede con una chiave privata o con una password (**Metodo di accesso**); salvarne una sostituisce l'altra.
- **Prova connessione** funziona sul modulo prima di salvare, con lo stesso giro di scrittura, lettura, elenco e cancellazione di `ervisio-repo storage test`.
- **Policy.** Repliche e cache si possono cambiare in qualsiasi momento. La primaria si può cambiare solo finché la policy non contiene oggetti, perché gli oggetti non vengono spostati tra le destinazioni. Una cache deve essere una destinazione locale.
- **Riavvio.** Le modifiche si applicano al prossimo avvio del server; la pagina lo ricorda fino ad allora.
- **Limiti.** Le destinazioni di `config.toml` sono in sola lettura qui e i loro nomi non si possono riusare. Nome, tipo e cifratura di una destinazione non si cambiano dopo la creazione. Una destinazione non si elimina finché una policy la usa o ci sono oggetti salvati su di essa. Finché ci sono oggetti, la posizione (endpoint, regione, bucket, prefisso, host, porta, utente, radice) è bloccata; si possono sostituire solo le credenziali. Se cambiano endpoint o host, credenziali e host noti vanno inseriti di nuovo. Qui non si possono creare destinazioni `local` né WebDAV. Le connessioni dal server alle destinazioni rifiutano indirizzi link-local, metadati cloud (`169.254.0.0/16`, `fd00:ec2::/32`), non specificati e multicast; gli indirizzi privati sono permessi. `ervisio-repo storage test` conosce solo le destinazioni del file.
- **Repliche aggiunte dopo.** Una nuova replica riceve i nuovi oggetti. Quelli salvati prima vengono copiati solo quando la verifica periodica li trova mancanti, quindi la copia completa dei dati vecchi è lenta. Una destinazione tolta da una policy viene semplicemente ignorata; i suoi oggetti restano dove sono.
- **Se all'avvio la configurazione salvata non si può applicare** (per esempio è cambiata la chiave master), il server scrive un avviso e parte con il solo `config.toml`, a meno che sposti una primaria: in quel caso rifiuta di partire, perché gli oggetti diventerebbero introvabili.
- **Stato.** Se la configurazione salvata non è stata applicata, Admin > Storage ne mostra il motivo. Una destinazione eliminata mentre è in uso resta in elenco come "Rimossa dopo il riavvio".

### Avatar

Gli utenti caricano un avatar in **Impostazioni > Profilo** (PNG, JPEG o GIF fino a 2 MiB; il server lo ritaglia a quadrato, lo ridimensiona a 460x460 e salva un PNG senza metadati). Le immagini sono indirizzate per contenuto (`avatars/<sha256>.png`), quindi i loro URL non cambiano mai.

- **Dove.** In **Admin > Storage > Avatar** scegli l'archiviazione locale (la prima destinazione `local`, predefinita) o una destinazione attiva (S3 o SFTP). La modifica vale subito per i nuovi caricamenti; gli avatar esistenti non vengono spostati e il server continua a servirli dalla destinazione attiva che li contiene.
- **Distribuzione.** `GET /avatars/<sha256>.png` su questo server, pubblico, con `Cache-Control: public, max-age=31536000, immutable`.
- **CDN.** Imposta **URL pubblico di base** perché i nuovi URL degli avatar puntino a `<base>/avatars/<sha256>.png`. La CDN può usare come origine questo server (percorso `/avatars/`) oppure direttamente il bucket della destinazione S3 (oggetti sotto `avatars/`, dopo il prefisso della destinazione; non funziona con una destinazione cifrata). Altrimenti gli URL degli avatar sono assoluti su `server.public_url`.

### Recupero

- `ervisio-repo storage overlay show [--config PERCORSO]` elenca la configurazione salvata (senza credenziali) e gli oggetti di ogni policy. `ervisio-repo storage overlay clear [--config PERCORSO] [--force]` la azzera, così il prossimo avvio usa solo `config.toml`. `clear` rifiuta se sposterebbe la primaria di una policy che contiene oggetti; `--force` lo supera, solo se quegli oggetti si trovano anche sotto la primaria del file.
- **Configurazione salvata corrotta.** Il server rifiuta di partire e indica la chiave `storage.overlay`. Non cancellare la riga a mano: contiene la primaria delle tue policy. Usa `storage overlay clear --force` dopo aver controllato le primarie in `config.toml`.
- **Chiave master persa o cambiata.** Le credenziali salvate non si possono decifrare. Il server parte con il solo `config.toml` (rifiuta solo se così sposterebbe una primaria). Ripristina la vecchia chiave, oppure esegui `storage overlay clear` e inserisci di nuovo le credenziali.
- **Oggetti fuori posto.** All'avvio il server conta gli oggetti per policy e destinazione. Se una policy ha oggetti sotto una destinazione che la configurazione salvata non rende più primaria, quella configurazione non viene applicata.
