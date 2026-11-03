import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";
import { Button } from "../Button.tsx";
import { Icon } from "../Icon.tsx";
import { Menu, MenuItem } from "../Menu.tsx";
import { Link } from "@tanstack/react-router";
import { NavLink, useNavItems } from "./nav.tsx";
import { SearchTrigger } from "./SearchPalette.tsx";

/** Direction A: 72 px rail, 248 px column, content on a --surface panel. Below 900 px: top bar + chips. */
export function ConsoleShell({ children }: { children: ReactNode }) {
  const t = useT("shell");
  const navItems = useNavItems();
  const items = navItems.map((item) => ({ item, label: t(item.labelKey) }));
  return (
    <div className="cs">
      <nav className="cs-rail" aria-label={t("nav.rail")}>
        <Link to="/" className="cs-logo" aria-label={t("brand.home")}>E</Link>
        {items.map(({ item, label }) => (
          <NavLink key={item.to} item={item} label={label} className="cs-ri" iconOnly>
            <Icon name={item.icon} />
          </NavLink>
        ))}
        <SearchTrigger className="cs-ri" iconOnly />
      </nav>
      <aside className="cs-nav" aria-label={t("nav.main")}>
        <div className="cs-brand">
          <small>{t("brand.org")}</small>
          <b>{t("brand.name")}</b>
        </div>
        {items.map(({ item, label }) => (
          <NavLink key={item.to} item={item} label={label} className="cs-ni">
            <span className="cs-kt"><Icon name={item.icon} /></span>
            <span>{label}</span>
          </NavLink>
        ))}
      </aside>
      <main className="cs-main">
        <div className="cs-top">
          <div className="cs-tb">
            <Menu
              trigger={<Button variant="ghost" size="sm" icon="menu" aria-label={t("nav.menu")} />}
            >
              {items.map(({ item, label }) => (
                <MenuItem key={item.to} asChild>
                  <Link to={item.to}><Icon name={item.icon} />{label}</Link>
                </MenuItem>
              ))}
            </Menu>
            <b><small>{t("brand.org")}</small>{t("brand.name")}</b>
            <SearchTrigger className="ui-iconbtn cs-search" iconOnly />
          </div>
          <nav className="cs-chips" aria-label={t("nav.main")}>
            {items.map(({ item, label }) => (
              <NavLink key={item.to} item={item} label={label} className="cs-ni">
                <span className="cs-kt"><Icon name={item.icon} /></span>
                <span>{label}</span>
              </NavLink>
            ))}
          </nav>
        </div>
        {children}
      </main>
    </div>
  );
}
