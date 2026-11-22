import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useId, useRef, useState, type ComponentType, type MouseEvent, type ReactNode } from "react";
import { request } from "../../api/http.ts";
import { useOrg, useSession, type User } from "../../api/identity.ts";
import { useUnreadCount } from "../../api/notifications.ts";
import { useOwnerRepos, useRepo, type Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { BrandMark } from "../BrandMark.tsx";
import { Icon } from "../Icon.tsx";
import { Menu, MenuItem, MenuLabel, MenuSeparator } from "../Menu.tsx";
import {
  globalPageKey, isOrgSettingsTab, NavLink, orgTabs, repoSub, repoTabs, useNavItems, usePathname, useShellContext, type RepoNavItem,
} from "./nav.tsx";
import { SearchTrigger } from "./SearchPalette.tsx";

type DialogComponent = ComponentType<{ open: boolean; onOpenChange: (open: boolean) => void }>;
/** Create dialogs for the New menu, injected by the app so the kit does not import pages. */
export type ShellDialogs = { NewRepo: DialogComponent; NewOrg: DialogComponent };
type Create = (what: "repo" | "org") => void;

const byUpdated = (a: Repo, b: Repo) => b.updatedAt.localeCompare(a.updatedAt);
const initials = (s: string) => s.slice(0, 2).toUpperCase();

/**
 * Console shell (redesign 2026-11-17, round 015 B "soft surfaces"): a sticky two-row header. Row one holds
 * the site menu, the logo, where you are (owner / repository with a switcher, the organization or the page),
 * the search pill, New, notifications and the account. Row two holds the tabs of the repository or the
 * organization, with icons; global pages have no second row (their links are in the site menu). Narrow
 * screens scroll the tab row sideways.
 */
export function ConsoleShell({ children, dialogs }: { children: ReactNode; dialogs?: ShellDialogs }) {
  const [creating, setCreating] = useState<"repo" | "org" | null>(null);
  const onCreate = dialogs ? setCreating : undefined;
  return (
    <div className="tb">
      <header className="tb-top">
        <TopRow onCreate={onCreate} />
        <Tabs />
      </header>
      <main className="tb-main">{children}</main>
      {dialogs && (
        <>
          <dialogs.NewRepo open={creating === "repo"} onOpenChange={(o) => setCreating(o ? "repo" : null)} />
          <dialogs.NewOrg open={creating === "org"} onOpenChange={(o) => setCreating(o ? "org" : null)} />
        </>
      )}
    </div>
  );
}

/**
 * Button plus a native popover of links: site navigation, not an application menu, so no menu roles and the
 * links stay in the Tab order. The popover is placed under its button when it opens, inside the viewport.
 */
function NavPopover({ label, button, buttonClass, current, children }: {
  label: string;
  button: ReactNode;
  buttonClass: string;
  current?: boolean;
  children: ReactNode;
}) {
  const id = useId();
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const place = (e: { newState: string }) => {
    const b = btn.current;
    const p = pop.current;
    if (e.newState !== "open" || !b || !p) return;
    const r = b.getBoundingClientRect();
    p.style.top = `${r.bottom + 6}px`;
    p.style.left = `${Math.max(8, Math.min(r.left, window.innerWidth - p.offsetWidth - 8))}px`;
  };
  // A followed link keeps the SPA on screen: close the popover so it does not stay over the new page.
  const closeOnLink = (e: MouseEvent<HTMLDivElement>) => {
    if (e.target instanceof Element && e.target.closest("a")) pop.current?.hidePopover();
  };
  return (
    <>
      <button ref={btn} type="button" className={buttonClass} popoverTarget={id} aria-controls={id} aria-label={label} title={label} aria-current={current ? "page" : undefined}>
        {button}
      </button>
      <div ref={pop} id={id} popover="auto" className="tb-popnav" onToggle={place} onClick={closeOnLink}>
        {children}
      </div>
    </>
  );
}

/* ---------- First row ---------- */

function TopRow({ onCreate }: { onCreate?: Create }) {
  const t = useT("shell");
  const user = useSession().data;
  return (
    <div className="tb-row">
      <SiteMenu />
      <Link to="/" className="tb-logo" title={t("brand.home")} aria-label={t("brand.home")}>
        <BrandMark />
      </Link>
      <Where />
      <SearchTrigger className="tb-find" pill />
      <div className="tb-end">
        <SearchTrigger className="tb-ib tb-mob" iconOnly />
        {user ? (
          <>
            {onCreate && (
              <NewMenu onCreate={onCreate} trigger={<button type="button" className="tb-new" aria-label={t("new.menu")} title={t("new.menu")}><Icon name="plus" /><span>{t("new.button")}</span></button>} />
            )}
            <Bell />
            <UserMenu user={user} trigger={<button type="button" className="tb-me" title={t("user.menu")} aria-label={t("user.menu")}><Avatar user={user} size="md" /></button>} />
          </>
        ) : (
          <Link to="/login" className="tb-signin">{t("nav.signIn")}</Link>
        )}
      </div>
    </div>
  );
}

/** Site menu: the global pages (home, repositories, explore, organizations, inbox, admin, account). */
function SiteMenu() {
  const t = useT("shell");
  const items = useNavItems();
  return (
    <NavPopover label={t("nav.site")} buttonClass="tb-ib tb-menu" button={<Icon name="menu" />}>
      <nav aria-label={t("nav.site")}>
        <ul className="tb-popnav-l">
          {items.map((it) => (
            <li key={it.to}>
              <NavLink item={it} label={t(it.labelKey)} className="tb-popnav-a">
                <Icon name={it.icon} />
                {t(it.labelKey)}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </NavPopover>
  );
}

/** Where you are: owner / repository (with a switcher), the organization, a profile or the global page. */
function Where() {
  const t = useT("shell");
  const ctx = useShellContext();
  const pathname = usePathname();
  const owner = (useParams({ strict: false }) as { owner?: string }).owner;
  if (ctx.kind === "repo") return <RepoSwitch owner={ctx.owner} name={ctx.name} />;
  if (ctx.kind === "org") {
    return (
      <Link to="/orgs/$org" params={{ org: ctx.org }} className="tb-where">
        <span className="tb-owner">{t("nav.orgs")} /</span>
        <b>{ctx.org}</b>
      </Link>
    );
  }
  const key = globalPageKey(pathname);
  return <span className="tb-where"><b>{owner ?? (key ? t(key) : t("brand.name"))}</b></span>;
}

function RepoSwitch({ owner, name }: { owner: string; name: string }) {
  const tr = useT("repos");
  const repo = useRepo(owner, name).data;
  return (
    <>
      <RepoMenu owner={owner} name={name} kind={repo?.ownerKind ?? "user"} />
      {repo && <span className="tb-pill">{tr(`visibility.${repo.visibility}`)}</span>}
    </>
  );
}

function RepoMenu({ owner, name, kind }: { owner: string; name: string; kind: "user" | "org" }) {
  const t = useT("shell");
  return (
    <Menu
      label={t("switch.repo")}
      trigger={
        <button type="button" className="tb-where" aria-label={t("switch.repoLabel", { repo: `${owner}/${name}` })}>
          <span className="tb-owner">{owner} /</span>
          <b>{name}</b>
          <Icon name="down" />
        </button>
      }
    >
      <OwnerRepos owner={owner} kind={kind} />
    </Menu>
  );
}

/** Repositories of the current owner: fetched only while the menu is open. */
function OwnerRepos({ owner, kind }: { owner: string; kind: "user" | "org" }) {
  const t = useT("shell");
  const repos = useOwnerRepos(owner, kind);
  const rows = [...(repos.data ?? [])].sort(byUpdated).slice(0, 8);
  return (
    <>
      <MenuLabel>{repos.isPending ? t("switch.loading") : rows.length ? t("switch.recent") : t("switch.empty")}</MenuLabel>
      {rows.map((r) => (
        <MenuItem key={r.fullName} asChild>
          <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }}>
            <Icon name={r.visibility === "public" ? "book" : "lock"} />
            {r.name}
          </Link>
        </MenuItem>
      ))}
      <MenuSeparator />
      <MenuItem asChild>
        {kind === "org" ? (
          <Link to="/orgs/$org" params={{ org: owner }}><Icon name="right" />{t("switch.all")}</Link>
        ) : (
          <Link to="/repos"><Icon name="right" />{t("switch.all")}</Link>
        )}
      </MenuItem>
    </>
  );
}

function Bell() {
  const t = useT("shell");
  const unread = useUnreadCount().data?.unread ?? 0;
  return (
    <Link
      to="/notifications"
      className={unread > 0 ? "tb-ib tb-dot" : "tb-ib"}
      title={t("nav.notifications")}
      aria-label={unread ? t("bell.unread", { count: unread }) : t("nav.notifications")}
      activeProps={{ "aria-current": "page" }}
    >
      <Icon name="bell" />
    </Link>
  );
}

function NewMenu({ onCreate, trigger }: { onCreate: Create; trigger: ReactNode }) {
  const t = useT("shell");
  const ctx = useShellContext();
  return (
    <Menu align="end" label={t("new.menu")} trigger={trigger}>
      <MenuItem icon="book" onSelect={() => onCreate("repo")}>{t("new.repo")}</MenuItem>
      <MenuItem icon="users" onSelect={() => onCreate("org")}>{t("new.org")}</MenuItem>
      {ctx.kind === "repo" && (
        <MenuItem asChild>
          <Link to="/$owner/$name/issues/new" params={{ owner: ctx.owner, name: ctx.name }}><Icon name="issue" />{t("new.issue")}</Link>
        </MenuItem>
      )}
    </Menu>
  );
}

/* ---------- Second row: tabs of the repository or organization ---------- */

function Tabs() {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  const admin = user?.role === "admin";
  if (ctx.kind === "global") return null;
  return (
    <nav className="tb-tabs" aria-label={t("nav.main")}>
      {ctx.kind === "repo" ? <RepoTabs owner={ctx.owner} name={ctx.name} admin={admin} /> : <OrgTabs org={ctx.org} admin={admin} />}
    </nav>
  );
}

function RepoTabs({ owner, name, admin }: { owner: string; name: string; admin: boolean }) {
  const t = useT("shell");
  const repo = useRepo(owner, name).data;
  const sub = repoSub(usePathname());
  const { main, more, settings } = repoTabs(admin || repo?.permission === "admin");
  const current = more.find((it) => it.sub === sub);
  // Open counts come only from GET /repos/{owner}/{name}; zero is not shown.
  const counts: Record<string, number | undefined> = { issues: repo?.openIssuesCount, pulls: repo?.openPullsCount };
  const tab = (it: RepoNavItem) => (
    // The router marks a Link active by path prefix, which would keep Code lit everywhere: match exactly and let `sub` decide.
    <Link key={it.to} to={it.to} params={{ owner, name }} activeOptions={{ exact: true }} className="tb-tab" aria-current={sub === it.sub ? "page" : undefined}>
      <Icon name={it.icon} />
      {t(it.labelKey)}
      {!!counts[it.sub] && <span className="ui-n">{counts[it.sub]}</span>}
    </Link>
  );
  return (
    <>
      {main.map(tab)}
      <NavPopover
        label={t("nav.more")}
        buttonClass="tb-tab"
        current={!!current}
        button={
          <>
            <Icon name={current?.icon ?? "dots"} />
            {current ? t(current.labelKey) : t("nav.more")}
            <Icon name="down" />
          </>
        }
      >
        <ul className="tb-popnav-l">
          {more.map((it) => (
            <li key={it.to}>
              <Link to={it.to} params={{ owner, name }} activeOptions={{ exact: true }} className="tb-popnav-a" aria-current={sub === it.sub ? "page" : undefined}>
                <Icon name={it.icon} />
                {t(it.labelKey)}
              </Link>
            </li>
          ))}
        </ul>
      </NavPopover>
      {settings && tab(settings)}
    </>
  );
}

function OrgTabs({ org, admin }: { org: string; admin: boolean }) {
  const tOrg = useT("org");
  const o = useOrg(org).data;
  const search = useSearch({ strict: false }) as { tab?: string };
  const isOwner = o?.role === "owner" || admin;
  const canManage = isOwner || o?.role === "admin";
  const active = search.tab === undefined ? "repos" : isOrgSettingsTab(search.tab) ? "settings" : search.tab;
  return (
    <>
      {orgTabs(canManage, isOwner).map((it) => (
        <Link key={it.tab} to="/orgs/$org" params={{ org }} search={{ tab: it.tab }} className="tb-tab" aria-current={active === it.tab ? "page" : undefined}>
          <Icon name={it.icon} />
          {tOrg(`tab.${it.tab}`)}
        </Link>
      ))}
    </>
  );
}

/* ---------- Account ---------- */

export function Avatar({ user, size }: { user: Pick<User, "username" | "avatarUrl">; size?: "md" | "lg" | "xl" }) {
  const cls = size ? `tb-av ${size}` : "tb-av";
  // A URL that does not load (gone, not an image, blocked) falls back to the initials.
  const [broken, setBroken] = useState("");
  if (user.avatarUrl && broken !== user.avatarUrl) return <img className={cls} src={user.avatarUrl} alt="" onError={() => setBroken(user.avatarUrl ?? "")} />;
  return <span className={cls} aria-hidden="true">{initials(user.username)}</span>;
}

function UserMenu({ user, trigger }: { user: User; trigger: ReactNode }) {
  const t = useT("shell");
  const qc = useQueryClient();
  const navigate = useNavigate();
  async function signOut() {
    try {
      await request("POST", "/auth/logout");
    } finally {
      qc.clear();
      await navigate({ to: "/login" });
    }
  }
  return (
    <Menu align="end" className="tb-pop" label={t("user.menu")} trigger={trigger}>
      <div className="tb-ph">
        <Avatar user={user} size="xl" />
        <span className="tb-ph-t">
          <b>{user.displayName || user.username}</b>
          <small>@{user.username}</small>
        </span>
      </div>
      <MenuItem asChild>
        <Link to="/$owner" params={{ owner: user.username }}><Icon name="user" />{t("user.profile")}</Link>
      </MenuItem>
      <MenuItem asChild>
        <Link to="/repos"><Icon name="book" />{t("user.repos")}</Link>
      </MenuItem>
      <MenuItem asChild>
        <Link to="/orgs"><Icon name="users" />{t("user.orgs")}</Link>
      </MenuItem>
      <MenuItem asChild>
        <Link to="/settings"><Icon name="user" />{t("user.settings")}</Link>
      </MenuItem>
      {user.role === "admin" && (
        <MenuItem asChild>
          <Link to="/admin"><Icon name="shield" />{t("group.admin")}</Link>
        </MenuItem>
      )}
      <MenuSeparator />
      <MenuItem icon="logout" onSelect={() => void signOut()}>{t("user.signOut")}</MenuItem>
    </Menu>
  );
}
