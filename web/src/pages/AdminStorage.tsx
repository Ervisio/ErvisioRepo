import { useStorageStatus, useStorageTest, type StorageCheckResult, type StorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Page, Table, TableBody, TableHead, Td, Th, Tr } from "../ui/index.ts";

type Target = StorageStatus["targets"][number];

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

function TestResult({ result }: { result: StorageCheckResult }) {
  const t = useT("admin");
  return (
    <div role="status" aria-label={t("storage.test.result", { target: result.target })}>
      <p className="ui-inline">
        <Badge tone={result.ok ? "ok" : "err"}>{result.ok ? t("storage.test.ok") : t("storage.test.failed")}</Badge>
        <span>{t("storage.test.duration", { ms: result.durationMs })}</span>
      </p>
      <Table>
        <TableHead>
          <Tr>
            <Th>{t("storage.test.step")}</Th>
            <Th>{t("storage.test.outcome")}</Th>
            <Th className="ui-num">{t("storage.test.time")}</Th>
          </Tr>
        </TableHead>
        <TableBody>
          {result.steps.map((s) => (
            <Tr key={s.name}>
              <Td className="ui-mono">{t(`storage.step.${s.name}`)}</Td>
              <Td>
                <Badge tone={s.ok ? "ok" : "err"}>{s.ok ? t("storage.test.ok") : t("storage.test.failed")}</Badge>
                {s.error && <span className="ui-mono"> {s.error}</span>}
              </Td>
              <Td className="ui-num">{t("storage.test.duration", { ms: s.durationMs })}</Td>
            </Tr>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function TargetCard({ target }: { target: Target }) {
  const t = useT("admin");
  const test = useStorageTest();
  const caps = Object.entries(target.caps)
    .filter(([k, v]) => k !== "maxConns" && v === true)
    .map(([k]) => t(`storage.cap.${k}`));
  return (
    <Card
      title={target.name}
      actions={
        <Button size="sm" icon="play" disabled={test.isPending} onClick={() => test.mutate(target.name)}>
          {test.isPending ? t("storage.test.running") : t("storage.test.run")}
        </Button>
      }
    >
      <dl className="ui-kv">
        <div className="ui-kv-row">
          <dt>{t("storage.kind")}</dt>
          <dd>{target.kind}</dd>
        </div>
        <div className="ui-kv-row">
          <dt>{t("storage.encryption")}</dt>
          <dd>
            <Badge tone={target.encrypted ? "ok" : "neutral"}>
              {target.encrypted ? t("storage.encrypted") : t("storage.plain")}
            </Badge>
          </dd>
        </div>
        <div className="ui-kv-row">
          <dt>{t("storage.roles")}</dt>
          <dd className="ui-mono">{target.roles.length ? target.roles.join(", ") : t("storage.unused")}</dd>
        </div>
        <div className="ui-kv-row">
          <dt>{t("storage.caps")}</dt>
          <dd>{caps.join(", ")}</dd>
        </div>
      </dl>
      {test.isError && (
        <div role="alert" className="ui-inline">
          <Badge tone="err">{t("storage.test.failed")}</Badge> {test.error.message}
        </div>
      )}
      {test.data && <TestResult result={test.data} />}
    </Card>
  );
}

export function AdminStorage() {
  const t = useT("admin");
  const tc = useT("common");
  const status = useStorageStatus();
  const title = t("storage.title");

  if (status.isPending) {
    return (
      <Page title={title}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (status.isError) {
    return (
      <Page title={title}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={t("storage.error")}
            description={status.error.message}
            action={<Button onClick={() => void status.refetch()}>{tc("action.retry")}</Button>}
          />
        </div>
      </Page>
    );
  }

  const s = status.data;
  return (
    <Page title={title} subtitle={t("storage.subtitle")}>
      {s.warnings.map((w) => (
        <Card key={w.code + (w.policy ?? "")} role="alert">
          <p className="ui-inline">
            <Badge tone="warn">{t("storage.warning")}</Badge>
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
              {s.policies.map((p) => (
                <Tr key={p.name}>
                  <Td className="ui-mono">{p.name}</Td>
                  <Td>{p.primary}</Td>
                  <Td>{p.replicas.join(", ") || "—"}</Td>
                  <Td>{p.cache ?? "—"}</Td>
                  <Td>{p.durability}</Td>
                  <Td className="ui-num">{p.objects}</Td>
                  <Td className="ui-num">{formatBytes(p.bytes)}</Td>
                  <Td className="ui-num">{p.pendingReplicas}</Td>
                  <Td className="ui-num">
                    {p.errors > 0 ? <Badge tone="err">{p.errors}</Badge> : p.errors}
                  </Td>
                </Tr>
              ))}
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

      {s.targets.map((target) => (
        <TargetCard key={target.name} target={target} />
      ))}
    </Page>
  );
}
