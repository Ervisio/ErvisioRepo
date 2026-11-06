import { useState, type FormEvent } from "react";
import { useCreateInstanceInvite } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Button, Card, Input } from "../ui/index.ts";
import { formatDate, Secret, useNotify } from "./account/common.tsx";

/** One-off sign-up invitation. The server has no list or revoke, so only the new link is shown. */
export function InstanceInvite() {
  const t = useT("admin");
  const notify = useNotify();
  const create = useCreateInstanceInvite();
  const [email, setEmail] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate({ email: email.trim() || undefined }, { onSuccess: () => setEmail(""), onError: notify.fail });
  }

  const inv = create.data;
  return (
    <Card title={t("invite.title")}>
      <p className="ui-note">{t("invite.hint")}</p>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("invite.email")} type="email" maxLength={254} value={email} onChange={(e) => setEmail(e.target.value)} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={create.isPending}>{t("invite.create")}</Button>
        </div>
      </form>
      {inv && (
        <>
          <Secret label={t("invite.link")} value={`${window.location.origin}/register?invite=${encodeURIComponent(inv.token)}`} />
          <p className="ui-note">{t("invite.expires", { date: formatDate(inv.expiresAt) })}</p>
        </>
      )}
    </Card>
  );
}
