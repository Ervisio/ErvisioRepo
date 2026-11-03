import { useState, type FormEvent } from "react";
import { SCOPES, useCreateToken, useDeleteToken, useTokens, type NewToken, type TokenScope } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input, Select } from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "./common.tsx";

const DAY = 86_400_000;
// Tokens always expire: the server caps the lifetime at 366 days.
const expiries: Record<string, number> = { "30": 30 * DAY, "90": 90 * DAY, "365": 365 * DAY };

export function Tokens() {
  const t = useT("account");
  const notify = useNotify();
  const tokens = useTokens();
  const create = useCreateToken();
  const del = useDeleteToken();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<TokenScope[]>(["repo:read"]);
  const [expiry, setExpiry] = useState("90");
  const [created, setCreated] = useState<NewToken>();

  function submit(e: FormEvent) {
    e.preventDefault();
    const ms = expiries[expiry] ?? 90 * DAY;
    create.mutate(
      { name: name.trim(), scopes, expiresAt: new Date(Date.now() + ms).toISOString() },
      {
        onSuccess: (tok) => {
          setCreated(tok);
          setName("");
          notify.ok(t("tokens.created"));
        },
        onError: notify.fail,
      },
    );
  }

  const toggle = (s: TokenScope) => setScopes((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]));

  return (
    <>
      <Card title={t("tokens.title")}>
        {created && (
          <div className="acc-block" role="status">
            <p className="ui-note">{t("tokens.once")}</p>
            <Secret label={created.token.name} value={created.secret} />
          </div>
        )}
        <Async query={tokens} empty={t("tokens.empty")}>
          {(rows) =>
            rows.map((tk) => (
              <div key={tk.id} className="ui-row acc-wrap">
                <div className="acc-grow">
                  <div className="acc-title">{tk.name}</div>
                  <div className="ui-note">
                    <span className="ui-mono">{tk.hint}</span> · {tk.scopes.join(", ")} · {t("tokens.expires", { date: formatDate(tk.expiresAt) })}
                    {tk.lastUsedAt ? ` · ${t("tokens.lastUsed", { date: formatDate(tk.lastUsedAt) })}` : ""}
                  </div>
                </div>
                {new Date(tk.expiresAt) < new Date() && <Badge tone="err">{t("tokens.expired")}</Badge>}
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t("tokens.revoke", { name: tk.name })}
                  onClick={() => del.mutate(tk.id, { onSuccess: () => notify.ok(t("tokens.revoked")), onError: notify.fail })}
                >
                  {t("remove")}
                </Button>
              </div>
            ))
          }
        </Async>
      </Card>
      <Card title={t("tokens.new")}>
        <form className="acc-form" onSubmit={submit}>
          <Input label={t("tokens.name")} required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          <fieldset className="acc-scopes">
            <legend>{t("tokens.scopes")}</legend>
            {SCOPES.map((s) => (
              <label key={s} className="acc-check">
                <input type="checkbox" checked={scopes.includes(s)} onChange={() => toggle(s)} />
                <span className="ui-mono">{s}</span>
              </label>
            ))}
          </fieldset>
          <Select
            label={t("tokens.expiry")}
            value={expiry}
            onValueChange={setExpiry}
            options={[
              { value: "30", label: t("tokens.expiry.30") },
              { value: "90", label: t("tokens.expiry.90") },
              { value: "365", label: t("tokens.expiry.365") },
            ]}
          />
          <div className="acc-actions">
            <Button variant="primary" type="submit" disabled={create.isPending || scopes.length === 0}>{t("tokens.generate")}</Button>
          </div>
        </form>
      </Card>
    </>
  );
}
