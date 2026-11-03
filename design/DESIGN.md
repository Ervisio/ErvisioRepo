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

## Implementazione (web/src/ui)

- Kit sopra primitive Radix: Button, Input, Select, Dialog, Menu, Tabs, Table, Badge, Toast, EmptyState, più Card e Icon. Lo stile è nostro, solo da token (`web/src/ui/ui.css`).
- Shell: `ConsoleShell`, `FoglioShell`, `CorsieShell` in `web/src/ui/shell`, scelte da `AppShell` leggendo `data-layout` su `<html>`. Ogni pagina usa `Page` (titolo, azioni, contenuto, lato) e la shell attiva più il CSS `data-layout` le dispongono.
- Catalogo dei componenti alla route `/_ui`.
- Font: il kit usa lo stack `--font` / `--mono` dei token. Figtree e JetBrains Mono non sono ancora self-hosted (richiederebbe una dipendenza o un asset): senza di loro si ricade su `system-ui`.

