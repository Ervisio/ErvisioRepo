import { useState, type FormEvent } from "react";
import {
  WEBHOOK_EVENTS,
  useDeleteHook,
  useDeliveries,
  useDelivery,
  useHooks,
  usePingHook,
  useRedeliver,
  useSaveHook,
  type Webhook,
  type WebhookCreate,
  type WebhookDelivery,
} from "../../api/settings.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, DialogClose, Input, Select, type BadgeTone } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "../account/common.tsx";
import { Gate, Pager, When } from "../code/common.tsx";

const statusTone: Record<WebhookDelivery["status"], BadgeTone> = { ok: "ok", failed: "err", pending: "warn" };

/** Webhooks of a repository or an organization; base is the collection path. */
export function Webhooks({ base, scope }: { base: string; scope: "repo" | "org" }) {
  const t = useT("hooks");
  const notify = useNotify();
  const hooks = useHooks(base);
  const remove = useDeleteHook(base);
  const ping = usePingHook(base);
  const [editing, setEditing] = useState<Webhook | "new" | undefined>(undefined);
  const [open, setOpen] = useState<string | undefined>(undefined);

  return (
    <>
      <Card
        title={t("title")}
        actions={
          <Button variant="primary" icon="plus" onClick={() => setEditing("new")}>
            {t("add")}
          </Button>
        }
      >
        <p className="ui-note acc-pad">{t(`hint.${scope}`)}</p>
        <Async query={hooks} empty={t("empty")}>
          {(rows) => (
            <ul className="rp-list wk-rules">
              {rows.map((h) => (
                <li key={h.id} className="wk-rule wk-hook">
                  <span className="wk-hook-main">
                    <span className="ui-mono wk-rule-pat">{h.url}</span>
                    <span className="wk-rule-sum">
                      {!h.active && <Badge tone="warn">{t("inactive")}</Badge>}
                      <Badge>{t(`format.${h.format}`)}</Badge>
                      <span className="ui-note">{h.events.includes("*") ? t("eventsAll") : t("eventsN", { count: h.events.length })}</span>
                      {h.lastDelivery && (
                        <Badge tone={statusTone[h.lastDelivery.status]}>
                          {t("last", { status: t(`status.${h.lastDelivery.status}`), code: String(h.lastDelivery.statusCode || "") })}
                        </Badge>
                      )}
                    </span>
                  </span>
                  <span className="acc-grow" />
                  <Button size="sm" onClick={() => setOpen(open === h.id ? undefined : h.id)} aria-expanded={open === h.id}>
                    {t("deliveries")}
                  </Button>
                  <Button size="sm" disabled={ping.isPending} onClick={() => ping.mutate(h.id, { onSuccess: () => notify.ok(t("pinged")), onError: notify.fail })}>
                    {t("ping")}
                  </Button>
                  <Button size="sm" icon="edit" onClick={() => setEditing(h)}>
                    {t("edit")}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    icon="trash"
                    aria-label={t("deleteOf", { url: h.url })}
                    onClick={() => window.confirm(t("deleteConfirm", { url: h.url })) && remove.mutate(h.id, { onSuccess: () => notify.ok(t("deleted")), onError: notify.fail })}
                  />
                  {open === h.id && <Deliveries base={base} hookId={h.id} />}
                </li>
              ))}
            </ul>
          )}
        </Async>
      </Card>
      <Dialog open={editing !== undefined} onOpenChange={(o) => !o && setEditing(undefined)} title={editing === "new" ? t("newTitle") : t("editTitle")} description={t("formHint")}>
        {editing !== undefined && <HookEditor base={base} hook={editing === "new" ? undefined : editing} close={() => setEditing(undefined)} />}
      </Dialog>
    </>
  );
}

function HookEditor({ base, hook, close }: { base: string; hook?: Webhook; close: () => void }) {
  const t = useT("hooks");
  const notify = useNotify();
  const save = useSaveHook(base);
  const [url, setUrl] = useState(hook?.url ?? "");
  const [contentType, setContentType] = useState<NonNullable<WebhookCreate["contentType"]>>(hook?.contentType ?? "json");
  const [format, setFormat] = useState<NonNullable<WebhookCreate["format"]>>(hook?.format ?? "github");
  const [secret, setSecret] = useState("");
  const [all, setAll] = useState(hook ? hook.events.includes("*") : false);
  const [events, setEvents] = useState<string[]>(hook ? hook.events.filter((e) => e !== "*") : ["push"]);
  const [active, setActive] = useState(hook?.active ?? true);
  const toggle = (e: string) => setEvents((xs) => (xs.includes(e) ? xs.filter((x) => x !== e) : [...xs, e]));

  function submit(e: FormEvent) {
    e.preventDefault();
    const body: WebhookCreate = { url: url.trim(), contentType, format, events: all ? ["*"] : events, active, ...(secret ? { secret } : {}) };
    save.mutate({ id: hook?.id, body }, { onSuccess: () => (notify.ok(t("saved")), close()), onError: notify.fail });
  }

  return (
    <form className="acc-form" onSubmit={submit}>
      <Input label={t("url")} type="url" required mono autoComplete="off" placeholder="https://example.com/hook" value={url} onChange={(e) => setUrl(e.target.value)} />
      <Select label={t("contentType")} value={contentType} onValueChange={(v) => setContentType(v as typeof contentType)} options={[{ value: "json", label: "application/json" }, { value: "form", label: "application/x-www-form-urlencoded" }]} />
      <Select label={t("formatLabel")} value={format} onValueChange={(v) => setFormat(v as typeof format)} options={(["github", "ervisio"] as const).map((f) => ({ value: f, label: t(`format.${f}`) }))} />
      <p className="ui-note">{t(`format.${format}.hint`)}</p>
      <Input label={t("secret")} type="password" autoComplete="new-password" hint={hook?.hasSecret ? t("secretKeep") : t("secretHint")} value={secret} onChange={(e) => setSecret(e.target.value)} />
      <fieldset className="wk-fieldset">
        <legend>{t("events")}</legend>
        <label className="acc-check">
          <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
          <span>{t("eventsAllLabel")}</span>
        </label>
        {!all && (
          <div className="wk-evgrid">
            {WEBHOOK_EVENTS.map((ev) => (
              <label key={ev} className="acc-check">
                <input type="checkbox" checked={events.includes(ev)} onChange={() => toggle(ev)} />
                <span className="ui-mono">{ev}</span>
              </label>
            ))}
          </div>
        )}
      </fieldset>
      <label className="acc-check">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} />
        <span>{t("active")}</span>
      </label>
      <div className="acc-actions">
        <DialogClose asChild>
          <Button variant="ghost">{t("cancel")}</Button>
        </DialogClose>
        <Button variant="primary" type="submit" disabled={save.isPending || !url.trim() || (!all && events.length === 0)}>
          {t("save")}
        </Button>
      </div>
    </form>
  );
}

function Deliveries({ base, hookId }: { base: string; hookId: string }) {
  const t = useT("hooks");
  const notify = useNotify();
  const [page, setPage] = useState(1);
  const [shown, setShown] = useState<string | undefined>(undefined);
  const list = useDeliveries(base, hookId, page);
  const redeliver = useRedeliver(base, hookId);

  return (
    <div className="wk-deliveries">
      <Gate query={list}>
        {(res) =>
          res.items.length === 0 ? (
            <p className="ui-note">{t("noDeliveries")}</p>
          ) : (
            <>
              <ul className="wk-dlist">
                {res.items.map((d) => (
                  <li key={d.id}>
                    <div className="wk-dl-row">
                      <Badge tone={statusTone[d.status]}>{d.statusCode ? String(d.statusCode) : t(`status.${d.status}`)}</Badge>
                      <span className="ui-mono">
                        {d.event}
                        {d.action ? `.${d.action}` : ""}
                      </span>
                      {d.redelivery && <Badge>{t("redelivery")}</Badge>}
                      <span className="ui-note">{t("duration", { ms: d.durationMs })}</span>
                      <When iso={d.createdAt} />
                      <span className="acc-grow" />
                      <Button size="sm" variant="ghost" aria-expanded={shown === d.id} onClick={() => setShown(shown === d.id ? undefined : d.id)}>
                        {shown === d.id ? t("hide") : t("details")}
                      </Button>
                      <Button size="sm" disabled={redeliver.isPending} onClick={() => redeliver.mutate(d.id, { onSuccess: () => notify.ok(t("redelivered")), onError: notify.fail })}>
                        {t("redeliver")}
                      </Button>
                    </div>
                    {d.error && <p className="ui-note wk-dl-err">{d.error}</p>}
                    {shown === d.id && <DeliveryDetail base={base} hookId={hookId} id={d.id} />}
                  </li>
                ))}
              </ul>
              <Pager page={page} hasMore={res.hasMore} onPage={setPage} />
            </>
          )
        }
      </Gate>
    </div>
  );
}

function Headers({ h }: { h: Record<string, string> }) {
  return <pre className="wk-pre">{Object.entries(h).map(([k, v]) => `${k}: ${v}`).join("\n")}</pre>;
}

function DeliveryDetail({ base, hookId, id }: { base: string; hookId: string; id: string }) {
  const t = useT("hooks");
  const q = useDelivery(base, hookId, id);
  return (
    <Gate query={q}>
      {(d) => (
        <div className="wk-dl-detail">
          <p className="ui-note">
            {t("guid")} <span className="ui-mono">{d.delivery.guid}</span>
          </p>
          <p className="ui-note">
            {t("sentTo")} <span className="ui-mono">{d.url}</span> {d.delivery.deliveredAt && formatDate(d.delivery.deliveredAt)}
          </p>
          <h4>{t("request")}</h4>
          <Headers h={d.requestHeaders} />
          <pre className="wk-pre">{d.requestBody}</pre>
          <h4>{t("response")}</h4>
          <Headers h={d.responseHeaders} />
          <pre className="wk-pre">{d.responseBody || t("emptyBody")}</pre>
        </div>
      )}
    </Gate>
  );
}
