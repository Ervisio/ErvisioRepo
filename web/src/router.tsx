import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Protected } from "./auth/Protected.tsx";
import { AdminAudit } from "./pages/AdminAudit.tsx";
import { AdminStorage } from "./pages/AdminStorage.tsx";
import { Account } from "./pages/account/Account.tsx";
import { Login } from "./pages/auth/Login.tsx";
import { ForgotPassword, ResetPassword } from "./pages/auth/PasswordReset.tsx";
import { Register } from "./pages/auth/Register.tsx";
import { TwoFactor } from "./pages/auth/TwoFactor.tsx";
import { VerifyEmail } from "./pages/auth/VerifyEmail.tsx";
import { Catalog } from "./pages/Catalog.tsx";
import { Home } from "./pages/Home.tsx";
import { OrgPage } from "./pages/Org.tsx";
import { OrgInviteAccept } from "./pages/OrgInvite.tsx";
import { BlobPage, type BlobMode } from "./pages/code/BlobPage.tsx";
import { ComparePage } from "./pages/code/ComparePage.tsx";
import { EditPage } from "./pages/code/EditPage.tsx";
import { BranchesPage, CommitPage, CommitsPage, TagsPage } from "./pages/code/HistoryPages.tsx";
import { SearchPage, type CodeSearchParams } from "./pages/code/SearchPage.tsx";
import { TreePage } from "./pages/code/TreePage.tsx";
import { ReleaseFormPage, ReleasePage, ReleasesPage } from "./pages/releases/ReleasesPages.tsx";
import { MyReposPage } from "./pages/repos/MyReposPage.tsx";
import { RepoSettings, type SettingsSection } from "./pages/repos/RepoSettings.tsx";
import { IssuePage, IssuesPage, NewIssuePage, type IssueSearch } from "./pages/issues/IssuesPages.tsx";
import { NotificationsPage, type InboxSearch } from "./pages/notifications/NotificationsPage.tsx";
import { PullPage, PullsPage, type PullSearch, type PullTab } from "./pages/pulls/PullsPages.tsx";
import { AppShell, ToastProvider } from "./ui/index.ts";

// The default search parser JSON-decodes values, so "1" arrives as a number: accept both forms.
const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : undefined);
const num = (v: unknown) => {
  const n = typeof v === "number" ? v : typeof v === "string" ? Number(v) : NaN;
  return Number.isInteger(n) && n > 1 ? n : undefined;
};
const bool = (v: unknown) => (v === true || v === "true" ? true : v === false || v === "false" ? false : undefined);
const nextSearch = (s: Record<string, unknown>): { next?: string } => ({ next: str(s.next) });
const tokenSearch = (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) });

const rootRoute = createRootRoute({ component: Outlet });

// Signed-out pages: no navigation shell.
const bareRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "bare",
  component: () => (
    <ToastProvider>
      <Outlet />
    </ToastProvider>
  ),
});
const oidcSearch = (s: Record<string, unknown>): { oidc_error?: string } => ({ oidc_error: str(s.oidc_error) });
const loginRoute = createRoute({
  getParentRoute: () => bareRoute,
  path: "/login",
  validateSearch: (s: Record<string, unknown>) => ({ ...nextSearch(s), ...oidcSearch(s) }),
  component: function LoginPage() {
    const s = loginRoute.useSearch();
    return <Login next={s.next} oidcError={s.oidc_error} />;
  },
});
const twoFactorRoute = createRoute({ getParentRoute: () => bareRoute, path: "/login/2fa", validateSearch: nextSearch, component: () => <TwoFactor next={twoFactorRoute.useSearch().next} /> });
const registerRoute = createRoute({ getParentRoute: () => bareRoute, path: "/register", validateSearch: (s: Record<string, unknown>): { invite?: string } => ({ invite: str(s.invite) }), component: () => <Register invite={registerRoute.useSearch().invite} /> });
const verifyRoute = createRoute({ getParentRoute: () => bareRoute, path: "/verify-email", validateSearch: tokenSearch, component: () => <VerifyEmail token={verifyRoute.useSearch().token} /> });
const forgotRoute = createRoute({ getParentRoute: () => bareRoute, path: "/forgot-password", component: ForgotPassword });
const resetRoute = createRoute({ getParentRoute: () => bareRoute, path: "/reset-password", validateSearch: tokenSearch, component: () => <ResetPassword token={resetRoute.useSearch().token} /> });

// Pages inside the navigation shell.
const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "shell",
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});
const guard = (node: ReactNode, admin = false) => <Protected admin={admin}>{node}</Protected>;
const homeRoute = createRoute({ getParentRoute: () => shellRoute, path: "/", component: Home });
const storageRoute = createRoute({ getParentRoute: () => shellRoute, path: "/admin/storage", component: () => guard(<AdminStorage />, true) });
const auditRoute = createRoute({ getParentRoute: () => shellRoute, path: "/admin/audit", component: () => guard(<AdminAudit />, true) });
const settingsRoute = createRoute({ getParentRoute: () => shellRoute, path: "/settings", validateSearch: oidcSearch, component: () => guard(<Account oidcError={settingsRoute.useSearch().oidc_error} />) });
const orgInviteRoute = createRoute({ getParentRoute: () => shellRoute, path: "/org-invite", validateSearch: tokenSearch, component: () => guard(<OrgInviteAccept token={orgInviteRoute.useSearch().token} />) });
const orgRoute = createRoute({ getParentRoute: () => shellRoute, path: "/orgs/$org", component: () => guard(<OrgPage name={orgRoute.useParams().org} />) });
const reposRoute = createRoute({ getParentRoute: () => shellRoute, path: "/repos", component: () => guard(<MyReposPage />) });
type RefPath = { ref?: string; path?: string };
const refPathSearch = (s: Record<string, unknown>): RefPath => ({ ref: str(s.ref), path: str(s.path) });
const pageSearch = (s: Record<string, unknown>): { page?: number } => ({ page: num(s.page) });
const blobModes: BlobMode[] = ["code", "blame", "preview"];
const repoRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name",
  validateSearch: refPathSearch,
  component: function RepoHome() {
    const p = repoRoute.useParams();
    const s = repoRoute.useSearch();
    return guard(<TreePage owner={p.owner} name={p.name} refName={s.ref} path={s.path} />);
  },
});
const blobRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/blob",
  validateSearch: (s: Record<string, unknown>): RefPath & { view?: BlobMode } => ({
    ...refPathSearch(s),
    view: blobModes.find((m) => m === s.view),
  }),
  component: function Blob() {
    const p = blobRoute.useParams();
    const s = blobRoute.useSearch();
    return guard(<BlobPage owner={p.owner} name={p.name} refName={s.ref} path={s.path ?? ""} view={s.view} />);
  },
});
const commitsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/commits",
  validateSearch: (s: Record<string, unknown>) => ({ ...refPathSearch(s), ...pageSearch(s) }),
  component: function Commits() {
    const p = commitsRoute.useParams();
    const s = commitsRoute.useSearch();
    return guard(<CommitsPage owner={p.owner} name={p.name} refName={s.ref} path={s.path} page={s.page} />);
  },
});
const commitRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/commit/$sha",
  component: function Commit() {
    const p = commitRoute.useParams();
    return guard(<CommitPage owner={p.owner} name={p.name} sha={p.sha} />);
  },
});
const branchesRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/branches",
  validateSearch: pageSearch,
  component: function Branches() {
    const p = branchesRoute.useParams();
    return guard(<BranchesPage owner={p.owner} name={p.name} page={branchesRoute.useSearch().page} />);
  },
});
const tagsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/tags",
  validateSearch: pageSearch,
  component: function Tags() {
    const p = tagsRoute.useParams();
    return guard(<TagsPage owner={p.owner} name={p.name} page={tagsRoute.useSearch().page} />);
  },
});
const compareRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/compare",
  validateSearch: (s: Record<string, unknown>): { base?: string; head?: string } => ({ base: str(s.base), head: str(s.head) }),
  component: function Compare() {
    const p = compareRoute.useParams();
    const s = compareRoute.useSearch();
    return guard(<ComparePage owner={p.owner} name={p.name} base={s.base} head={s.head} />);
  },
});
const codeSearchRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/search",
  validateSearch: (s: Record<string, unknown>): CodeSearchParams => ({
    q: str(s.q),
    ref: str(s.ref),
    path: str(s.path),
    regexp: bool(s.regexp),
    ignoreCase: bool(s.ignoreCase),
  }),
  component: function CodeSearch() {
    const p = codeSearchRoute.useParams();
    return guard(<SearchPage owner={p.owner} name={p.name} params={codeSearchRoute.useSearch()} />);
  },
});
const editRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/edit",
  validateSearch: refPathSearch,
  component: function Edit() {
    const p = editRoute.useParams();
    const s = editRoute.useSearch();
    return guard(<EditPage owner={p.owner} name={p.name} refName={s.ref} path={s.path} mode="edit" />);
  },
});
const newFileRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/new",
  validateSearch: refPathSearch,
  component: function NewFile() {
    const p = newFileRoute.useParams();
    const s = newFileRoute.useSearch();
    return guard(<EditPage owner={p.owner} name={p.name} refName={s.ref} path={s.path} mode="new" />);
  },
});
const releasesRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/releases",
  validateSearch: pageSearch,
  component: function Releases() {
    const p = releasesRoute.useParams();
    return guard(<ReleasesPage owner={p.owner} name={p.name} page={releasesRoute.useSearch().page} />);
  },
});
const releaseNewRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/releases/new",
  component: function ReleaseNew() {
    const p = releaseNewRoute.useParams();
    return guard(<ReleaseFormPage owner={p.owner} name={p.name} />);
  },
});
const releaseRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/releases/$id",
  component: function ReleaseOne() {
    const p = releaseRoute.useParams();
    return guard(<ReleasePage owner={p.owner} name={p.name} id={p.id} />);
  },
});
const releaseEditRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/releases/$id/edit",
  component: function ReleaseEdit() {
    const p = releaseEditRoute.useParams();
    return guard(<ReleaseFormPage owner={p.owner} name={p.name} id={p.id} />);
  },
});
const issueStates = ["closed", "all"] as const;
const issuesRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/issues",
  validateSearch: (s: Record<string, unknown>): IssueSearch => ({
    state: issueStates.find((x) => x === s.state),
    q: str(s.q),
    labels: str(s.labels),
    milestone: str(s.milestone),
    assignee: str(s.assignee),
    author: str(s.author),
    mentioned: str(s.mentioned),
    sort: s.sort === "updated" || s.sort === "comments" ? s.sort : undefined,
    page: num(s.page),
  }),
  component: function Issues() {
    const p = issuesRoute.useParams();
    return guard(<IssuesPage owner={p.owner} name={p.name} search={issuesRoute.useSearch()} />);
  },
});
const issueNewRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/issues/new",
  component: function IssueNew() {
    const p = issueNewRoute.useParams();
    return guard(<NewIssuePage owner={p.owner} name={p.name} />);
  },
});
const issueRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/issues/$number",
  component: function IssueOne() {
    const p = issueRoute.useParams();
    return guard(<IssuePage owner={p.owner} name={p.name} number={Number(p.number)} />);
  },
});
const pullStates = ["closed", "merged", "all"] as const;
const pullsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/pulls",
  validateSearch: (s: Record<string, unknown>): PullSearch => ({
    state: pullStates.find((x) => x === s.state),
    q: str(s.q),
    author: str(s.author),
    reviewRequested: str(s.reviewRequested),
    base: str(s.base),
    sort: s.sort === "updated" ? "updated" : undefined,
    compare: bool(s.compare),
    page: num(s.page),
  }),
  component: function Pulls() {
    const p = pullsRoute.useParams();
    return guard(<PullsPage owner={p.owner} name={p.name} search={pullsRoute.useSearch()} />);
  },
});
const pullTabs = ["commits", "files"] as const;
const pullRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/pulls/$number",
  validateSearch: (s: Record<string, unknown>): { tab?: PullTab; page?: number } => ({ tab: pullTabs.find((x) => x === s.tab), page: num(s.page) }),
  component: function PullOne() {
    const p = pullRoute.useParams();
    const s = pullRoute.useSearch();
    return guard(<PullPage owner={p.owner} name={p.name} number={Number(p.number)} tab={s.tab} page={s.page} />);
  },
});
const inboxFilters = ["participating", "all"] as const;
const notificationsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/notifications",
  validateSearch: (s: Record<string, unknown>): InboxSearch => ({ filter: inboxFilters.find((x) => x === s.filter), page: num(s.page), repo: str(s.repo) }),
  component: () => guard(<NotificationsPage search={notificationsRoute.useSearch()} />),
});
const settingsSections = ["protection", "tags", "hooks", "github"] as const;
const repoSettingsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/$owner/$name/settings",
  validateSearch: (s: Record<string, unknown>): { section?: SettingsSection } => ({ section: settingsSections.find((x) => x === s.section) }),
  component: function Settings() {
    const p = repoSettingsRoute.useParams();
    return guard(<RepoSettings owner={p.owner} name={p.name} section={repoSettingsRoute.useSearch().section} />);
  },
});
// Component catalog for the design kit (Fase UI).
const catalogRoute = createRoute({ getParentRoute: () => shellRoute, path: "/_ui", component: Catalog });

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    bareRoute.addChildren([loginRoute, twoFactorRoute, registerRoute, verifyRoute, forgotRoute, resetRoute]),
    shellRoute.addChildren([homeRoute, storageRoute, auditRoute, settingsRoute, orgInviteRoute, orgRoute, reposRoute,
      repoRoute,
      issuesRoute,
      issueNewRoute,
      issueRoute,
      pullsRoute,
      pullRoute,
      notificationsRoute,
      blobRoute,
      commitsRoute,
      commitRoute,
      branchesRoute,
      tagsRoute,
      compareRoute,
      codeSearchRoute,
      editRoute,
      newFileRoute,
      releasesRoute,
      releaseNewRoute,
      releaseRoute,
      releaseEditRoute,
      repoSettingsRoute,
      catalogRoute,
    ]),
  ]),
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
