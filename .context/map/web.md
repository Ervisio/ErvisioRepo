# web
Purpose: React + Vite SPA served by the core. Kit in web/src/ui, pages in web/src/pages, typed API clients in web/src/api, copy in web/src/i18n.
Entry points:
- web/src/router.tsx: TanStack Router tree. Signed-out routes sit under "bare" (no shell); everything else under "shell", which wraps AppShell and passes the shell dialogs (NewRepo, NewOrg) so the kit never imports pages.
- web/src/ui/shell/AppShell.tsx: picks the shell from the active layout (data-layout on <html>): console (default), foglio, corsie.
- web/src/ui/shell/ConsoleShell.tsx: the default shell (redesign 2026-11-17, round 015 B). header.tb-top: TopRow (SiteMenu popover = useNavItems, logo, Where = RepoSwitch / org / globalPageKey label, SearchTrigger pill .tb-find, New .tb-new, Bell .tb-dot, avatar) and Tabs (repo or org only; global pages have no second row). Repo tabs = repoTabs(): main (Code, Issues, PRs, Actions, Releases) + More + Settings. More and the site menu are `NavPopover`: native popover of links, no menu roles.
- web/src/styles/app.css: console block uses tb-* classes; .tb-main is a 920 px centred column, widened to 1160 px when it holds .page-body-aside or .st. The window scrolls (virtual code rows rely on it). .sb-kt survives only for Home.
- web/src/styles/tokens.css: console tokens are scoped to :root[data-layout="console"] (and its light variant), remapping core surfaces; use --acc-fill + --on-accent for filled accent, never raw --accent behind text.
- web/src/ui/shell/nav.tsx: single source of nav data for all three shells (global items, admin block, repo groups, org tabs, shell context). Add nav entries here, not in the shells.
- web/src/ui/shell/layout.ts + useAppearance.ts: effective layout = user preference (/user/preferences) || /meta defaultLayout || console; theme from the same preference (data-theme). localStorage only caches for first paint. Appearance section in account settings.
- web/src/ui/SettingsLayout.tsx: shared settings frame for account, repo, org and instance settings.
Flow:
- Route guard: pages are wrapped by Protected (admin flag for /admin/*). Nav hiding is cosmetic; the guard is the real check.
- Shell context comes from route params: owner+name = repo, org = org, otherwise global. repoSub() and the org ?tab= search decide the active item.
- Home (/) is a dashboard. Admin routes: /admin (AdminOverview), /admin/users (+ /$login), /admin/invites, /admin/settings/$section (auth|registration|email|git|actions|webhooks|appearance|server; /admin/settings redirects to auth), /admin/storage, /admin/runners, /admin/audit. All sit under the pathless layout route id "admin" (guard + AdminFrame = SettingsLayout sub-nav, AdminPane for each pane); code in web/src/pages/admin/, hooks in api/admin.ts, key-to-section map in settingsCatalog.ts. Edits only send changed keys; 422 field errors come from ApiError.errors.
- Org settings are one tab ("settings") with its own sub-navigation: hooks, secrets, variables, runners are reached through it. isOrgSettingsTab maps them back to "settings" for the active highlight.
- Repo settings page (RepoSettings) and RepoAccess share the settings frame.
- Repo pages (RepoFrame in pages/code/common.tsx): outside console they show their own tab bar; in console the top bar tabs come from repoTabs() in nav.tsx (Code, Issues, PRs, Actions + More) and RepoFrame shows only the repo name, a non-public badge and page actions. TreePage in console keeps History, Add file and Settings in RepoMore (⋯); Pin, Watch, Fork, Star and the Code drop-down live in pages/code/RepoHead.tsx.
- Repo settings "storage" section is instance-admin only (pin/unpin/evict via useTiering).
Invariants:
- Every UI string goes in web/src/i18n/{en,it}/<ns>.json with the same keys. `make check-i18n` (npm run check-i18n in web/) enforces it. Namespaces in use include shell, home, admin, account, org, repos, ui.
- Colors, radii and fonts come from tokens only (web/src/styles/tokens.css, copied from design/tokens.css; kit styles in web/src/ui/ui.css). No new hex values in pages.
- Section hue is set by a sec-* class (code, pr, issue, act, pkg). Status colors never use the hue.
- Console rules: DESIGN.md decision "Console a barra unica (2026-11-10)" supersedes the acrylic one; Foglio/Corsie keep the core rules.
- Icons in nav and rail need aria-label; the active item carries aria-current.
- Verification for this area: npm run build (tsc + vite), npm run lint, npm run typecheck, check-i18n. No new tests.
Gotchas:
- design/DESIGN.md section "A Console: regole" describes the first Console (round 006); the top-bar decision (2026-11-10) supersedes it and 009a. Code follows the top bar.
- Flat nav for Foglio and Corsie (useNavItems) uses adminItems.slice(1) to drop the Overview entry. Reordering adminItems changes what these layouts show.
- NavLink uses exact matching only for "/" and "/admin"; other admin and repo links match by prefix, so a new route that shares a prefix will highlight two items.
- Search params are JSON-decoded by the router: numbers arrive as numbers, so use str/num/bool helpers in router.tsx rather than raw values.
- The /_ui component catalog is reachable by URL but is not in the nav.
- Font stack falls back to system-ui; Figtree and JetBrains Mono are not self-hosted.
- The token values for warn, info and several section hues are reconstructed, not from the core. Replace them when the core tokens.css arrives; names stay stable.
Related:
- design/DESIGN.md (decisions, Console rules), design/tokens.css, design/mock/ (reference HTML).
- docs/design/ux-audit.md (UX audit behind the shell and home decisions).
- docs/PLAN.md §11 (UI phase), §10.5 (agent rules).
- web/src/api/ (identity, repos, issues, notifications, useStorage): the data the shell and home read.
- map/plugin.md: plugin SDK styling contract uses the same token names.

## Palettes (2026-11-15)
- tokens.css: `:root` = GitHub dark, `[data-theme=light]` = GitHub light; classic values live under `:root[data-palette=classic]` (oled = dark, soft = light). design/tokens.css mirrors it.
- ui/shell/layout.ts `paint()` maps the preference (system, light, dark, oled, soft) to data-theme + data-palette. Enum also in api/openapi.yaml, internal/dashboard, migration 00240.
- Kit tokens `--bd` (1px ring: `--line` in GitHub, transparent in classic), `--edge`, `--field`. The GitHub-only overrides sit at the end of ui.css under `:root:not([data-palette=classic])`; filled controls use `--acc-fill` + `--on-accent`.
- Trap: those overrides have specificity 0,3,0. Page CSS that must undo the ring needs `:root:root` (e.g. `.st-sec` card reset in ui.css).
- Home: see "Redesign phase 1" below (the `.hm-*` dashboard is gone).

## Repository code pages (2026-11-15)
- `Page` has a `quiet` prop (sr-only h1, no header); `RepoFrame` sets it in the Console layout. Visibility pill = `.tb-pill` in ConsoleShell. Repo pages use a 1060px column (`.tb-main`), settings follow it.
- TreePage: one box = latest-commit strip + fixed-column table (stretched name link makes the row clickable). Readme: box with header strip, 75ch body. BlobPage: toolbar strip with segmented view, Raw/History icon buttons, ⋯ menu (Edit, Download, Delete).
- Not shown for lack of data: commits count (strip links to History) and the last commit of a single file (blob response has none).

## Work, Actions, settings pages (2026-11-15)
- Issue/PR detail (pages/work/Conversation.tsx, common.tsx; `.wk-*` in work.css): grid `.wk-dgrid` = timeline + 300px sticky side column. Side = Status box (issue state, or merge readiness + merge button; the old timeline merge box is gone) then metadata groups with one pencil each. Under ~900px the side goes on top and metadata fold into `<details>`.
- Actions run (pages/actions/RunPage.tsx, JobLog.tsx; `.ax-*`): job list left (selection via the `job` search param), steps as expandable rows, log split per step; `##[error]` lines highlighted. Graph and gantt views removed.
- Settings frame (ui/SettingsLayout.tsx, `.st-*` in ui.css): `Page compact` title, search only with 13+ items, sections as boxes, danger zone = error border. Admin has no top-bar tabs any more (AdminFrame left menu only).
- Console widths: `.tb-main` 1060px for repository, detail and settings pages; home 1160px.
- Repository home sidebar (pages/code/RepoSidebar.tsx, Console only, root view only): About, Releases (first page of useReleases, shows 30+ when full), Contributors (useContributors -> GET /repos/{owner}/{name}/contributors, internal/gitbrowse/contributors.go), Languages (useLanguages -> GET .../languages, internal/gitbrowse/languages.go: bytes per extension from one ls-tree, built-in table, server folds <0.1% into "Other" which the UI translates). Home column 1216px.

## GitHub-style rework (2026-11-16)
- Read routes of repositories are not guarded (anonymous browsing of public repos); RepoFrame shows a sign-in prompt on 404 for anonymous callers. Write routes stay behind `guard`.
- Settings kit restyled to GitHub (round 010 A) purely in ui.css `.st-*`: open sections, first section title = page title, stacked rows, toggles left of the label via `:has`.
- Lists: `RepoRows` (client filter/type/sort) and `RepoRow` in pages/repos/RepoList.tsx, `.rl-*` in app.css; Explore uses RepoRow with server search.
- Org page: `OrgHead` + `.oh-grid` with `OrgAside` (members, teams); settings tabs render their own compact Page.
- Profile: `/$owner` -> pages/repos/ProfilePage.tsx (`useUserProfile`, GET /users/{username}), `.up-*`; avatars elsewhere are still not links.

## Repository header (2026-11-16, round 012 A)
- `RepoHead` (pages/code/RepoHead.tsx, `.rh-*` in app.css) is rendered by RepoFrame in the Console before every repo page: owner avatar, owner / name, visibility, Pin (usePinToggle), Watch (useRepoSubscription levels), Fork (ForkDialog), Star (`useStar` in api/repos.ts, optimistic on ["repos","one",owner,name]). Signed out: links to /login?next=.
- `CloneMenu` replaces CloneDialog and ArchiveMenu: kit Menu with `MenuRadioGroup`/`MenuRadioItem` (new in ui/Menu.tsx) for HTTPS/SSH/CLI, copy item, archive links. SSH only when cloneUrls() derives a host.
- No watcher or fork counts in the API: none shown.
- Profile README: `ProfileReadme` in ProfilePage.tsx reads repo user/user (public only) and its tree readme, rendered with `Readme` (new label/action props). Owner without it gets a CTA: create repo (if missing) + POST /contents README.md, then the edit route.

## Redesign phase 1 (2026-11-17, soft surfaces)
- Tokens: `--b-r` 14 (containers), `--b-rc` 10 (controls), `--b-gap`; filled state pills `--st-open/--st-merged/--st-closed` + `--on-state` (GitHub hex per DESIGN contrast notes).
- ui.css end block: in Console, Button/Input/Select/Card/Table/Tabs/Badge are ring-less soft surfaces (34 px controls on --surface; inside a card fields go --bg, buttons --raise; Tabs = segmented). The GitHub ring rule now skips Console for those.
- Reusable primitives (any area): `.ui-box` + `.ui-in` (surface box with inner --bg list), `.ui-box-h`, `.ui-li` row, `.ui-n` count, `.ui-chip` (+ -ok -err -warn -acc -rev), `.ui-seg`/`.ui-segi` (aria-pressed or aria-current), `.ui-sech` (section title + count + link), `.ui-state` (+ -merged -closed -draft), `.ui-udot`, `.ui-tm`, `.ui-st-*` icon colours.
- Home (`.db-*`, 1340 px): tiles = useUserIssues review_requested / assigned issues / created prs + useUserRuns failure; activity; repos; Instance (admin: runners, jobs, storage bytes). Omitted for lack of data: repo issue/PR counts, "For you / Following", "changes requested", oldest review time.
- Notifications (`.nt-*`, full width, 100dvh − 61px): views unread / review / assigned / all (`filter` search; review and assigned filter the loaded page by reason); preview fetches useIssue / usePull. Omitted: Saved (no API), checks list.
- Tab counts: Issues and Pull requests show `openIssuesCount` / `openPullsCount` (GET repo only; issue/PR mutations invalidate ["repos","one",owner,name]). No release count.

## Actions and releases redesign (2026-11-17, rounds 023 A and 024 B)
- Runs (pages/actions/RunsPage.tsx, `.ax-*` in styles/actions.css): `.ax-nav` workflow column (+ "Management" → repo settings runners, admin only) and runs as `.ax-rr` rows inside `.ui-box` / `.ui-in`; event/status Selects sit in the box header, branch filter above.
- Run (RunPage.tsx): `.ax-jn` job column; `?job=summary` shows RunSummary (also the view when there are no jobs). JobLog card = `.ui-box.ax-jc`, header search + timestamps apply to every step (`LogView`), steps auto-open on failure, running or search hit. Log `.ax-log` (dark inset, `##[group]` accent, `##[error]` err). `StatusIcon bare` shows `.ax-spin` while running.
- Runners (Runners.tsx, `.rn-*`): one component for admin, org and repo; `.rn-sum` pills, `.rn-t` table in `.ui-box` (role=region, tabIndex), `Meter` (value + bar, >70 warn, >90 err), `Details` expanded row (`.rn-kv`, jobs with spinner). The admin left menu shell is not part of this.
- Releases (pages/releases/ReleasesPages.tsx, `.rel-*` at the end of styles/actions.css): Releases/Tags `ui-seg` of links, `Hero` for the latest (first page only), `.rel-t` table for the rest, detail `.rel-dg` with sticky `.rel-pane`, form `.rel-ng`. `useUploader` uploads queued files one by one after create/update (native `<progress>`), `DropZone` = label + sr-only file input.
- Omitted for lack of API: run author filter and free-text run search, per-run ⋯ menu, Cache/Metrics/Usage entries, release/tag counts, release search, reactions, signed-tag info, "Generate notes", previous-tag picker, "Latest release" flag. Compare in the release panel only when the previous release is on the first page. Old `.rl-card`/`.rl-body` rules in ui/code/code.css are now unused.

## Code and history (2026-11-17, rounds 018 A / 020)
- Repo pages other than the home pass `head={false}` to `RepoFrame` (no RepoHead in Console).
- `CodeLayout` (pages/code/FileTree.tsx, `.cx-*` in code.css): tree sidebar for folder, blob and edit pages; folders via `useTreeNames` (tree without lastCommits), "Go to file" filters the loaded tree queries; closed state in localStorage `erepo.codeTree`.
- `LastCommit` strip (TreePage.tsx) heads folder and file boxes (`.ui-box` + `.ui-in`); file toolbar `.cx-code`/`.cx-tb`.
- Editor: `Editor` takes `options` (indent tab/2/4, wrap; cm.ts compartments); Save opens a dialog (`CommitFields` with description, `pull` option -> compare page); "Preview changes" = `lineDiff` (ui/code/linediff.ts, LCS) rendered by `DiffBody`.
- `DiffView` (ui/code/DiffView.tsx) is the shared diff layout for commit, compare and PR files: props `diff`, `header`, `toolbar`, `fileActions`; full-height file column (`--dv-top` 105px in Console), split by default, word highlight. Pages wrap it in `.cd-page` to go full width.
- History: `useCommitLog` (infinite) timeline `.cm-*`, `CommitRow` (also used by PRs) with ref chips from the first 100 branches/tags; branches/tags tables `.br-*`; search `.sr-*` with client facets (extension, top folder).
- Omitted (no API): per-commit check status, new/delete branch, protected-branch lock, search over commits/issues/PRs, "Most relevant" sort, PR preselection after "new branch + PR" (lands on compare).

## Issues and pull requests (2026-11-17, rounds 021 B / 022 B)
- Lists (`ListFrame`, `ListSearch`, `FilterChip`, `RowGroups`, `WorkRow` in pages/work/common.tsx; `.wk-lg/.wk-vw/.wk-ir` in styles/work.css): views are links (`.wk-vw-a`, aria-current), issue labels below them, chips are kit Menus. Issues group by milestone on the loaded page (`group=none` turns it off). The old A/B/C list layouts, split preview and j/k step navigation are gone (layout.ts deleted).
- Detail: `WorkHeader` (breadcrumb, 30 px title, rename) + `DetailLayout` (`.wk-dgrid`, sticky `.wk-props` panel 300 px, top 121 px). `CommentBox` is now a post (`.wk-post`, `compact` for review threads); `TimelineExtra` has `kind` and consecutive events render in one `.wk-evs` block. `SideBlock` = key/value row `.wk-prow`; editors are `AssigneesMenu` / `LabelsMenu` / `MilestoneMenu` (any trigger). `PropsFooter` replaces NotifyBlock (subscribe, copy link, ⋯ with lock/delete/draft). Repo watch level lives only in RepoHead now.
- PR: `PullMeta` (state pill, refs, copy branch, Code menu), `PullTabs` with counts (commits page 1, files, statuses + check runs), `?tab=checks` = PullChecks, status card `.wk-stc` with `useHeadLines` + `CheckRows`, green `.wk-merge` split. Files changed = `DiffView` with `header` / `toolbar` inside `.cd-page`; review threads stay in a box under the diff (no inline threads).
- Composer: soft box (`.wk-cps`), `before` / `extra` slots, write/preview `ui-seg`, `FormatBar` (role=toolbar, roving tabindex), textarea `field-sizing: content`.
- Omitted (no API): view counts other than Open, label counts, PR checks N/M and review verdict in list rows, "Assigned to me" for PRs, linking a PR from the new issue form, inline diff threads, viewed-file tracking.

## Sign-in pages (2026-11-17, round 029)
- pages/auth/AuthFrame.tsx is the only chrome (no header): `.auth-pg` column 400 px (`wide` = 460 px), `.auth-box` + `.auth-in` card, `.auth-nx` box for the footer link, `.auth-lf` version from useMeta. Rules in styles/auth.css (pre-login block); fields/buttons inside `.auth-in` are overridden with `:root:root` to beat the console layout. OAuth consent also uses AuthFrame (`wide`).
- pages/auth/parts.tsx: `OtpInput` (single transparent input over six decorative boxes), `CodeBoxes` (device code), `Details` (hairline rows), `Or` divider.
- Omitted for lack of data/routes: Terms/Privacy/Status footer links, runner label chips and "verified app" line.
