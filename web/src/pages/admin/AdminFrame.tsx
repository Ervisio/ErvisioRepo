import { Link, Outlet } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useAdminOverview } from "../../api/admin.ts";
import { useUpdateStatus } from "../../api/updates.ts";
import { useMeta } from "../../api/useMeta.ts";
import { useStorageStatus } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon, Page, SettingsLayout, SettingsSection, type SettingsLinkProps, type SettingsNavGroup } from "../../ui/index.ts";
import { usePathname } from "../../ui/shell/nav.tsx";
import { generalSections } from "./settingsCatalog.ts";

/** Sub-navigation id of a pathname under /admin. Auth and email have their own entries; the other setting sections share "settings". */
function activeId(pathname: string): string {
  const [, , first, second] = pathname.split("/");
  if (first === "settings") return second === "auth" || second === "email" ? second : "settings";
  return first || "overview";
}

function renderAdminLink(it: { id: string }, props: SettingsLinkProps): ReactNode {
  switch (it.id) {
    case "users": return <Link to="/admin/users" {...props} />;
    case "invites": return <Link to="/admin/invites" {...props} />;
    case "storage": return <Link to="/admin/storage" {...props} />;
    case "runners": return <Link to="/admin/runners" {...props} />;
    case "updates": return <Link to="/admin/updates" {...props} />;
    case "audit": return <Link to="/admin/audit" {...props} />;
    case "settings": return <Link to="/admin/settings/$section" params={{ section: generalSections[0] }} {...props} />;
    case "auth":
    case "email": return <Link to="/admin/settings/$section" params={{ section: it.id }} {...props} />;
    default: return <Link to="/admin" {...props} />;
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/** Layout route of /admin/*: the settings frame with the administration menu (round 028 a). */
export function AdminFrame() {
  const t = useT("admin");
  const active = activeId(usePathname());
  const meta = useMeta().data;
  const warnings = useStorageStatus().data?.warnings.length ?? 0;
  const counts = useAdminOverview("24h").data?.counts;
  const newer = useUpdateStatus().data?.newer.length ?? 0;
  const n = (v: number | undefined) => (v === undefined ? undefined : String(v));
  const groups: SettingsNavGroup[] = [
    {
      title: "",
      items: [
        { id: "overview", icon: "info", label: t("overview.title") },
        { id: "users", icon: "users", label: t("nav.users"), keywords: "accounts roles suspend", count: n(counts?.users) },
        { id: "invites", icon: "mail", label: t("nav.invites"), keywords: "sign-up", count: counts?.invites ? String(counts.invites) : undefined },
      ],
    },
    {
      title: t("group.infra"),
      items: [
        { id: "storage", icon: "db", label: t("storage.title"), count: warnings > 0 ? t("nav.warnings", { count: warnings }) : undefined, warn: warnings > 0 },
        { id: "runners", icon: "cpu", label: t("nav.runners"), count: counts?.runners ? String(counts.runners) : undefined },
        { id: "updates", icon: "download", label: t("nav.updates"), keywords: "upgrade version release docker", count: newer > 0 ? t("updates.navNew") : undefined, warn: newer > 0 },
      ],
    },
    {
      title: t("group.instance"),
      items: [
        { id: "settings", icon: "gear", label: t("nav.settings"), keywords: "server registration git ssh actions webhooks appearance" },
        { id: "auth", icon: "lock", label: t("section.auth") },
        { id: "email", icon: "mail", label: t("section.email") },
        { id: "audit", icon: "book", label: t("audit.title") },
      ],
    },
  ];
  return (
    <Page section="act" title={t("frame.title")} quiet>
      <SettingsLayout
        label={t("frame.nav")}
        groups={groups}
        active={active}
        renderLink={renderAdminLink}
        wide
        header={{
          avatar: <span className="st-ph-ic" aria-hidden="true"><Icon name="gear" /></span>,
          name: t("frame.title"),
          label: meta ? `${hostOf(meta.publicUrl)} · v${meta.version}` : t("frame.subtitle"),
        }}
      >
        <Outlet />
      </SettingsLayout>
    </Page>
  );
}

/** One pane of the administration area: a heading with an optional hint, then the content. */
export function AdminPane({ title, subtitle, actions, children }: { title: string; subtitle?: string; actions?: ReactNode; children: ReactNode }) {
  return <SettingsSection title={title} description={subtitle} actions={actions}>{children}</SettingsSection>;
}
