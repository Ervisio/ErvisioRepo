import { Link, useParams, useRouterState } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useSession } from "../../api/identity.ts";
import type { IconName } from "../Icon.tsx";

// Single source of navigation data for the three shells. Labels are keys of the "shell" namespace.

export type Section = "code" | "pr" | "issue" | "act" | "pkg";

type GlobalTo = "/" | "/explore" | "/repos" | "/orgs" | "/notifications" | "/settings" | "/login";
type AdminTo = "/admin" | "/admin/users" | "/admin/settings" | "/admin/storage" | "/admin/runners" | "/admin/audit";

export type NavItem = { to: GlobalTo | AdminTo; icon: IconName; labelKey: string; section?: Section };

export const homeItem: NavItem = { to: "/", icon: "home", labelKey: "nav.home" };
export const exploreItem: NavItem = { to: "/explore", icon: "search", labelKey: "nav.explore", section: "code" };
export const reposItem: NavItem = { to: "/repos", icon: "book", labelKey: "nav.repos", section: "code" };
export const orgsItem: NavItem = { to: "/orgs", icon: "users", labelKey: "nav.orgs", section: "pr" };
export const inboxItem: NavItem = { to: "/notifications", icon: "bell", labelKey: "nav.notifications", section: "issue" };
const accountItem: NavItem = { to: "/settings", icon: "gear", labelKey: "nav.account", section: "issue" };
const signInItem: NavItem = { to: "/login", icon: "lock", labelKey: "nav.signIn", section: "issue" };
// The flat shells have no room for the whole admin block: one entry, the admin frame has its own sub-navigation.
const adminItem: NavItem = { to: "/admin", icon: "shield", labelKey: "group.admin", section: "act" };

/** Administration block of the Console sidebar, shown to instance admins only. */
export const adminItems: readonly NavItem[] = [
  { to: "/admin", icon: "info", labelKey: "nav.overview" },
  { to: "/admin/users", icon: "users", labelKey: "nav.adminUsers" },
  { to: "/admin/settings", icon: "gear", labelKey: "nav.adminSettings" },
  { to: "/admin/storage", icon: "db", labelKey: "nav.storage" },
  { to: "/admin/runners", icon: "cpu", labelKey: "nav.runners" },
  { to: "/admin/audit", icon: "clock", labelKey: "nav.audit" },
];

/** Flat navigation for the Foglio and Corsie shells. */
export function useNavItems(): readonly NavItem[] {
  const user = useSession().data;
  if (!user) return [homeItem, exploreItem, signInItem];
  return [homeItem, reposItem, exploreItem, orgsItem, inboxItem, ...(user.role === "admin" ? [adminItem] : []), accountItem];
}

/* Repository context. `sub` is the first path segment after /owner/name that makes the item active. */
type RepoTo =
  | "/$owner/$name"
  | "/$owner/$name/issues"
  | "/$owner/$name/pulls"
  | "/$owner/$name/commits"
  | "/$owner/$name/branches"
  | "/$owner/$name/tags"
  | "/$owner/$name/releases"
  | "/$owner/$name/search"
  | "/$owner/$name/actions"
  | "/$owner/$name/settings";
export type RepoNavItem = { to: RepoTo; sub: string; icon: IconName; labelKey: string; section: Section };
export type NavGroup<T> = { titleKey: string; items: T[] };

export function repoGroups(canAdmin: boolean): NavGroup<RepoNavItem>[] {
  return [
    {
      titleKey: "group.repository",
      items: [
        { to: "/$owner/$name", sub: "", icon: "code", labelKey: "repo.code", section: "code" },
        { to: "/$owner/$name/issues", sub: "issues", icon: "issue", labelKey: "repo.issues", section: "issue" },
        { to: "/$owner/$name/pulls", sub: "pulls", icon: "pr", labelKey: "repo.pulls", section: "pr" },
        { to: "/$owner/$name/commits", sub: "commits", icon: "commit", labelKey: "repo.commits", section: "code" },
        { to: "/$owner/$name/branches", sub: "branches", icon: "branch", labelKey: "repo.branches", section: "code" },
        { to: "/$owner/$name/tags", sub: "tags", icon: "tag", labelKey: "repo.tags", section: "code" },
        { to: "/$owner/$name/releases", sub: "releases", icon: "pkg", labelKey: "repo.releases", section: "pkg" },
        { to: "/$owner/$name/search", sub: "search", icon: "search", labelKey: "repo.search", section: "code" },
      ],
    },
    { titleKey: "group.automation", items: [{ to: "/$owner/$name/actions", sub: "actions", icon: "play", labelKey: "repo.actions", section: "act" }] },
    // The shell appends "Back to home" to this group, so it is there even without settings.
    { titleKey: "group.manage", items: canAdmin ? [{ to: "/$owner/$name/settings", sub: "settings", icon: "gear", labelKey: "repo.settings", section: "issue" }] : [] },
  ];
}

/** Sub-page key of a repository path: "" for code pages. */
export function repoSub(pathname: string): string {
  const seg = pathname.split("/").filter(Boolean)[2] ?? "";
  return ["issues", "pulls", "commits", "branches", "tags", "releases", "search", "actions", "settings"].includes(seg) ? seg : "";
}

/* Organization context: the org page tabs, opened with ?tab=. */
export type OrgTab = "repos" | "members" | "teams" | "invites" | "hooks" | "secrets" | "variables" | "runners" | "settings";
export type OrgNavItem = { tab: OrgTab; icon: IconName; section: Section };

export function orgTabs(canManage: boolean, isOwner: boolean): OrgNavItem[] {
  return [
    { tab: "repos", icon: "book", section: "code" },
    { tab: "members", icon: "users", section: "pr" },
    { tab: "teams", icon: "users", section: "pr" },
    ...(canManage ? [{ tab: "invites" as const, icon: "plus" as const, section: "pr" as const }] : []),
    // Webhooks, secrets, variables and runners live under Settings, in its own sub-navigation.
    ...(isOwner ? [{ tab: "settings" as const, icon: "gear" as const, section: "issue" as const }] : []),
  ];
}

const orgSettingsTabs = ["settings", "hooks", "secrets", "variables", "runners"] as const;
export type OrgSettingsTab = (typeof orgSettingsTabs)[number];
export const isOrgSettingsTab = (tab: string): tab is OrgSettingsTab => (orgSettingsTabs as readonly string[]).includes(tab);

export type ShellContext =
  | { kind: "global" }
  | { kind: "repo"; owner: string; name: string }
  | { kind: "org"; org: string };

/** Where the user is: a repository, an organization or the global pages. */
export function useShellContext(): ShellContext {
  const p = useParams({ strict: false }) as { owner?: string; name?: string; org?: string };
  if (p.owner && p.name) return { kind: "repo", owner: p.owner, name: p.name };
  if (p.org) return { kind: "org", org: p.org };
  return { kind: "global" };
}

export const usePathname = () => useRouterState({ select: (s) => s.location.pathname });

export function NavLink({ item, label, className, iconOnly, children }: {
  item: NavItem;
  label: string;
  className: string;
  iconOnly?: boolean;
  children?: ReactNode;
}) {
  return (
    <Link
      to={item.to}
      className={item.section ? `${className} sec-${item.section}` : className}
      aria-label={iconOnly ? label : undefined}
      title={iconOnly ? label : undefined}
      activeProps={{ "aria-current": "page" }}
      activeOptions={{ exact: item.to === "/" || item === adminItems[0] }}
    >
      {children}
    </Link>
  );
}
