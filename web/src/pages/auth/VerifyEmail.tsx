import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Input } from "../../ui/index.ts";
import { AuthFrame } from "./AuthFrame.tsx";
import { useProblemText } from "./useProblemText.ts";

type State = "idle" | "working" | "ok" | "failed";

/** Confirms an address with ?token= from the mail; also offers to send a new mail. */
export function VerifyEmail({ token }: { token?: string }) {
  const t = useT("auth");
  const problem = useProblemText();
  const [state, setState] = useState<State>(token ? "working" : "idle");
  const [error, setError] = useState<string>();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    request("POST", "/auth/email/verify", { token }).then(
      () => setState("ok"),
      (err: unknown) => {
        setError(problem(err));
        setState("failed");
      },
    );
  }, [token, problem]);

  async function resend(e: FormEvent) {
    e.preventDefault();
    setError(undefined);
    try {
      await request("POST", "/auth/email/resend", { email: email.trim() });
      setSent(true);
    } catch (err) {
      setError(problem(err));
    }
  }

  const toLogin = <Link to="/login">{t("register.toLogin")}</Link>;
  if (state === "working") {
    return (
      <AuthFrame title={t("verify.title")}>
        <p role="status">{t("verify.working")}</p>
      </AuthFrame>
    );
  }
  if (state === "ok") {
    return (
      <AuthFrame title={t("verify.title")} footer={toLogin}>
        <div role="status">
          <EmptyState icon="check" title={t("verify.ok")} description={t("verify.okBody")} />
        </div>
      </AuthFrame>
    );
  }
  return (
    <AuthFrame title={t("verify.title")} subtitle={state === "failed" ? t("verify.failed") : t("verify.resendHint")} footer={toLogin}>
      {sent ? (
        <p role="status">{t("verify.sent")}</p>
      ) : (
        <form className="auth-form" onSubmit={(e) => void resend(e)}>
          <Input label={t("field.email")} type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          {error && <p className="auth-error" role="alert">{error}</p>}
          <Button variant="primary" type="submit">{t("verify.resend")}</Button>
        </form>
      )}
    </AuthFrame>
  );
}
