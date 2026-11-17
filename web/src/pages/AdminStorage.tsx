import { useState } from "react";
import {
  useDeleteStorageTarget, useStorageConfig, useStorageDraftTest, useStorageStatus, useStorageTest,
  type StorageConfig, type StorageStatus, type StorageTargetConfig, type StorageTargetInput,
} from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { AdminPane } from "./admin/AdminFrame.tsx";
import { formatBytes } from "./code/common.tsx";
import { AvatarStorageCard } from "./admin/AvatarStorageCard.tsx";
import { HotCacheCard } from "./admin/HotCacheCard.tsx";
import { StoragePolicyDialog } from "./admin/StoragePolicyDialog.tsx";
import { StorageTargetDialog } from "./admin/StorageTargetDialog.tsx";
import { TestResult } from "./admin/StorageTestResult.tsx";
import { useNotify } from "./account/common.tsx";
import { Badge, Button, Card, ConfirmDialog, EmptyState, Icon, Table, TableBody, TableHead, Td, Th, Tr } from "../ui/index.ts";

type RunningTarget = StorageStatus["targets"][number];

/** The part of a saved target the connection test needs; the server falls back to the stored credentials. */
function draftOf(c: StorageTargetConfig): StorageTargetInput {
  const base = { name: c.name, encrypt: c.encrypt };
  if (c.kind === "sftp") return { ...base, kind: "sftp", host: c.host, port: c.port, user: c.user, root: c.root, maxConns: c.maxConns };
  return { ...base, kind: "s3", endpoint: c.endpoint, bucket: c.bucket, region: c.region, prefix: c.prefix, pathStyle: c.pathStyle };
}

function location(c: StorageTargetConfig): string {
  if (c.kind === "local") return c.path ?? "";
  if (c.kind === "s3") return [c.endpoint, c.bucket, c.prefix].filter(Boolean).join(" / ");
  if (c.kind === "sftp") return `${c.user ?? ""}@${c.host ?? ""}:${c.port ?? 22}${c.root ?? ""}`;
  return "";
}

function DestRow({ target, running, secretsAvailable, onEdit }: {
  target: StorageTargetConfig;
  running: RunningTarget | undefined;
  secretsAvailable: boolean;
  onEdit: (t: StorageTargetConfig) => void;
}) {
  const t = useT("admin");
  const notify = useNotify();
  const named = useStorageTest();
  const draft = useStorageDraftTest();
  const del = useDeleteStorageTarget();
  const [deleting, setDeleting] = useState(false);
  const [open, setOpen] = useState(false);
  const own = target.source === "database";
  const test = own ? draft : named;
  const caps = running
    ? Object.entries(running.caps).filter(([k, v]) => k !== "maxConns" && v === true).map(([k]) => t(`storage.cap.${k}`))
    : [];
  const creds = [
    target.hasAccessKey && t("storage.cred.accessKey"),
    target.hasSecretKey && t("storage.cred.secretKey"),
    target.hasPrivateKey && t("storage.cred.privateKey"),
    target.hasPassphrase && t("storage.cred.passphrase"),
    target.hasPassword && t("storage.cred.password"),
    target.hasKnownHosts && t("storage.cred.knownHosts"),
  ].filter(Boolean);
  const where = location(target);
  const run = () => (own ? draft.mutate(draftOf(target)) : named.mutate(target.name));
  const disk = running?.disk;
  const pct = disk && disk.total > 0 ? Math.round((disk.used / disk.total) * 100) : undefined;
  const pending = target.restartPending || target.removedAfterRestart;
  const failed = test.isError || (test.data && !test.data.ok);
  const detailsId = `st-d-${target.name}`;

  return (
    <li className="as-dw">
      <div className="as-dt">
        <span className="as-nm">
          <b>{target.name}</b>
          {where && <small>{where}</small>}
        </span>
        <span className="ui-tm">{t(`storage.kind.${target.kind}`)}{target.encrypt && ` · ${t("storage.encrypted")}`}</span>
        <span className="as-u">
          {disk ? (
            <>
              {formatBytes(disk.used)} / {formatBytes(disk.total)}
              <span className="as-bar"><i className={pct !== undefined && pct > 90 ? "err" : pct !== undefined && pct > 70 ? "hi" : undefined} style={{ width: `${pct ?? 0}%` }} /></span>
            </>
          ) : (
            <span className="ui-tm">{t("storage.space.unknown")}</span>
          )}
        </span>
        <span>
          {failed ? <span className="ui-chip ui-chip-err">{t("storage.state.failed")}</span>
            : pending ? <span className="ui-chip ui-chip-warn">{t("storage.restartPending")}</span>
            : running ? <span className="ui-chip ui-chip-ok">{t("storage.healthy")}</span>
            : <span className="ui-chip">{t("storage.state.idle")}</span>}
        </span>
        <span className="as-act">
          <Button size="sm" disabled={test.isPending} onClick={run}>{test.isPending ? t("storage.test.running") : t("storage.test.run")}</Button>
          <Button size="sm" variant="ghost" icon="dots" aria-label={t("storage.details", { name: target.name })} aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen((o) => !o)} />
        </span>
      </div>
      {test.isError && (
        <div role="alert" className="ui-inline">
          <Badge tone="err" icon="alert">{t("storage.test.failed")}</Badge> {test.error.message}
        </div>
      )}
      {test.data && <TestResult result={test.data} name={target.name} />}
      {open && (
        <div className="as-more" id={detailsId}>
          <p className="ui-inline">
            <Badge tone={own ? "info" : "neutral"} icon={own ? undefined : "lock"}>{t(`storage.source.${target.source}`)}</Badge>
            {!own && <span>{t("storage.readonly")}</span>}
            {target.removedAfterRestart && <Badge tone="warn">{t("storage.removedAfterRestart")}</Badge>}
            {target.locked && <Badge tone="neutral" icon="lock">{t("storage.target.holds", { objects: target.objects })}</Badge>}
          </p>
          <dl className="ui-kv">
            {own && (
              <div className="ui-kv-row">
                <dt>{t("storage.credentials")}</dt>
                <dd>{creds.length ? creds.join(", ") : t("storage.cred.none")}</dd>
              </div>
            )}
            <div className="ui-kv-row">
              <dt>{t("storage.roles")}</dt>
              <dd className="ui-mono">{target.usedBy.length ? target.usedBy.join(", ") : t("storage.unused")}</dd>
            </div>
            {running && (
              <div className="ui-kv-row">
                <dt>{t("storage.caps")}</dt>
                <dd>{caps.join(", ")}</dd>
              </div>
            )}
          </dl>
          {own && !secretsAvailable && <p className="ui-note">{t("storage.target.nokey")}</p>}
          {own && !target.removedAfterRestart && (
            <div className="acc-inline">
              <Button size="sm" icon="edit" onClick={() => onEdit(target)}>{t("storage.edit")}</Button>
              <ConfirmDialog
                open={deleting}
                onOpenChange={setDeleting}
                trigger={
                  <Button size="sm" variant="danger" icon="trash" disabled={target.usedBy.length > 0 || target.locked} title={target.usedBy.length > 0 ? t("storage.target.inuse") : target.locked ? t("storage.target.holds", { objects: target.objects }) : undefined}>
                    {t("storage.delete")}
                  </Button>
                }
                title={t("storage.target.delete.title", { name: target.name })}
                description={t("storage.target.delete.hint")}
                confirmLabel={t("storage.delete")}
                pending={del.isPending}
                onConfirm={() =>
                  del.mutate(target.name, {
                    onSuccess: () => { setDeleting(false); notify.ok(t("storage.target.delete.done")); },
                    onError: (e) => { setDeleting(false); notify.fail(e); },
                  })
                }
              />
            </div>
          )}
        </div>
      )}
    </li>
  );
}

export function AdminStorage() {
  const t = useT("admin");
  const tc = useT("common");
  const notify = useNotify();
  const status = useStorageStatus();
  const config = useStorageConfig();
  const [targetDialog, setTargetDialog] = useState<{ open: boolean; existing?: StorageTargetConfig }>({ open: false });
  const [policyName, setPolicyName] = useState<string | undefined>();
  const title = t("storage.title");

  if (status.isPending || config.isPending) {
    return (
      <AdminPane title={title}>
        <p role="status">{tc("state.loading")}</p>
      </AdminPane>
    );
  }
  const failed = status.isError ? status : config.isError ? config : undefined;
  if (failed || !status.data || !config.data) {
    return (
      <AdminPane title={title}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={t("storage.error")}
            description={failed?.error?.message}
            action={<Button onClick={() => { void status.refetch(); void config.refetch(); }}>{tc("action.retry")}</Button>}
          />
        </div>
      </AdminPane>
    );
  }

  const s = status.data;
  const cfg: StorageConfig = config.data;
  const planned = new Map(cfg.policies.map((p) => [p.name, p]));
  const running = new Map(s.targets.map((x) => [x.name, x]));
  const saved = () => {
    setTargetDialog({ open: false });
    setPolicyName(undefined);
    notify.ok(t("storage.saved"));
  };

  return (
    <AdminPane title={title} subtitle={t("storage.subtitle")} actions={<Button variant="primary" icon="plus" onClick={() => setTargetDialog({ open: true })}>{t("storage.target.add")}</Button>}>
      {cfg.fallback && (
        <div className="adm-notice" role="alert">
          <Icon name="alert" />
          <div>
            <b>{t("storage.fallback.title")}</b>
            <p className="ui-note">{t("storage.fallback.body", { cause: cfg.fallbackCause ?? "" })}</p>
          </div>
        </div>
      )}
      {cfg.restartRequired && (
        <div className="adm-notice" role="status">
          <Icon name="alert" />
          <div>
            <b>{t("storage.restart.title")}</b>
            <p className="ui-note">{t("storage.restart.body")}</p>
          </div>
        </div>
      )}
      {s.warnings.map((w) => (
        <div key={w.code + (w.policy ?? "")} className="ao-note" role="alert">
          <Icon name="alert" />
          <span><b>{t("storage.warning")}</b> {t(`storage.warning.${w.code}`, { policy: w.policy ?? "" })}</span>
        </div>
      ))}

      <section aria-labelledby="as-pol">
        <h3 id="as-pol" className="ao-h3">{t("storage.policies")}</h3>
        {s.policies.length === 0 ? (
          <EmptyState icon="layers" title={t("storage.policies.empty")} />
        ) : (
          <ul className="as-flow">
            {s.policies.map((p) => {
              const next = planned.get(p.name);
              const primary = next?.primary ?? p.primary;
              const replicas = next?.replicas ?? p.replicas;
              const cache = next ? next.effectiveCache : p.cache;
              const ttl = next ? next.cacheTtlSeconds : p.cacheTtlSeconds;
              return (
                <li key={p.name} className="as-fl">
                  <span className="as-fl-n">
                    <b className="ui-mono">{p.name}</b>
                    {next?.restartPending && <Badge tone="warn">{t("storage.restartPending")}</Badge>}
                  </span>
                  <ol className="as-ch" aria-label={t("storage.chain", { policy: p.name })}>
                    <li className="as-nd">{primary} <small>{t("storage.role.primary")}</small></li>
                    {replicas.map((r) => <li key={r} className="as-nd">{r} <small>{t("storage.role.replica")}</small></li>)}
                    {replicas.length === 0 && (
                      <li>
                        {next ? (
                          <button type="button" className="as-nd as-miss" onClick={() => setPolicyName(p.name)}><Icon name="plus" />{t("storage.addReplica")}</button>
                        ) : (
                          <span className="as-nd as-miss">{t("storage.noReplica")}</span>
                        )}
                      </li>
                    )}
                    {cache && (
                      <li className="as-nd">
                        {cache} <small>{next?.cacheAuto ? t("storage.cache.auto") : t("storage.role.cache")}{ttl > 0 && ` · ${t("storage.cache.ttl", { days: Math.round(ttl / 86400) })}`}</small>
                      </li>
                    )}
                  </ol>
                  <span className="as-fl-s">
                    <span className="ui-tm">{t("storage.objectsN", { n: p.objects })} · {formatBytes(p.bytes)}</span>
                    {p.errors > 0 ? (
                      <span className="ui-chip ui-chip-err">{t("storage.errorsN", { n: p.errors })}</span>
                    ) : p.pendingReplicas > 0 ? (
                      <span className="ui-chip ui-chip-warn">{t("storage.pendingN", { n: p.pendingReplicas })}</span>
                    ) : null}
                    {next && <Button size="sm" variant="ghost" icon="edit" aria-label={t("storage.policy.edit", { policy: p.name })} onClick={() => setPolicyName(p.name)} />}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section aria-labelledby="as-dest">
        <h3 id="as-dest" className="ao-h3">{t("storage.targets")}</h3>
        <p className="ui-note">{t("storage.targets.hint")}</p>
        <div className="as-dt as-dh" aria-hidden="true">
          <span>{t("storage.col.name")}</span><span>{t("storage.kind")}</span><span>{t("storage.col.space")}</span><span>{t("storage.col.state")}</span><span />
        </div>
        <ul className="as-dl">
          {cfg.targets.map((target) => (
            <DestRow
              key={target.name}
              target={target}
              running={running.get(target.name)}
              secretsAvailable={cfg.secretsAvailable}
              onEdit={(x) => setTargetDialog({ open: true, existing: x })}
            />
          ))}
        </ul>
      </section>

      {s.caches.length > 0 && (
        <Card title={t("storage.caches")}>
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("storage.target")}</Th>
                <Th className="ui-num">{t("storage.used")}</Th>
                <Th className="ui-num">{t("storage.budget")}</Th>
                <Th className="ui-num">{t("storage.entries")}</Th>
              </Tr>
            </TableHead>
            <TableBody>
              {s.caches.map((c) => (
                <Tr key={c.target}>
                  <Td>{c.target}</Td>
                  <Td className="ui-num">{formatBytes(c.used)}</Td>
                  <Td className="ui-num">{c.budget > 0 ? formatBytes(c.budget) : t("storage.unlimited")}</Td>
                  <Td className="ui-num">{c.entries}</Td>
                </Tr>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <HotCacheCard setting={cfg.hotCache} caches={s.caches} />

      <AvatarStorageCard />

      <StorageTargetDialog
        open={targetDialog.open}
        existing={targetDialog.existing}
        secretsAvailable={cfg.secretsAvailable}
        onOpenChange={(o) => setTargetDialog((d) => ({ ...d, open: o }))}
        onSaved={saved}
      />
      <StoragePolicyDialog
        policy={policyName ? planned.get(policyName) : undefined}
        targets={cfg.targets}
        onOpenChange={(o) => { if (!o) setPolicyName(undefined); }}
        onSaved={saved}
      />
    </AdminPane>
  );
}
