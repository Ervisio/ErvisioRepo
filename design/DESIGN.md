# ErvisioRepo: design system

Riferimento per la Fase UI (docs/PLAN.md §11). Il kit in `web/src/ui` parte da qui.

## Stato

- Direzione visiva: **in attesa di scelta** tra A Console, B Foglio, C Corsie (round `001-direzione-visiva`).
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

In attesa.
