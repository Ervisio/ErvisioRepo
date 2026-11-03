import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { useMeta } from "../../api/useMeta.ts";
import type { components } from "../../api/schema";
import { useT } from "../../i18n/useT.ts";
import { currentLocale } from "../../i18n/locale.ts";
import { Button, EmptyState, Input } from "../../ui/index.ts";
import { AuthFrame } from "./AuthFrame.tsx";
import { useProblemText } from "./useProblemText.ts";

type Accepted = components["schemas"]["RegisterAccepted"];

export function Register({ invite }: { invite?: string }) {
  const t = useT("auth");
  const problem = useProblemText();
  const meta = useMeta();
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [inviteToken, setInviteToken] = useState(invite ?? "");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [accepted, setAccepted] = useState<Accepted>();

  const needsInvite = meta.data?.registration === "invite";

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const body: components["schemas"]["RegisterRequest"] = { username: username.trim(), email: email.trim(), password, locale: currentLocale() };
      if (inviteToken.trim()) body.inviteToken = inviteToken.trim();
      setAccepted(await request<Accepted>("POST", "/auth/register", body));
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  if (accepted) {
    return (
      <AuthFrame title={t("register.doneTitle")} footer={<Link to="/login">{t("register.toLogin")}</Link>}>
        <div role="status">
          <EmptyState
            icon="check"
            title={accepted.verificationRequired ? t("register.checkMail") : t("register.ready")}
            description={accepted.verificationRequired ? t("register.checkMailBody", { email }) : t("register.readyBody")}
          />
        </div>
      </AuthFrame>
    );
  }

  if (meta.data?.registration === "closed") {
    return (
      <AuthFrame title={t("register.title")} footer={<Link to="/login">{t("register.toLogin")}</Link>}>
        <EmptyState icon="lock" title={t("register.closed")} description={t("register.closedBody")} />
      </AuthFrame>
    );
  }

  return (
    <AuthFrame
      title={t("register.title")}
      subtitle={needsInvite ? t("register.inviteOnly") : t("register.subtitle")}
      footer={
        <>
          {t("register.haveAccount")} <Link to="/login">{t("register.toLogin")}</Link>
        </>
      }
    >
      <form className="auth-form" onSubmit={(e) => void submit(e)}>
        <Input label={t("field.username")} name="username" hint={t("field.username.hint")} autoComplete="username" autoCapitalize="none" spellCheck={false} maxLength={39} required value={username} onChange={(e) => setUsername(e.target.value)} />
        <Input label={t("field.email")} name="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <Input label={t("field.password")} name="password" type="password" autoComplete="new-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {needsInvite && (
          <Input label={t("field.invite")} name="invite" mono required value={inviteToken} onChange={(e) => setInviteToken(e.target.value)} />
        )}
        {error && <p className="auth-error" role="alert">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy}>{t("register.submit")}</Button>
      </form>
    </AuthFrame>
  );
}
