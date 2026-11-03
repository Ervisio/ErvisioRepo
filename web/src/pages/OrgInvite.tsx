import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { acceptOrgInvite } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Button, Card, EmptyState, Page } from "../ui/index.ts";
import { useProblemText } from "./auth/useProblemText.ts";

/** Landing page of the org invitation email (/org-invite?token=); the user must be signed in. */
export function OrgInviteAccept({ token }: { token?: string }) {
  const t = useT("org");
  const problem = useProblemText();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  if (!token) {
    return (
      <Page section="pr" title={t("accept.title")}>
        <EmptyState icon="alert" title={t("accept.noToken")} description={t("accept.noTokenBody")} />
      </Page>
    );
  }

  async function accept(tok: string) {
    setBusy(true);
    setError(undefined);
    try {
      const r = await acceptOrgInvite(tok);
      await qc.invalidateQueries({ queryKey: ["orgs"] });
      await navigate({ to: "/orgs/$org", params: { org: r.org } });
    } catch (e) {
      setError(problem(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page section="pr" title={t("accept.title")}>
      <Card title={t("accept.title")}>
        <div className="acc-form">
          <p>{t("accept.body")}</p>
          {error && <p className="auth-error" role="alert">{error}</p>}
          <div className="acc-actions">
            <Link to="/" className="ui-btn ui-btn-secondary">{t("accept.decline")}</Link>
            <Button variant="primary" disabled={busy} onClick={() => void accept(token)}>{t("accept.submit")}</Button>
          </div>
        </div>
      </Card>
    </Page>
  );
}
