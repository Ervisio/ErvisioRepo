import { Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { ervisioCodeDevicesKey } from "../../api/identity.ts";
import type { components } from "../../api/schema";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Input } from "../../ui/index.ts";
import { formatDate } from "../account/common.tsx";
import { AuthFrame } from "./AuthFrame.tsx";
import { CodeBoxes, Details } from "./parts.tsx";
import { useProblemText } from "./useProblemText.ts";

type DeviceAuthorization = components["schemas"]["DeviceAuthorization"];
type Done = "approved" | "denied";

const path = (code: string) => `/user/device/${encodeURIComponent(code.trim())}`;

/** OAuth device flow approval (/login/device): enter the code shown by a CLI, review and authorize it. */
export function DeviceLogin({ userCode, fromCode }: { userCode?: string; fromCode: boolean }) {
  const t = useT("auth");
  const problem = useProblemText();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [code, setCode] = useState(userCode ?? "");
  const [pending, setPending] = useState<DeviceAuthorization>();
  const [done, setDone] = useState<Done>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const looked = useRef(false);
  const ervisioCode = pending?.clientId === "ErvisioCodeRemote";

  async function lookup(value: string) {
    setError(undefined);
    setBusy(true);
    try {
      setPending(await request<DeviceAuthorization>("GET", path(value)));
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!userCode || looked.current) return;
    looked.current = true;
    void lookup(userCode);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once for the pre-filled code
  }, [userCode]);

  async function decide(approve: boolean) {
    if (!pending) return;
    setError(undefined);
    setBusy(true);
    try {
      await request("POST", path(pending.userCode), { approve });
      setDone(approve ? "approved" : "denied");
      if (approve && ervisioCode) {
        void qc.invalidateQueries({ queryKey: ervisioCodeDevicesKey });
        void navigate({ to: "/code", replace: true });
      }
    } catch (err) {
      setError(problem(err));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void lookup(code);
  }

  const home = fromCode || ervisioCode
    ? <Link to="/code">{t("device.codeHub")}</Link>
    : <Link to="/">{t("device.home")}</Link>;
  if (done) {
    return (
      <AuthFrame title={t("device.title")} footer={home}>
        <div role="status">
          <EmptyState
            icon={done === "approved" ? "check" : "x"}
            title={t(done === "approved" ? "device.approved" : "device.denied")}
            description={t(done === "approved" ? "device.approvedBody" : "device.deniedBody")}
          />
        </div>
      </AuthFrame>
    );
  }
  if (pending) {
    return (
      <AuthFrame title={t("device.authorizeTitle", { app: pending.appName })} subtitle={t("device.authorizeSub")} footer={home}>
        <div className="auth-form">
          <CodeBoxes code={pending.userCode} label={t("device.code")} />
          <Details
            rows={[
              { label: t("device.requested"), value: `${formatDate(pending.createdAt)}${pending.requestIp ? ` · ${pending.requestIp}` : ""}` },
              { label: t("device.scopes"), value: pending.requestedScopes.join(", ") || "—", mono: true },
              { label: t("device.granted"), value: pending.scopes.join(", "), mono: true },
            ]}
          />
          <p className="auth-note">{t(ervisioCode ? "device.ervisioCodeWarning" : "device.warning")}</p>
          {error && <p className="auth-error" role="alert">{error}</p>}
          <div className="auth-actions">
            <Button disabled={busy} onClick={() => void decide(false)}>{t("device.cancel")}</Button>
            <Button variant="primary" disabled={busy} onClick={() => void decide(true)}>{t("device.authorize")}</Button>
          </div>
        </div>
      </AuthFrame>
    );
  }
  return (
    <AuthFrame title={t("device.title")} subtitle={t("device.subtitle")} footer={home}>
      <form className="auth-form" onSubmit={submit}>
        <Input
          label={t("device.code")}
          mono
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          placeholder="XXXX-XXXX"
          maxLength={9}
          required
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
        />
        {error && <p className="auth-error" role="alert">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy || code.trim() === ""}>{t("device.continue")}</Button>
      </form>
    </AuthFrame>
  );
}
