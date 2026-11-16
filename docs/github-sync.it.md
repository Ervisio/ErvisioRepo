# Sincronizzazione con GitHub e GitHub Actions

Un repository si può collegare a un repository GitHub (Impostazioni > GitHub)
tramite una connessione GitHub (installazione di una App o token
fine-grained):

- **mirror**: il repository segue GitHub e rifiuta i push;
- **backup**: branch e tag vengono inviati a GitHub dopo ogni push;
- **sync**: entrambe le direzioni, solo fast-forward; in caso di divergenza il
  commit di GitHub resta su `sync-conflict/github/<branch>`.

Un repository si può anche importare una volta da GitHub (dati git, label,
milestone, issue, pull request, release e, con il tipo **actions**, la
configurazione Actions e lo storico delle esecuzioni).

## Workflow di un repository GitHub

I file in `.github/workflows` arrivano con git. La pagina Actions elenca i
workflow del branch predefinito, più quelli che sono stati eseguiti qui o
(importati) su GitHub. Un file in `.ervisio/workflows` nasconde quello con lo
stesso nome in `.github/workflows`.

## Eseguire le Actions sui push sincronizzati

I commit scritti dalla sincronizzazione (modalità mirror e sync) non hanno un
utente dietro, quindi di norma non avviano workflow. Attiva **Esegui le
Actions sui push sincronizzati** sul collegamento (`runActions` in
`PUT /repos/{owner}/{name}/github/sync`) per eseguire i workflow del
repository anche per loro, come per un push:

- le esecuzioni mostrano `ervisio-sync[bot]` come autore (`github.actor`,
  `sender` e `pusher` dell'evento);
- anche questi push aggiornano le pianificazioni (schedule) del branch
  predefinito;
- i push fatti dai workflow (job token) non avviano mai nuove esecuzioni, e i
  push di Ervisio verso GitHub (backup, sync) non creano nulla in locale,
  quindi qui nulla gira due volte. GitHub esegue comunque la sua copia dei
  workflow su GitHub.

## Import di variabili, secret ed esecuzioni

Impostazioni > Secret (o Variabili) mostra **Importa da GitHub** su un
repository collegato o importato
(`POST /repos/{owner}/{name}/actions/github/import`). Con la connessione del
collegamento (o dell'import) legge:

- le variabili del repository, gli environment e le loro variabili: quelle
  già presenti restano, a meno di attivare "Sostituisci le variabili già
  presenti";
- i nomi dei secret (repository ed environment): GitHub non restituisce mai i
  valori, quindi compaiono come **secret da compilare** finché qui non esiste
  un secret con quel nome;
- le ultime 100 esecuzioni dei workflow, in sola lettura (al massimo 500 per
  repository): l'elenco delle esecuzioni le mostra con un badge GitHub e un
  link ai log su GitHub. Non vengono mai pianificate né eseguite qui.

Dopo il primo import lo storico viene aggiornato a ogni giro di
sincronizzazione del collegamento. Una connessione senza il permesso di
lettura necessario (Actions, Variables, Secrets, Environments per una App)
riceve un messaggio chiaro nello stato dell'import invece di un errore.
