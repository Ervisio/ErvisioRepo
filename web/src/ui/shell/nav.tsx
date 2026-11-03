import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import type { IconName } from "../Icon.tsx";

export type Section = "code" | "pr" | "issue" | "act" | "pkg";

export type NavItem = { to: "/" | "/admin/storage" | "/_ui"; icon: IconName; labelKey: string; section: Section };

// Placeholder navigation until the repo pages exist: the hue mapping is already the final one.
export const navItems: readonly NavItem[] = [
  { to: "/", icon: "grid", labelKey: "nav.instance", section: "code" },
  { to: "/admin/storage", icon: "pkg", labelKey: "nav.storage", section: "act" },
  { to: "/_ui", icon: "layers", labelKey: "nav.components", section: "pr" },
];

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
