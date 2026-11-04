import * as RDialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState, type ComponentType, type ReactNode } from "react";
import { request } from "../../api/http.ts";
import { useMyOrgs, useOrg, useSession, type User } from "../../api/identity.ts";
import { useUnreadCount } from "../../api/notifications.ts";
import { useMyRepos, useOwnerRepos, useRepo, type Repo } from "../../api/repos.ts";
import { useStorageStatus } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { Menu, MenuItem, MenuLabel } from "../Menu.tsx";
import {
  adminItems, homeItem, inboxItem, NavLink, orgsItem, orgTabs, repoGroups, reposItem, repoSub,
  usePathname, useShellContext, type NavItem,
} from "./nav.tsx";
import { SearchField, SearchTrigger } from "./SearchPalette.tsx";

type DialogComponent = ComponentType<{ open: boolean; onOpenChange: (open: boolean) => void }>;
/** Create dialogs for the New menu, injected by the app so the kit does not import pages. */
export type ShellDialogs = { NewRepo: DialogComponent; NewOrg: DialogComponent };

const byUpdated = (a: Repo, b: Repo) => b.updatedAt.localeCompare(a.updatedAt);
const initial = (s: string) => s.slice(0, 1).toUpperCase();

/**
 * Console shell (round 006, variant B): a 264 px sidebar with a context switcher and grouped
 * navigation, a top bar (search, New, notifications, user menu) and content on a --surface panel.
 * Below 900 px the sidebar becomes a drawer opened from the top bar.
 */
export function ConsoleShell({ children, dialogs }: { children: ReactNode; dialogs?: ShellDialogs }) {
  const t = useT("shell");
  const pathname = usePathname();
  // The drawer closes on any link click inside it, and also when the path changes under it.
  const [drawerAt, setDrawerAt] = useState<string | null>(null);
  const drawer = drawerAt === pathname;
  const [creating, setCreating] = useState<"repo" | "org" | null>(null);

  return (
    <div className="sb">
      <nav className="sb-side" aria-label={t("nav.main")}>
        <Sidebar />
      </nav>
      <RDialog.Root open={drawer} onOpenChange={(o) => setDrawerAt(o ? pathname : null)}>
        <RDialog.Portal>
          <RDialog.Overlay className="ui-overlay" />
          <RDialog.Content className="sb-drawer" aria-describedby={undefined} onClickCapture={(e) => { if ((e.target as Element).closest("a")) setDrawerAt(null); }}>
            <RDialog.Title className="sr-only">{t("nav.main")}</RDialog.Title>
            <nav className="sb-side" aria-label={t("nav.main")}>
              <Sidebar />
            </nav>
          </RDialog.Content>
        </RDialog.Portal>
      </RDialog.Root>
      <div className="sb-col">
        <TopBar onMenu={() => setDrawerAt(pathname)} onCreate={dialogs ? setCreating : undefined} />
        <main className="sb-main">{children}</main>
      </div>
      {dialogs && (
        <>
          <dialogs.NewRepo open={creating === "repo"} onOpenChange={(o) => setCreating(o ? "repo" : null)} />
          <dialogs.NewOrg open={creating === "org"} onOpenChange={(o) => setCreating(o ? "org" : null)} />
        </>
      )}
    </div>
  );
}

/* ---------- Sidebar ---------- */

function Sidebar() {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  return (
    <>
      <Link to="/" className="sb-brand" aria-label={t("brand.home")}>
        <span className="sb-logo" aria-hidden="true">E</span>
        <span>{t("brand.name")}</span>
      </Link>
      {user ? (
        <>
          {ctx.kind === "repo" ? <RepoSwitcher user={user} owner={ctx.owner} name={ctx.name} /> : <Switcher user={user} owner={ctx.kind === "org" ? ctx.org : user.username} kind={ctx.kind === "org" ? "org" : "user"} />}
          {ctx.kind === "repo" ? (
            <RepoNav owner={ctx.owner} name={ctx.name} admin={user.role === "admin"} />
          ) : ctx.kind === "org" ? (
            <OrgNav org={ctx.org} admin={user.role === "admin"} />
          ) : (
            <GlobalNav user={user} />
          )}
          <span className="sb-sp" />
          {user.role === "admin" && <AdminBlock />}
        </>
      ) : (
        <>
          <span className="sb-gt">{t("group.navigate")}</span>
          <Item item={homeItem} />
          <Item item={{ to: "/login", icon: "lock", labelKey: "nav.signIn" }} />
        </>
      )}
    </>
  );
}

function Item({ item, count, extra }: { item: NavItem; count?: number; extra?: ReactNode }) {
  const t = useT("shell");
  const label = t(item.labelKey);
  return (
    <NavLink item={item} label={label} className="sb-li">
      <span className="sb-kt"><Icon name={item.icon} /></span>
      <span className="sb-lbl">{label}</span>
      {count !== undefined && <em>{count}</em>}
      {extra}
    </NavLink>
  );
}

function RepoSwitcher({ user, owner, name }: { user: User; owner: string; name: string }) {
  const kind = useRepo(owner, name).data?.ownerKind ?? "user";
  return <Switcher user={user} owner={owner} kind={kind} repoName={name} />;
}

/** owner ▾ / repo ▾: the owner menu lists you and your organizations, the repo menu that owner's recent repositories. */
function Switcher({ user, owner, kind, repoName }: { user: User; owner: string; kind: "user" | "org"; repoName?: string }) {
  const t = useT("shell");
  const orgs = useMyOrgs();
  return (
    <div className="sb-sw">
      <Menu
        label={t("switch.owner")}
        trigger={
          <button type="button" className="sb-own" aria-label={t("switch.ownerLabel", { owner })}>
            <span className={kind === "org" ? "sb-ot sec-pr" : "sb-ot sec-issue"}>{initial(owner)}</span>
            <span className="sb-sw-t">{owner}</span>
            <Icon name="down" />
          </button>
        }
      >
        <MenuLabel>{t("switch.you")}</MenuLabel>
        <MenuItem asChild>
          <Link to="/repos"><span className="sb-ot sm sec-issue">{initial(user.username)}</span>{user.username}</Link>
        </MenuItem>
        {(orgs.data?.length ?? 0) > 0 && <MenuLabel>{t("switch.orgs")}</MenuLabel>}
        {orgs.data?.map((o) => (
          <MenuItem key={o.name} asChild>
            <Link to="/orgs/$org" params={{ org: o.name }}><span className="sb-ot sm sec-pr">{initial(o.name)}</span>{o.displayName || o.name}</Link>
          </MenuItem>
        ))}
      </Menu>
      <span className="sb-sl" aria-hidden="true">/</span>
      <Menu
        label={t("switch.repo")}
        trigger={
          <button type="button" className={repoName ? "sb-rp" : "sb-rp none"} aria-label={repoName ? t("switch.repoLabel", { repo: repoName }) : t("switch.pick")}>
            <span className="sb-sw-t">{repoName ?? t("switch.pick")}</span>
            <Icon name="down" />
          </button>
        }
      >
        <OwnerRepos owner={owner} kind={kind} />
      </Menu>
    </div>
  );
}

/** Repositories of the switcher's owner: fetched only while the menu is open. */
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

function GlobalNav({ user }: { user: User }) {
  const t = useT("shell");
  const tRepo = useT("repos");
  const repos = useMyRepos();
  const orgs = useMyOrgs();
  const pinned = [...(repos.data ?? [])].sort(byUpdated).slice(0, 5);
  return (
    <>
      <span className="sb-gt">{t("group.navigate")}</span>
      <Item item={homeItem} />
      <Item item={reposItem} count={repos.data?.length} />
      <Item item={orgsItem} count={orgs.data?.length} />
      <Item item={inboxItem} />
      {pinned.length > 0 && (
        <>
          <span className="sb-gt">{t("group.recent")}</span>
          {pinned.map((r, i) => (
            <Link key={r.fullName} to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="sb-li">
              <span className={`sb-ot sec-${(["code", "pkg", "pr", "act", "issue"] as const)[i % 5]}`} aria-hidden="true">{initial(r.name)}</span>
              <span className="sb-lbl">{r.owner !== user.username && <small>{r.owner} / </small>}{r.name}</span>
              {r.visibility !== "public" && <Icon name="lock" aria-label={tRepo(`visibility.${r.visibility}`)} />}
            </Link>
          ))}
        </>
      )}
    </>
  );
}

function RepoNav({ owner, name, admin }: { owner: string; name: string; admin: boolean }) {
  const t = useT("shell");
  const repo = useRepo(owner, name);
  const sub = repoSub(usePathname());
  const groups = repoGroups(admin || repo.data?.permission === "admin");
  return (
    <>
      {groups.map((g, gi) => (
        <div key={g.titleKey} className="sb-grp">
          <span className="sb-gt">{t(g.titleKey)}</span>
          {g.items.map((it) => (
            <Link key={it.to} to={it.to} params={{ owner, name }} className={`sb-li sec-${it.section}`} aria-current={sub === it.sub ? "page" : undefined}>
              <span className="sb-kt"><Icon name={it.icon} /></span>
              <span className="sb-lbl">{t(it.labelKey)}</span>
            </Link>
          ))}
          {gi === groups.length - 1 && <BackHome />}
        </div>
      ))}
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
  const active = search.tab ?? "repos";
  return (
    <>
      <span className="sb-gt">{t("group.organization")}</span>
      {orgTabs(canManage, isOwner).map((it) => (
        <Link key={it.tab} to="/orgs/$org" params={{ org }} search={{ tab: it.tab }} className={`sb-li sec-${it.section}`} aria-current={active === it.tab ? "page" : undefined}>
          <span className="sb-kt"><Icon name={it.icon} /></span>
          <span className="sb-lbl">{tOrg(`tab.${it.tab}`)}</span>
        </Link>
      ))}
      <span className="sb-gt">{t("group.manage")}</span>
      <BackHome />
    </>
  );
}

function BackHome() {
  const t = useT("shell");
  return (
    <Link to="/" className="sb-li">
      <span className="sb-kt sb-kt-q"><Icon name="back" /></span>
      <span className="sb-lbl">{t("nav.backHome")}</span>
    </Link>
  );
}

function AdminBlock() {
  const t = useT("shell");
  const storage = useStorageStatus();
  const warnings = storage.data?.warnings.length ?? 0;
  return (
    <div className="sb-adm" role="group" aria-labelledby="sb-adm-title">
      <span className="sb-gt" id="sb-adm-title">
        <Icon name="shield" />
        {t("group.admin")}
        <small>{t("group.adminOnly")}</small>
      </span>
      {adminItems.map((it) => (
        <Item
          key={it.to}
          item={it}
          extra={it.to === "/admin/storage" && warnings > 0 ? <span className="sb-wd" role="img" aria-label={t("admin.storageWarnings", { count: warnings })} /> : undefined}
        />
      ))}
    </div>
  );
}

/* ---------- Top bar ---------- */

function TopBar({ onMenu, onCreate }: { onMenu: () => void; onCreate?: (what: "repo" | "org") => void }) {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  const home = usePathname() === "/";
  return (
    <header className="sb-bar">
      <button type="button" className="sb-ib sb-menu" onClick={onMenu} aria-label={t("nav.menu")}>
        <Icon name="menu" />
      </button>
      <span className="sb-ctxm">
        {ctx.kind === "repo" ? (
          <><small>{ctx.owner} /</small>{ctx.name}</>
        ) : ctx.kind === "org" ? (
          ctx.org
        ) : (
          t("brand.name")
        )}
      </span>
      <SearchField className="sb-find" />
      <span className="sb-grow" />
      {user ? (
        <>
          {onCreate && (
            <Menu
              align="end"
              label={t("new.menu")}
              trigger={
                <button type="button" className={home ? "sb-new" : "sb-new sb-new-q"} aria-label={t("new.menu")}>
                  <Icon name="plus" />
                  <span className="sb-new-t">{t("new.button")}</span>
                  <Icon name="down" />
                </button>
              }
            >
              <MenuItem icon="book" onSelect={() => onCreate("repo")}>{t("new.repo")}</MenuItem>
              <MenuItem icon="users" onSelect={() => onCreate("org")}>{t("new.org")}</MenuItem>
              {ctx.kind === "repo" && (
                <MenuItem asChild>
                  <Link to="/$owner/$name/issues/new" params={{ owner: ctx.owner, name: ctx.name }}><Icon name="issue" />{t("new.issue")}</Link>
                </MenuItem>
              )}
            </Menu>
          )}
          <SearchTrigger className="sb-ib sb-mob" iconOnly />
          <Bell />
          <UserMenu user={user} />
        </>
      ) : (
        <Link to="/login" className="sb-me">
          <Icon name="lock" />
          <span>{t("nav.signIn")}</span>
        </Link>
      )}
    </header>
  );
}

function Bell() {
  const t = useT("shell");
  const unread = useUnreadCount().data?.unread ?? 0;
  return (
    <Link to="/notifications" className="sb-ib" aria-label={unread ? t("bell.unread", { count: unread }) : t("nav.notifications")} activeProps={{ "aria-current": "page" }}>
      <Icon name="bell" />
      {unread > 0 && <span className="sb-cnt" aria-hidden="true">{unread > 99 ? "99+" : unread}</span>}
    </Link>
  );
}

export function Avatar({ user, size }: { user: Pick<User, "username" | "avatarUrl">; size?: "lg" | "xl" }) {
  const cls = size ? `sb-av ${size}` : "sb-av";
  if (user.avatarUrl) return <img className={cls} src={user.avatarUrl} alt="" />;
  return <span className={cls} aria-hidden="true">{user.username.slice(0, 2).toUpperCase()}</span>;
}

function UserMenu({ user }: { user: User }) {
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
    <Menu
      align="end"
      className="sb-pop"
      label={t("user.menu")}
      trigger={
        <button type="button" className="sb-me" aria-label={t("user.menu")}>
          <Avatar user={user} size="lg" />
          <span className="sb-me-n">{user.username}</span>
          <Icon name="down" />
        </button>
      }
    >
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
