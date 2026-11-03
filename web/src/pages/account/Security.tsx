import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import {
  passkeyDelete,
  passkeyRegisterBegin,
  passkeyRegisterFinish,
  regenerateRecoveryCodes,
  totpBegin,
  totpConfirm,
  totpDisable,
  useSecurity,
  type Reauth,
  type SecurityStatus,
  type TotpSetup,
} from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { createCredential, webauthnSupported, type CreationOptionsJSON } from "../../lib/webauthn.ts";
import { Badge, Button, Card, Dialog, DialogClose, Input } from "../../ui/index.ts";
import { formatDate, Secret, useNotify } from "./common.tsx";
import { QrCode } from "./QrCode.tsx";

type Ask = (title: string, action: (r: Reauth) => Promise<void>) => void;

/**
 * Every security change needs a fresh proof of identity: the password, or a current
 * authenticator code when TOTP is on. ask() opens the dialog and runs the action with it.
 */
function useReauth(totp: boolean): { ask: Ask; dialog: ReactNode } {
  const t = useT("account");
  const notify = useNotify();
  const [pending, setPending] = useState<{ title: string; action: (r: Reauth) => Promise<void> }>();
  const [useCode, setUseCode] = useState(false);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!pending) return;
    setBusy(true);
    try {
      await pending.action(useCode ? { code: value.trim() } : { password: value });
      setPending(undefined);
    } catch (err) {
      if (err instanceof DOMException) notify.ok(t("passkeys.cancelled"));
      else notify.fail(err);
    } finally {
      setBusy(false);
      setValue("");
    }
  }

  const dialog = (
    <Dialog
      open={!!pending}
      onOpenChange={(open) => {
        if (!open) setPending(undefined);
      }}
      title={pending?.title ?? ""}
      description={t("reauth.body")}
    >
      <form className="acc-form" onSubmit={(e) => void submit(e)}>
        {useCode ? (
          <Input label={t("totp.code")} mono required autoFocus inputMode="numeric" autoComplete="one-time-code" maxLength={8} value={value} onChange={(e) => setValue(e.target.value)} />
        ) : (
          <Input label={t("password.current")} type="password" autoComplete="current-password" required autoFocus value={value} onChange={(e) => setValue(e.target.value)} />
        )}
        {totp && (
          <Button variant="ghost" onClick={() => { setUseCode(!useCode); setValue(""); }}>
            {useCode ? t("reauth.usePassword") : t("reauth.useCode")}
          </Button>
        )}
        <div className="acc-actions">
          <DialogClose asChild><Button>{t("cancel")}</Button></DialogClose>
          <Button variant="primary" type="submit" disabled={busy}>{t("reauth.confirm")}</Button>
        </div>
      </form>
    </Dialog>
  );
  return {
    ask: (title, action) => {
      setValue("");
      setPending({ title, action });
    },
    dialog,
  };
}

function RecoveryCodes({ codes }: { codes: string[] }) {
  const t = useT("account");
  return (
    <div className="acc-block" role="status">
      <p className="ui-note">{t("recovery.once")}</p>
      <Secret label={t("recovery.codes")} value={codes.join("\n")} multiline />
    </div>
  );
}

function Totp({ status, ask, refresh }: { status: SecurityStatus; ask: Ask; refresh: () => Promise<void> }) {
  const t = useT("account");
  const notify = useNotify();
  const [setup, setSetup] = useState<TotpSetup>();
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[]>();
  const [busy, setBusy] = useState(false);

  async function confirm(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const res = await totpConfirm(code.trim());
      setSetup(undefined);
      setCodes(res.codes);
      notify.ok(t("totp.enabled"));
      await refresh();
    } catch (err) {
      notify.fail(err);
    } finally {
      setBusy(false);
    }
  }

  const begin = () =>
    ask(t("totp.enable"), async (r) => {
      setSetup(await totpBegin(r));
      setCode("");
    });
  const disable = () =>
    ask(t("totp.disableTitle"), async (r) => {
      await totpDisable(r);
      setCodes(undefined);
      notify.ok(t("totp.disabled"));
      await refresh();
    });
  const regenerate = () =>
    ask(t("recovery.regenerate"), async (r) => {
      setCodes((await regenerateRecoveryCodes(r)).codes);
      await refresh();
    });

  return (
    <Card
      title={t("totp.title")}
      actions={
        setup ? undefined : status.totp ? (
          <Button size="sm" variant="danger" onClick={disable}>{t("totp.disable")}</Button>
        ) : (
          <Button size="sm" variant="primary" onClick={begin}>{t("totp.enable")}</Button>
        )
      }
    >
      <div className="acc-block">
        <p className="ui-inline">
          <Badge tone={status.totp ? "ok" : "neutral"}>{status.totp ? t("totp.on") : t("totp.off")}</Badge>
          {status.required && !status.totp && <Badge tone="warn">{t("totp.required")}</Badge>}
          <span>{t("totp.hint")}</span>
        </p>
        {setup && (
          <form className="acc-setup" onSubmit={(e) => void confirm(e)}>
            <QrCode value={setup.uri} label={t("totp.qr")} />
            <div className="acc-form">
              <p className="ui-note">{t("totp.scan")}</p>
              <Secret label={t("totp.secret")} value={setup.secret} />
              <Input label={t("totp.code")} mono required inputMode="numeric" autoComplete="one-time-code" maxLength={8} value={code} onChange={(e) => setCode(e.target.value)} />
              <div className="acc-actions">
                <Button onClick={() => setSetup(undefined)}>{t("cancel")}</Button>
                <Button variant="primary" type="submit" disabled={busy}>{t("totp.confirm")}</Button>
              </div>
            </div>
          </form>
        )}
        {codes && <RecoveryCodes codes={codes} />}
        {(status.totp || status.passkeys.length > 0) && !codes && (
          <p className="ui-inline">
            <span>{t("recovery.left", { count: status.recoveryCodesLeft })}</span>
            <Button size="sm" onClick={regenerate}>{t("recovery.regenerate")}</Button>
          </p>
        )}
      </div>
    </Card>
  );
}

function Passkeys({ status, ask, refresh }: { status: SecurityStatus; ask: Ask; refresh: () => Promise<void> }) {
  const t = useT("account");
  const notify = useNotify();
  const [name, setName] = useState("");

  function add(e: FormEvent) {
    e.preventDefault();
    const label = name.trim();
    ask(t("passkeys.add"), async (r) => {
      const { challenge, options } = await passkeyRegisterBegin(r);
      const credential = await createCredential((options as { publicKey: CreationOptionsJSON }).publicKey);
      await passkeyRegisterFinish({ challenge, name: label, credential });
      setName("");
      notify.ok(t("passkeys.added"));
      await refresh();
    });
  }

  const remove = (id: string, label: string) =>
    ask(t("passkeys.remove", { name: label }), async (r) => {
      await passkeyDelete(id, r);
      notify.ok(t("passkeys.removed"));
      await refresh();
    });

  return (
    <Card title={t("passkeys.title")}>
      {status.passkeys.length === 0 ? (
        <p className="ui-note acc-pad">{t("passkeys.empty")}</p>
      ) : (
        status.passkeys.map((p) => (
          <div key={p.id} className="ui-row">
            <span className="acc-grow">{p.name}</span>
            <span className="ui-note">{p.lastUsedAt ? t("passkeys.lastUsed", { date: formatDate(p.lastUsedAt) }) : t("passkeys.never")}</span>
            <Button size="sm" variant="ghost" aria-label={t("passkeys.remove", { name: p.name })} onClick={() => remove(p.id, p.name)}>
              {t("remove")}
            </Button>
          </div>
        ))
      )}
      {!status.webauthnAvailable ? (
        <p className="ui-note acc-pad">{t("passkeys.disabled")}</p>
      ) : webauthnSupported() ? (
        <form className="acc-inline" onSubmit={add}>
          <Input label={t("passkeys.name")} required maxLength={60} value={name} onChange={(e) => setName(e.target.value)} />
          <Button type="submit" icon="plus">{t("passkeys.add")}</Button>
        </form>
      ) : (
        <p className="ui-note acc-pad">{t("passkeys.unsupported")}</p>
      )}
    </Card>
  );
}

export function Security() {
  const t = useT("account");
  const qc = useQueryClient();
  const security = useSecurity();
  const { ask, dialog } = useReauth(security.data?.totp ?? false);
  const refresh = () => qc.invalidateQueries({ queryKey: ["user", "security"] });

  if (security.isPending) return <p role="status" className="ui-note acc-pad">…</p>;
  if (security.isError) return <p role="alert" className="ui-note acc-pad">{t("error.load")}: {security.error.message}</p>;
  return (
    <>
      <Totp status={security.data} ask={ask} refresh={refresh} />
      <Passkeys status={security.data} ask={ask} refresh={refresh} />
      {dialog}
    </>
  );
}
