import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useRouter } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import {
  isUnauthenticated,
  mfaChallenge,
  mfaEnrollTotp,
  mfaEnrollTotpConfirm,
  mfaRecovery,
  mfaTotp,
  mfaWebauthnBegin,
  mfaWebauthnFinish,
  type User,
} from "../../api/identity.ts";
import { safeNext } from "../../auth/Protected.tsx";
import { useT } from "../../i18n/useT.ts";
import { getCredential, webauthnSupported, type RequestOptionsJSON } from "../../lib/webauthn.ts";
import { Button, Input } from "../../ui/index.ts";
import { Secret } from "../account/common.tsx";
import { QrCode } from "../account/QrCode.tsx";
import { AuthFrame } from "./AuthFrame.tsx";
import { OtpInput, Or } from "./parts.tsx";
import { useProblemText } from "./useProblemText.ts";

type Mode = "totp" | "recovery";

/**
 * Second step after the password. The pending challenge (GET /auth/mfa) says which
 * factors the account has; when an organization requires 2FA and the account has none,
 * the user enrols an authenticator app here before the session is created.
 */
export function TwoFactor({ next }: { next?: string }) {
  const t = useT("auth");
  const problem = useProblemText();
  const router = useRouter();
  const qc = useQueryClient();
  const challenge = useQuery({ queryKey: ["auth", "mfa"], retry: false, queryFn: mfaChallenge });
  const [mode, setMode] = useState<Mode>("totp");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [setup, setSetup] = useState<{ secret: string; uri: string }>();
  const [codes, setCodes] = useState<{ user: User; codes: string[] }>();
  const target = safeNext(next);

  const done = (user: User) => {
    qc.setQueryData(["user"], user);
    router.history.push(target);
  };

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof DOMException ? t("passkey.cancelled") : problem(err));
    } finally {
      setBusy(false);
    }
  }

  const back = <Link to="/login" search={{ next }}>{t("twofactor.back")}</Link>;

  if (challenge.isPending) {
    return <AuthFrame title={t("twofactor.title")}><p role="status" className="ui-note">{t("twofactor.loading")}</p></AuthFrame>;
  }
  if (challenge.isError) {
    return (
      <AuthFrame title={t("twofactor.title")} footer={back}>
        <p className="auth-error" role="alert">
          {isUnauthenticated(challenge.error) ? t("twofactor.expired") : problem(challenge.error)}
        </p>
      </AuthFrame>
    );
  }

  const methods = challenge.data.methods;

  if (codes) {
    return (
      <AuthFrame title={t("enroll.doneTitle")} subtitle={t("enroll.doneHint")}>
        <Secret label={t("enroll.codes")} value={codes.codes.join("\n")} multiline />
        <Button variant="primary" onClick={() => done(codes.user)}>{t("enroll.continue")}</Button>
      </AuthFrame>
    );
  }

  if (challenge.data.enrollmentRequired) {
    return (
      <AuthFrame title={t("enroll.title")} subtitle={t("enroll.subtitle")} footer={back}>
        {!setup ? (
          <div className="auth-form">
            {error && <p className="auth-error" role="alert">{error}</p>}
            <Button variant="primary" disabled={busy} onClick={() => void run(async () => setSetup(await mfaEnrollTotp()))}>
              {t("enroll.start")}
            </Button>
          </div>
        ) : (
          <form
            className="auth-form"
            onSubmit={(e: FormEvent) => {
              e.preventDefault();
              void run(async () => {
                const r = await mfaEnrollTotpConfirm(code.trim());
                setCodes({ user: r.user, codes: r.recoveryCodes });
              });
            }}
          >
            <QrCode value={setup.uri} label={t("enroll.qr")} />
            <Secret label={t("enroll.secret")} value={setup.secret} />
            <OtpInput label={t("field.code")} value={code} onChange={setCode} />
            {error && <p className="auth-error" role="alert">{error}</p>}
            <Button variant="primary" type="submit" disabled={busy}>{t("twofactor.submit")}</Button>
          </form>
        )}
      </AuthFrame>
    );
  }

  const recovery = mode === "recovery" || !methods.includes("totp");
  const canKey = methods.includes("webauthn") && webauthnSupported();
  const canSwitch = methods.includes("totp") && methods.includes("recovery");
  return (
    <AuthFrame title={t("twofactor.title")} subtitle={recovery ? t("twofactor.recoveryHint") : t("twofactor.totpHint")} footer={back}>
      <form
        className="auth-form"
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          void run(async () => done(await (recovery ? mfaRecovery(code.trim()) : mfaTotp(code.trim()))));
        }}
      >
        {recovery ? (
          <Input
            label={t("field.recoveryCode")}
            name="code"
            mono
            autoFocus
            required
            autoComplete="one-time-code"
            spellCheck={false}
            autoCapitalize="none"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        ) : (
          <OtpInput label={t("field.code")} value={code} onChange={setCode} />
        )}
        {error && <p className="auth-error" role="alert">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy}>{t("twofactor.submit")}</Button>
        {(canKey || canSwitch) && (
          <>
            <Or>{t("twofactor.other")}</Or>
            <div className="auth-alt">
              {canKey && (
                <Button
                  icon="lock"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      const { options } = await mfaWebauthnBegin();
                      const credential = await getCredential((options as { publicKey: RequestOptionsJSON }).publicKey);
                      done(await mfaWebauthnFinish(credential));
                    })
                  }
                >
                  {t("twofactor.useKey")}
                </Button>
              )}
              {canSwitch && (
                <Button
                  onClick={() => {
                    setMode(recovery ? "totp" : "recovery");
                    setCode("");
                    setError(undefined);
                  }}
                >
                  {recovery ? t("twofactor.useApp") : t("twofactor.useRecovery")}
                </Button>
              )}
            </div>
          </>
        )}
      </form>
    </AuthFrame>
  );
}
