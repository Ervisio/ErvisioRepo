import { useState, type FormEvent } from "react";
import {
  resendEmailVerification,
  useAddEmail,
  useDeleteEmail,
  useEmails,
  useSetPrimaryEmail,
} from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input } from "../../ui/index.ts";
import { Async, useNotify } from "./common.tsx";

/** Email addresses of the account: add (verified by a link), make primary, remove. */
export function Emails() {
  const t = useT("account");
  const notify = useNotify();
  const emails = useEmails();
  const add = useAddEmail();
  const setPrimary = useSetPrimaryEmail();
  const remove = useDeleteEmail();
  const [address, setAddress] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    add.mutate(address.trim(), {
      onSuccess: () => {
        setAddress("");
        notify.ok(t("emails.added"));
      },
      onError: notify.fail,
    });
  }

  return (
    <Card title={t("emails.title")}>
      <Async query={emails} empty={t("emails.empty")}>
        {(rows) =>
          rows.map((m) => (
            <div key={m.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title">
                  <span className="ui-mono">{m.address}</span>
                  {m.primary && <Badge tone="ok">{t("emails.primary")}</Badge>}
                  {!m.verified && <Badge tone="warn">{t("emails.unverified")}</Badge>}
                </div>
              </div>
              <div className="acc-actions">
                {!m.verified && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      void resendEmailVerification(m.id).then(() => notify.ok(t("emails.resent")), (err: unknown) => notify.fail(err))
                    }
                  >
                    {t("emails.resend")}
                  </Button>
                )}
                {!m.primary && (
                  <>
                    <Button
                      size="sm"
                      aria-label={t("emails.makePrimaryFor", { address: m.address })}
                      onClick={() => setPrimary.mutate(m.id, { onSuccess: () => notify.ok(t("emails.primaryChanged")), onError: notify.fail })}
                    >
                      {t("emails.makePrimary")}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={t("emails.removeFor", { address: m.address })}
                      onClick={() => remove.mutate(m.id, { onSuccess: () => notify.ok(t("emails.removed")), onError: notify.fail })}
                    >
                      {t("remove")}
                    </Button>
                  </>
                )}
              </div>
            </div>
          ))
        }
      </Async>
      <form className="acc-inline" onSubmit={submit}>
        <Input label={t("emails.new")} type="email" required autoComplete="email" maxLength={254} value={address} onChange={(e) => setAddress(e.target.value)} hint={t("emails.hint")} />
        <Button type="submit" icon="plus" disabled={add.isPending}>{t("emails.add")}</Button>
      </form>
    </Card>
  );
}
