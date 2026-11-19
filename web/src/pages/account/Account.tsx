import { Link, useNavigate } from "@tanstack/react-router";
import { useSession } from "../../api/identity.ts";
import { canRegisterApps } from "../../api/oauth.ts";
import { useMeta } from "../../api/useMeta.ts";
import { useT } from "../../i18n/useT.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { Page, SettingsLayout, type SettingsNavGroup, type SettingsNavItem } from "../../ui/index.ts";
import { Appearance } from "./Appearance.tsx";
import { Emails } from "./Emails.tsx";
import { ErvisioCodeDevices } from "./ErvisioCodeDevices.tsx";
import { Keys } from "./Keys.tsx";
import { Linked } from "./Linked.tsx";
import { NotificationPrefs } from "./Notifications.tsx";
import { Password } from "./Password.tsx";
import { Profile } from "./Profile.tsx";
import { Security } from "./Security.tsx";
import { Sessions } from "./Sessions.tsx";
import { Tokens } from "./Tokens.tsx";
import { AuthorizedApps } from "../oauth/AuthorizedApps.tsx";
import { OAuthAppsSection, type AppsNav, type AppTarget } from "../oauth/OAuthApps.tsx";
import { AppsSection, type AppsNav as GhAppsNav, type AppTarget as GhAppTarget } from "../apps/Apps.tsx";
import { InstalledApps, type InstalledNav } from "../apps/Installed.tsx";

export const accountSections = ["emails", "notifications", "appearance", "security", "sessions", "ervisiocode-devices", "tokens", "keys", "linked", "applications", "installations", "apps", "oauth-apps"] as const;
export type AccountSection = (typeof accountSections)[number];

/** Account settings. Rendered inside <Protected>, so the session is already loaded. */
export function Account({ section, app, installation, oidcError }: { section?: AccountSection; app?: string; installation?: number; oidcError?: string }) {
  const t = useT("account");
  const user = useSession().data;
  const meta = useMeta().data;
  const navigate = useNavigate();
  if (!user) return null;
  const appSearch = (to: AppTarget) => ({ section: "oauth-apps" as const, app: to.kind === "list" ? undefined : to.kind === "new" ? "new" : to.clientId });
  const appsNav: AppsNav = {
    link: (to, props) => <Link to="/settings" search={appSearch(to)} className={props.className}>{props.children}</Link>,
    go: (to) => void navigate({ to: "/settings", search: appSearch(to) }),
  };
  const ghSearch = (to: GhAppTarget) => ({ section: "apps" as const, app: to.kind === "list" ? undefined : to.kind === "new" ? "new" : to.slug });
  const ghAppsNav: GhAppsNav = {
    link: (to, props) => <Link to="/settings" search={ghSearch(to)} className={props.className}>{props.children}</Link>,
    go: (to) => void navigate({ to: "/settings", search: ghSearch(to) }),
  };
  const instSearch = (id: number | undefined) => ({ section: "installations" as const, installation: id });
  const installedNav: InstalledNav = {
    link: (id, props) => <Link to="/settings" search={instSearch(id)} className={props.className}>{props.children}</Link>,
    go: (id) => void navigate({ to: "/settings", search: instSearch(id) }),
  };
  const active = section ?? (oidcError ? "linked" : undefined);
  const item = (id: AccountSection | "profile", icon: SettingsNavItem["icon"]): SettingsNavItem => ({ id, icon, label: t(`tab.${id}`) });
  const groups: SettingsNavGroup[] = [
    { title: t("group.personal"), items: [item("profile", "user"), item("emails", "info"), item("notifications", "bell"), item("appearance", "layers")] },
    { title: t("group.access"), items: [item("security", "shield"), item("sessions", "clock"), item("ervisiocode-devices", "terminal"), item("keys", "code"), item("linked", "users"), item("applications", "check"), item("installations", "pkg")] },
    { title: t("group.developer"), items: [item("apps", "grid"), item("oauth-apps", "cpu"), item("tokens", "lock")] },
  ];
  return (
    <Page section="issue" title={t("title")} quiet>
      <SettingsLayout
        label={t("tabs")}
        header={{
          avatar: <Avatar user={user} />,
          name: user.displayName || user.username,
          label: t("header.personal"),
          action: <Link to="/$owner" params={{ owner: user.username }} className="ui-btn ui-btn-secondary">{t("header.profile")}</Link>,
        }}
        groups={groups}
        active={active ?? "profile"}
        renderLink={(it, props) => <Link to="/settings" search={{ section: it.id === "profile" ? undefined : (it.id as AccountSection) }} {...props} />}
      >
        {active === undefined && <Profile user={user} />}
        {active === "emails" && <Emails />}
        {active === "notifications" && <NotificationPrefs />}
        {active === "appearance" && <Appearance />}
        {active === "security" && <><Password /><Security /></>}
        {active === "sessions" && <Sessions />}
        {active === "ervisiocode-devices" && <ErvisioCodeDevices />}
        {active === "tokens" && <Tokens />}
        {active === "keys" && <Keys />}
        {active === "linked" && <Linked oidcError={oidcError} />}
        {active === "applications" && <AuthorizedApps />}
        {active === "installations" && <InstalledApps owner={{ kind: "user" }} installation={installation} nav={installedNav} />}
        {active === "apps" && (
          <AppsSection owner={{ kind: "user" }} app={app} nav={ghAppsNav} canRegister={canRegisterApps(meta?.appRegistration, "user", user.role === "admin")} />
        )}
        {active === "oauth-apps" && (
          <OAuthAppsSection owner={{ kind: "user" }} app={app} nav={appsNav} canRegister={canRegisterApps(meta?.appRegistration, "user", user.role === "admin")} />
        )}
      </SettingsLayout>
    </Page>
  );
}
