import { Link, Outlet } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";
import { Page, SettingsLayout, SettingsSection, type SettingsLinkProps, type SettingsNavGroup, type SettingsNavItem } from "../../ui/index.ts";
import { usePathname } from "../../ui/shell/nav.tsx";
import { settingSections, type SettingSection } from "./settingsCatalog.ts";

const sectionIcons: Record<SettingSection, SettingsNavItem["icon"]> = {
  auth: "lock",
  registration: "users",
  email: "bell",
  git: "branch",
  actions: "play",
  webhooks: "code",
  appearance: "grid",
  server: "gear",
};

/** Sub-navigation id of a pathname under /admin. */
function activeId(pathname: string): string {
  const [, , first, second] = pathname.split("/");
  if (first === "settings") return `settings:${second ?? "auth"}`;
  return first || "overview";
}

function renderAdminLink(it: SettingsNavItem, props: SettingsLinkProps): ReactNode {
  if (it.id.startsWith("settings:")) {
    return <Link to="/admin/settings/$section" params={{ section: it.id.slice("settings:".length) }} {...props} />;
  }
  switch (it.id) {
    case "users": return <Link to="/admin/users" {...props} />;
    case "invites": return <Link to="/admin/invites" {...props} />;
    case "storage": return <Link to="/admin/storage" {...props} />;
    case "runners": return <Link to="/admin/runners" {...props} />;
    case "audit": return <Link to="/admin/audit" {...props} />;
    default: return <Link to="/admin" {...props} />;
  }
}

/** Layout route of /admin/*: the settings frame with the administration sub-navigation. */
export function AdminFrame() {
  const t = useT("admin");
  const active = activeId(usePathname());
  const groups: SettingsNavGroup[] = [
    {
      title: t("group.instance"),
      items: [
        { id: "overview", icon: "info", label: t("overview.title") },
        { id: "users", icon: "users", label: t("nav.users"), keywords: "accounts roles suspend" },
        { id: "invites", icon: "plus", label: t("nav.invites"), keywords: "sign-up" },
      ],
    },
    {
      title: t("group.settings"),
      items: settingSections.map((s) => ({ id: `settings:${s}`, icon: sectionIcons[s], label: t(`section.${s}`) })),
    },
    {
      title: t("group.operations"),
      items: [
        { id: "storage", icon: "db", label: t("storage.title") },
        { id: "runners", icon: "cpu", label: t("nav.runners") },
        { id: "audit", icon: "clock", label: t("audit.title") },
      ],
    },
  ];
  return (
    <Page section="act" title={t("frame.title")} subtitle={t("frame.subtitle")}>
      <SettingsLayout label={t("frame.nav")} groups={groups} active={active} renderLink={renderAdminLink}>
        <Outlet />
      </SettingsLayout>
    </Page>
  );
}

/** One pane of the administration area: a heading with an optional hint, then the content. */
export function AdminPane({ title, subtitle, actions, children }: { title: string; subtitle?: string; actions?: ReactNode; children: ReactNode }) {
  return <SettingsSection title={title} description={subtitle} actions={actions}>{children}</SettingsSection>;
}
