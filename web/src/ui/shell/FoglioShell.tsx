import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { navItems, NavLink } from "./nav.tsx";

/** Direction B: centered sheet, top bar with pill tabs, no side column. */
export function FoglioShell({ children }: { children: ReactNode }) {
  const t = useT("shell");
  return (
    <div className="fs">
      <header className="fs-bar">
        <Link to="/" className="fs-mark"><i aria-hidden="true" />{t("brand.name")}</Link>
      </header>
      <nav className="fs-tabs" aria-label={t("nav.main")}>
        {navItems.map((item) => (
          <NavLink key={item.to} item={item} label={t(item.labelKey)} className="fs-tab">
            <Icon name={item.icon} />
            <span>{t(item.labelKey)}</span>
          </NavLink>
        ))}
      </nav>
      <main className="fs-main">{children}</main>
    </div>
  );
}
