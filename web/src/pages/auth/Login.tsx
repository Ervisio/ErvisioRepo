import { useQueryClient } from "@tanstack/react-query";
import { Link, useRouter } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { isChallenge, oidcLoginURL, passkeyLoginBegin, passkeyLoginFinish, useProviders, type LoginResult, type User } from "../../api/identity.ts";
import { useMeta } from "../../api/useMeta.ts";
import { safeNext } from "../../auth/Protected.tsx";
import { useT } from "../../i18n/useT.ts";
import { getCredential, webauthnSupported, type RequestOptionsJSON } from "../../lib/webauthn.ts";
import { Button, Input } from "../../ui/index.ts";
import { AuthFrame } from "./AuthFrame.tsx";
import { useProblemText } from "./useProblemText.ts";

export function Login({ next, oidcError }: { next?: string; oidcError?: string }) {
  const t = useT("auth");
  const problem = useProblemText();
  const router = useRouter();
  const qc = useQueryClient();
  const meta = useMeta();
  const providers = useProviders();
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | undefined>(oidcError ? oidcErrorText(t, oidcError) : undefined);
  const [busy, setBusy] = useState(false);
  const target = safeNext(next);

  const done = (user: User) => {
    qc.setQueryData(["user"], user);
    router.history.push(target);
  };

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const res = await request<LoginResult>("POST", "/auth/login", { login: login.trim(), password });
      if (isChallenge(res)) {
        router.history.push(`/login/2fa?next=${encodeURIComponent(target)}`);
      } else {
        done(res);
      }
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  async function withPasskey() {
    setBusy(true);
    setError(undefined);
    try {
      const { challenge, options } = await passkeyLoginBegin();
      const credential = await getCredential((options as { publicKey: RequestOptionsJSON }).publicKey);
      done(await passkeyLoginFinish({ challenge, credential }));
    } catch (err) {
      setError(err instanceof DOMException ? t("passkey.cancelled") : problem(err));
    } finally {
      setBusy(false);
    }
  }

  const registration = meta.data?.registration;
  return (
    <AuthFrame
      title={t("login.title")}
      subtitle={t("login.subtitle")}
      footer={
        registration && registration !== "closed" ? (
          <>
            {t("login.noAccount")} <Link to="/register">{t("login.register")}</Link>
          </>
        ) : undefined
      }
    >
      <form className="auth-form" onSubmit={(e) => void submit(e)}>
        <Input label={t("field.login")} name="login" autoComplete="username webauthn" autoFocus required value={login} onChange={(e) => setLogin(e.target.value)} />
        <Input label={t("field.password")} name="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <p className="auth-error" role="alert">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy}>{t("login.submit")}</Button>
        <Link to="/forgot-password" className="auth-link">{t("login.forgot")}</Link>
      </form>
      {(webauthnSupported() || (providers.data?.length ?? 0) > 0) && <div className="auth-sep" role="separator"><span>{t("login.or")}</span></div>}
      <div className="auth-alt">
        {webauthnSupported() && (
          <Button icon="lock" disabled={busy} onClick={() => void withPasskey()}>{t("login.passkey")}</Button>
        )}
        {providers.data?.map((p) => (
          <a key={p.name} className="ui-btn ui-btn-secondary" href={oidcLoginURL(p.name, target)}>
            {t("login.provider", { name: p.label })}
          </a>
        ))}
      </div>
    </AuthFrame>
  );
}

const OIDC_ERRORS = ["unknown_provider", "invalid_state", "provider_error", "not_linked", "email_taken", "email_unverified", "linked_elsewhere", "already_linked", "link_session"];

/** Text for the oidc_error code the server appends when an OIDC flow fails. */
export function oidcErrorText(t: (key: string) => string, code: string): string {
  return t(OIDC_ERRORS.includes(code) ? `oidc.${code}` : "oidc.server_error");
}
