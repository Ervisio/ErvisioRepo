# Card del profilo

ErvisioRepo disegna card SVG con le statistiche di un utente, come le card dei
README di GitHub (github-profile-summary-cards, streak-stats). Mettile nel
README del profilo (un repository con lo stesso nome dell'utente) o in
qualsiasi file Markdown:

```html
<p align="center">
  <img width="49%" src="https://git.example.com/api/v1/users/USERNAME/cards/stats.svg?theme=neon" alt="Statistiche" />
  <img width="49%" src="https://git.example.com/api/v1/users/USERNAME/cards/languages.svg?theme=neon" alt="Linguaggi" />
</p>
<p align="center">
  <img src="https://git.example.com/api/v1/users/USERNAME/cards/streak.svg?theme=neon" alt="Serie" />
</p>
<p align="center">
  <img src="https://git.example.com/api/v1/users/USERNAME/cards/activity.svg?theme=neon" alt="Contributi" />
</p>
```

## Card

`/api/v1/users/{username}/cards/{card}.svg`, dove `{card}` è:

| Card | Mostra |
|---|---|
| `stats` | stelle e numero di repository pubblici, contributi dell'ultimo anno e, da sempre, push, pull request aperte e issue aperte |
| `languages` | linguaggi per byte di codice nei repository pubblici aggiornati più di recente |
| `streak` | contributi totali, serie in corso e serie più lunga con le date |
| `activity` | calendario dei contributi delle ultime 53 settimane |
| `hours` | contributi dell'ultimo anno per ora del giorno, nel fuso orario del profilo (UTC se manca) |

## Parametri

| Parametro | Valori | Predefinito |
|---|---|---|
| `theme` | `dark`, `light`, `neon` (viola e rosa) | `dark` |
| `hideBorder` | `true`, `false` | `false` |

## Note

- Le card mostrano sempre quello che vede un visitatore non collegato:
  repository pubblici e l'attività al loro interno. I repository privati e
  interni non contano mai, nemmeno quando hai fatto l'accesso.
- I contributi sono gli eventi di attività dell'utente (push, issue e pull
  request aperte, chiuse o unite, release, commenti), gli stessi del
  calendario della pagina profilo. I giorni sono in UTC.
- La card dei linguaggi legge solo i repository sul livello di storage
  veloce; quelli archiviati (freddi) vengono saltati e non vengono
  ripristinati per lei.
- Le immagini restano in cache per 30 minuti.
