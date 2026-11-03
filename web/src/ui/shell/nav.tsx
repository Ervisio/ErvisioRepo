import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useSession } from "../../api/identity.ts";
import type { IconName } from "../Icon.tsx";

export type Section = "code" | "pr" | "issue" | "act" | "pkg";

export type NavItem = { to: "/" | "/repos" | "/admin/storage" | "/admin/audit" | "/_ui" | "/settings" | "/login"; icon: IconName; labelKey: string; section: Section };

// Placeholder navigation until the repo pages exist: the hue mapping is already the final one.
const base: readonly NavItem[] = [
  { to: "/", icon: "grid", labelKey: "nav.instance", section: "code" },
  { to: "/_ui", icon: "layers", labelKey: "nav.components", section: "pr" },
];
const reposItem: NavItem = { to: "/repos", icon: "code", labelKey: "nav.repos", section: "code" };
const adminItems: readonly NavItem[] = [
  { to: "/admin/storage", icon: "pkg", labelKey: "nav.storage", section: "act" },
  { to: "/admin/audit", icon: "clock", labelKey: "nav.audit", section: "act" },
];
const accountItem: NavItem = { to: "/settings", icon: "gear", labelKey: "nav.account", section: "issue" };
const signInItem: NavItem = { to: "/login", icon: "lock", labelKey: "nav.signIn", section: "issue" };

/** Navigation entries for the current session: admin pages and account/sign-in depend on who is looking. */
export function useNavItems(): readonly NavItem[] {
  const session = useSession();
  const user = session.data;
  if (!user) return [...base, signInItem];
  return [base[0]!, reposItem, ...(user.role === "admin" ? adminItems : []), base[1]!, accountItem];
}

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
      className={`${className} sec-${item.section}`}
      aria-label={iconOnly ? label : undefined}
      title={iconOnly ? label : undefined}
      activeProps={{ "aria-current": "page" }}
      activeOptions={{ exact: true }}
    >
      {children}
    </Link>
  );
}
