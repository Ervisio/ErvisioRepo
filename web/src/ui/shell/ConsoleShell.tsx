import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Fragment, useState, type ComponentType, type ReactNode } from "react";
import { request } from "../../api/http.ts";
import { useOrg, useSession, type User } from "../../api/identity.ts";
import { useUnreadCount } from "../../api/notifications.ts";
import { useOwnerRepos, useRepo, type Repo } from "../../api/repos.ts";
import { useStorageStatus } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { Menu, MenuItem, MenuLabel, MenuSeparator } from "../Menu.tsx";
import {
  adminItems, exploreItem, homeItem, isOrgSettingsTab, NavLink, orgsItem, orgTabs, reposItem, repoSub, repoTabs,
  usePathname, useShellContext, type NavItem,
} from "./nav.tsx";
import { SearchTrigger } from "./SearchPalette.tsx";

type DialogComponent = ComponentType<{ open: boolean; onOpenChange: (open: boolean) => void }>;
/** Create dialogs for the New menu, injected by the app so the kit does not import pages. */
export type ShellDialogs = { NewRepo: DialogComponent; NewOrg: DialogComponent };
type Create = (what: "repo" | "org") => void;

const byUpdated = (a: Repo, b: Repo) => b.updatedAt.localeCompare(a.updatedAt);
const initials = (s: string) => s.slice(0, 2).toUpperCase();
const inAdmin = (pathname: string) => pathname === "/admin" || pathname.startsWith("/admin/");

/**
 * Console shell (Variant Studio round 001, variant B): one sticky bar on top. The first row holds
 * the logo, where you are (a repository switcher inside a repository) and four icons: search,
 * notifications, create and account. The second row holds the tabs of the current context, with
 * the less used repository pages under "More". The page sits in a centred column below.
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

/* ---------- First row ---------- */

function TopRow({ onCreate }: { onCreate?: Create }) {
  const t = useT("shell");
  const user = useSession().data;
  return (
    <div className="tb-row">
      <Link to="/" className="tb-logo" title={t("brand.home")} aria-label={t("brand.home")}>
        <span aria-hidden="true">E</span>
      </Link>
      <Where />
      <span className="tb-grow" />
      <SearchTrigger className="tb-ib" iconOnly />
      {user ? (
        <>
          <Bell />
          {onCreate && (
            <NewMenu onCreate={onCreate} trigger={<button type="button" className="tb-ib tb-hide-m" title={t("new.menu")} aria-label={t("new.menu")}><Icon name="plus" /></button>} />
          )}
          <UserMenu user={user} trigger={<button type="button" className="tb-ib" title={t("user.menu")} aria-label={t("user.menu")}><Avatar user={user} /></button>} />
        </>
      ) : (
        <Link to="/login" className="tb-signin">{t("nav.signIn")}</Link>
      )}
    </div>
  );
}

/** Where you are: the repository (with a switcher), the organization, the admin area or the product. */
function Where() {
  const t = useT("shell");
  const ctx = useShellContext();
  const admin = inAdmin(usePathname());
  if (ctx.kind === "repo") return <RepoSwitch owner={ctx.owner} name={ctx.name} />;
  if (ctx.kind === "org") {
    return (
      <Link to="/orgs/$org" params={{ org: ctx.org }} className="tb-where">
        <b>{ctx.org}</b>
      </Link>
    );
  }
  return <span className="tb-where"><b>{admin ? t("group.admin") : t("brand.name")}</b></span>;
}

function RepoSwitch({ owner, name }: { owner: string; name: string }) {
  const t = useT("shell");
  const repo = useRepo(owner, name).data;
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
      <OwnerRepos owner={owner} kind={repo?.ownerKind ?? "user"} />
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

/* ---------- Second row: tabs of the current context ---------- */

function Tabs() {
  const t = useT("shell");
  const user = useSession().data;
  const ctx = useShellContext();
  const pathname = usePathname();
  const admin = user?.role === "admin";
  let body: ReactNode;
  if (ctx.kind === "repo") body = <RepoTabs owner={ctx.owner} name={ctx.name} admin={admin} />;
  else if (ctx.kind === "org") body = <OrgTabs org={ctx.org} admin={admin} />;
  else if (admin && inAdmin(pathname)) body = <AdminTabs />;
  else body = (user ? [homeItem, reposItem, exploreItem, orgsItem] : [homeItem, exploreItem]).map((it) => <Tab key={it.to} item={it} />);
  return (
    <nav className="tb-tabs" aria-label={t("nav.main")}>
      {body}
    </nav>
  );
}

function Tab({ item, extra }: { item: NavItem; extra?: ReactNode }) {
  const t = useT("shell");
  const label = t(item.labelKey);
  return (
    <NavLink item={item} label={label} className="tb-tab">
      {label}
      {extra}
    </NavLink>
  );
}

function RepoTabs({ owner, name, admin }: { owner: string; name: string; admin: boolean }) {
  const t = useT("shell");
  const repo = useRepo(owner, name).data;
  const sub = repoSub(usePathname());
  const { main, more } = repoTabs(admin || repo?.permission === "admin");
  const current = more.find((it) => it.sub === sub);
  return (
    <>
      {main.map((it) => (
        <Link key={it.to} to={it.to} params={{ owner, name }} className="tb-tab" aria-current={sub === it.sub ? "page" : undefined}>
          {t(it.labelKey)}
        </Link>
      ))}
      <Menu
        label={t("nav.more")}
        trigger={
          <button type="button" className="tb-tab" aria-current={current ? "page" : undefined}>
            {current ? t(current.labelKey) : t("nav.more")}
            <Icon name="down" />
          </button>
        }
      >
        {more.map((it) => (
          <Fragment key={it.to}>
            {it.sub === "settings" && <MenuSeparator />}
            <MenuItem asChild>
              <Link to={it.to} params={{ owner, name }} aria-current={sub === it.sub ? "page" : undefined}>
                <Icon name={it.icon} />
                {t(it.labelKey)}
              </Link>
            </MenuItem>
          </Fragment>
        ))}
      </Menu>
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
          {tOrg(`tab.${it.tab}`)}
        </Link>
      ))}
    </>
  );
}

function AdminTabs() {
  const t = useT("shell");
  const warnings = useStorageStatus().data?.warnings.length ?? 0;
  return (
    <>
      {adminItems.map((it) => (
        <Tab
          key={it.to}
          item={it}
          extra={it.to === "/admin/storage" && warnings > 0 ? <span className="tb-warn" role="img" aria-label={t("admin.storageWarnings", { count: warnings })} /> : undefined}
        />
      ))}
    </>
  );
}

/* ---------- Account ---------- */

export function Avatar({ user, size }: { user: Pick<User, "username" | "avatarUrl">; size?: "lg" | "xl" }) {
  const cls = size ? `tb-av ${size}` : "tb-av";
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
    <Menu align="end" className="tb-pop" label={t("user.menu")} trigger={trigger}>
      <div className="tb-ph">
        <Avatar user={user} size="xl" />
        <span className="tb-ph-t">
          <b>{user.displayName || user.username}</b>
          <small>@{user.username}</small>
        </span>
      </div>
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
