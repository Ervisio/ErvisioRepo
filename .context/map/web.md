# web
Purpose: React + Vite SPA served by the core. Kit in web/src/ui, pages in web/src/pages, typed API clients in web/src/api, copy in web/src/i18n.
Entry points:
- web/src/router.tsx: TanStack Router tree. Signed-out routes sit under "bare" (no shell); everything else under "shell", which wraps AppShell and passes the shell dialogs (NewRepo, NewOrg) so the kit never imports pages.
- web/src/ui/shell/AppShell.tsx: picks the shell from the active layout (data-layout on <html>): console (default), foglio, corsie.
- web/src/ui/shell/ConsoleShell.tsx: the default shell (round 009a acrylic). Rail of squircles (useRailArea picks the lit one), 248 px channel column (GlobalNav/RepoNav/OrgNav/AdminNav + account card), glass column with top bar; below 720 px bottom bar + drawer. CSS in styles/app.css (.sb-*), acrylic tokens scoped to :root[data-layout="console"] in styles/tokens.css (remap --bg/--surface/--sunk/--raise).
- web/src/ui/shell/nav.tsx: single source of nav data for all three shells (global items, admin block, repo groups, org tabs, shell context). Add nav entries here, not in the shells.
- web/src/ui/shell/layout.ts + useAppearance.ts: effective layout = user preference (/user/preferences) || /meta defaultLayout || console; theme from the same preference (data-theme). localStorage only caches for first paint. Appearance section in account settings.
- web/src/ui/SettingsLayout.tsx: shared settings frame for account, repo, org and instance settings.
Flow:
- Route guard: pages are wrapped by Protected (admin flag for /admin/*). Nav hiding is cosmetic; the guard is the real check.
- Shell context comes from route params: owner+name = repo, org = org, otherwise global. repoSub() and the org ?tab= search decide the active item.
- Home (/) is a dashboard. Admin routes: /admin (AdminOverview), /admin/users (+ /$login), /admin/invites, /admin/settings/$section (auth|registration|email|git|actions|webhooks|appearance|server; /admin/settings redirects to auth), /admin/storage, /admin/runners, /admin/audit. All sit under the pathless layout route id "admin" (guard + AdminFrame = SettingsLayout sub-nav, AdminPane for each pane); code in web/src/pages/admin/, hooks in api/admin.ts, key-to-section map in settingsCatalog.ts. Edits only send changed keys; 422 field errors come from ApiError.errors.
- Org settings are one tab ("settings") with its own sub-navigation: hooks, secrets, variables, runners are reached through it. isOrgSettingsTab maps them back to "settings" for the active highlight.
- Repo settings page (RepoSettings) and RepoAccess share the settings frame.
- Repo pages (RepoFrame in pages/code/common.tsx) show their own tab bar only outside the console layout; in console the sidebar Repository group (repoGroups) lists Code, Issues, PRs, Commits, Branches, Tags, Releases, Search. Keep the two lists in step.
- Repo settings "storage" section is instance-admin only (pin/unpin/evict via useTiering).
Invariants:
- Every UI string goes in web/src/i18n/{en,it}/<ns>.json with the same keys. `make check-i18n` (npm run check-i18n in web/) enforces it. Namespaces in use include shell, home, admin, account, org, repos, ui.
- Colors, radii and fonts come from tokens only (web/src/styles/tokens.css, copied from design/tokens.css; kit styles in web/src/ui/ui.css). No new hex values in pages.
- Section hue is set by a sec-* class (code, pr, issue, act, pkg). Status colors never use the hue.
- Console rules: DESIGN.md decision "Console acrylic (2026-11-06)" overrides "no glass/gradients" and weight 800 for the Console only; Foglio/Corsie keep the core rules.
- Icons in nav and rail need aria-label; the active item carries aria-current.
- Verification for this area: npm run build (tsc + vite), npm run lint, npm run typecheck, check-i18n. No new tests.
Gotchas:
- design/DESIGN.md section "A Console: regole" describes the first Console; rounds 006 and 009a superseded it. Code follows 009a.
- Flat nav for Foglio and Corsie (useNavItems) uses adminItems.slice(1) to drop the Overview entry. Reordering adminItems changes what these layouts show.
- NavLink uses exact matching only for "/" and "/admin"; other admin and repo links match by prefix, so a new route that shares a prefix will highlight two items.
- Search params are JSON-decoded by the router: numbers arrive as numbers, so use str/num/bool helpers in router.tsx rather than raw values.
- The /_ui component catalog is reachable by URL but is not in the nav.
- Font stack falls back to system-ui; Figtree and JetBrains Mono are not self-hosted.
- The token values for warn, info and several section hues are reconstructed, not from the core. Replace them when the core tokens.css arrives; names stay stable.
- Mobile drawer state is keyed by pathname, so navigating anywhere closes it.
Related:
- design/DESIGN.md (decisions, Console rules), design/tokens.css, design/mock/ (reference HTML).
- docs/design/ux-audit.md (UX audit behind the shell and home decisions).
- docs/PLAN.md §11 (UI phase), §10.5 (agent rules).
- web/src/api/ (identity, repos, issues, notifications, useStorage): the data the shell and home read.
- map/plugin.md: plugin SDK styling contract uses the same token names.
