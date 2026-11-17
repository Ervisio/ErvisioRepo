import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useAdminOverview, type AdminOverview as Overview, type AdminService, type OverviewRange } from "../api/admin.ts";
import { useAudit } from "../api/identity.ts";
import { useStorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Button, EmptyState, Icon, type IconName } from "../ui/index.ts";
import { AdminPane } from "./admin/AdminFrame.tsx";
import { AreaChart, JobBars, type ChartRow } from "./admin/Charts.tsx";
import { formatClock } from "./admin/format.ts";
import { formatBytes, When } from "./code/common.tsx";

const ranges: OverviewRange[] = ["24h", "7d"];

const serviceIcon: Record<AdminService["id"], IconName> = {
  server: "terminal", database: "db", storage: "pkg", hotCache: "clock", runners: "cpu", email: "mail", jobs: "gear",
};
const statusTone: Record<AdminService["status"], string> = { ok: "ui-chip ui-chip-ok", warn: "ui-chip ui-chip-warn", err: "ui-chip ui-chip-err", off: "ui-chip" };
const nf = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

/** Time of bucket i of the overview. */
const bucketAt = (o: Overview, i: number) => new Date(new Date(o.start).getTime() + i * o.stepSeconds * 1000);

function Charts({ o }: { o: Overview }) {
  const t = useT("admin");
  const at = (i: number) => formatClock(bucketAt(o, i));
  const reqRows: ChartRow[] = o.requests.map((v, i) => ({ label: at(i), value: t("ov.perMin", { n: nf.format(v) }) }));
  const ok = o.jobs.reduce((n, b) => n + b.success, 0);
  const failed = o.jobs.reduce((n, b) => n + b.failure, 0);
  const jobRows: ChartRow[] = o.jobs.map((b, i) => ({ label: at(i), value: t("ov.jobsAt", { ok: b.success, failed: b.failure }) }));
  const last = o.disk.at(-1);
  const first = o.disk[0];
  const diskRows: ChartRow[] = o.disk.map((d) => ({ label: formatClock(new Date(d.at)), value: `${formatBytes(d.used)} / ${formatBytes(d.total)}` }));
  const growth = first && last && last !== first ? (last.used - first.used) / ((new Date(last.at).getTime() - new Date(first.at).getTime()) / 86_400_000) : undefined;
  const startLabel = formatClock(new Date(o.start));
  return (
    <div className="ao-chs">
      <AreaChart title={t("ov.requests")} value={nf.format(o.requestsNow)} axis={[startLabel, t("ov.now")]} values={o.requests} rows={reqRows} />
      <JobBars
        title={t("ov.jobs")}
        value={nf.format(ok + failed)}
        axis={[t("ov.succeeded", { n: ok }), <span key="f" className={failed > 0 ? "ao-bad" : undefined}>{t("ov.failed", { n: failed })}</span>]}
        bars={o.jobs}
        rows={jobRows}
      />
      {last ? (
        <AreaChart
          title={o.diskTarget ? t("ov.diskOf", { target: o.diskTarget }) : t("ov.disk")}
          value={`${Math.round((last.used / Math.max(last.total, 1)) * 100)}%`}
          axis={[`${formatBytes(last.used)} / ${formatBytes(last.total)}`, growth !== undefined ? t("ov.perDay", { size: `${growth >= 0 ? "+" : "−"}${formatBytes(Math.abs(growth))}` }) : ""]}
          values={o.disk.map((d) => d.used)}
          max={last.total}
          rows={diskRows}
        />
      ) : (
        <figure className="ao-ch">
          <figcaption className="ao-ch-k"><span>{t("ov.disk")}</span><b>—</b></figcaption>
          <p className="ui-note">{t("ov.noDisk")}</p>
        </figure>
      )}
    </div>
  );
}

function serviceDetail(s: AdminService, t: (k: string, p?: Record<string, string | number>) => string) {
  switch (s.id) {
    case "server": return t("svc.server.d", { version: s.version ?? "" });
    case "database": return s.bytes !== undefined ? t("svc.database.d", { size: formatBytes(s.bytes) }) : t("svc.database.err");
    case "storage": return s.status === "off" ? t("svc.off") : s.warnings ? t("svc.storage.warn", { n: s.warnings }) : t("svc.storage.ok");
    case "hotCache":
      if (s.status === "off") return t("svc.off");
      return s.budget ? t("svc.hot.of", { used: formatBytes(s.bytes ?? 0), budget: formatBytes(s.budget) }) : t("svc.hot.used", { used: formatBytes(s.bytes ?? 0) });
    case "runners": return s.total ? t("svc.runners.d", { online: s.online ?? 0, total: s.total, queued: s.queued ?? 0 }) : t("svc.runners.none");
    case "email": return s.host ? t("svc.email.d", { host: s.host }) : t("svc.email.off");
    case "jobs": return s.status === "ok" ? t("svc.jobs.ok") : t("svc.jobs.err");
  }
}

function serviceLink(id: AdminService["id"]) {
  switch (id) {
    case "storage":
    case "hotCache": return { to: "/admin/storage" } as const;
    case "runners": return { to: "/admin/runners" } as const;
    case "email": return { to: "/admin/settings/$section", params: { section: "email" } } as const;
    case "server": return { to: "/admin/settings/$section", params: { section: "server" } } as const;
    default: return undefined;
  }
}

function Services({ o, onRecheck, checking }: { o: Overview; onRecheck: () => void; checking: boolean }) {
  const t = useT("admin");
  const server = o.services.find((s) => s.id === "server");
  const since = server?.uptimeSeconds !== undefined ? new Date(new Date(o.checkedAt).getTime() - server.uptimeSeconds * 1000).toISOString() : undefined;
  return (
    <section aria-labelledby="ao-svc">
      <div className="ao-sh">
        <h3 id="ao-svc">{t("ov.services")}</h3>
        <span className="ui-tm">{t("ov.checked")} <When iso={o.checkedAt} /></span>
        <Button size="sm" icon="clock" onClick={onRecheck} disabled={checking}>{t("ov.recheck")}</Button>
      </div>
      <div className="ui-box">
        <div className="ui-in">
          <div className="ao-sv ao-sv-h" aria-hidden="true"><span /><span>{t("ov.service")}</span><span>{t("ov.state")}</span><span /></div>
          <ul className="ao-svl">
            {o.services.map((s) => {
              const link = serviceLink(s.id);
              return (
                <li key={s.id} className={s.status === "warn" || s.status === "err" ? `ao-sv ao-sv-${s.status}` : "ao-sv"}>
                  <span className="ao-ic"><Icon name={serviceIcon[s.id]} /></span>
                  <span className="ao-sv-m">
                    <b>{t(`svc.${s.id}`)}</b>
                    <small>
                      {serviceDetail(s, t)}
                      {s.id === "server" && since && <> · {t("svc.server.up")} <When iso={since} /></>}
                    </small>
                  </span>
                  <span><span className={statusTone[s.status]}>{t(`svc.status.${s.status}`)}</span></span>
                  {link ? (
                    <Link {...link} className="ui-btn ui-btn-sm ui-btn-ghost ao-sv-a" aria-label={t("ov.openService", { name: t(`svc.${s.id}`) })}>
                      {s.status === "warn" || s.status === "err" ? t("ov.fix") : t("ov.details")}
                    </Link>
                  ) : <span />}
                </li>
              );
            })}
          </ul>
        </div>
      </div>
    </section>
  );
}

function AuditTail() {
  const t = useT("admin");
  const audit = useAudit({ action: "", actor: "" }, undefined);
  const entries = audit.data?.entries.slice(0, 6) ?? [];
  return (
    <section aria-labelledby="ao-audit">
      <div className="ui-sech">
        <h2 id="ao-audit" className="ao-h3">{t("audit.title")}</h2>
        <Link to="/admin/audit">{t("ov.openAudit")}</Link>
      </div>
      {audit.isError && <p className="ui-note" role="alert">{audit.error.message}</p>}
      {audit.data && entries.length === 0 && <p className="ui-note">{t("audit.empty")}</p>}
      {entries.length > 0 && (
        <ol className="ao-tail">
          {entries.map((e) => (
            <li key={e.id}>
              <time dateTime={e.at} className="ao-t">{formatClock(new Date(e.at))}</time>
              <span className={e.action.endsWith("_failed") || e.action.includes("warning") ? "ao-wn" : "ao-a"}>{e.action}</span>
              <span>{e.actor ?? "system"}</span>
              <span className="ao-o">{e.targetType ? `${e.targetType}:${e.targetId}` : ""}</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

/** Admin overview (round 028 a): range, warnings, activity charts, services and the latest audit entries. */
export function AdminOverview() {
  const t = useT("admin");
  const tc = useT("common");
  const [range, setRange] = useState<OverviewRange>("24h");
  const ov = useAdminOverview(range);
  const warnings = useStorageStatus().data?.warnings ?? [];
  const seg = (
    <div className="ui-seg" role="group" aria-label={t("ov.range")}>
      {ranges.map((r) => (
        <button key={r} type="button" className="ui-segi" aria-pressed={r === range} onClick={() => setRange(r)}>{t(`ov.range.${r}`)}</button>
      ))}
    </div>
  );
  return (
    <AdminPane title={t("overview.title")} actions={seg}>
      {warnings.map((w) => (
        <div key={w.code + (w.policy ?? "")} className="ao-note" role="alert">
          <Icon name="alert" />
          <span><b>{t("ov.storageWarn")}</b> {t(`storage.warning.${w.code}`, { policy: w.policy ?? "" })}</span>
          <Link to="/admin/storage" className="ui-btn ui-btn-sm ui-btn-secondary">{t("ov.configure")}</Link>
        </div>
      ))}
      {ov.isPending && <p role="status" className="ui-note">{tc("state.loading")}</p>}
      {ov.isError && (
        <div role="alert">
          <EmptyState icon="alert" title={t("ov.error")} description={ov.error.message} action={<Button onClick={() => void ov.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      )}
      {ov.data && (
        <>
          <Charts o={ov.data} />
          <Services o={ov.data} onRecheck={() => void ov.refetch()} checking={ov.isFetching} />
        </>
      )}
      <AuditTail />
    </AdminPane>
  );
}
