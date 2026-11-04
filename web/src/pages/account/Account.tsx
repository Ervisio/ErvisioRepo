import { useSession } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Page, Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/index.ts";
import { Keys } from "./Keys.tsx";
import { Linked } from "./Linked.tsx";
import { NotificationPrefs } from "./Notifications.tsx";
import { Password } from "./Password.tsx";
import { Profile } from "./Profile.tsx";
import { Security } from "./Security.tsx";
import { Sessions } from "./Sessions.tsx";
import { Tokens } from "./Tokens.tsx";

/** Account settings. Rendered inside <Protected>, so the session is already loaded. */
export function Account({ oidcError }: { oidcError?: string }) {
  const t = useT("account");
  const user = useSession().data;
  if (!user) return null;
  return (
    <Page section="issue" title={t("title")} subtitle={`@${user.username}`}>
      <Tabs defaultValue={oidcError ? "linked" : "profile"}>
        <TabsList aria-label={t("tabs")}>
          <TabsTrigger value="profile">{t("tab.profile")}</TabsTrigger>
          <TabsTrigger value="security">{t("tab.security")}</TabsTrigger>
          <TabsTrigger value="tokens">{t("tab.tokens")}</TabsTrigger>
          <TabsTrigger value="keys">{t("tab.keys")}</TabsTrigger>
          <TabsTrigger value="linked">{t("tab.linked")}</TabsTrigger>
          <TabsTrigger value="notifications">{t("tab.notifications")}</TabsTrigger>
        </TabsList>
        <TabsContent value="profile"><Profile user={user} /></TabsContent>
        <TabsContent value="security"><Password /><Security /><Sessions /></TabsContent>
        <TabsContent value="tokens"><Tokens /></TabsContent>
        <TabsContent value="keys"><Keys /></TabsContent>
        <TabsContent value="linked"><Linked oidcError={oidcError} /></TabsContent>
        <TabsContent value="notifications"><NotificationPrefs /></TabsContent>
      </Tabs>
    </Page>
  );
}
