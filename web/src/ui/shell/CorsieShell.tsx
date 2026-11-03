import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../Icon.tsx";
import { NavLink, useNavItems } from "./nav.tsx";
import { SearchTrigger } from "./SearchPalette.tsx";

/** Direction C: top band with a segmented section switch; page side zone becomes a sticky lane. */
export function CorsieShell({ children }: { children: ReactNode }) {
  const t = useT("shell");
  const navItems = useNavItems();
  return (
    <div className="rs">
      <header className="rs-band">
        <Link to="/" className="rs-brand">
          <span className="rs-logo" aria-hidden="true">E</span>
          <b>{t("brand.name")}</b>
        </Link>
        <nav className="rs-seg" aria-label={t("nav.main")}>
          {navItems.map((item) => (
            <NavLink key={item.to} item={item} label={t(item.labelKey)} className="rs-segi">
              <Icon name={item.icon} />
              <span>{t(item.labelKey)}</span>
            </NavLink>
          ))}
        </nav>
        <SearchTrigger className="ui-btn ui-btn-ghost ui-btn-sm sp-trigger" />
      </header>
      <main className="rs-main">{children}</main>
    </div>
  );
}
