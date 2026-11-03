# Decisione round 002 — issue, notifiche, impostazioni repo (2026-11-03)

- **Approvata: A (righe dense)** come layout di default.
- Nota utente: "Default A con possibilità di scegliere B e C" → B (colonna filtri) e C (split view) restano selezionabili dall'utente come layout alternativi delle schermate issue/notifiche (preferenza per utente, persistita).
- Correzioni richieste:
  - B: il riquadro stato/assegnatari/etichette nel dettaglio issue (`.ib-strip`) non è simmetrico con quello sotto.
  - C: la barra di navigazione del dettaglio (`.ic-nav`, "Issue 5 di 23 · Precedente k · Successiva j") va in colonna: bug di layout.
  - A: audit contrasto < 4.5:1 su chip etichetta (`.ia-lb.c-h-*`) e avatar (`.av-*`) a 10–12px: alzare il contrasto.
