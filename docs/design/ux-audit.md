# Audit UX della web app (2026-11-04)

Base visiva: direzione Console (`design/DESIGN.md`). Questo audit riguarda
struttura, navigazione e impostazioni, non la palette.

## Problemi

### Navigazione
- La sidebar mette sullo stesso piano cose diverse: Instance, Repositories,
  Storage/Runners/Audit (solo admin), Notifications, Components (catalogo dev
  `/_ui`, visibile a tutti) e Account.
- La rail di icone a sinistra ripete la sidebar voce per voce e non aggiunge nulla.
- Non c'è un menu utente (avatar, profilo, esci). "Sign out" è un link nella
  pagina Account.
- Le organizzazioni non hanno un punto d'ingresso. Si raggiungono solo da
  Account › Profilo.
- Il contesto (repo, org) non si vede nella sidebar: dentro un repo la nav
  resta quella globale.

### Home
- "Instance" è una tabella chiave/valore di metadati. Mancano repo recenti,
  attività, issue e PR assegnate, stato dei runner e un pulsante "Nuovo".

### Impostazioni
- Ogni area ha un modello diverso:
  - Account: 6 tab.
  - Repo: 9 tab orizzontali.
  - Org: 9 tab.
  - Admin: pagine sparse nella nav globale.
- Molte cose sono nel posto sbagliato:
  - "Crea organizzazione" e le email sono in Profilo.
  - "Notifiche" è una sola checkbox.
- I form sono una colonna stretta da 500px su una card larga 1000px, con un
  unico "Salva" in fondo. Non si capisce cosa è cambiato.
- Le zone pericolose (trasferisci, elimina) sono mescolate ai campi normali.

### Funzioni senza UI (l'API c'è già)
- Collaboratori del repo (PUT/DELETE collaborators).
- Label e milestone si creano ma non si modificano né si eliminano.
- Fork.
- Membri dei team, modifica dei team ed eliminazione dell'org.
- Inviti di istanza (POST /admin/invites): la modalità di registrazione
  "invite" è inutilizzabile.
- Pin/evict dello storage.

### Funzioni senza API né UI
- Gestione utenti admin: elenco, sospensione, ruolo, reset 2FA.
- Impostazioni d'istanza dal web. Oggi tutto il `config.toml` si cambia solo
  a mano:
  - `[security]`: registrazione, directory utenti
  - `[auth]`: policy password, sessioni, 2FA obbligatoria, TTL token
  - `[[auth.oidc]]`
  - `[mail]`
  - `[git]`
  - `[ssh]`
  - `[actions]`
  - `[webhooks]`
  - `[storage]`

### Visivo
- L'empty state del repo impila titoli e blocchi di comandi senza gerarchia.
- Gli header delle pagine non sono coerenti: alcuni hanno sottotitolo e badge,
  altri no.

## Architettura proposta

1. **Shell**:
   - In alto a sinistra uno switcher di contesto: istanza › org/utente › repo.
   - Una sidebar contestuale:
     - Globale: Home, Repository, Organizzazioni, Notifiche.
     - Dentro un repo: Codice, Issue, PR, Actions, Impostazioni.
   - In basso la voce "Amministrazione", solo per gli admin.
   - In alto a destra un menu utente con profilo, impostazioni, tema e esci.
   - La rail di icone va tolta, oppure diventa la versione compatta della sidebar.
   - `/_ui` sparisce dalla nav.
2. **Home**:
   - Repo recenti e attività.
   - Issue e PR che mi riguardano.
   - Per gli admin, lo stato dell'istanza (runner, storage, avvisi).
3. **Impostazioni unificate**: un solo layout per Account, Repo, Org e Admin.
   - Sezioni in una sottonav verticale a sinistra.
   - Form a due colonne (etichetta e descrizione | controllo).
   - Barra di salvataggio che compare solo con modifiche pendenti.
   - "Zona pericolosa" separata in fondo.
4. **Amministrazione**:
   - Panoramica.
   - Utenti e inviti.
   - Autenticazione (policy, OIDC).
   - Registrazione.
   - Email.
   - Git & SSH.
   - Actions & runner.
   - Storage.
   - Webhook.
   - Audit.
   - Le chiavi del `config.toml` diventano impostazioni salvate nel DB, con il
     file come default. Servono endpoint nuovi, contract-first.

## Ordine di lavoro

1. Varianti (Variant Studio) di shell e navigazione, home e layout impostazioni.
2. Shell, menu utente e home: solo frontend.
3. Layout impostazioni unificato e UI per le API già esistenti.
4. Area admin:
   - Endpoint utenti, inviti e impostazioni d'istanza (openapi, sqlc).
   - Poi la UI.
