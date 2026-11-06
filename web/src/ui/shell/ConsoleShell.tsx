import * as RDialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState, type ComponentType, type ReactNode } from "react";
import { request } from "../../api/http.ts";
import { useMyOrgs, useOrg, useSession, type User } from "../../api/identity.ts";
import { useUnreadCount } from "../../api/notifications.ts";
import { useUserPins } from "../../api/personal.ts";
import { useMyRepos, useOwnerRepos, useRepo, type Repo } from "../../api/repos.ts";
import { useStorageStatus } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { Menu, MenuItem, MenuLabel } from "../Menu.tsx";
import {
  accountItem, adminItems, exploreItem, homeItem, inboxItem, isOrgSettingsTab, NavLink, orgsItem, orgTabs, repoGroups, reposItem, repoSub,
  usePathname, useShellContext, type NavItem,
} from "./nav.tsx";
import { SearchField } from "./SearchPalette.tsx";

type DialogComponent = ComponentType<{ open: boolean; onOpenChange: (open: boolean) => void }>;
/** Create dialogs for the New menu, injected by the app so the kit does not import pages. */
export type ShellDialogs = { NewRepo: DialogComponent; NewOrg: DialogComponent };
type Create = (what: "repo" | "org") => void;

const byUpdated = (a: Repo, b: Repo) => b.updatedAt.localeCompare(a.updatedAt);
const initials = (s: string) => s.slice(0, 2).toUpperCase();
const sq = (on: boolean, extra = "") => `sb-sq${extra}${on ? " on" : ""}`;
const crumbItems = [reposItem, orgsItem, exploreItem, inboxItem, accountItem, ...adminItems];
const areas = [["/admin", "admin"], ["/settings", "account"], ["/notifications", "inbox"], ["/explore", "explore"], ["/repos", "you"]] as const;

/** Which rail squircle the current global page belongs to; null inside a repository or organization. */
function useRailArea() {
  const pathname = usePathname();
  if (useShellContext().kind !== "global") return null;
  return areas.find(([prefix]) => pathname === prefix || pathname.startsWith(`${prefix}/`))?.[1] ?? "home";
}

/**
 * Console shell (round 009, variant A): a rail of squircles (home, owners, create, explore |
 * notifications, admin, account), a 248 px channel column for the current context and the page on
 * a glass panel, over slowly drifting accent shapes. Below 720 px rail and column give way to a
 * bottom bar whose menu button opens the column as a drawer.
 */
export function ConsoleShell({ children, dialogs }: { children: ReactNode; dialogs?: ShellDialogs }) {
  const t = useT("shell");
  const pathname = usePathname();
  // The drawer closes on any link click inside it, and also when the path changes under it.
  const [drawerAt, setDrawerAt] = useState<string | null>(null);
  const drawer = drawerAt === pathname;
  const [creating, setCreating] = useState<"repo" | "org" | null>(null);
  const onCreate = dialogs ? setCreating : undefined;

  return (
    <div className="sb">
      <div className="sb-fx" aria-hidden="true"><i /><i /><i /><i /><i /><i /></div>
      <Rail onCreate={onCreate} />
      <nav className="sb-side sb-glass" aria-label={t("nav.main")}>
        <Side />
      </nav>
      <RDialog.Root open={drawer} onOpenChange={(o) => setDrawerAt(o ? pathname : null)}>
        <RDialog.Portal>
          <RDialog.Overlay className="ui-overlay" />
          <RDialog.Content className="sb-drawer sb-side sb-glass" aria-describedby={undefined} onClickCapture={(e) => { if ((e.target as Element).closest("a")) setDrawerAt(null); }}>
            <RDialog.Title className="sr-only">{t("nav.main")}</RDialog.Title>
            <Side />
          </RDialog.Content>
        </RDialog.Portal>
      </RDialog.Root>
      <div className="sb-col sb-glass">
        <TopBar onCreate={onCreate} />
        <main className="sb-main">{children}</main>
      </div>
      <MobileBar onMenu={() => setDrawerAt(pathname)} />
      {dialogs && (
        <>
          <dialogs.NewRepo open={creating === "repo"} onOpenChange={(o) => setCreating(o ? "repo" : null)} />
          <dialogs.NewOrg open={creating === "org"} onOpenChange={(o) => setCreating(o ? "org" : null)} />
        </>
      )}
    </div>
  );
}

/* ---------- Rail ---------- */

function Rail({ onCreate }: { onCreate?: Create }) {
  const t = useT("shell");
  const user = useSession().data;
  const area = useRailArea();
  return (
    <nav className="sb-rail" aria-label={t("nav.rail")}>
      <Link to="/" className={sq(area === "home", " logo")} title={t("brand.home")} aria-label={t("brand.home")}>
        <span className="sb-logo" aria-hidden="true">E</span>
      </Link>
      <span className="sb-sep" aria-hidden="true" />
      {user && <Owners user={user} you={area === "you"} />}
      {user && onCreate && (
        <NewMenu onCreate={onCreate} trigger={<button type="button" className="sb-sq" title={t("new.menu")} aria-label={t("new.menu")}><Icon name="plus" /></button>} />
      )}
      <Link to={exploreItem.to} className={sq(area === "explore")} title={t(exploreItem.labelKey)} aria-label={t(exploreItem.labelKey)}>
        <Icon name={exploreItem.icon} />
      </Link>
      <span className="sb-grow" />
      {user ? (
        <>
          <Bell on={area === "inbox"} />
          {user.role === "admin" && (
            <Link to="/admin" className={sq(area === "admin")} title={t("group.admin")} aria-label={t("group.admin")}>
              <Icon name="shield" />
            </Link>
          )}
          <UserMenu
            user={user}
            trigger={<button type="button" className={sq(area === "account", " sb-me-av")} title={t("user.menu")} aria-label={t("user.menu")}><Avatar user={user} /></button>}
          />
        </>
      ) : (
        <Link to="/login" className="sb-sq" title={t("nav.signIn")} aria-label={t("nav.signIn")}>
          <Icon name="lock" />
        </Link>
      )}
    </nav>
  );
}

/** You and your organizations; the owner of the current repository or organization is lit. */
function Owners({ user, you }: { user: User; you: boolean }) {
  const t = useT("shell");
  const ctx = useShellContext();
  const orgs = useMyOrgs();
  const owner = ctx.kind === "repo" ? ctx.owner : ctx.kind === "org" ? ctx.org : undefined;
  return (
    <>
      <Link to="/repos" className={sq(you || owner === user.username)} title={t("user.repos")} aria-label={t("user.repos")}>
        {initials(user.username)}
      </Link>
      {orgs.data?.map((o) => (
        <Link key={o.name} to="/orgs/$org" params={{ org: o.name }} className={sq(owner === o.name)} title={o.displayName || o.name} aria-label={o.displayName || o.name}>
          {initials(o.name)}
        </Link>
      ))}
    </>
  );
}

function Bell({ on }: { on: boolean }) {
  const t = useT("shell");
  const unread = useUnreadCount().data?.unread ?? 0;
  return (
    <Link to="/notifications" className={sq(on)} title={t("nav.notifications")} aria-label={unread ? t("bell.unread", { count: unread }) : t("nav.notifications")}>
      <Icon name="bell" />
      {unread > 0 && <span className="sb-dot" aria-hidden="true" />}
    </Link>
  );
}

/* ---------- Channel column ---------- */

function Side() {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  const area = useRailArea();
  if (!user) {
    return (
      <>
        <Title name={t("brand.name")} />
        <Item item={homeItem} />
        <Item item={exploreItem} />
        <Item item={{ to: "/login", icon: "lock", labelKey: "nav.signIn" }} />
      </>
    );
  }
  return (
    <>
      {ctx.kind === "repo" ? (
        <RepoNav owner={ctx.owner} name={ctx.name} admin={user.role === "admin"} />
      ) : ctx.kind === "org" ? (
        <OrgNav org={ctx.org} admin={user.role === "admin"} />
      ) : area === "admin" && user.role === "admin" ? (
        <AdminNav />
      ) : (
        <GlobalNav user={user} />
      )}
      <span className="sb-grow" />
      <UserMenu
        user={user}
        trigger={
          <button type="button" className="sb-mecard" aria-label={t("user.menu")}>
            <Avatar user={user} size="lg" />
            <span className="sb-mecard-t">
              <b>{user.displayName || user.username}</b>
              <small>{t("user.online")}</small>
            </span>
            <Icon name="gear" />
          </button>
        }
      />
    </>
  );
}

function Title({ name, sub }: { name: string; sub?: string }) {
  return (
    <div className="sb-title">
      <b>{name}</b>
      {sub && <small>{sub}</small>}
    </div>
  );
}

function Item({ item, count, hot, extra }: { item: NavItem; count?: number; hot?: boolean; extra?: ReactNode }) {
  const t = useT("shell");
  const label = t(item.labelKey);
  return (
    <NavLink item={item} label={label} className="sb-nv">
      <Icon name={item.icon} />
      <span className="sb-lbl">{label}</span>
      {!!count && <span className={hot ? "sb-cnt hot" : "sb-cnt"}>{count > 99 ? "99+" : count}</span>}
      {extra}
    </NavLink>
  );
}

function GlobalNav({ user }: { user: User }) {
  const t = useT("shell");
  const tRepo = useT("repos");
  const repos = useMyRepos();
  const orgs = useMyOrgs();
  const unread = useUnreadCount().data?.unread;
  const pins = useUserPins().data ?? [];
  const pinNames = new Set(pins.map((r) => r.fullName));
  const recent = [...(repos.data ?? [])].filter((r) => !pinNames.has(r.fullName)).sort(byUpdated);
  const pinned = [...pins, ...recent].slice(0, Math.max(5, pins.length));
  return (
    <>
      <Title name={t("brand.name")} sub={`@${user.username}`} />
      <Item item={homeItem} />
      <Item item={reposItem} count={repos.data?.length} />
      <Item item={exploreItem} />
      <Item item={orgsItem} count={orgs.data?.length} />
      <Item item={inboxItem} count={unread} hot />
      {pinned.length > 0 && (
        <>
          <span className="sb-nvh">{t("group.pinned")}</span>
          {pinned.map((r) => (
            <Link key={r.fullName} to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="sb-nv">
              <Icon name={r.visibility === "public" ? "book" : "lock"} aria-label={r.visibility === "public" ? undefined : tRepo(`visibility.${r.visibility}`)} />
              <span className="sb-lbl">{r.owner !== user.username && <small>{r.owner} / </small>}{r.name}</span>
            </Link>
          ))}
        </>
      )}
    </>
  );
}

function RepoNav({ owner, name, admin }: { owner: string; name: string; admin: boolean }) {
  const t = useT("shell");
  const tRepo = useT("repos");
  const repo = useRepo(owner, name).data;
  const sub = repoSub(usePathname());
  const groups = repoGroups(admin || repo?.permission === "admin");
  return (
    <>
      <Menu
        label={t("switch.repo")}
        trigger={
          <button type="button" className="sb-title sb-title-btn" aria-label={t("switch.repoLabel", { repo: name })}>
            <b>{name}</b>
            <small>{repo ? `${owner} · ${tRepo(`visibility.${repo.visibility}`)}` : owner}</small>
            <Icon name="down" />
          </button>
        }
      >
        <OwnerRepos owner={owner} kind={repo?.ownerKind ?? "user"} />
      </Menu>
      {groups.map((g) => (
        <div key={g.titleKey} className="sb-grp" role="group" aria-label={t(g.titleKey)}>
          <span className="sb-nvh" aria-hidden="true">{t(g.titleKey)}</span>
          {g.items.map((it) => (
            <Link key={it.to} to={it.to} params={{ owner, name }} className="sb-nv" aria-current={sub === it.sub ? "page" : undefined}>
              <Icon name={it.icon} />
              <span className="sb-lbl">{t(it.labelKey)}</span>
            </Link>
          ))}
        </div>
      ))}
    </>
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

function OrgNav({ org, admin }: { org: string; admin: boolean }) {
  const t = useT("shell");
  const tOrg = useT("org");
  const o = useOrg(org).data;
  const search = useSearch({ strict: false }) as { tab?: string };
  const isOwner = o?.role === "owner" || admin;
  const canManage = isOwner || o?.role === "admin";
  const active = search.tab === undefined ? "repos" : isOrgSettingsTab(search.tab) ? "settings" : search.tab;
  return (
    <>
      <Title name={o?.displayName || org} sub={t("group.organization")} />
      {orgTabs(canManage, isOwner).map((it) => (
        <Link key={it.tab} to="/orgs/$org" params={{ org }} search={{ tab: it.tab }} className="sb-nv" aria-current={active === it.tab ? "page" : undefined}>
          <Icon name={it.icon} />
          <span className="sb-lbl">{tOrg(`tab.${it.tab}`)}</span>
        </Link>
      ))}
    </>
  );
}

function AdminNav() {
  const t = useT("shell");
  const storage = useStorageStatus();
  const warnings = storage.data?.warnings.length ?? 0;
  return (
    <>
      <Title name={t("group.admin")} sub={t("nav.instance")} />
      {adminItems.map((it) => (
        <Item
          key={it.to}
          item={it}
          extra={it.to === "/admin/storage" && warnings > 0 ? <span className="sb-wd" role="img" aria-label={t("admin.storageWarnings", { count: warnings })} /> : undefined}
        />
      ))}
    </>
  );
}

/* ---------- Top bar ---------- */

function TopBar({ onCreate }: { onCreate?: Create }) {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  const pathname = usePathname();
  const item = crumbItems.filter((i) => pathname === i.to || pathname.startsWith(`${i.to}/`)).sort((a, b) => b.to.length - a.to.length)[0] ?? homeItem;
  return (
    <header className="sb-top">
      <span className="sb-crumb">
        {ctx.kind === "repo" ? (
          <><span className="m">{ctx.owner} /</span>{ctx.name}</>
        ) : ctx.kind === "org" ? (
          <><Icon name="users" />{ctx.org}</>
        ) : (
          <><Icon name={item.icon} />{t(item.labelKey)}</>
        )}
      </span>
      <span className="sb-grow" />
      <SearchField className="sb-search" />
      {user && onCreate && (
        <NewMenu
          onCreate={onCreate}
          trigger={
            <button type="button" className="sb-btn pri" aria-label={t("new.menu")}>
              <Icon name="plus" />
              <span className="sb-hide-m">{t("new.button")}</span>
            </button>
          }
        />
      )}
    </header>
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

/* ---------- Mobile bar ---------- */

function MobileBar({ onMenu }: { onMenu: () => void }) {
  const t = useT("shell");
  const user = useSession().data;
  const unread = useUnreadCount().data?.unread ?? 0;
  const area = useRailArea();
  return (
    <nav className="sb-mbar sb-glass" aria-label={t("nav.rail")}>
      <Link to="/" className={area === "home" ? "on" : undefined}><Icon name="home" />{t("nav.home")}</Link>
      <Link to="/explore" className={area === "explore" ? "on" : undefined}><Icon name="search" />{t("nav.explore")}</Link>
      {user && (
        <Link to="/notifications" className={area === "inbox" ? "on" : undefined} aria-label={unread ? t("bell.unread", { count: unread }) : undefined}>
          <Icon name="bell" />{t("nav.notifications")}
        </Link>
      )}
      <button type="button" onClick={onMenu}><Icon name="menu" />{t("nav.menu")}</button>
    </nav>
  );
}

/* ---------- Account ---------- */

export function Avatar({ user, size }: { user: Pick<User, "username" | "avatarUrl">; size?: "lg" | "xl" }) {
  const cls = size ? `sb-av ${size}` : "sb-av";
  if (user.avatarUrl) return <img className={cls} src={user.avatarUrl} alt="" />;
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
    <Menu align="end" className="sb-pop" label={t("user.menu")} trigger={trigger}>
      <div className="sb-ph">
        <Avatar user={user} size="xl" />
        <span className="sb-ph-t">
          <b>{user.displayName || user.username}</b>
          <small>@{user.username}</small>
        </span>
        {user.role === "admin" && <span className="sb-badge">{t("user.admin")}</span>}
      </div>
      <MenuItem asChild>
        <Link to="/settings"><Icon name="user" />{t("user.settings")}</Link>
      </MenuItem>
      <MenuItem asChild>
        <Link to="/repos"><Icon name="book" />{t("user.repos")}</Link>
      </MenuItem>
      <MenuItem asChild>
        <Link to="/orgs"><Icon name="users" />{t("user.orgs")}</Link>
      </MenuItem>
      <MenuItem icon="logout" onSelect={() => void signOut()}>{t("user.signOut")}</MenuItem>
    </Menu>
  );
}
