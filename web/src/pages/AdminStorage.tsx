import { useState } from "react";
import {
  useDeleteStorageTarget, useStorageConfig, useStorageDraftTest, useStorageStatus, useStorageTest,
  type StorageConfig, type StorageStatus, type StorageTargetConfig, type StorageTargetInput,
} from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { AdminPane } from "./admin/AdminFrame.tsx";
import { AvatarStorageCard } from "./admin/AvatarStorageCard.tsx";
import { StoragePolicyDialog } from "./admin/StoragePolicyDialog.tsx";
import { StorageTargetDialog } from "./admin/StorageTargetDialog.tsx";
import { TestResult } from "./admin/StorageTestResult.tsx";
import { useNotify } from "./account/common.tsx";
import { Badge, Button, Card, ConfirmDialog, EmptyState, Icon, Table, TableBody, TableHead, Td, Th, Tr } from "../ui/index.ts";

type RunningTarget = StorageStatus["targets"][number];

function formatBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return i === 0 ? `${v} ${units[i]}` : `${v.toFixed(1)} ${units[i]}`;
}

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

function TargetCard({ target, running, secretsAvailable, onEdit }: {
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

  return (
    <Card
      title={target.name}
      actions={
        <>
          <Button size="sm" icon="play" disabled={test.isPending} onClick={run}>
            {test.isPending ? t("storage.test.running") : t("storage.test.run")}
          </Button>
          {own && !target.removedAfterRestart && (
            <>
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
            </>
          )}
        </>
      }
    >
      <p className="ui-inline">
        <Badge tone={own ? "info" : "neutral"} icon={own ? undefined : "lock"}>{t(`storage.source.${target.source}`)}</Badge>
        {!own && <span>{t("storage.readonly")}</span>}
        {target.restartPending && <Badge tone="warn">{t("storage.restartPending")}</Badge>}
        {target.removedAfterRestart && <Badge tone="warn">{t("storage.removedAfterRestart")}</Badge>}
        {target.locked && <Badge tone="neutral" icon="lock">{t("storage.target.holds", { objects: target.objects })}</Badge>}
      </p>
      <dl className="ui-kv">
        <div className="ui-kv-row">
          <dt>{t("storage.kind")}</dt>
          <dd>{target.kind}</dd>
        </div>
        {where && (
          <div className="ui-kv-row">
            <dt>{t("storage.location")}</dt>
            <dd className="ui-mono">{where}</dd>
          </div>
        )}
        <div className="ui-kv-row">
          <dt>{t("storage.encryption")}</dt>
          <dd>
            <Badge tone={target.encrypt ? "ok" : "neutral"}>
              {target.encrypt ? t("storage.encrypted") : t("storage.plain")}
            </Badge>
          </dd>
        </div>
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
      {own && !secretsAvailable && <p className="ui-note acc-pad">{t("storage.target.nokey")}</p>}
      {test.isError && (
        <div role="alert" className="ui-inline">
          <Badge tone="err" icon="alert">{t("storage.test.failed")}</Badge> {test.error.message}
        </div>
      )}
      {test.data && <TestResult result={test.data} name={target.name} />}
    </Card>
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
    <AdminPane title={title} subtitle={t("storage.subtitle")}>
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
        <Card key={w.code + (w.policy ?? "")} role="alert">
          <p className="ui-inline">
            <Badge tone="warn" icon="alert">{t("storage.warning")}</Badge>
            <span>{t(`storage.warning.${w.code}`, { policy: w.policy ?? "" })}</span>
          </p>
        </Card>
      ))}

      <Card title={t("storage.policies")}>
        {s.policies.length === 0 ? (
          <EmptyState icon="layers" title={t("storage.policies.empty")} />
        ) : (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("storage.policy")}</Th>
                <Th>{t("storage.primary")}</Th>
                <Th>{t("storage.replicas")}</Th>
                <Th>{t("storage.cache")}</Th>
                <Th>{t("storage.durability")}</Th>
                <Th className="ui-num">{t("storage.objects")}</Th>
                <Th className="ui-num">{t("storage.size")}</Th>
                <Th className="ui-num">{t("storage.pending")}</Th>
                <Th className="ui-num">{t("storage.errors")}</Th>
              </Tr>
            </TableHead>
            <TableBody>
              {s.policies.map((p) => {
                const next = planned.get(p.name);
                const primary = next?.primary ?? p.primary;
                const replicas = next?.replicas ?? p.replicas;
                const cache = next ? next.cache : p.cache;
                return (
                  <Tr key={p.name}>
                    <Td>
                      <span className="ui-mono">{p.name}</span>
                      {next && (
                        <span className="adm-meta">
                          {next.restartPending && <Badge tone="warn">{t("storage.restartPending")}</Badge>}
                          <Button size="sm" icon="edit" aria-label={t("storage.policy.edit", { policy: p.name })} onClick={() => setPolicyName(p.name)}>
                            {t("storage.edit")}
                          </Button>
                        </span>
                      )}
                    </Td>
                    <Td>{primary}</Td>
                    <Td>{replicas.join(", ") || "—"}</Td>
                    <Td>{cache ?? "—"}</Td>
                    <Td>{p.durability}</Td>
                    <Td className="ui-num">{p.objects}</Td>
                    <Td className="ui-num">{formatBytes(p.bytes)}</Td>
                    <Td className="ui-num">{p.pendingReplicas}</Td>
                    <Td className="ui-num">
                      {p.errors > 0 ? <Badge tone="err">{p.errors}</Badge> : p.errors}
                    </Td>
                  </Tr>
                );
              })}
            </TableBody>
          </Table>
        )}
      </Card>

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

      <AvatarStorageCard />

      <Card title={t("storage.targets")} actions={<Button size="sm" variant="primary" icon="plus" onClick={() => setTargetDialog({ open: true })}>{t("storage.target.add")}</Button>}>
        <p className="ui-note acc-pad">{t("storage.targets.hint")}</p>
      </Card>
      {cfg.targets.map((target) => (
        <TargetCard
          key={target.name}
          target={target}
          running={running.get(target.name)}
          secretsAvailable={cfg.secretsAvailable}
          onEdit={(x) => setTargetDialog({ open: true, existing: x })}
        />
      ))}

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
