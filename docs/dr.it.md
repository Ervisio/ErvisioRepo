# Guida al disaster recovery

Come ricostruire un'istanza ErvisioRepo su un host nuovo dopo aver perso quello vecchio. Riferimento ai comandi e contesto: [backup.it.md](backup.it.md). Dettagli di installazione: [install.it.md](install.it.md). English version: [dr.md](dr.md).

## Ambito, RPO e RTO

Nell'ambito: perdita totale dell'host e perdite parziali (database, storage, un repository, chiavi). Fuori ambito: failover multi-nodo; c'è un solo nodo di controllo e il recupero parte dallo storage remoto.

Sono ipotesi di pianificazione, non garanzie. Misura i tuoi valori con la prova di ripristino descritta sotto.

| Voce | Ipotesi |
| --- | --- |
| RPO, dati git | Vicino a zero. Il journal sui target remoti è sempre aggiornato. |
| RPO, database | Fino a 24 h con il `pg_dump` giornaliero; minuti con l'archiviazione wal-g (facoltativa, vedi backup.it.md). |
| RTO | Dominato dal tempo per preparare l'host e ripristinare il dump. I repository vengono marcati cold e si scaldano al primo accesso, quindi la dimensione non ritarda l'avvio. |

Quello che è stato creato tra l'ultimo dump e il guasto (utenti, token, PR) si perde, a meno che usi wal-g. I push git sopravvivono: `restore` re-indicizza gli oggetti del journal più recenti del dump.

## Cosa salvare

| Cosa | Dove si trova | Come è protetto |
| --- | --- | --- |
| Journal git (classe di storage `git`) | Target remoti (S3, SFTP, WebDAV) | Repliche della policy `git`. Le copie calde in `storage.hot_path` sono solo una cache. |
| PostgreSQL | `database.url` | `pg_dump -Fc` giornaliero nella policy `backup` (serve una policy di storage chiamata `backup`), wal-g facoltativo. |
| Configurazione | `/etc/ervisio-repo/config.toml` (Docker: `deploy/docker/config/config.toml`) | Recovery kit. |
| Master key | `security.master_key_file` (default `/etc/ervisio-repo/master.key`) | Recovery kit. Non viene mai sovrascritta una volta creata. |
| Chiavi host SSH | `ssh.host_key_dir` (solo con `ssh.enabled`) | Recovery kit. |
| Credenziali dei target | File indicati da `credentials_file`, `key_file`, `known_hosts_file`, `password_file` in `config.toml` | NON nel kit (TODO: da confermare; il kit contiene solo il file di configurazione, la master key e le chiavi host). Salvale a parte. |
| Segreti client OIDC, password SMTP | File indicati da `client_secret_file`, `password_file` | Come sopra: salvali a parte. |
| `.env` di Docker | `deploy/docker/.env` (contiene `POSTGRES_PASSWORD`) | Non nel kit. Salvalo, oppure imposta una nuova password sul nuovo host. |
| Configurazione del runner | `/etc/ervisio-repo/runner.toml` | Non nel kit; se la perdi, registra di nuovo i runner. |

Segreti e chiavi: non esiste un'impostazione separata per una «seal key». Le credenziali cifrate nel database (connessioni GitHub, segreti 2FA) usano chiavi derivate dalla master key, e anche i target cifrati (`encrypt = true`) dipendono da essa. Senza la master key quei valori e gli oggetti cifrati non si possono leggere. Anche i segreti di Actions sono cifrati così (HKDF-SHA256 da `security.master_key_file`): perdere la master key significa perdere tutti i segreti di repository e organizzazioni.

Esporta il kit dopo l'installazione e dopo ogni modifica alla configurazione, e tienilo offline, lontano dai backup:

```sh
ervisio-repo recovery-kit export --out kit.erk --config /etc/ervisio-repo/config.toml
```

La passphrase si legge da stdin oppure da `--passphrase-file`; minimo 12 caratteri.

## Ripristino su un host nuovo

Prerequisiti: il kit e la sua passphrase, i file con le credenziali dei target, accesso di rete ai target remoti, `pg_dump` e `pg_restore` della stessa major di PostgreSQL, e un database PostgreSQL vuoto (`database.url`).

### Installazione da pacchetto (deb/rpm)

```sh
# 1. Installa la stessa versione o una più recente
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# 2. Rimetti al loro posto configurazione, master key e chiavi host originali
sudo ervisio-repo recovery-kit import --in kit.erk
#    --root <dir> installa sotto un'altra directory; --force sovrascrive file diversi
# 3. Ripristina i file di credenziali citati da config.toml, poi verifica ogni target
sudo runuser -u ervisio-repo -- ervisio-repo storage test <target> --config /etc/ervisio-repo/config.toml
# 4. Ripristina il database dall'ultimo dump e riconcilia i repository
sudo runuser -u ervisio-repo -- ervisio-repo restore --config /etc/ervisio-repo/config.toml
#    un dump preciso: --dump <chiave>;  database ripristinato con wal-g: --skip-db
# 5. Facoltativo: scalda i repository più usati
sudo runuser -u ervisio-repo -- ervisio-repo prewarm --all --config /etc/ervisio-repo/config.toml
# 6. Avvia
sudo systemctl enable --now ervisio-repo
```

Il pacchetto crea una nuova `master.key` all'installazione; l'import del kit al punto 2 deve rimettere quella originale. Senza `--force`, un file esistente e diverso fa fallire l'import. Usa `--force` solo dopo aver verificato che la nuova chiave non sia mai stata usata. Controlla poi i proprietari (`master.key` è `0640 root:ervisio-repo`). TODO: confermare che `recovery-kit import` ripristini proprietario e permessi nel layout del pacchetto.

Esegui `restore` come utente `ervisio-repo`, così i file scritti nel percorso caldo hanno il proprietario giusto.

### Docker Compose

```sh
cd deploy/docker
# 1. Metti al loro posto i file originali prima del primo avvio
mkdir -p config
cp /percorso/ripristinato/config.toml config/config.toml     # dal kit
cp /percorso/ripristinato/master.key  config/master.key
# 2. Avvia solo Postgres
docker compose up -d postgres
# 3. Ripristina database e riconcilia i repository dentro l'immagine app
docker compose run --rm --no-deps app restore --config /etc/ervisio-repo/config.toml
# 4. Avvia tutto
docker compose up -d --wait
```

In alternativa alla copia a mano: esegui `ervisio-repo recovery-kit import --in kit.erk --root ./restored` su una macchina con il binario, poi copia il contenuto di `restored/` in `deploy/docker/config/`.

TODO: verificare che l'entrypoint dell'immagine inoltri `restore` (l'entrypoint è `tini`, il `command` è `serve`), il `database.url` che si aspetta il Postgres di Compose (`POSTGRES_PASSWORD` in `.env` deve coincidere) e che eseguire prima `setup.sh` non generi una `master.key` diversa (ne mantiene una esistente).

## Checklist di verifica

- [ ] `ervisio-repo version` mostra la build attesa; `curl -fsS http://127.0.0.1:3400/healthz` risponde.
- [ ] `ervisio-repo migrate status --config ...` non mostra migrazioni in sospeso.
- [ ] `restore` non ha stampato righe `degraded:`. Ognuna indica un repository con manifest mancante o non valido; indagare prima dell'uso.
- [ ] Nessuna riga `orphan journal` inattesa (dati nel journal senza riga di repository: il repository è stato creato dopo il dump).
- [ ] `ervisio-repo storage test <target>` passa per ogni target.
- [ ] Accesso come admin; 2FA e login OIDC (se configurato) funzionano. Se no, la master key è sbagliata.
- [ ] Clone di un repository via HTTPS e via SSH. L'impronta della chiave host SSH deve coincidere con la vecchia, altrimenti i client avvisano.
- [ ] Push di un commit su un repository, poi nuovo clone.
- [ ] Apertura di un repository cold: si scalda al primo accesso. Con `storage.paranoid = true` viene anche controllato con `git fsck`.
- [ ] I runner registrati si riconnettono (TODO: confermare se i runner sopravvivono al restore o vanno registrati di nuovo).
- [ ] Aggiorna `server.public_url` e DNS/Caddy (`ervisio-repo caddy status`) sul nuovo host.
- [ ] Esporta un nuovo recovery kit dall'istanza ripristinata.

## Guasti parziali

**Database perso, storage integro.** Installa configurazione e master key (import del kit), poi `ervisio-repo restore`. Perdi le modifiche successive all'ultimo dump. Con wal-g: ripristina base backup e WAL come descritto in backup.it.md, poi `ervisio-repo restore --skip-db`.

**Nessun dump disponibile.** `restore` fallisce con «no dump in the backup class». I soli manifest del journal non bastano a ricostruire utenti, permessi o nomi dei repository. TODO: non c'è un modo supportato per ricostruire il database dal solo journal; tieni i dump su almeno una replica.

**Target di storage perso, database integro.** Se una replica è sopravvissuta, imposta come `primary` della policy quella replica, aggiungi un nuovo target vuoto come replica ed esegui `ervisio-repo storage test <nuovo>`. TODO: confermare se esiste un comando o un job che ri-replica gli oggetti esistenti su un nuovo target; per ora assumi che arrivino solo le nuove scritture. Se tutte le copie del journal sono perse, i repository esistono solo nella copia calda: salvali subito da `storage.hot_path`.

**Perso solo il disco locale.** Le copie calde sono una cache. Avvia il server; i repository si scaldano dal journal all'accesso. `ervisio-repo prewarm <owner>/<repo>` oppure `--all` lo fa in anticipo.

**Repository corrotto.** Meglio farlo riscaldare di nuovo dal journal che ripararlo sul posto. TODO: non c'è un comando CLI per fare eviction di un solo repository; confermare l'azione via API admin o UI. Con `storage.paranoid = true` un repository che non supera `git fsck` viene rifiutato all'hydration, e il danno non si propaga.

**Master key persa.** Senza la chiave, target cifrati e credenziali cifrate sono irrecuperabili. Se hai il kit, importalo. Se non hai né l'una né l'altro, tutto ciò che sta nei target con `encrypt = true` è perso; i target non cifrati e il database restano leggibili, ma le credenziali delle connessioni GitHub e i segreti 2FA vanno reinseriti o azzerati.

**Chiavi host SSH perse.** Non è grave: il server ne genera di nuove al primo avvio, ma ogni client vede l'avviso di cambio chiave host. Ripristinale dal kit per evitarlo.

**Chiavi di firma perse.** La chiave di firma delle release (usata da `scripts/release/sign`, la metà pubblica è incorporata in `deploy/install.sh`) è un segreto del maintainer, non fa parte di un'istanza. Se la perdi, genera una nuova coppia con `genkey` dello strumento di firma, pubblica la nuova chiave pubblica in `install.sh` e firma di nuovo le release. Le chiavi di firma dei commit degli utenti sono per utente, non sull'host del server. TODO: confermare che non esistano altre chiavi di firma lato server oltre alla master key.

## Prova di ripristino

Eseguila ogni trimestre e dopo ogni cambio del layout di storage.

1. Prepara un host usa e getta (VM o un progetto Compose separato con `BIND_ADDRESS`, `APP_PORT` e `SSH_PORT` diversi). Non puntarlo mai in scrittura ai target di produzione: usa una credenziale in sola lettura, oppure ripristina da una copia del bucket.
2. Segui «Ripristino su un host nuovo» usando solo il kit, la passphrase e i file di credenziali che tieni offline. Se un passaggio richiede altro, aggiungilo a questa guida.
3. Esegui la checklist di verifica e annota il tempo impiegato come RTO misurato, e l'età del dump come RPO misurato.
4. Confronta numero di repository e qualche `git rev-parse` per branch con la produzione.
5. Distruggi l'host e ruota ogni credenziale che è stata copiata in giro.

Non avviare l'istanza della prova con il `server.public_url` o i target dei webhook di produzione attivi: invierebbe webhook e mail come se fosse la produzione.
