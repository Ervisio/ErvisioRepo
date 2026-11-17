# Profile cards

ErvisioRepo draws SVG cards with a user's statistics, like the GitHub README
cards (github-profile-summary-cards, streak-stats). Put them in a profile
README (a repository named after the user) or in any Markdown file:

```html
<p align="center">
  <img width="49%" src="https://git.example.com/api/v1/users/USERNAME/cards/stats.svg?theme=neon" alt="Stats" />
  <img width="49%" src="https://git.example.com/api/v1/users/USERNAME/cards/languages.svg?theme=neon" alt="Languages" />
</p>
<p align="center">
  <img src="https://git.example.com/api/v1/users/USERNAME/cards/streak.svg?theme=neon" alt="Streak" />
</p>
<p align="center">
  <img src="https://git.example.com/api/v1/users/USERNAME/cards/activity.svg?theme=neon" alt="Contributions" />
</p>
```

## Cards

`/api/v1/users/{username}/cards/{card}.svg`, where `{card}` is:

| Card | Shows |
|---|---|
| `stats` | stars and number of public repositories, contributions of the last year, and all-time pushes, pull requests opened and issues opened |
| `languages` | languages by bytes of code across the most recently updated public repositories |
| `streak` | total contributions, current streak and longest streak with their dates |
| `activity` | contribution calendar of the last 53 weeks |
| `hours` | contributions of the last year per hour of the day, in the time zone of the profile (UTC if none) |

## Parameters

| Parameter | Values | Default |
|---|---|---|
| `theme` | `dark`, `light`, `neon` (violet and pink) | `dark` |
| `hideBorder` | `true`, `false` | `false` |

## Notes

- The cards always show what a signed-out visitor can see: public
  repositories and the activity in them. Private and internal repositories
  never count, even when you are signed in.
- Contributions are the activity events of the user (pushes, issues and pull
  requests opened, closed or merged, releases, comments), the same as the
  calendar of the profile page. Days are UTC.
- The languages card reads only repositories that are on the fast storage
  tier; archived (cold) repositories are skipped and are not restored for it.
- Images are cached for 30 minutes.
