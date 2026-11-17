import { Link } from "@tanstack/react-router";
import { Fragment, useEffect, useRef, useState, type SyntheticEvent } from "react";
import {
  useDeleteRunner,
  useRegistrationToken,
  useRunners,
  useSetRunnerPaused,
  useUpdateAllRunners,
  useUpdateRunner,
  type ActionsRegistrationToken,
  type ActionsRunner,
  type ActionsScope,
} from "../../api/actions.ts";
import { useMeta } from "../../api/useMeta.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, EmptyState, Input, Menu, MenuItem, SettingsSection, Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "../account/common.tsx";
import { formatBytes, When } from "../code/common.tsx";
import { duration } from "./common.tsx";

type RunnerMetrics = NonNullable<ActionsRunner["metrics"]>;
type RunnerJob = ActionsRunner["jobs"][number];

/** Name the built-in runner registers with (setup.sh, runner-setup). */
const BUILTIN_RUNNER = "builtin";

/** Scope string the server reports for runners registered at this scope. */
const scopeId = (s: ActionsScope) => (s.kind === "instance" ? "instance" : s.kind === "org" ? `org:${s.org}` : `repo:${s.owner}/${s.name}`).toLowerCase();

const pct = (used?: number, total?: number) => (used !== undefined && total ? (used / total) * 100 : undefined);

/** Busiest GPU of the machine, as a percentage. */
function gpuPct(m: RunnerMetrics): number | undefined {
  const vals = m.gpus.map((g) => g.utilizationPercent).filter((v): v is number => v !== undefined);
  return vals.length ? Math.max(...vals) : undefined;
}

/** Status as a chip (text carries the meaning; the tone only supports it). Offline adds how long ago. */
function StatusChip({ runner: r }: { runner: ActionsRunner }) {
  const t = useT("actions");
  const tone = r.status === "busy" || r.status === "online" ? "ui-chip-warn" : r.status === "idle" ? "ui-chip-ok" : r.status === "paused" ? "ui-chip-acc" : "";
  return (
    <span className={`ui-chip ${tone}`.trim()}>
      {t(`runners.st.${r.status}`)}
      {r.status === "offline" && r.lastSeenAt && <> · <When iso={r.lastSeenAt} /></>}
    </span>
  );
}

/** v-prefixed version for display. */
const vv = (v: string) => `v${v.replace(/^v/, "")}`;

/** Runners an admin can update now from the instance page. */
const canUpdate = (r: ActionsRunner) => r.selfUpdate && r.updateAvailable && !r.updatePending && r.status !== "offline";

/**
 * Update state next to the labels: updating, last failure, or available
 * (older than the server). The built-in runner follows the server image and
 * shows nothing; a runner that cannot update itself says how to update it.
 */
function UpdateChip({ runner: r }: { runner: ActionsRunner }) {
  const t = useT("actions");
  const meta = useMeta();
  if (r.name === BUILTIN_RUNNER && r.scope === "instance") return null;
  if (r.updatePending) {
    return <span className="ui-chip ui-chip-acc">{t("runners.upd.pending", { version: vv(r.updateVersion ?? "") })}</span>;
  }
  if (!r.updateAvailable) return null;
  if (r.updateError) {
    return <span className="ui-chip ui-chip-err" title={r.updateError}>{t("runners.upd.failed")}</span>;
  }
  const target = meta.data?.version ? vv(meta.data.version) : "";
  const hint = r.selfUpdate ? t("runners.upd.availableHint", { version: target }) : t("runners.upd.manualHint");
  return <span className="ui-chip ui-chip-warn" title={hint}>{t("runners.upd.available")}</span>;
}

/** Header button: ask every outdated online runner to update itself. */
function UpdateAll({ rows }: { rows: ActionsRunner[] }) {
  const t = useT("actions");
  const notify = useNotify();
  const all = useUpdateAllRunners();
  const n = rows.filter(canUpdate).length;
  if (n === 0) return null;
  return (
    <Button
      size="sm"
      icon="download"
      disabled={all.isPending}
      onClick={() =>
        all.mutate(undefined, {
          onSuccess: (res) => notify.ok(t("runners.upd.requestedAll", { n: res.requested, version: vv(res.version) })),
          onError: notify.fail,
        })
      }
    >
      {t("runners.upd.all", { n })}
    </Button>
  );
}

/** Value above a thin bar: accent, warn above 70 %, error above 90 % (round 023 A). */
function Meter({ value, text }: { value?: number; text?: string }) {
  if (value === undefined) return <span className="ui-tm">—</span>;
  const v = Math.min(100, Math.max(0, value));
  return (
    <span className="rn-m">
      <span className="rn-num">{text ?? `${Math.round(v)}%`}</span>
      <span className="rn-bar" aria-hidden="true">
        <i className={v > 90 ? "rn-cr" : v > 70 ? "rn-hi" : undefined} style={{ width: `${v}%` }} />
      </span>
    </span>
  );
}

/**
 * Runners of one scope as a dense table with live resource usage. The
 * instance pool is the default for every repository; org and repository
 * runners are an optional extra.
 */
/** labelName drops the backend of a raw runner label ("ubuntu-latest:docker://…" → "ubuntu-latest"). */
const labelName = (l: string) => l.split(":")[0];

export function Runners({ scope }: { scope: ActionsScope }) {
  const t = useT("actions");
  const list = useRunners(scope);
  const [dialog, setDialog] = useState<"new" | "token" | null>(null);
  const instance = scope.kind === "instance";
  const rows = list.data ?? [];
  const builtin = instance ? rows.find((r) => r.name === BUILTIN_RUNNER) : undefined;
  const openNew = () => setDialog("new");

  return (
    <SettingsSection
      title={instance ? t("runners.title") : t("runners.selfHosted")}
      description={instance ? t("runners.hintInstance") : t("runners.hintScoped")}
      actions={
        <>
          {instance && <UpdateAll rows={rows} />}
          <Button size="sm" onClick={() => setDialog("token")}>{t("runners.tokenAction")}</Button>
          <Button size="sm" variant={instance ? "primary" : "secondary"} icon="plus" onClick={openNew}>
            {t("runners.new")}
          </Button>
        </>
      }
    >
      {rows.length > 0 && <Summary rows={rows} />}
      {instance && rows.length > 0 && !builtin && (
        <p className="rn-bi">
          <b>{t("runners.builtin")}</b> <span className="rn-dim">· {t("runners.builtinNone")}</span>
        </p>
      )}
      {list.isSuccess && rows.length === 0 ? (
        <Empty instance={instance} onNew={openNew} />
      ) : (
        <Async query={list} empty="">
          {(data) => <RunnerTable scope={scope} rows={data} />}
        </Async>
      )}
      <Dialog
        open={dialog !== null}
        onOpenChange={(o) => !o && setDialog(null)}
        title={dialog === "token" ? t("runners.tokenAction") : t("runners.new")}
        description={t("runners.newHint")}
      >
        {dialog && <NewRunner scope={scope} advanced={dialog === "token"} />}
      </Dialog>
    </SettingsSection>
  );
}

/** Summary pills: online, running jobs, offline and free slots. */
function Summary({ rows }: { rows: ActionsRunner[] }) {
  const t = useT("actions");
  const live = rows.filter((r) => r.status !== "offline");
  const offline = rows.length - live.length;
  const running = rows.reduce((n, r) => n + r.running, 0);
  const capacity = live.reduce((n, r) => n + r.capacity, 0);
  const busy = live.reduce((n, r) => n + Math.min(r.running, r.capacity), 0);
  return (
    <p className="rn-sum">
      <span><i className="rn-dot rn-dot-ok" aria-hidden="true" /><b>{live.length}</b> {t("runners.sum.online")}</span>
      <span><i className="rn-dot rn-dot-busy" aria-hidden="true" /><b>{running}</b> {t("runners.sum.running")}</span>
      {offline > 0 && <span><i className="rn-dot" aria-hidden="true" /><b>{offline}</b> {t("runners.sum.offline")}</span>}
      {capacity > 0 && <span>{t("runners.sum.free")} <b>{t("runners.sum.of", { n: capacity - busy, total: capacity })}</b></span>}
    </p>
  );
}

function Empty({ instance, onNew }: { instance: boolean; onNew: () => void }) {
  const t = useT("actions");
  const meta = useMeta();
  const url = meta.data?.publicUrl.replace(/\/+$/, "");
  return (
    <div className="rn-empty">
      <EmptyState icon="cpu" title={t("runners.emptyTitle")} description={t("runners.emptyHint")} />
      {url && <Secret label={t("runners.oneLiner")} value={`curl -fsSL ${url}/runner/install.sh | sudo sh`} />}
      <div className="rn-empty-a">
        <Button variant="primary" icon="plus" onClick={onNew}>{t("runners.new")}</Button>
      </div>
      {instance && <p className="ui-note">{t("runners.builtinNone")}</p>}
    </div>
  );
}

function RunnerTable({ scope, rows }: { scope: ActionsScope; rows: ActionsRunner[] }) {
  const t = useT("actions");
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const toggle = (id: string) =>
    setOpen((s) => {
      const n = new Set(s);
      if (!n.delete(id)) n.add(id);
      return n;
    });
  const mine = scopeId(scope);

  return (
    <div className="ui-box rn-wrap" role="region" aria-label={t("runners.title")} tabIndex={0}>
      <table className="rn-t">
        <thead>
          <tr>
            <th scope="col">{t("runners.col.runner")}</th>
            <th scope="col">{t("runners.status")}</th>
            <th scope="col">CPU</th>
            <th scope="col">RAM</th>
            <th scope="col">GPU</th>
            <th scope="col">{t("runners.jobs")}</th>
            <th scope="col"><span className="sr-only">{t("cfg.actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const expanded = open.has(r.id);
            const m = r.status === "offline" ? undefined : r.metrics;
            const gpu = m && gpuPct(m);
            const cls = [expanded ? "rn-open" : "", r.status === "offline" ? "rn-off" : ""].filter(Boolean).join(" ");
            return (
              <Fragment key={r.id}>
                <tr className={cls || undefined} onClick={() => toggle(r.id)}>
                  <th scope="row">
                    <button type="button" className="rn-name" aria-expanded={expanded} aria-controls={`rn-x-${r.id}`} onClick={(e) => { e.stopPropagation(); toggle(r.id); }}>
                      {r.name}
                    </button>
                    <span className="rn-labs">
                      {r.ephemeral && <span className="ui-chip ui-chip-acc">{t("runners.ephemeral")}</span>}
                      <UpdateChip runner={r} />
                      {r.labels.map((l) => <span key={l} className="ui-chip" title={l}>{labelName(l)}</span>)}
                    </span>
                  </th>
                  <td><StatusChip runner={r} /></td>
                  <td><Meter value={m?.cpuPercent} /></td>
                  <td>
                    <Meter
                      value={m && pct(m.memoryUsedBytes, m.memoryTotalBytes)}
                      text={m?.memoryUsedBytes !== undefined && m.memoryTotalBytes ? `${formatBytes(m.memoryUsedBytes)} / ${formatBytes(m.memoryTotalBytes)}` : undefined}
                    />
                  </td>
                  <td><Meter value={gpu} text={m && gpu !== undefined ? `${m.gpus[0]?.name ?? "GPU"} · ${Math.round(gpu)}%` : undefined} /></td>
                  <td className="rn-num ui-tm">{r.capacity > 0 ? `${r.running} / ${r.capacity}` : r.running}</td>
                  <td className="rn-act">
                    {r.scope.toLowerCase() === mine && <RowMenu scope={scope} runner={r} />}
                  </td>
                </tr>
                {expanded && (
                  <tr className="rn-x" id={`rn-x-${r.id}`}>
                    <td colSpan={7}>
                      <Details runner={r} />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** Pause/Resume and Remove; clicks never reach the row (menu content renders in a portal but bubbles through React). */
function RowMenu({ scope, runner: r }: { scope: ActionsScope; runner: ActionsRunner }) {
  const t = useT("actions");
  const notify = useNotify();
  const del = useDeleteRunner(scope);
  const pause = useSetRunnerPaused(scope);
  const update = useUpdateRunner();
  const meta = useMeta();
  const label = t("runners.menuOf", { name: r.name });
  return (
    <div onClick={(e) => e.stopPropagation()}>
      <Menu align="end" label={label} trigger={<Button size="sm" icon="dots" aria-label={label} title={label} />}>
        <MenuItem
          icon={r.paused ? "play" : "clock"}
          onSelect={() =>
            pause.mutate(
              { id: r.id, paused: !r.paused },
              { onSuccess: () => notify.ok(t(r.paused ? "runners.resumed" : "runners.paused")), onError: notify.fail },
            )
          }
        >
          {t(r.paused ? "runners.resume" : "runners.pause")}
        </MenuItem>
        {scope.kind === "instance" && canUpdate(r) && (
          <MenuItem
            icon="download"
            onSelect={() => update.mutate(r.id, { onSuccess: () => notify.ok(t("runners.upd.requested", { name: r.name })), onError: notify.fail })}
          >
            {meta.data?.version ? t("runners.upd.toVersion", { version: vv(meta.data.version) }) : t("runners.upd.action")}
          </MenuItem>
        )}
        <MenuItem icon="trash" danger onSelect={() => del.mutate(r.id, { onSuccess: () => notify.ok(t("runners.deleted")), onError: notify.fail })}>
          {t("runners.remove")}
        </MenuItem>
      </Menu>
    </div>
  );
}

/** Expanded row: version, disk, load, heartbeat and system, then the jobs running now. */
function Details({ runner: r }: { runner: ActionsRunner }) {
  const t = useT("actions");
  const m = r.metrics;
  const sys = [m?.os, m?.arch, m?.cpuCount ? t("runners.d.cores", { n: m.cpuCount }) : "", m?.hostname].filter(Boolean).join(" · ");
  return (
    <div className="rn-xp">
      <dl className="rn-kv">
        <div><dt>{t("runners.version")}</dt><dd>{r.version ? vv(r.version) : "—"}</dd></div>
        {r.updateError && r.updateAvailable && <div><dt>{t("runners.upd.lastError")}</dt><dd>{r.updateError}</dd></div>}
        <div>
          <dt>{t("runners.d.disk")}</dt>
          <dd className="rn-num">{m?.diskUsedBytes !== undefined && m.diskTotalBytes ? `${formatBytes(m.diskUsedBytes)} / ${formatBytes(m.diskTotalBytes)}` : "—"}</dd>
        </div>
        <div><dt>{t("runners.d.loadTitle")}</dt><dd className="rn-num">{m?.load1 !== undefined ? m.load1.toFixed(1) : "—"}</dd></div>
        <div><dt>{t("runners.d.heartbeat")}</dt><dd>{r.lastSeenAt ? <When iso={r.lastSeenAt} /> : "—"}</dd></div>
        {sys && <div><dt>{t("runners.d.system")}</dt><dd>{sys}</dd></div>}
        {m?.gpus.map((g, i) => (
          <div key={`${g.name}-${i}`}>
            <dt>GPU</dt>
            <dd>
              {g.name}
              {g.memoryUsedBytes !== undefined && g.memoryTotalBytes ? <span className="rn-num"> · {formatBytes(g.memoryUsedBytes)} / {formatBytes(g.memoryTotalBytes)}</span> : null}
            </dd>
          </div>
        ))}
        <div><dt>{t("runners.scope")}</dt><dd>{r.scope === "instance" ? t("runners.d.instance") : <span className="ui-mono">{r.scope}</span>}</dd></div>
      </dl>
      {r.jobs.length > 0 ? (
        <ul className="rn-jobs" aria-label={t("runners.d.jobs")}>
          {r.jobs.map((j) => <JobRow key={j.id} job={j} />)}
        </ul>
      ) : (
        <p className="ui-note rn-nojob">{t("runners.d.noJobs")}</p>
      )}
      {!m && <p className="ui-note rn-nojob">{t("runners.d.noMetrics")}</p>}
    </div>
  );
}

function JobRow({ job: j }: { job: RunnerJob }) {
  const t = useT("actions");
  const repo = j.repository;
  const title = [j.workflowName, j.runNumber !== undefined ? `#${j.runNumber}` : "", j.name].filter(Boolean).join(" · ") || t("runners.d.job");
  return (
    <li className="rn-job">
      <span className="ax-spin" aria-hidden="true" />
      {j.visible && repo && j.runId ? (
        <Link to="/$owner/$name/actions/runs/$runId" params={{ owner: repo.owner, name: repo.name, runId: j.runId }} className="rn-job-t">{title}</Link>
      ) : (
        <b className="rn-job-t">{j.visible ? title : t("runners.d.private")}</b>
      )}
      {j.visible && repo && <code className="ax-ref">{repo.fullName}</code>}
      {j.startedAt && <span className="ui-tm rn-push">{t("runners.d.since", { duration: duration(j.startedAt) })}</span>}
    </li>
  );
}

type RunnerOS = "linux" | "macos" | "windows" | "docker";

const SAFE_NAME = /[^A-Za-z0-9._-]/g;
const SAFE_LABELS = /[^A-Za-z0-9._,-]/g;

/**
 * Install instructions for a new runner: the device-approval one-liner first,
 * token commands under "Advanced". With advanced the token section starts open.
 */
function NewRunner({ scope, advanced }: { scope: ActionsScope; advanced?: boolean }) {
  const t = useT("actions");
  const notify = useNotify();
  const meta = useMeta();
  const gen = useRegistrationToken(scope);
  const [token, setToken] = useState<ActionsRegistrationToken | null>(null);
  const [name, setName] = useState("");
  const [labels, setLabels] = useState("");
  const [capacity, setCapacity] = useState("");
  const [os, setOS] = useState<RunnerOS>("linux");
  const { mutate } = gen;

  const started = useRef(false);
  // The registration token is created the first time the advanced section opens.
  const start = () => {
    if (started.current) return;
    started.current = true;
    mutate(undefined, { onSuccess: setToken, onError: notify.fail });
  };
  const onToggle = (e: SyntheticEvent<HTMLDetailsElement>) => {
    if (e.currentTarget.open) start();
  };
  useEffect(() => {
    if (advanced) start();
  });

  if (!meta.data) return <p className="ui-note">{t("runners.creating")}</p>;

  const url = meta.data.publicUrl.replace(/\/+$/, "");
  const ver = /^\d+\.\d+\.\d+$/.test(meta.data.version) ? meta.data.version : "latest";
  const n = name.replace(SAFE_NAME, "");
  const l = labels.replace(SAFE_LABELS, "").replace(/^,+|,+$/g, "");
  const c = /^\d{1,2}$/.test(capacity) ? capacity : "";
  const commands = token
    ? {
        linux: `curl -fsSL ${url}/runner/install.sh | sudo sh -s -- --url ${url} --token ${token.token}${n ? ` --name ${n}` : ""}${l ? ` --labels ${l}` : ""}${c ? ` --capacity ${c}` : ""}`,
        macos: `curl -fsSL ${url}/runner/install.sh | sh -s -- --url ${url} --token ${token.token}${n ? ` --name ${n}` : ""}${l ? ` --labels ${l}` : ""}${c ? ` --capacity ${c}` : ""}`,
        windows: `& ([scriptblock]::Create((irm ${url}/runner/install.ps1))) -Url ${url} -Token ${token.token}${n ? ` -Name ${n}` : ""}${l ? ` -Labels ${l}` : ""}${c ? ` -Capacity ${c}` : ""}`,
        docker: [
          "docker run -d --name ervisio-runner --restart unless-stopped --user root --no-healthcheck \\",
          "  -v /var/run/docker.sock:/var/run/docker.sock -v ervisio-runner:/var/lib/ervisio-runner \\",
          `  -e ERVISIO_RUNNER_URL=${url} -e ERVISIO_RUNNER_TOKEN=${token.token} \\`,
          "  -e ERVISIO_RUNNER_DATA_DIR=/var/lib/ervisio-runner -e HOME=/var/lib/ervisio-runner \\",
          ...(n || l || c ? [`  ${[n && `-e ERVISIO_RUNNER_NAME=${n}`, l && `-e ERVISIO_RUNNER_LABELS=${l}`, c && `-e ERVISIO_RUNNER_CAPACITY=${c}`].filter(Boolean).join(" ")} \\`] : []),
          `  --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:${ver} \\`,
          "  daemon --config /var/lib/ervisio-runner/config.toml",
        ].join("\n"),
      }
    : null;

  return (
    <div className="rn-new">
      <Secret label={t("runners.oneLiner")} value={`curl -fsSL ${url}/runner/install.sh | sudo sh`} />
      <p className="ui-note">
        {t("runners.oneLinerHint")} <a href={`${url}/runner/approve`}>{`${url}/runner/approve`}</a>
      </p>
      <p className="ui-note">{t("runners.otherOS")}</p>
      <Secret label={t("runners.os.macosNoSudo")} value={`curl -fsSL ${url}/runner/install.sh | sh`} />
      <Secret label={t("runners.os.windowsDevice")} value={`irm ${url}/runner/install.ps1 | iex`} />
      <details className="rn-adv" open={advanced || undefined} onToggle={onToggle}>
        <summary>{t("runners.advanced")}</summary>
        {!commands || !token ? (
          <p className="ui-note">{t("runners.creating")}</p>
        ) : (
          <div className="rn-new">
            <p className="ui-note">{t("runners.tokenLabel", { when: formatDate(token.expiresAt) })}</p>
            <div className="rn-fields">
              <Input label={t("runners.name")} placeholder={t("runners.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} mono />
              <Input label={t("runners.extraLabels")} hint={t("runners.extraLabelsHint")} placeholder="gpu,big" value={labels} onChange={(e) => setLabels(e.target.value)} mono />
              <Input label={t("runners.capacity")} hint={t("runners.capacityHint")} inputMode="numeric" value={capacity} onChange={(e) => setCapacity(e.target.value)} mono />
            </div>
            <Tabs value={os} onValueChange={(v) => setOS(v as RunnerOS)}>
              <TabsList aria-label={t("runners.platform")}>
                <TabsTrigger value="linux">Linux</TabsTrigger>
                <TabsTrigger value="macos">macOS</TabsTrigger>
                <TabsTrigger value="windows">Windows</TabsTrigger>
                <TabsTrigger value="docker">Docker</TabsTrigger>
              </TabsList>
              {(Object.keys(commands) as RunnerOS[]).map((k) => (
                <TabsContent key={k} value={k}>
                  <Secret label={t(`runners.os.${k}`)} value={commands[k]} multiline={k === "docker"} />
                </TabsContent>
              ))}
            </Tabs>
            <p className="ui-note">{t("runners.backendNote")}</p>
            <Secret label={t("runners.tokenOnly")} value={token.token} />
          </div>
        )}
      </details>
    </div>
  );
}
