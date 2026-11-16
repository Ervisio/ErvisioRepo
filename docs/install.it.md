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

`setup.sh` chiede anche se avviare il runner Actions integrato (`--runner yes|no`, predefinito yes); vedi [Runner](#runner). Profilo opzionale:

```sh
docker compose --profile backup up -d    # sidecar per l'archiviazione dei WAL (vedi docs/backup.it.md)
```

## Runner

I runner eseguono i job di Actions. Li fornisce chi gestisce l'istanza, come i runner ospitati di GitHub: i runner registrati in **Admin > Runner** formano un pool condiviso che prende i job di tutti i repository, quindi gli utenti scrivono solo `runs-on: ubuntu-latest` senza configurare nulla. I runner di organizzazione e di repository (nelle rispettive Impostazioni > Actions) sono un'aggiunta facoltativa per chi vuole macchine dedicate.

Ogni nuovo runner ha queste etichette: `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux` e `x64` o `arm64`, tutte con job in container Docker dalle immagini di `deploy/runner-images` (`ghcr.io/catthehacker/ubuntu:act-*`). Le etichette aggiuntive (`--labels gpu,big`) si sommano a quelle predefinite con la stessa immagine; `nome=docker://immagine` associa un'etichetta a un'altra immagine.

**Distribuzione del carico.** I runner si collegano solo in uscita e interrogano il server in long-poll. Un job va al primo runner che lo chiede e ha tutte le etichette di `runs-on`; un runner chiede solo quando ha uno slot libero, quindi un runner occupato non trattiene i job a scapito di quelli liberi. `capacity` (job contemporanei) vale di default metà delle CPU, almeno 1; si imposta con `--capacity N` all'installazione o con `runner.capacity` nella configurazione. Più macchine con le stesse etichette si dividono la coda: un runner sul server e altri su host separati distribuiscono il carico.

### Runner integrato sul server

- **Docker Compose**: `./setup.sh` (o `./setup.sh --runner yes`) scrive `COMPOSE_PROFILES=runner` in `.env`, avvia `app`, crea un token di registrazione d'istanza con `ervisio-repo actions runner-token` dentro il container `app` e lo salva in `data/runner/registration-token` (permessi 0600). Il container `runner` (`ervisio-runner daemon`) si registra come `builtin` al primo avvio, cancella il file del token e tiene credenziale e `config.toml` generato in `data/runner`. I token scadono dopo `actions.registration_token_ttl` (1h): non esiste un segreto condiviso a lunga durata. `RUNNER_LABELS` e `RUNNER_CAPACITY` in `.env` impostano etichette aggiuntive e capacità prima del primo avvio.
- **Pacchetti nativi**: `sudo ervisio-repo actions runner-setup` registra il runner `builtin` sull'indirizzo locale del server, scrive `/etc/ervisio-repo/runner.toml` e abilita `ervisio-runner.service` (servono Docker e il gruppo `docker`). Opzioni: `--labels`, `--capacity`, `--url`, `--no-start`.

Admin > Runner mostra in alto lo stato del runner integrato. Per stampare un token di qualsiasi ambito dalla shell del server: `ervisio-repo actions runner-token --scope instance|org/<owner>|repo/<owner>/<name>`.

I container dei job clonano il repository da `public_url` del server: deve essere raggiungibile dagli host dei runner (non `localhost`).

### Runner remoti

In Admin > Runner premi **Nuovo runner**: la finestra crea un token di registrazione (valido 1h) e mostra il comando per ogni piattaforma con URL del server, token e, se indicati, nome, etichette aggiuntive e capacità.

- **Linux** (systemd): `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --url https://git.example.com --token errt_…` installa `/usr/local/bin/ervisio-runner`, crea l'utente `ervisio-runner` (aggiunto al gruppo `docker` dopo conferma, o con `--docker`), scrive `/etc/ervisio-runner/config.toml`, registra e avvia `ervisio-runner.service`. Senza `sudo` installa in `~/.local/bin` con un'unità systemd utente (`loginctl enable-linger` la tiene attiva dopo il logout).
- **macOS** (launchd): lo stesso comando senza `sudo` installa `~/.local/bin/ervisio-runner` e il LaunchAgent `dev.ervisio.runner` (log in `~/Library/Application Support/ervisio-runner/logs`). I job richiedono Docker Desktop avviato.
- **Windows**: in PowerShell come amministratore, `& ([scriptblock]::Create((irm https://git.example.com/runner/install.ps1))) -Url https://git.example.com -Token errt_…` installa `%ProgramFiles%\ErvisioRunner\ervisio-runner.exe`, tiene configurazione e stato in `%ProgramData%\ervisio-runner` (solo SYSTEM e Administrators) e registra l'attività pianificata `ErvisioRunner` (all'avvio, come SYSTEM, riavviata in caso di errore). Si usa un'attività pianificata perché il runner non è un servizio Windows nativo e così non servono strumenti esterni. I job richiedono Docker Desktop con container Linux.
- **Docker**: la finestra mostra un comando `docker run … --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:<versione> daemon …`; il runner si registra da `ERVISIO_RUNNER_URL`/`ERVISIO_RUNNER_TOKEN` (anche `_NAME`, `_LABELS`, `_CAPACITY`) e tiene lo stato nel volume `ervisio-runner`.

Gli script sono serviti dal server su `/runner/install.sh` e `/runner/install.ps1` e scaricano il runner della stessa versione del server dalla release GitHub (`ervisio-runner_<ver>_<os>_<arch>.tar.gz`, `.zip` su Windows), verificandolo con `ervisio-runner_<ver>_checksums.txt`. `--version X.Y.Z` sceglie un'altra release.

**Modalità host.** Senza Docker gli installer si fermano. `--host` (`-HostMode` su Windows) esegue i job direttamente sulla macchina come utente del runner, con etichette `self-hosted`, sistema operativo e architettura: nessun isolamento, usala solo per repository fidati.

**Sicurezza.** Un runner con il socket Docker può avviare container sul suo host, il che equivale a essere root: usa macchine dedicate. Le pull request da fork di collaboratori esterni attendono approvazione e non ricevono mai segreti.

**Disinstallazione.** `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --uninstall` (aggiungi `--purge` per cancellare configurazione e credenziale), `-Uninstall [-Purge]` su Windows, `docker rm -f ervisio-runner` per Docker. Poi elimina il runner in Admin > Runner.

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

### Cache calda

Lo storage è infrastruttura dell'istanza e lo gestiscono solo gli admin. Quando un tipo di dato tiene i suoi dati su una destinazione remota (S3, SFTP o WebDAV), il server tiene in automatico sul disco locale ciò che è in uso, senza una cache da configurare per ogni tipo:

- **Cosa resta in locale.** Tutto ciò che è stato letto o scritto di recente. I repository Git vengono copiati sul disco locale al primo accesso (fetch, clone, push, navigazione dalla UI web) e restano lì finché vengono usati. Oggetti LFS, asset delle release, blob del registry e artifact e cache di Actions vengono copiati sul disco locale quando sono scritti o letti la prima volta. Le scritture vengono confermate solo quando sono al sicuro sul primario remoto (e, per git con `durability = "sync"`, nel suo journal remoto): la copia locale non è mai l'unica.
- **Quando passa sul remoto.** Dopo un periodo senza letture né scritture la copia locale viene rimossa e il dato torna a essere servito dallo storage remoto, per poi essere ricopiato al prossimo accesso. Valori predefiniti: 14 giorni per i repository (riportare in locale un repository freddo costa il suo download completo), 7 giorni per oggetti LFS, asset delle release e blob del registry, 3 giorni per i dati di Actions (gli artifact si scaricano subito dopo il run, le cache vengono rilette da ogni run che le usa). L'ultimo uso viene registrato al massimo una volta al minuto per repository o oggetto, quindi le letture non costano scritture in più.
- **Budget del disco.** Con un `budget` sulla destinazione locale, circa il 35% è riservato ai file in cache (LFS 15%, registry 10%, release 5%, Actions 5%, solo per i tipi di dato che usano la cache calda) e il resto ai repository. Oltre `high_watermark` (predefinito 90%) i dati meno usati passano sul remoto fino a `low_watermark` (predefinito 75%). I repository fissati non si spostano mai.
- **Impostazione.** **Admin > Storage > Cache calda** la attiva o disattiva (predefinito: attiva) e imposta un solo "sposta sul remoto dopo N giorni di inattività" per tutti i tipi di dato (vuoto: i valori predefiniti sopra). Un tipo di dato può usare un valore suo, o spegnere la sua cache, dalla finestra della sua policy (**Cache**: Automatica, Nessuna cache o una destinazione locale). Lo stesso in `config.toml`: `[storage.hot_cache] enabled = true`, `ttl = "10d"`; per tipo di dato `cache = "none"` o `cache_ttl = "30d"` in `[storage.policy.<tipo>]`. Le modifiche valgono dal prossimo riavvio.
- **Visibilità.** La scheda Cache calda mostra lo spazio usato da repository e file in cache rispetto al budget, i repository sul disco locale con dimensione, ultimo uso e quando passeranno sul remoto, e permette di fissare, sbloccare o spostare subito un repository. Gli utenti non admin vedono sempre i repository come disponibili: dove stanno i dati non viene mostrato.
- **Installazioni solo locali.** Quando il primario di un tipo di dato è una destinazione locale non cambia nulla: non c'è una copia remota a cui tornare, quindi non viene rimosso niente. Gli avatar non passano dalla cache (sono piccoli e serviti con cache HTTP di lunga durata).

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
