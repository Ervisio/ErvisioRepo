# ErvisioRepo: design system

Riferimento per la Fase UI (docs/PLAN.md §11). Il kit in `web/src/ui` parte da qui.

## Stato

- Direzione visiva: **A Console** come default. B Foglio e C Corsie restano disponibili come layout che l'admin attiva dalle impostazioni (round `001-direzione-visiva`).
- Token: `design/tokens.css`. Parte dei valori è ricostruita, vedi sotto.
- Mock: `design/mock/direzione-{a,b,c}.html`, generati da `design/mock/build.py` (contenuti in `content.py`, base comune in `shared.css`, icone e navigazione in `mock.js`).

Per riaprire la galleria:

```sh
node ~/.claude/skills/variant-studio/scripts/studio.mjs start --project design --open
```

## Token

I nomi seguono il contratto dello SDK plugin (docs/sdk.md, Styling): `--bg --surface --sunk --ink --ink2 --ink3`, `--h-<sezione>` e `--h-<sezione>-s`, `--ok --warn --err --info` con `-s`, font Figtree e JetBrains Mono.

Il `tokens.css` del core non è nel workspace. Ho ricostruito i token così:

| Token | Valore OLED | Fonte |
|---|---|---|
| `--bg` | `#000000` | verificato (ErvisioLogs `index.css`) |
| `--surface` | `#0e0e10` | verificato |
| `--sunk` | `#18181b` | verificato |
| `--ink` / `--ink2` / `--ink3` | `#f4f4f6` / `#b0b0ba` / `#8f8f9a` | verificato |
| `--ok` / `--err` | `#3ddc97` / `#ff6b81` | verificato |
| `--h-log` | `#ffb547` | verificato |
| tema chiaro: bg, surface, sunk, ink | `#ececef #f7f7f9 #fff #16161a #50505b #6b6b76` | verificato |
| `--warn` / `--info` | `#ffd166` / `#6cb6ff` | ricostruito |
| `--h-ov --h-term --h-file --h-svc --h-sw --h-usr --h-plg` | vedi tokens.css | ricostruito |
| tinte `-s` | hue a opacità .14 (stato .13) | ricostruito |
| `--raise` | `#222226` | nuovo, solo ErvisioRepo: hover dentro `--sunk` |
| `--syn-*` | derivati dalle hue | nuovo, alimenta il tema Shiki |

Quando arriva il `tokens.css` del core, sostituisci i valori ricostruiti. I nomi restano.

Raggi: `--r-ctl` 12 px (campi, pulsanti, righe, chip), `--r-card` 18 px (card, blocchi codice), `--r-sheet` 20 px (pannello contenuti, dialog). Le pill di stato usano 999 px.

## Sezioni e hue

Ogni sezione del repo imposta `--hue` e `--hue-s` con una classe `sec-*`. Icone di sezione, tab attiva, CTA principale e focus ring prendono la hue.

| Sezione | Classe | Hue del core |
|---|---|---|
| Codice | `sec-code` | `--h-file` (blu) |
| Pull request | `sec-pr` | `--h-svc` (viola) |
| Issue | `sec-issue` | `--h-usr` (rosa) |
| Actions | `sec-act` | `--h-term` (turchese) |
| Pacchetti | `sec-pkg` | `--h-sw` (arancio) |

Gli stati usano i colori di stato e mai la hue: verde passato o aggiunto, rosso fallito o rimosso, giallo avviso, blu in corso.

## Regole valide per tutte le direzioni

- Sfondo OLED nero pieno. Separi i blocchi con `--surface`, `--sunk` e spazio, senza linee divisorie grigie.
- Niente gradienti, glow, vetro od ombre decorative.
- Una sola CTA piena per vista, nella hue della sezione con testo nero.
- Titoli Figtree 800 con tracking negativo; corpo Figtree 500 a 14 px; numeri tabulari.
- Il mono serve solo per valori reali: codice, log, sha, digest, nomi di branch, percorsi, comandi.
- Codice e log: legature disattivate, numeri di riga in `--ink3`, sfondo `--bg` dentro le card.
- Diff: righe aggiunte su `--ok-s`, rimosse su `--err-s`, hunk su `--sunk`. I thread inline restano fissi a sinistra quando il diff scorre.
- Copy in frase normale, verbi attivi, niente MAIUSCOLO, niente frecce nei pulsanti, niente meta uniti dal punto centrale.
- Transizioni 150–200 ms. Con `prefers-reduced-motion` lo spinner si ferma.
- Responsive di riferimento a 1280 e 390 px (Playwright copre anche 360 e 768). Sotto i 900 px le colonne laterali scendono sotto il contenuto e il codice scorre in orizzontale dentro la sua card.

## Direzione scelta

**A Console come default.** L'utente l'ha approvata il 2026-11-03 con questa nota: "Default su A ma l'admin dalle impostazioni può decidere quale visuale attivare anche tra B e C".

### Layout selezionabile dall'admin

- In Impostazioni istanza c'è un'opzione Aspetto con tre valori: `console` (default), `foglio` e `corsie`. Il server la salva a livello di istanza e la serve nella config iniziale.
- Il kit legge l'opzione e imposta `data-layout` su `<html>`. Il valore `console` è anche il fallback quando manca.
- I tre layout condividono token, hue di sezione, primitive di codice, diff e log e copy. Cambiano solo la shell e la composizione di ogni pagina.
- Struttura in `web/src/ui`: componenti base comuni (Button, Card, Tabs, Pill, FileRow, CodeView, DiffView, LogView, JobRow, TagRow, LayerBar) e tre shell (`ConsoleShell`, `FoglioShell`, `CorsieShell`). Ogni pagina dichiara le sue zone (titolo, azioni, contenuto, lato) e la shell attiva le dispone.
- Gli screenshot Playwright di riferimento coprono A a 1280 e 390 px. B e C hanno uno smoke test a 1280 px.

### A Console: regole

Le regole qui sotto descrivono la prima Console; shell e tema attuali sono quelli della decisione "Console a barra unica (2026-11-10)".


- Shell desktop:
  - Rail Ervisio da 72 px: logo, dashboard, repository, revisioni, notifiche e tile dei repo recenti. Le icone sono tonde a 44 px e diventano quadrate (14 px) quando sono attive.
  - Colonna repo da 248 px: owner/nome, chip di visibilità, voci di sezione e card Storage.
  - Pannello contenuti su `--surface` con raggio 20.
- Voci di sezione alte 44 px, con tile da 32 px su `--hue-s` e icona in hue. La voce attiva ha la tile piena in hue con icona nera e la riga su `--sunk`.
- Il contenuto sta in card `--sunk` con raggio 18 e padding 6. Le righe dentro le card hanno raggio 12 e passano a `--raise` all'hover.
- Testata di pagina: h1 a 26 px peso 800, controlli `--sunk` alti 36 px, una CTA piena in hue.
- Layout tipici:
  - Home e pacchetto: griglia contenuto + colonna da 280 px.
  - PR diff e Actions: colonna da 240 px (file della PR o job) + contenuto.
- Sotto i 900 px la rail e la colonna repo spariscono. In alto restano una barra con menu e repo e una fila di chip di sezione scorrevole. La griglia passa a una colonna e nelle righe dei file il messaggio di commit sparisce.
- Accessibilità: le icone della rail hanno `aria-label` e la voce attiva ha `aria-current`. I target sono di almeno 44 px su desktop e 38 px nei chip mobile.

Riferimento visivo: `design/mock/direzione-a.html` e lo screenshot approvato in `design/.variant-studio/rounds/001-direzione-visiva/_shots/`. B e C restano in `direzione-b.html` e `direzione-c.html` come specifica dei layout alternativi.

## Decisioni dell'utente (2026-11-03)

- **Token ricostruiti accettati.** `design/tokens.css` (copiato in `web/src/styles/tokens.css`) resta valido; si sostituisce con il `tokens.css` del core quando arriva. I nomi sono stabili.
- **Mappatura hue confermata.** Codice blu, Pull request viola, Issue rosa, Actions turchese, Pacchetti arancio (tabella Sezioni e hue).
- **Layout.** Il default lo sceglie l'admin a livello di istanza; ogni utente può sovrascriverlo nelle impostazioni. Per ora la scelta sta in `localStorage` (`ervisio.layout`, vedi `web/src/ui/shell/layout.ts`, con TODO per le due impostazioni).
- **Screenshot di riferimento.** A Console a 1280x800 e 390x844; B Foglio e C Corsie solo a 1280x800. Stanno in `web/tests/visual/__screenshots__/` e si rigenerano con `npm run test:visual -- --update-snapshots`. I file dipendono dal sistema operativo (suffisso `-darwin`, `-linux`): i riferimenti per la CI vanno generati su Linux.

- **Run delle Actions (2026-11-04).** Default A (righe dense), B e C selezionabili come per issue e notifiche. Dettaglio in `design/wip/003-actions-run/DECISION.md`.

- **Shell e navigazione (2026-11-04).** Round `006-shell-navigazione`, variante B "Sidebar unica". Sostituisce rail + colonna della Console:
  - Sidebar da 264 px:
    - In alto un selettore di contesto (owner ▾ / repo ▾).
    - Le voci sono divise in gruppi con titolini (Navigate, Pinned; dentro un repo Repository, Automation, Manage).
    - In fondo il blocco "Administration", visibile solo agli admin, in hue ambra `--h-log`.
  - Barra superiore: ricerca (con lo scope del repo quando sei dentro), New ▾, notifiche col numero dei non letti e menu utente (avatar).
  - Home = dashboard: Needs your attention, Activity, Recent repositories, Instance health (solo admin).
  - `/_ui` esce dalla nav.
  - Audit dell'utente in `docs/design/ux-audit.md`.
- **Impostazioni (2026-11-04).** Round `005-impostazioni`, variante A "Sottomenu". È un solo layout per account, repo, org e istanza:
  - Sottonav verticale a gruppi da ~220 px dentro il pannello, con ricerca.
  - Righe a due colonne (etichetta e descrizione | controllo) e marcatore sui campi modificati.
  - Barra di salvataggio sticky che compare solo con modifiche pendenti.
  - Zona pericolosa separata.
  - I valori impostati nel file di config sono bloccati e lo dicono.
  - In tema chiaro le etichette piccole usano `--ink2`, perché `--ink3` su `--surface` fa 4,46:1.

- **Console acrylic (2026-11-06).** Round `009-shell-acrylic-a-type`, variante A "Morbido". Sostituisce la sidebar unica del round 006 e, solo per la Console, le regole "niente vetro, gradienti, glow" e i pesi 800. Foglio e Corsie restano invariati.
  - Struttura: rail di squircle da 44 px (logo, tu e le tue org, +, esplora | notifiche, admin, avatar con pallino di stato), colonna canali da 248 px e pannello principale in vetro. La colonna mostra il contesto: home (nav globale + fissati e recenti), repo (Codice, Collaborazione, Automazione, Gestione), org, amministrazione; in fondo la card utente.
  - Tema: token `--accent` (#7c8cff), `--acc-fill`, `--acc-text`, `--sel`, `--glass`, `--edge`, `--line`, `--card`, `--hover` sotto `:root[data-layout="console"]` in `web/src/styles/tokens.css`; le superfici del core (`--bg`, `--surface`, `--sunk`, `--raise`) sono rimappate lì. Sei forme accent sfocate si muovono sullo sfondo (ferme con `prefers-reduced-motion`); vetro `blur(28px) saturate(150%)`. In chiaro fondo tinto accent, vetro bianco con ombra, `--acc-fill` scurito per il contrasto.
  - Tipografia: corpo 400, voci attive e pulsanti 500, titoli 600; h1 a 22 px. Voci della colonna alte 38 px con gap 4.
  - Sotto i 720 px rail e colonna spariscono: barra inferiore in vetro (Home, Esplora, Notifiche, Menu) e la colonna si apre come drawer.
  - Accent fisso per ora: nessuna preferenza API per accent o movimento.

- **Console a barra unica (2026-11-10).** Round `001-repo-home-minimal`, variante B "Solo barra in alto". Sostituisce la Console acrylic del round 009: niente rail, colonna, vetro e forme sullo sfondo.
  - Struttura: barra in alto sticky su due righe. Riga 1 (60 px): logo, selettore owner / repo, ricerca, notifiche, Nuovo, avatar. Riga 2 (46 px): tab del contesto. Nel repo Codice, Issue, Pull request, Actions e "Altro" con il resto. Pagina centrata a 920 px (1160 px con colonna laterale o impostazioni).
  - Home del repo: titolo con il solo nome, descrizione sotto. Barra con branch, Clona (primario) e menu ⋯ (cronologia, aggiungi file, archivi, fissa, fork, impostazioni). Lista file senza card, righe da 48 px; README a 68ch, 72 px sotto la lista.
  - Testo: corpo 14,5 px con interlinea 1,65; codice e controlli a 1,5. Titoli 600, h1 a 30 px.
  - Contrasto: logo, avatar, Clona e sottolineatura dei tab usano `--acc-fill` con `--on-accent` (in chiaro il fill è scurito).

- **Impostazioni stile GitHub (2026-11-16).** Round `010-impostazioni-github`, variante A "GitHub classico". Sostituisce l'aspetto del round 005 (box con striscia e righe a due colonne); restano sottonav a gruppi, ricerca, marcatore dei campi modificati, barra di salvataggio e blocchi da config.toml.
  - Sottonav da 240 px: gruppi separati da un filetto, voce attiva su `--raise` in 600 con barretta `--acc-fill` da 4 px a sinistra.
  - Sezioni aperte, senza box: titolo 20 px peso 400 su un filetto `--line`; la prima sezione fa da titolo di pagina a 24 px.
  - Campi in colonna (etichetta, descrizione, controllo) larghi al massimo 520 px; i toggle stanno a sinistra dell'etichetta. Controlli da 32 px, 44 px con puntatore coarse.
  - Liste e tabelle in un box bordato con righe separate da filetti; zona pericolosa = titolo normale sopra un box bordato `--err`.
  - Sotto i 900 px la sottonav diventa una fila di pillole scorrevole.

- **Profilo, org e liste (2026-11-16).** Round `011-profilo-org-liste`, variante A "GitHub classico".
  - Liste di repository (org, I miei repo, profilo, Esplora): barra filtri (trova, tipo, ordinamento) sopra righe separate da filetti; nome in `--acc-text` 600 16 px, pillola di visibilità, descrizione, "Aggiornato" e dimensione.
  - Organizzazione: testata con avatar quadrato 96 px, nome e badge, descrizione, filetto sotto; nel tab Repository colonna laterale da 296 px con persone e team.
  - Profilo utente `/{username}`: avatar tondo 296 px, nome, login e "Modifica profilo" a sinistra, repository a destra. Pubblico anche senza login.

- **Intestazione repository (2026-11-16).** Round `012-intestazione-repo`, variante A "GitHub fedele".
  - Ogni pagina di repository (Console) apre con un'intestazione: avatar del proprietario 24 px (quadrato per le org, tondo per gli utenti), "proprietario / **nome**" a 20 px, pillola di visibilità; a destra Fissa, Segui (livello di notifica, menu), Fork e Stella con il conteggio. Filetto `--line` sotto. Senza login Segui, Fork e Stella portano al login.
  - Controlli da 32 px, 44 px con puntatore coarse; sotto i 720 px le etichette diventano solo per screen reader e restano icone e conteggi.
  - Pulsante primario "Codice" con card a tendina allineata a destra: titolo Clona, segmentato HTTPS / SSH / CLI, URL con copia, suggerimento, poi Scarica ZIP e Scarica tar.gz. Il menu ⋯ tiene solo Cronologia, Aggiungi file e Impostazioni.
  - Barra laterale Informazioni: riga "N stelle". Persone che seguono e fork non hanno ancora un conteggio nell'API e non compaiono.
  - Profilo: il README del repository pubblico `utente/utente` sta in un box sopra i repository, con "utente / README.md" e matita per il proprietario; se manca, solo il proprietario vede una card tratteggiata "Presentati con un README" che crea repository e README.md.

- **App e bot (2026-11-16).** Round `013-app-e-bot`, variante A "GitHub fedele".
  - Impostazioni sviluppatore dentro il frame delle impostazioni: gruppo "Sviluppatore" nella sottonav con App, OAuth App e Token personali.
  - Modulo di registrazione a campi in colonna (etichetta, controllo), titoli di sezione su filetto, pulsante primario in fondo.
  - Permessi (per le App, A2): box bordato con una riga per permesso e segmentato a tre vie Nessuno / Lettura / Scrittura. Eventi come chip.
  - Installazione e consenso: card centrata con logo 56 px (quadrato con iniziale se manca), "di proprietario", account, permessi in parole, Autorizza primario e Annulla.
  - Elenco `/apps`: righe separate da filetti con logo 48 px, nome, proprietario, descrizione, installazioni e "Installa". Pagina di configurazione di Claude (A3) nelle impostazioni dell'org.
  - A1 implementa: OAuth App personali e di org (elenco, registrazione, pagina dell'app con client ID, secret mostrati una volta, modifica, eliminazione), "Applicazioni" autorizzate con revoca, pagina di consenso `/login/oauth/authorize`, impostazione `security.app_registration`. "App" resta un segnaposto fino ad A2.
  - A2 implementa: sezione "App" (utente e org) con elenco, registrazione (slug con anteprima `slug[bot]`, permessi a matrice, eventi a chip limitati dai permessi: abbassare un permesso toglie gli eventi non più coperti; metadati fissi in Lettura; i permessi solo-scrittura non offrono Lettura), pagina dell'App a schede (Generali, Permessi ed eventi, Chiavi e secret, Installazioni, Consegne recenti, Avanzate). Chiave privata: scaricata subito come `<slug>.<data>.private-key.pem` e mostrata una volta. "App installate" nel gruppo Accesso (utente) e Organizzazione (org): elenco, configurazione (repository tutti/scelti, sospendi, disinstalla) e riquadro "Nuovi permessi richiesti" con tabella concesso/richiesto e "Accetta". Installazione in `/apps/:slug/installations/new`: card centrata 620 px, account come righe a radio (già installata = link a Configura), repository, permessi in parole.

- **Riprogettazione: stile base (2026-11-17).** Round `015-fondamenta-stile` variante B "Superfici morbide" con la colonna del round `016-colonna-info` variante A "Colonna aperta". Base di tutte le schermate riprogettate: layout e funzioni come GitHub, stile più moderno. Kit dei mock in `design/mock/soft.css` (+ `design/mock/sprite.html` per le icone).
  - Barra sticky su `--bg` all'88% con blur 12 px e filetto `--line` al 60%. Riga 1 (60 px): menu, logo 30 px, owner / repo, ricerca a pillola centrale (max 460 px, `⌘K`), Nuovo, notifiche con pallino, avatar. Riga 2: tab a pillola alte 34 px, raggio 10, attiva su `--raise` con icona in `--accent`, conteggi in chip tondo.
  - Niente filetti attorno ai blocchi: superfici a strati (`--surface` attorno, `--bg` dentro), raggio 14 per i contenitori e 10 per i controlli. Pulsanti 34 px su `--surface` senza bordo; primario `--acc-fill` (non verde); varianti ghost trasparenti.
  - Intestazione repo: avatar 56 px raggio 16, "owner / **nome**" a 28 px peso 600 (owner in `--ink2` 400), pillola di visibilità, descrizione 15 px, topic a pillola su `--sel`. A destra Fissa (ghost), Segui, Fork, Stella con conteggi.
  - Lista file: card `--surface` con striscia dell'ultimo commit e lista su `--bg` interna; righe 40 px raggio 8, hover `--surface`.
  - Colonna Informazioni (300 px): sezioni aperte separate da filetto leggero, titolo 14 px 600 con conteggio a chip; conteggi in riga; link a righe 32 px con hover; release con tile verde; contributori con nome e numero di commit; linguaggi in elenco con percentuale a destra.
  - Mobile: tab scorrevoli, intestazione in colonna con azioni a tutta larghezza, conteggi sopra i file, ricerca a icona.

- **Dashboard e notifiche (2026-11-17).** Round `017-dashboard-notifiche`, variante C "Riquadri e anteprima".
  - Dashboard: in alto quattro riquadri in una superficie (Review richieste, Issue assegnate, Le tue PR aperte, Run falliti) con numero 26 px e riga di contesto; il riquadro attivo filtra la coda sotto (bordo interno `--accent`). Sotto: coda (righe con icona di stato, titolo, repo #n, chip del motivo, tempo) e attività per te / seguiti; a destra (340 px) repository con conteggi issue/PR e blocco Istanza (solo admin).
  - Notifiche come posta: elenco da 420 px (titolo, segmentato In arrivo / Review / Assegnate / Salvate, filtro testo; righe con pallino non letto, icona, repo #n, tempo, titolo, chip) e anteprima del thread a destra con Fatto, Salva, Apri, stato, descrizione e controlli. Sotto i 720 px solo elenco.
  - Contrasto (audit): il tempo nella riga selezionata (`--raise`) usa `--ink2`; la pillola di stato piena usa `#238636`/`#1f883d` con testo bianco, non `--ok`.

- **Codice (2026-11-17).** Round `018-codice`, variante A "Albero laterale".
  - Griglia 280 px + contenuto (max 1440): albero in una superficie sticky con chiudi albero, Aggiungi file, branch, "Vai al file" (t); cartella aperta, file corrente su `--bg` 500. Sotto i 900 px l'albero si apre da un pulsante.
  - Cartella: percorso con link `--accent` e ultimo segmento in grassetto, Aggiungi file e ⋯; card con striscia dell'ultimo commit (Cronologia) e righe dei file.
  - File: percorso con copia, card con l'ultimo commit del file, poi area codice su `--bg` con toolbar (segmentato Codice / Blame, righe e dimensione, Raw, copia, scarica, matita, ⋯); numeri di riga `--ink3`, righe selezionate su giallo 12% con barretta.
  - Modifica: percorso con nome file modificabile e "in main", Annulla e "Salva modifiche…" primario; toolbar Modifica / Anteprima modifiche, rientro, a capo. Il salvataggio apre un dialog (messaggio, descrizione, commit diretto o nuovo branch + PR).

- **Cronologia e ricerca (2026-11-17).** Round `020-cronologia-mix` (combinazione di 019 B, C e A).
  - Commit: linea del tempo per giorno, ogni commit in una riga `--surface` raggio 12 con avatar 36 px, titolo, autore, stato dei controlli, ref (branch `--sel`, tag `--ok-s`), SHA, copia, sfoglia. "Carica altri commit" in fondo.
  - Dettaglio commit (vale anche per confronto e "File modificati" delle PR): colonna dei file a tutta altezza (240 px, sticky, filtro, raggruppati per cartella, +/−) e a destra intestazione grande (avatar 44, titolo 24 px, corpo, autore, SHA, genitore, tag) con Unificata/Affiancata e Sfoglia; diff affiancato di default.
  - Branch: tabella in una superficie (Branch, Ultimo commit, Indietro / avanti, Pull request, azioni), segmentato Tutti / I tuoi / Obsoleti, Cerca, Confronta, Nuovo branch.
  - Ricerca nel codice: campo in alto, filtri a sinistra (tipo con conteggi, linguaggi, percorsi), risultati come card per file con le righe e la corrispondenza evidenziata.
  - Contrasto (audit): nelle righe aggiunte/rimosse numeri di riga in `--ink2`; evidenziazione delle parole al 25% con testo `--ink`; conteggi −N piccoli in un rosso più chiaro (`#ff7b72`).

- **Issue (2026-11-17).** Round `021-issue`, variante B "Viste e proprietà". Il dettaglio fa da base anche alla conversazione delle PR.
  - Elenco: viste salvate a sinistra (Aperte, Assegnate a me, Create da me, Menzioni, Chiuse, poi Etichette con pallino colore e conteggio); titolo della vista, Milestone, Nuova issue; ricerca + filtri a chip (Etichetta, Assegnatario, Raggruppa); righe su una linea (icona, #n mono, titolo, etichette, commenti, avatar) raggruppate per milestone. Sotto i 900 px le viste diventano pillole scorrevoli.
  - Etichette: pillola con colore al 18%, testo misto 55% col colore e bordo al 40%.
  - Dettaglio: percorso "Issue / #n", titolo 30 px; post senza card (avatar 32, nome, tempo, ⋯, markdown, reazioni) separati da filetti; eventi compatti rientrati; composer in superficie. Pannello Proprietà sticky a destra (300 px): righe chiave/valore modificabili (Stato, Assegnatari, Etichette, Milestone, Sviluppo | Aperta da, Partecipanti) e azioni Disiscriviti, copia link, ⋯.
  - Nuova: composer centrato 780 px con titolo 24 px senza bordo, descrizione, chip per assegnatario/etichette/milestone/PR, "Creane un'altra", Crea issue.

- **Pull request (2026-11-17).** Round `022-pull-request`, variante B "Stato nel pannello".
  - Elenco: viste come le issue (Aperte, Review richieste, Create da me, Assegnate a me, Unite, Chiuse), righe con stato review a chip (Review richiesta viola, Approvata verde, Modifiche richieste rosso, Bozza) e controlli N/M.
  - Intestazione su tutta la larghezza: percorso, titolo, pillola di stato, "X vuole unire N commit in `base` da `head`" con ref su `--sel` e copia, Modifica, Codice ▾; poi tab Conversazione / Commit / Controlli / File modificati con conteggi e sottolineatura `--acc-fill`.
  - Conversazione: post ed eventi come le issue; il pannello a destra apre con una card di stato su `--bg` (pillola "Pronta per il merge", review, controlli N/M, conflitti, elenco controlli compatto, split "Squash e unisci" verde con metodo, "Elimina il branch dopo il merge"), poi le proprietà (Revisori con stato, Assegnatari, Etichette, Milestone, Chiude, Partecipanti).
  - File modificati: layout del dettaglio commit (020) sotto intestazione e tab, barra "N file +/− · X di N visti", Unificata/Affiancata, "Rivedi modifiche ▾"; thread inline in superficie con risposta.
  - Contrasto (audit): conteggi −N a 11 px in `#ff7b72`.

- **Actions (2026-11-17).** Round `023-actions`, variante A "GitHub fedele". Sostituisce il round 014 per il monitor dei runner.
  - Run: colonna sinistra con i workflow (Tutti, CI, Release, …) e la sezione Gestione (Cache, Runner, Metriche); a destra filtri (evento, stato, branch, autore) e lista dei run in superficie: icona di stato, titolo del commit, "Workflow #n: evento di autore", ref su `--sel`, tempo e durata.
  - Run e log: intestazione con titolo, stato, Riesegui ▾ e ⋯; colonna dei job (Riepilogo, job con stato e durata, Utilizzo, File del workflow); card del job con step espandibili, lo step fallito aperto sul log (sfondo `#010409`, numeri di riga, gruppi `##[group]` in `--accent`, righe d'errore su `--err-s`), ricerca nel log e Scarica.
  - Runner: striscia di riepilogo a pillole (online, occupati, slot liberi) e tabella densa (Nome + etichette, Stato a chip, CPU / RAM / GPU con barre, Job N/M, ⋯); la riga selezionata si espande con versione, disco, load, heartbeat e job in corso. Barre: accento, giallo sopra il 70%, rosso sopra il 90%.

- **Release (2026-11-17).** Round `024-release`, variante B "Ultima in evidenza".
  - Elenco: titolo, segmentato Release / Tag con conteggi, Cerca, Nuova release. L'ultima release in un riquadro largo su `--surface` raggio 18: a sinistra su `--bg` badge Ultima, tag, titolo 30 px, autore, SHA, firma, note; a destra gli allegati con dimensione e scarica, e il download principale per la piattaforma. Sotto, le altre in tabella (Versione mono `--accent`, Titolo con badge Pre-release o Bozza, Autore, Allegati, Data, ⋯).
  - Badge: Ultima su `--ok-s` con bordo al 40%, Pre-release su `--warn-s`, Bozza su `--raise`.
  - Dettaglio: note a sinistra (titolo 30 px con badge, Modifica, ⋯, "X ha pubblicato…", markdown, reazioni); a destra pannello sticky 340 px con Tag (firma), Commit con copia, Branch, Confronta, Iscriviti, poi allegati su `--bg` e azioni Carica / Elimina.
  - Nuova: due colonne. Sinistra titolo, note con Scrivi / Anteprima e "Genera le note", allegati con area di trascinamento e barra di caricamento. Destra Tag (con "Nuovo tag, creato alla pubblicazione"), Destinazione, Tag precedente, Pre-release / Ultima release, Pubblica release, Salva bozza.

- **Profilo, organizzazione, esplora (2026-11-17).** Round `025-persone`, variante A "GitHub fedele".
  - Profilo: colonna 280 px con avatar 260 px, nome, username, bio, Modifica profilo; dati come GitHub (ruolo nell'istanza, azienda, posizione, ora locale, sito, email pubblica, link social, data di iscrizione), poi Organizzazioni in riga. A destra schede Panoramica / Repository / Stelle sottolineate in `--acc-fill`; In evidenza (2 colonne, Personalizza), grafico dei contributi a tutta larghezza con anni, Attività.
  - Organizzazione: avatar quadrato 84, nome, descrizione, membri / sito / verificata, Impostazioni e Nuovo repository; schede Panoramica / Repository / Team / Persone / Runner; repository fissati e poi elenco con filtri (Tipo, Linguaggio, Ordina), riga con Stella e attività recente in sparkline. Colonna destra 300 px: Persone (avatar in riga, Invita), Linguaggi principali con barra divisa e percentuali a lato, Team con conteggi.
  - Esplora: segmentato Repository / Organizzazioni / Utenti, ricerca e filtri, righe come sopra; colonna destra con "Più stellati questa settimana" e Organizzazioni.
  - I tuoi repository: segmentato Tutti / Personali / Organizzazioni / Stelle, Nuovo, filtri Visibilità / Linguaggio / Ordina, righe come sopra.
  - Il grafico dei contributi e la sparkline richiedono un endpoint nuovo.

- **Impostazioni (2026-11-17).** Round `026-impostazioni`, variante A "GitHub fedele". Vale per account, repository, organizzazione e admin.
  - Intestazione con avatar 48, nome e "Le tue impostazioni personali" / "Impostazioni dell'organizzazione", "Vai al profilo". Nel repository la pagina sta sotto la scheda Impostazioni.
  - Menu sticky 250 px a gruppi (titoletto 12 px `--ink3` con filetto sopra); voce attiva su `--raise` con barretta `--acc-fill` a sinistra. Sotto i 900 px diventa una fila scorrevole.
  - Contenuto max 780 px: sezioni con titolo 22 px e filetto sotto, campi in griglia a due colonne, pulsante primario in fondo a ogni sezione.
  - Visibilità come tre opzioni a scheda; la scelta ha bordo `--acc-fill` su `--sel` e la descrizione in `--ink2` (audit contrasto).
  - Zona pericolosa: titolo `#ff7b72`, righe in un riquadro con bordo rosso al 45%, pulsanti con testo rosso.
  - Persone dell'organizzazione: ricerca, filtro ruolo, Invita; righe con avatar, nome, stato 2FA, team, ruolo a menu e ⋯; inviti in attesa con Annulla.

- **Amministrazione (2026-11-17).** Round `028-admin-mix` variante a (027 B con la panoramica di 027 C). Guscio delle impostazioni (026 A) con menu Panoramica, Utenti, Inviti, Organizzazioni | Infrastruttura: Storage (con "1 avviso" in `--warn`), Runner, Job e coda | Istanza: Impostazioni, Autenticazione, Email, Audit.
  - Panoramica: intervallo 24 ore / 7 giorni; avviso su `--warn-s` con azione; tre grafici (richieste al minuto ad area, job Actions a barre verdi con i falliti in rosso, disco locale) con valore grande e assi; tabella Servizi in superficie (Server, Database, Storage, Cache calda, Runner, Email, Job in background) con icona, nome, dettaglio, stato in colonna fissa 128 px allineata a sinistra e azione in colonna 104 px; "Ricontrolla"; coda dell'audit in stile terminale a colonne (ora, evento in `--accent` o `--warn`, utente, oggetto).
  - Utenti: Invita utente; filtri a pillole con conteggi (Tutti, Admin, Sospesi, Senza 2FA, Bot) e ricerca; righe con selezione, avatar, nome e email, ruolo e stato a chip, 2FA, ultima attività, ⋯.
  - Storage: ogni policy come catena di nodi mono (primario → repliche → cache) con "Aggiungi replica" tratteggiato in `--warn` se manca; tabella destinazioni (nome e percorso, tipo, spazio con barra, stato, Prova / Svuota).
  - Le serie temporali (richieste, job, disco) richiedono una raccolta nuova lato server.

- **Accesso (2026-11-17).** Round `029-accesso`, variante A "GitHub fedele". Vale per login, registrazione, recupero password, verifica email, due passaggi, device code dei runner e consenso OAuth.
  - Pagina senza header dell'app: colonna centrata (400 px, 460 px per runner e OAuth) con logo 44 px, titolo 24 px centrato e sottotitolo; modulo in una scheda (`--surface` con interno `--bg`, campi su `--surface`); sotto un riquadro con bordo per il link secondario ("Nuovo qui? Crea un account"); in fondo Termini, Privacy, Stato e versione.
  - Login: username o email, password con "Password dimenticata?", Accedi a tutta larghezza, "oppure", passkey e provider esterni.
  - Due passaggi: sei caselle mono 46×54, Verifica, poi chiave di sicurezza e codice di recupero.
  - Runner: codice del dispositivo in caselle (4 + 4) da confrontare col terminale, dati del server (host, sistema, indirizzo, etichette), segmentato Istanza / Organizzazione / Repository, Rifiuta e Approva runner.
  - OAuth: avatar dell'app e dell'utente, permessi richiesti con icona e descrizione, Annulla e Autorizza, URL di ritorno in mono.

## Implementazione (web/src/ui)

- Kit sopra primitive Radix: Button, Input, Select, Dialog, Menu, Tabs, Table, Badge, Toast, EmptyState, più Card e Icon. Lo stile è nostro, solo da token (`web/src/ui/ui.css`).
- Shell: `ConsoleShell`, `FoglioShell`, `CorsieShell` in `web/src/ui/shell`, scelte da `AppShell` leggendo `data-layout` su `<html>`. Ogni pagina usa `Page` (titolo, azioni, contenuto, lato) e la shell attiva più il CSS `data-layout` le dispongono.
- Catalogo dei componenti alla route `/_ui`.
- Font: il kit usa lo stack `--font` / `--mono` dei token. Figtree e JetBrains Mono non sono ancora self-hosted (richiederebbe una dipendenza o un asset): senza di loro si ricade su `system-ui`.

