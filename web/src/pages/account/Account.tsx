import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { request } from "../../api/http.ts";
import { useSession } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Page, Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/index.ts";
import { Keys } from "./Keys.tsx";
import { Linked } from "./Linked.tsx";
import { Password } from "./Password.tsx";
import { Profile } from "./Profile.tsx";
import { Security } from "./Security.tsx";
import { Sessions } from "./Sessions.tsx";
import { Tokens } from "./Tokens.tsx";

/** Account settings. Rendered inside <Protected>, so the session is already loaded. */
export function Account({ oidcError }: { oidcError?: string }) {
  const t = useT("account");
  const user = useSession().data;
  const qc = useQueryClient();
  const navigate = useNavigate();
  async function signOut() {
    try {
      await request("POST", "/auth/logout");
    } finally {
      qc.clear();
      await navigate({ to: "/login" });
    }
  }
  if (!user) return null;
  return (
    <Page section="issue" title={t("title")} subtitle={`@${user.username}`} actions={<Button variant="ghost" onClick={() => void signOut()}>{t("signOut")}</Button>}>
      <Tabs defaultValue={oidcError ? "linked" : "profile"}>
        <TabsList aria-label={t("tabs")}>
          <TabsTrigger value="profile">{t("tab.profile")}</TabsTrigger>
          <TabsTrigger value="security">{t("tab.security")}</TabsTrigger>
          <TabsTrigger value="tokens">{t("tab.tokens")}</TabsTrigger>
          <TabsTrigger value="keys">{t("tab.keys")}</TabsTrigger>
          <TabsTrigger value="linked">{t("tab.linked")}</TabsTrigger>
        </TabsList>
        <TabsContent value="profile"><Profile user={user} /></TabsContent>
        <TabsContent value="security"><Password /><Security /><Sessions /></TabsContent>
        <TabsContent value="tokens"><Tokens /></TabsContent>
        <TabsContent value="keys"><Keys /></TabsContent>
        <TabsContent value="linked"><Linked oidcError={oidcError} /></TabsContent>
      </Tabs>
    </Page>
  );
}
