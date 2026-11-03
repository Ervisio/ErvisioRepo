import { useState, type FormEvent } from "react";
import { changePassword } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Input } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

/** Password change; the server signs out every other session. */
export function Password() {
  const t = useT("account");
  const notify = useNotify();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const mismatch = again !== "" && again !== next;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (next !== again) return;
    setBusy(true);
    try {
      await changePassword({ currentPassword: current || undefined, newPassword: next });
      setCurrent("");
      setNext("");
      setAgain("");
      notify.ok(t("password.changed"));
    } catch (err) {
      notify.fail(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("password.title")}>
      <form className="acc-form" onSubmit={(e) => void submit(e)}>
        <p className="ui-note">{t("password.hint")}</p>
        <Input label={t("password.current")} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} hint={t("password.currentHint")} />
        <Input label={t("password.new")} type="password" autoComplete="new-password" required maxLength={1024} value={next} onChange={(e) => setNext(e.target.value)} />
        <Input label={t("password.again")} type="password" autoComplete="new-password" required maxLength={1024} value={again} onChange={(e) => setAgain(e.target.value)} error={mismatch ? t("password.mismatch") : undefined} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={busy || mismatch}>{t("password.change")}</Button>
        </div>
      </form>
    </Card>
  );
}
