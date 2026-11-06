import { Link } from "@tanstack/react-router";
import { useSession } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Page, SettingsLayout, type SettingsNavGroup, type SettingsNavItem } from "../../ui/index.ts";
import { Appearance } from "./Appearance.tsx";
import { Emails } from "./Emails.tsx";
import { Keys } from "./Keys.tsx";
import { Linked } from "./Linked.tsx";
import { NotificationPrefs } from "./Notifications.tsx";
import { Password } from "./Password.tsx";
import { Profile } from "./Profile.tsx";
import { Security } from "./Security.tsx";
import { Sessions } from "./Sessions.tsx";
import { Tokens } from "./Tokens.tsx";

export const accountSections = ["emails", "notifications", "appearance", "security", "sessions", "tokens", "keys", "linked"] as const;
export type AccountSection = (typeof accountSections)[number];

/** Account settings. Rendered inside <Protected>, so the session is already loaded. */
export function Account({ section, oidcError }: { section?: AccountSection; oidcError?: string }) {
  const t = useT("account");
  const user = useSession().data;
  if (!user) return null;
  const active = section ?? (oidcError ? "linked" : undefined);
  const item = (id: AccountSection | "profile", icon: SettingsNavItem["icon"]): SettingsNavItem => ({ id, icon, label: t(`tab.${id}`) });
  const groups: SettingsNavGroup[] = [
    { title: t("group.personal"), items: [item("profile", "user"), item("emails", "info"), item("notifications", "bell"), item("appearance", "layers")] },
    { title: t("group.access"), items: [item("security", "shield"), item("sessions", "clock"), item("tokens", "lock"), item("keys", "code"), item("linked", "users")] },
  ];
  return (
    <Page section="issue" title={t("title")} subtitle={`@${user.username}`}>
      <SettingsLayout
        label={t("tabs")}
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
        {active === "tokens" && <Tokens />}
        {active === "keys" && <Keys />}
        {active === "linked" && <Linked oidcError={oidcError} />}
      </SettingsLayout>
    </Page>
  );
}
