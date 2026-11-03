import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Input } from "../../ui/index.ts";
import { AuthFrame } from "./AuthFrame.tsx";
import { useProblemText } from "./useProblemText.ts";

/** Asks for a reset mail. The answer is the same whether or not the address exists. */
export function ForgotPassword() {
  const t = useT("auth");
  const problem = useProblemText();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await request("POST", "/auth/password/reset", { email: email.trim() });
      setSent(true);
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthFrame title={t("forgot.title")} subtitle={sent ? undefined : t("forgot.subtitle")} footer={<Link to="/login">{t("twofactor.back")}</Link>}>
      {sent ? (
        <div role="status">
          <EmptyState icon="check" title={t("forgot.sent")} description={t("forgot.sentBody")} />
        </div>
      ) : (
        <form className="auth-form" onSubmit={(e) => void submit(e)}>
          <Input label={t("field.email")} type="email" autoComplete="email" autoFocus required value={email} onChange={(e) => setEmail(e.target.value)} />
          {error && <p className="auth-error" role="alert">{error}</p>}
          <Button variant="primary" type="submit" disabled={busy}>{t("forgot.submit")}</Button>
        </form>
      )}
    </AuthFrame>
  );
}

/** Sets the new password with ?token= from the mail. */
export function ResetPassword({ token }: { token?: string }) {
  const t = useT("auth");
  const problem = useProblemText();
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password !== again) {
      setError(t("reset.mismatch"));
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await request("POST", "/auth/password/reset/confirm", { token, password });
      setDone(true);
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  const toLogin = <Link to="/login">{t("register.toLogin")}</Link>;
  if (!token) {
    return (
      <AuthFrame title={t("reset.title")} footer={<Link to="/forgot-password">{t("forgot.title")}</Link>}>
        <div role="alert">
          <EmptyState icon="alert" title={t("reset.noToken")} description={t("reset.noTokenBody")} />
        </div>
      </AuthFrame>
    );
  }
  if (done) {
    return (
      <AuthFrame title={t("reset.title")} footer={toLogin}>
        <div role="status">
          <EmptyState icon="check" title={t("reset.done")} description={t("reset.doneBody")} />
        </div>
      </AuthFrame>
    );
  }
  return (
    <AuthFrame title={t("reset.title")} subtitle={t("reset.subtitle")} footer={toLogin}>
      <form className="auth-form" onSubmit={(e) => void submit(e)}>
        <Input label={t("field.newPassword")} type="password" autoComplete="new-password" autoFocus required value={password} onChange={(e) => setPassword(e.target.value)} />
        <Input label={t("field.passwordAgain")} type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
        {error && <p className="auth-error" role="alert">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy}>{t("reset.submit")}</Button>
      </form>
    </AuthFrame>
  );
}
