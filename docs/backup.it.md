# Backup e disaster recovery

Per ricostruire un'istanza ErvisioRepo servono tre cose:

1. **Il journal git** (classe di storage `git`): pack, indici, snapshot dei ref
   e manifest sui target remoti. È sempre aggiornato; le copie calde sul disco
   locale sono una cache.
2. **Il database PostgreSQL**: utenti, permessi, metadati dei repository.
3. **Il recovery kit**: file di configurazione, master key e chiavi host SSH.

Per la procedura di ripristino passo per passo su un host nuovo, la prova di ripristino e gli scenari di guasto, vedi [dr.it.md](dr.it.md).

## Database: pg_dump giornaliero

Se esiste una policy di storage chiamata `backup`, il server esegue
`pg_dump -Fc` ogni 24 ore e salva il dump in `backup/postgres/<ora UTC>.dump`,
con le repliche di quella policy. I dump più vecchi della sua `retention`
vengono cancellati; gli ultimi 3 restano sempre. Sul server devono essere
installati `pg_dump` e `pg_restore` della stessa major di PostgreSQL.

## Database: archiviazione continua con wal-g (facoltativa)

Per un punto di ripristino di minuti invece che di un giorno, archivia i WAL
con [wal-g](https://github.com/wal-g/wal-g) sull'host PostgreSQL:

```ini
# postgresql.conf
wal_level = replica
archive_mode = on
archive_command = 'wal-g wal-push %p'
archive_timeout = 60
```

Configura il bucket di wal-g con le variabili `WALG_*`, fai un base backup al
giorno (`wal-g backup-push $PGDATA`) e pulisci con
`wal-g delete retain FULL 7 --confirm`. Per ripristinare: `wal-g backup-fetch`
più `restore_command = 'wal-g wal-fetch %f %p'`, poi
`ervisio-repo restore --skip-db` per riallineare i repository.

## Recovery kit

```sh
ervisio-repo recovery-kit export --out kit.erk --config /etc/ervisio-repo/config.toml
```

Il kit è cifrato con la tua passphrase (almeno 12 caratteri; argon2id e
AES-256-GCM). Conservalo offline, separato dai backup: senza la master key i
target cifrati non sono leggibili.

## Ripristino su una macchina nuova

```sh
ervisio-repo recovery-kit import --in kit.erk          # config, master key, chiavi host
ervisio-repo restore --config /etc/ervisio-repo/config.toml
ervisio-repo prewarm --all                             # facoltativo
ervisio-repo serve
```

`restore` carica l'ultimo dump (o `--dump <chiave>`), migra lo schema, mette
ogni repository a freddo, prende ogni `manifest_seq` dal manifest remoto (che
può essere più recente del dump), reindicizza gli oggetti del journal che il
dump non conosce e mette in stato degradato i repository con manifest mancante
o non valido. I manifest senza riga di repository sono elencati come orfani.
I repository vengono idratati al primo accesso, o in anticipo con `prewarm`.
