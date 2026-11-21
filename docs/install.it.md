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

`setup.sh` scrive `.env`, `config/config.toml` e `config/master.key`, costruisce l'immagine e avvia lo stack `ervisiorepo` (`postgres` e `app`). Rilanciarlo conserva segreti e configurazione. `--ervisio yes` abilita il socket di amministrazione usato dal plugin opzionale per Ervisio Console, condiviso in `${ERVISIO_SOCKET_DIR}` (montato in `/run/ervisio-repo`).

L'app ascolta su `127.0.0.1:3400` (web e API) e `127.0.0.1:2222` (SSH); cambia `BIND_ADDRESS`, `APP_PORT` e `SSH_PORT` in `.env`. Le reti sono due: `private` (Postgres, interna) e `ervisiorepo_edge`, a cui si unisce solo `app` (alias `ervisiorepo-app`) perché un Caddy in container lo raggiunga.

`setup.sh` chiede anche se avviare il runner Actions integrato (`--runner yes|no`, predefinito yes); vedi [Runner](#runner). Profilo opzionale:

```sh
docker compose --profile backup up -d    # sidecar per l'archiviazione dei WAL (vedi docs/backup.it.md)
```

### Aggiornamenti

**Admin > Aggiornamenti** mostra la versione in esecuzione e le release pubblicate su GitHub da allora (controllo ogni 6 ore; `updates.check = false` lo disattiva). Con Docker l'aggiornamento parte da lì:

- `setup.sh` avvia anche il servizio `updater` (`--updater yes|no`, predefinito yes, profilo `updater` in `COMPOSE_PROFILES`). È un container `docker:cli` ed è l'unico oltre al runner con il socket Docker, che sull'host equivale a root: non pubblica porte e non sta sulla rete del database. Il server non riceve mai il socket: scrive la versione richiesta in `run/updater/request.json`, verificata sulle release di GitHub.
- L'updater esegue `update.sh`: backup di `.env`, `compose.yaml`, `config/config.toml` e dump del database in `backups/<ora>/` (restano i cinque più recenti), `compose.yaml` e script della nuova release, `ERVISIO_VERSION` in `.env` (tutte le altre chiavi restano), `docker compose pull` e `up --wait`. Se un passo fallisce, `.env` e `compose.yaml` tornano com'erano e ripartono i container precedenti. `config/master.key` non viene mai toccata.
- **Aggiornamenti automatici** (interruttore nella stessa pagina): una nuova release viene installata un'ora dopo la pubblicazione; una versione che fallisce non viene ritentata per 24 ore.

Senza updater, o da terminale, lo stesso aggiornamento è un solo comando sull'host:

```sh
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/docker/update.sh | sh
# opzioni: --dir /opt/ervisio-repo/deploy/docker  --version 1.2.3  --yes  --no-db-backup
```

In un checkout git `update.sh` non tocca `compose.yaml` e gli script (usa `git pull`) e cambia solo la versione dell'immagine. I pacchetti nativi si aggiornano con `install.sh` come sopra, poi `sudo systemctl restart ervisio-repo`; la pagina mostra anche questo comando. Le migrazioni girano all'avvio della nuova versione e un rollback non le annulla: per tornare indietro c'è il dump del database.

## Runner

I runner eseguono i job di Actions. Li fornisce chi gestisce l'istanza, come i runner ospitati di GitHub: i runner registrati in **Admin > Runner** formano un pool condiviso che prende i job di tutti i repository, quindi gli utenti scrivono solo `runs-on: ubuntu-latest` senza configurare nulla. I runner di organizzazione e di repository (nelle rispettive Impostazioni > Actions) sono un'aggiunta facoltativa per chi vuole macchine dedicate.

Ogni nuovo runner ha queste etichette: `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux` e `x64` o `arm64`, tutte con job in container Docker dalle immagini di `deploy/runner-images` (`ghcr.io/catthehacker/ubuntu:act-*`). Le etichette aggiuntive (`--labels gpu,big`) si sommano a quelle predefinite con la stessa immagine; `nome=docker://immagine` associa un'etichetta a un'altra immagine.

**Distribuzione del carico.** I runner si collegano solo in uscita e interrogano il server in long-poll. Un job va al primo runner che lo chiede e ha tutte le etichette di `runs-on`; un runner chiede solo quando ha uno slot libero, quindi un runner occupato non trattiene i job a scapito di quelli liberi. `capacity` (job contemporanei) vale di default metà delle CPU, almeno 1; si imposta con `--capacity N` all'installazione o con `runner.capacity` nella configurazione. Più macchine con le stesse etichette si dividono la coda: un runner sul server e altri su host separati distribuiscono il carico.

### Runner integrato sul server

- **Docker Compose**: `./setup.sh` (o `./setup.sh --runner yes`) scrive `COMPOSE_PROFILES=runner` in `.env`, avvia `app`, crea un token di registrazione d'istanza con `ervisio-repo actions runner-token` dentro il container `app` e lo salva in `data/runner/registration-token` (permessi 0600). Il container `runner` (`ervisio-runner daemon`) si registra come `builtin` al primo avvio, cancella il file del token e tiene credenziale e `config.toml` generato in `data/runner`. I token scadono dopo `actions.registration_token_ttl` (1h): non esiste un segreto condiviso a lunga durata. `RUNNER_LABELS` e `RUNNER_CAPACITY` in `.env` impostano etichette aggiuntive e capacità prima del primo avvio.
- **Pacchetti nativi**: `sudo ervisio-repo actions runner-setup` registra il runner `builtin` sull'indirizzo locale del server, scrive `/etc/ervisio-repo/runner.toml` e abilita `ervisio-runner.service` (servono Docker e il gruppo `docker`). Opzioni: `--labels`, `--capacity`, `--url`, `--no-start`.

Admin > Runner mostra in alto lo stato del runner integrato. Per stampare un token di qualsiasi ambito dalla shell del server: `ervisio-repo actions runner-token --scope instance|org/<owner>|repo/<owner>/<name>`.

I container dei job clonano il repository da `public_url` del server: deve essere raggiungibile dagli host dei runner (non `localhost`).

### Aggiungere un runner su un altro server Linux

Sul nuovo server (Debian, Ubuntu, Fedora, RHEL, Rocky o Alma, amd64 o arm64) basta un comando:

```sh
curl -fsSL https://git.example.com/runner/install.sh | sudo sh
```

1. Lo script riconosce la distribuzione e, se Docker manca, installa Docker Engine dal repository ufficiale (`download.docker.com`, apt o dnf) e lo abilita. `--no-docker` salta Docker ed esegue i job sull'host (vedi Modalità host).
2. Scarica `ervisio-runner` della stessa versione del server dalla release GitHub e ne verifica lo SHA-256.
3. Stampa un codice e un link: apri `https://git.example.com/runner/approve` (dopo l'accesso) e inserisci il codice, oppure segui il link diretto `…/runner/approve?code=ABCD-EFGH`. La pagina mostra la macchina (nome host, sistema, architettura, CPU, memoria, Docker, versione del runner, IP della richiesta) e permette di cambiare nome, etichette e capacità e di scegliere l'ambito: **Istanza** (amministratori), un'organizzazione (i suoi proprietari) o un repository (i suoi amministratori). Approva o Rifiuta. Sul server non serve digitare nulla: lo script attende anche senza terminale e il codice scade dopo 15 minuti.
4. All'approvazione lo script riceve un token di registrazione monouso (mai un token utente), registra il runner, scrive `/etc/ervisio-runner/config.toml` (capacità predefinita metà delle CPU, almeno 1; etichette `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux`, l'architettura), crea l'utente di sistema `ervisio-runner` nel gruppo `docker` e abilita `ervisio-runner.service`.
5. Installa `ervisio-runner-prune.timer` (giornaliero, con ritardo casuale fino a 1h, recupera le esecuzioni perse): rimuove immagini, container, reti e cache di build Docker inutilizzati da 7 giorni, i volumi anonimi inutilizzati, cache e workspace del runner non toccati da 14 giorni e, quando la directory dati di Docker ha meno del 15% libero, tutte le immagini inutilizzate. Le immagini dei container in esecuzione non vengono mai rimosse. Log: `journalctl -u ervisio-runner-prune`. Non viene installato con `--no-docker`.
6. Stampa un riepilogo: nome, etichette, capacità, `systemctl status ervisio-runner` e il comando di disinstallazione.

Le opzioni vanno dopo `sh -s --`: `--name`, `--labels gpu,big`, `--capacity N`, `--no-docker`, `--version X.Y.Z`, `--token errt_…` (salta l'approvazione, per l'automazione), `--url` (predefinito: il server che ha servito lo script), `--yes`. Rieseguito su una macchina già installata, lo script chiede se registrare di nuovo; senza terminale (o con `--upgrade`) sostituisce solo il binario; `--reconfigure` registra di nuovo.

I codici sono limitati per IP (20 all'ora), i codici sbagliati nella pagina di approvazione rallentano i tentativi; approvazioni e rifiuti finiscono nel registro di audit.

### Altre piattaforme e installazioni non interattive

**Nuovo runner** in Admin > Runner (o nelle impostazioni Actions di organizzazione/repository) mostra il comando sopra. Sotto **Avanzate / non interattivo** crea un token di registrazione (valido 1h) e mostra i comandi con token:

- **Linux** (systemd): `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --token errt_…`. Senza `sudo` installa in `~/.local/bin` con un'unità systemd utente (`loginctl enable-linger` la tiene attiva dopo il logout) e non installa Docker.
- **macOS** (launchd): `curl -fsSL https://git.example.com/runner/install.sh | sh` (approvazione col codice) oppure con `-s -- --token errt_…`; installa `~/.local/bin/ervisio-runner` e il LaunchAgent `dev.ervisio.runner` (log in `~/Library/Application Support/ervisio-runner/logs`). I job richiedono Docker Desktop avviato.
- **Windows**: in PowerShell come amministratore, `irm https://git.example.com/runner/install.ps1 | iex` (approvazione col codice) oppure `& ([scriptblock]::Create((irm https://git.example.com/runner/install.ps1))) -Token errt_…` installa `%ProgramFiles%\ErvisioRunner\ervisio-runner.exe`, tiene configurazione e stato in `%ProgramData%\ervisio-runner` (solo SYSTEM e Administrators) e registra l'attività pianificata `ErvisioRunner` (all'avvio, come SYSTEM, riavviata in caso di errore). Docker Desktop con container Linux non viene installato automaticamente.
- **Docker**: un comando `docker run … --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:<versione> daemon …`; il runner si registra da `ERVISIO_RUNNER_URL`/`ERVISIO_RUNNER_TOKEN` (anche `_NAME`, `_LABELS`, `_CAPACITY`) e tiene lo stato nel volume `ervisio-runner`.

Gli script sono serviti su `/runner/install.sh` e `/runner/install.ps1` con URL e versione del server già inseriti, e scaricano `ervisio-runner_<ver>_<os>_<arch>.tar.gz` (`.zip` su Windows) verificato con `ervisio-runner_<ver>_checksums.txt`.

**Stato, pausa e carico.** Gli elenchi dei runner mostrano stato (online, inattivo, occupato, in pausa, offline), job in esecuzione / capacità (comunicata dal runner), etichette, versione e ultimo contatto. **Pausa** lascia finire i job in corso senza prenderne di nuovi (per la manutenzione); **Riprendi** lo riattiva. La capacità si imposta all'installazione (`--capacity`) o in `runner.capacity` della configurazione (riavvia il servizio).

**Modalità host.** `--no-docker` (o `--host`; `-HostMode` su Windows) esegue i job direttamente sulla macchina come utente del runner, con etichette `self-hosted`, sistema operativo e architettura: nessun isolamento, usala solo per repository fidati.

**Sicurezza.** L'utente `ervisio-runner` è nel gruppo `docker`, il che equivale a essere root su quell'host: usa macchine dedicate. Le pull request da fork di collaboratori esterni attendono approvazione e non ricevono mai segreti.

**Disinstallazione.** `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --uninstall` ferma e rimuove servizio, timer di pulizia, utente, binario, configurazione e stato, e toglie il runner dal server (`ervisio-runner deregister`); se il server non è raggiungibile, eliminalo in Admin > Runner. `-Uninstall` su Windows; `docker rm -f ervisio-runner` per Docker (poi eliminalo dall'interfaccia).

### Runner on demand (macchine spente quando sono inattive)

Una macchina costosa (per esempio un'istanza EC2) può restare spenta e accendersi solo quando un job ne ha bisogno.

1. Prepara qualcosa che accenda la macchina con una chiamata HTTPS protetta da token, per esempio una Lambda AWS con Function URL che chiama `ec2:StartInstances` e controlla un header come `x-token`.
2. Installa il runner sulla macchina con `--idle-shutdown 15m`: dopo 15 minuti senza job smette di prendere lavoro, lascia finire i job in corso e spegne la macchina (lo spegnimento lo fa un'unità root, `ervisio-runner-poweroff.path`; l'utente del runner resta senza privilegi). `--idle-shutdown 0` lo disattiva.
3. In Admin > Runner apri il menu ⋯ del runner, **Avvio on demand…**: URL di avvio, header del token (`x-token`; vuoto invia `Authorization: Bearer`), token (salvato cifrato con la master key) e intervallo minimo (predefinito 180 s).

Quando il runner è offline (nessun contatto da 75 s) e un job in coda corrisponde alle sue etichette, il server fa una POST all'URL di avvio, al massimo una volta per intervallo; se un runner online potrebbe prendere il job, aspetta prima 90 s. Una chiamata rifiutata (4xx come 401/403, per esempio fasce orarie bloccate) si riprova dopo 10 minuti, gli altri errori (409 mentre la macchina si sta ancora spegnendo, rete, 5xx) dopo un minuto. **Avvia ora** nello stesso menu chiama subito l'URL. La riga del runner mostra il chip **On demand** e l'esito dell'ultima chiamata. Solo i runner d'istanza possono essere on demand.

**Cross-compilazione per Windows e macOS.** Docker su Linux esegue solo container Linux. `deploy/runner-images/cross/Dockerfile` aggiunge all'immagine dei job mingw-w64, zig, clang, NSIS, osslsigncode e wine: costruiscila sulla macchina del runner (`docker build -t ervisio/runner-cross:24.04 deploy/runner-images/cross`) e aggiungi un'etichetta come `"highpriority-cross=docker://ervisio/runner-cross:24.04"` a `runner.labels`. Produce binari Windows e macOS, ma non può firmare o notarizzare per Apple né provare sui sistemi veri.

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
- **Visibilità.** La scheda Cache calda mostra lo spazio usato da repository e file in cache rispetto al budget, i repository sul disco locale con dimensione, ultimo uso e quando passeranno sul remoto, e permette di cercare dove si trova un repository (cerca o incolla il suo URL). La **dimensione massima** limita il disco locale usato da repository e file in cache (predefinito 20 GB, `max_size`); oltre il limite quelli usati meno di recente lasciano il disco. I repository oltre la soglia **Non tenere in cache i repository oltre** (predefinito 2 GB, `large_repo`), o tutti con la cache spenta, vengono presi a ogni uso e tolti subito dopo. Gli utenti non admin vedono sempre i repository come disponibili: dove stanno i dati non viene mostrato.
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
