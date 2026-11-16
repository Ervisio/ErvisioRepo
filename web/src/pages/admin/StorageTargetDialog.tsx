import { useId, useState } from "react";
import {
  usePutStorageTarget, useStorageDraftTest,
  type StorageTargetConfig, type StorageTargetInput,
} from "../../api/useStorage.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, Icon, Input, Select, Toggle } from "../../ui/index.ts";
import { TestResult } from "./StorageTestResult.tsx";

type Kind = "s3" | "sftp";
/** How an SFTP target signs in. */
type Auth = "key" | "password";

type Form = {
  kind: Kind;
  name: string;
  encrypt: boolean;
  endpoint: string;
  bucket: string;
  region: string;
  prefix: string;
  pathStyle: boolean;
  host: string;
  port: string;
  user: string;
  root: string;
  maxConns: string;
  accessKey: string;
  secretKey: string;
  privateKey: string;
  passphrase: string;
  knownHosts: string;
  auth: Auth;
  password: string;
};

const FIELD_PATHS = new Set([
  "name", "kind", "encrypt", "endpoint", "bucket", "region", "prefix", "host", "port", "user", "root", "maxConns",
  "secrets.accessKey", "secrets.secretKey", "secrets.privateKey", "secrets.passphrase", "secrets.knownHosts", "secrets.password",
]);

function initial(existing?: StorageTargetConfig): Form {
  return {
    kind: existing?.kind === "sftp" ? "sftp" : "s3",
    name: existing?.name ?? "",
    encrypt: existing?.encrypt ?? false,
    endpoint: existing?.endpoint ?? "",
    bucket: existing?.bucket ?? "",
    region: existing?.region ?? "",
    prefix: existing?.prefix ?? "",
    pathStyle: existing?.pathStyle ?? false,
    host: existing?.host ?? "",
    port: existing?.port ? String(existing.port) : "22",
    user: existing?.user ?? "",
    root: existing?.root ?? "",
    maxConns: existing?.maxConns ? String(existing.maxConns) : "4",
    accessKey: "",
    secretKey: "",
    privateKey: "",
    passphrase: "",
    knownHosts: "",
    auth: existing?.hasPassword && !existing.hasPrivateKey ? "password" : "key",
    password: "",
  };
}

/** The empty string becomes "left out": the server keeps a stored credential and applies defaults. */
const opt = (s: string): string | undefined => (s.trim() === "" ? undefined : s);

function toInput(f: Form): StorageTargetInput {
  const base = { name: f.name.trim(), kind: f.kind, encrypt: f.encrypt };
  if (f.kind === "s3") {
    return {
      ...base,
      endpoint: opt(f.endpoint), bucket: opt(f.bucket), region: opt(f.region), prefix: opt(f.prefix), pathStyle: f.pathStyle,
      secrets: { accessKey: opt(f.accessKey), secretKey: opt(f.secretKey) },
    };
  }
  const num = (s: string): number | undefined => {
    if (s.trim() === "") return undefined;
    const n = Number(s);
    return Number.isInteger(n) ? n : -1; // the server rejects it with a field message
  };
  return {
    ...base,
    host: opt(f.host), port: num(f.port), user: opt(f.user), root: opt(f.root), maxConns: num(f.maxConns),
    // Only the chosen method is sent: the server then drops the credential of the other one.
    secrets: f.auth === "key"
      ? { privateKey: opt(f.privateKey), passphrase: opt(f.passphrase), knownHosts: opt(f.knownHosts) }
      : { password: opt(f.password), knownHosts: opt(f.knownHosts) },
  };
}

function Area({ label, hint, error, value, onChange, placeholder }: {
  label: string; hint?: string; error?: string; value: string; onChange: (v: string) => void; placeholder?: string;
}) {
  const id = useId();
  const note = error ?? hint;
  return (
    <div className={error ? "ui-field ui-field-err" : "ui-field"}>
      <label htmlFor={id}>{label}</label>
      <textarea
        id={id} className="ui-input ui-mono acc-area" rows={5} spellCheck={false} autoComplete="off" value={value}
        placeholder={placeholder} aria-invalid={error ? true : undefined} aria-describedby={note ? `${id}-note` : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {note && <p id={`${id}-note`} className="ui-note" role={error ? "alert" : undefined}>{note}</p>}
    </div>
  );
}

function TargetForm({ existing, secretsAvailable, onSaved, onClose }: {
  existing?: StorageTargetConfig;
  secretsAvailable: boolean;
  onSaved: () => void;
  onClose: () => void;
}) {
  const t = useT("admin");
  const tu = useT("ui");
  const [f, setF] = useState<Form>(() => initial(existing));
  const put = usePutStorageTarget();
  const test = useStorageDraftTest();
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((p) => ({ ...p, [k]: v }));
  const editing = existing !== undefined;
  // Objects live here: where they resolve cannot change, only the credentials.
  const locked = existing?.locked === true;
  // A new address never inherits the stored credentials.
  const moved = existing !== undefined && (
    f.endpoint.trim() !== (existing.endpoint ?? "") || f.host.trim() !== (existing.host ?? "") || Number(f.port) !== (existing.port ?? 22)
  ) && f.kind === existing.kind;

  // Save and connection test reject a draft the same way: show their field errors under the fields.
  const failure = put.error ?? test.error;
  const problems = failure instanceof ApiError ? (failure.errors ?? []) : [];
  const err = (path: string) => problems.find((p) => p.path === path)?.msg;
  const general = [
    ...problems.filter((p) => !FIELD_PATHS.has(p.path)).map((p) => p.msg),
    ...(failure && !(failure instanceof ApiError && failure.errors?.length) ? [failure.message] : []),
  ];
  const stored = (has: boolean | undefined) => (editing && has && !moved ? t("storage.cred.keep") : moved ? t("storage.cred.moved") : undefined);
  const mustType = (has: boolean | undefined) => !editing || !has || moved;

  const save = () => {
    test.reset();
    put.mutate({ name: f.name.trim(), input: toInput(f) }, { onSuccess: onSaved });
  };
  const runTest = () => {
    put.reset();
    test.mutate(toInput(f));
  };
  const authOptions = [
    { value: "key", label: t("storage.f.auth.key") },
    { value: "password", label: t("storage.f.auth.password") },
  ];
  const kindOptions = [
    { value: "s3", label: t("storage.kind.s3") },
    { value: "sftp", label: t("storage.kind.sftp") },
  ];

  return (
    <form className="acc-form" onSubmit={(e) => { e.preventDefault(); save(); }}>
      {!secretsAvailable && (
        <div className="adm-notice" role="alert">
          <Icon name="alert" />
          <p className="ui-note">{t("storage.target.nokey")}</p>
        </div>
      )}
      <Select label={t("storage.f.kind")} value={f.kind} onValueChange={(v) => set("kind", v === "sftp" ? "sftp" : "s3")} options={kindOptions} disabled={editing} />
      {locked && (
        <div className="adm-notice" role="status">
          <Icon name="lock" />
          <p className="ui-note">{t("storage.target.locked", { objects: existing?.objects ?? 0 })}</p>
        </div>
      )}
      <Input
        label={t("storage.f.name")} mono autoComplete="off" required value={f.name} disabled={editing}
        hint={t("storage.f.name.hint")} error={err("name")} onChange={(e) => set("name", e.target.value)}
      />
      <div className="ui-field">
        <div className="acc-toggle">
          <Toggle label={t("storage.f.encrypt")} checked={f.encrypt} disabled={editing} onChange={(v) => set("encrypt", v)} />
          <span>{t("storage.f.encrypt")}</span>
        </div>
        <p className="ui-note">{err("encrypt") ?? t("storage.f.encrypt.hint")}</p>
      </div>

      {f.kind === "s3" ? (
        <>
          <Input label={t("storage.f.endpoint")} mono required autoComplete="off" placeholder="https://s3.example.com" value={f.endpoint} disabled={locked}
            error={err("endpoint")} onChange={(e) => set("endpoint", e.target.value)} />
          <div className="adm-pair">
            <Input label={t("storage.f.bucket")} mono required autoComplete="off" value={f.bucket} disabled={locked}
            error={err("bucket")} onChange={(e) => set("bucket", e.target.value)} />
            <Input label={t("storage.f.region")} mono autoComplete="off" placeholder="us-east-1" value={f.region} disabled={locked}
            hint={t("storage.f.region.hint")} error={err("region")} onChange={(e) => set("region", e.target.value)} />
          </div>
          <Input label={t("storage.f.prefix")} mono autoComplete="off" placeholder="erepo/" value={f.prefix} disabled={locked}
            hint={t("storage.f.prefix.hint")} error={err("prefix")} onChange={(e) => set("prefix", e.target.value)} />
          <div className="ui-field">
            <div className="acc-toggle">
              <Toggle label={t("storage.f.pathStyle")} checked={f.pathStyle} disabled={locked} onChange={(v) => set("pathStyle", v)} />
              <span>{t("storage.f.pathStyle")}</span>
            </div>
            <p className="ui-note">{t("storage.f.pathStyle.hint")}</p>
          </div>
          <Input label={t("storage.f.accessKey")} type="password" mono autoComplete="off" required={mustType(existing?.hasAccessKey)}
            value={f.accessKey} hint={stored(existing?.hasAccessKey)} error={err("secrets.accessKey")} onChange={(e) => set("accessKey", e.target.value)} />
          <Input label={t("storage.f.secretKey")} type="password" mono autoComplete="new-password" required={mustType(existing?.hasSecretKey)}
            value={f.secretKey} hint={stored(existing?.hasSecretKey)} error={err("secrets.secretKey")} onChange={(e) => set("secretKey", e.target.value)} />
        </>
      ) : (
        <>
          <div className="adm-pair">
            <Input label={t("storage.f.host")} mono required autoComplete="off" value={f.host} disabled={locked}
            error={err("host")} onChange={(e) => set("host", e.target.value)} />
            <Input label={t("storage.f.port")} mono inputMode="numeric" autoComplete="off" value={f.port} disabled={locked}
            error={err("port")} onChange={(e) => set("port", e.target.value)} />
          </div>
          <Input label={t("storage.f.user")} mono required autoComplete="off" value={f.user} disabled={locked}
            error={err("user")} onChange={(e) => set("user", e.target.value)} />
          <Input label={t("storage.f.root")} mono autoComplete="off" placeholder="/srv/erepo" value={f.root} disabled={locked}
            hint={t("storage.f.root.hint")} error={err("root")} onChange={(e) => set("root", e.target.value)} />
          <Input label={t("storage.f.maxConns")} mono inputMode="numeric" autoComplete="off" value={f.maxConns}
            error={err("maxConns")} onChange={(e) => set("maxConns", e.target.value)} />
          <Select label={t("storage.f.auth")} value={f.auth} onValueChange={(v) => set("auth", v === "password" ? "password" : "key")} options={authOptions} />
          {f.auth === "key" ? (
            <>
              <Area label={t("storage.f.privateKey")} value={f.privateKey} onChange={(v) => set("privateKey", v)}
                placeholder={editing && existing?.hasPrivateKey ? t("storage.cred.stored") : "-----BEGIN OPENSSH PRIVATE KEY-----"}
                hint={stored(existing?.hasPrivateKey) ?? t("storage.f.privateKey.hint")} error={err("secrets.privateKey")} />
              <Input label={t("storage.f.passphrase")} type="password" mono autoComplete="new-password" value={f.passphrase}
                hint={f.privateKey !== "" ? t("storage.f.passphrase.new") : (stored(existing?.hasPassphrase) ?? t("storage.f.passphrase.hint"))}
                error={err("secrets.passphrase")} onChange={(e) => set("passphrase", e.target.value)} />
            </>
          ) : (
            <Input label={t("storage.f.password")} type="password" autoComplete="new-password" required={mustType(existing?.hasPassword)} value={f.password}
              hint={stored(existing?.hasPassword) ?? t("storage.f.password.hint")} error={err("secrets.password") ?? err("secrets.privateKey")}
              onChange={(e) => set("password", e.target.value)} />
          )}
          <Area label={t("storage.f.knownHosts")} value={f.knownHosts} onChange={(v) => set("knownHosts", v)}
            placeholder={editing && existing?.hasKnownHosts ? t("storage.cred.stored") : "example.com ssh-ed25519 AAAA..."}
            hint={stored(existing?.hasKnownHosts) ?? t("storage.f.knownHosts.hint")} error={err("secrets.knownHosts")} />
        </>
      )}

      {general.length > 0 && (
        <div role="alert" className="adm-err">
          {general.map((m) => <p key={m} className="adm-err">{m}</p>)}
        </div>
      )}
      {test.isError && general.length === 0 && (
        <div role="alert" className="ui-inline">
          <span className="ui-note">{t("storage.test.failed")}: {t("storage.test.fix")}</span>
        </div>
      )}
      {test.data && <TestResult result={test.data} name={f.name.trim() || undefined} />}

      <div className="ui-dialog-foot">
        <Button icon="play" disabled={test.isPending || put.isPending} onClick={runTest}>
          {test.isPending ? t("storage.test.running") : t("storage.test.run")}
        </Button>
        <DialogClose asChild><Button variant="ghost" onClick={onClose}>{tu("dialog.cancel")}</Button></DialogClose>
        <Button type="submit" variant="primary" disabled={put.isPending || !secretsAvailable}>{t("storage.target.save")}</Button>
      </div>
    </form>
  );
}

/** Add or edit an S3-compatible or SFTP target. Credentials are write-only: blank keeps the stored value. */
export function StorageTargetDialog({ open, onOpenChange, existing, secretsAvailable, onSaved }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing?: StorageTargetConfig;
  secretsAvailable: boolean;
  onSaved: () => void;
}) {
  const t = useT("admin");
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={existing ? t("storage.target.edit", { name: existing.name }) : t("storage.target.add")}
      description={t("storage.target.hint")}
    >
      {open && <TargetForm existing={existing} secretsAvailable={secretsAvailable} onSaved={onSaved} onClose={() => onOpenChange(false)} />}
    </Dialog>
  );
}
