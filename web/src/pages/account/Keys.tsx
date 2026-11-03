import { useState, type FormEvent } from "react";
import { useAddGPGKey, useAddSSHKey, useDeleteGPGKey, useDeleteSSHKey, useGPGKeys, useSSHKeys } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Input } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "./common.tsx";

function Area({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <div className="ui-field">
      <label>
        {label}
        <textarea className="ui-input ui-mono acc-area" required rows={5} spellCheck={false} placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} />
      </label>
    </div>
  );
}

function SSH() {
  const t = useT("account");
  const notify = useNotify();
  const keys = useSSHKeys();
  const add = useAddSSHKey();
  const del = useDeleteSSHKey();
  const [title, setTitle] = useState("");
  const [key, setKey] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    add.mutate(
      { title: title.trim(), key: key.trim() },
      {
        onSuccess: () => {
          setTitle("");
          setKey("");
          notify.ok(t("ssh.added"));
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Card title={t("ssh.title")}>
      <Async query={keys} empty={t("ssh.empty")}>
        {(rows) =>
          rows.map((k) => (
            <div key={k.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title">{k.title}</div>
                <div className="ui-note"><span className="ui-mono">{k.fingerprint}</span> · {k.type}{k.bits ? ` ${k.bits}` : ""} · {formatDate(k.createdAt)}</div>
              </div>
              <Button size="sm" variant="ghost" aria-label={t("keys.remove", { name: k.title })} onClick={() => del.mutate(k.id, { onSuccess: () => notify.ok(t("keys.removed")), onError: notify.fail })}>
                {t("remove")}
              </Button>
            </div>
          ))
        }
      </Async>
      <form className="acc-form acc-pad" onSubmit={submit}>
        <Input label={t("ssh.name")} required maxLength={80} value={title} onChange={(e) => setTitle(e.target.value)} />
        <Area label={t("ssh.key")} value={key} onChange={setKey} placeholder="ssh-ed25519 AAAA…" />
        <p className="ui-note">{t("ssh.hint")}</p>
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={add.isPending}>{t("ssh.add")}</Button>
        </div>
      </form>
    </Card>
  );
}

function GPG() {
  const t = useT("account");
  const notify = useNotify();
  const keys = useGPGKeys();
  const add = useAddGPGKey();
  const del = useDeleteGPGKey();
  const [armored, setArmored] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    add.mutate(
      { armoredKey: armored.trim() },
      {
        onSuccess: () => {
          setArmored("");
          notify.ok(t("gpg.added"));
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Card title={t("gpg.title")}>
      <Async query={keys} empty={t("gpg.empty")}>
        {(rows) =>
          rows.map((k) => (
            <div key={k.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title ui-mono">{k.keyId}</div>
                <div className="ui-note">{k.emails.join(", ")}{k.expiresAt ? ` · ${t("tokens.expires", { date: formatDate(k.expiresAt) })}` : ""}</div>
              </div>
              <Button size="sm" variant="ghost" aria-label={t("keys.remove", { name: k.keyId })} onClick={() => del.mutate(k.id, { onSuccess: () => notify.ok(t("keys.removed")), onError: notify.fail })}>
                {t("remove")}
              </Button>
            </div>
          ))
        }
      </Async>
      <form className="acc-form acc-pad" onSubmit={submit}>
        <Area label={t("gpg.key")} value={armored} onChange={setArmored} placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----" />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={add.isPending}>{t("gpg.add")}</Button>
        </div>
      </form>
    </Card>
  );
}

export function Keys() {
  return (
    <>
      <SSH />
      <GPG />
    </>
  );
}
