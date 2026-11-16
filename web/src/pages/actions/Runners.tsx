import { Link } from "@tanstack/react-router";
import { Fragment, useEffect, useRef, useState, type SyntheticEvent } from "react";
import {
  useDeleteRunner,
  useRegistrationToken,
  useRunners,
  useSetRunnerPaused,
  type ActionsRegistrationToken,
  type ActionsRunner,
  type ActionsScope,
} from "../../api/actions.ts";
import { useMeta } from "../../api/useMeta.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Dialog, EmptyState, Input, Menu, MenuItem, SettingsSection, Tabs, TabsContent, TabsList, TabsTrigger } from "../../ui/index.ts";
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

function StatusDot({ runner }: { runner: ActionsRunner }) {
  const t = useT("actions");
  const label = t(`runners.st.${runner.status}`);
  return (
    <span className={`rn-dot rn-dot-${runner.status}`} title={label}>
      <span className="sr-only">{label}</span>
    </span>
  );
}

/** Percentage with a thin bar; warn from 75 %, error from 90 %. */
function Meter({ value }: { value?: number }) {
  if (value === undefined) return <span className="rn-none">—</span>;
  const v = Math.min(100, Math.max(0, value));
  return (
    <div className="rn-m">
      <span className="rn-num">{Math.round(v)}%</span>
      <span className="rn-bar" aria-hidden="true">
        <i className={v >= 90 ? "rn-cr" : v >= 75 ? "rn-hi" : undefined} style={{ width: `${v}%` }} />
      </span>
    </div>
  );
}

/**
 * Runners of one scope as a dense table with live resource usage. The
 * instance pool is the default for every repository; org and repository
 * runners are an optional extra.
 */
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
          <Button size="sm" onClick={() => setDialog("token")}>{t("runners.tokenAction")}</Button>
          <Button size="sm" variant={instance ? "primary" : "secondary"} icon="plus" onClick={openNew}>
            {t("runners.new")}
          </Button>
        </>
      }
    >
      {rows.length > 0 && <Summary rows={rows} />}
      {instance && rows.length > 0 && (
        <div className="rn-bi">
          {builtin ? (
            <>
              <StatusDot runner={builtin} />
              <span className="rn-bi-t">
                <b>{t("runners.builtin")}</b>
                <span className="rn-dim">
                  {" · "}
                  {t(`runners.st.${builtin.status}`)}
                  {builtin.labels.length > 0 && <> · <span className="ui-mono">{builtin.labels.join(", ")}</span></>}
                  {builtin.version && <> · v{builtin.version}</>}
                </span>
              </span>
            </>
          ) : (
            <>
              <span className="rn-dot rn-dot-offline" aria-hidden="true" />
              <span className="rn-bi-t">
                <b>{t("runners.builtin")}</b> <span className="rn-dim">· {t("runners.builtinNone")}</span>
              </span>
            </>
          )}
        </div>
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

function Summary({ rows }: { rows: ActionsRunner[] }) {
  const t = useT("actions");
  const online = rows.filter((r) => r.status !== "offline" && r.status !== "paused").length;
  const running = rows.reduce((n, r) => n + r.running, 0);
  const capacity = rows.reduce((n, r) => n + r.capacity, 0);
  return (
    <p className="rn-sum">
      <span><b>{rows.length}</b> {t("runners.sum.runners")}</span>
      <span><span className="rn-dot rn-dot-idle" aria-hidden="true" /> <b>{online}</b> {t("runners.sum.online")}</span>
      {capacity > 0 && <span><b>{running} / {capacity}</b> {t("runners.sum.slots")}</span>}
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
    <div className="rn-wrap">
      <table className="rn-t">
        <thead>
          <tr>
            <th scope="col">{t("runners.col.runner")}</th>
            <th scope="col">CPU</th>
            <th scope="col">RAM</th>
            <th scope="col">GPU</th>
            <th scope="col">{t("runners.jobs")}</th>
            <th scope="col">{t("runners.seen")}</th>
            <th scope="col"><span className="sr-only">{t("cfg.actions")}</span></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const expanded = open.has(r.id);
            const m = r.status === "offline" ? undefined : r.metrics;
            const sys = [r.metrics?.os, r.metrics?.arch, r.metrics?.hostname].filter(Boolean).join(" · ");
            const cls = [expanded ? "rn-open" : "", r.status === "offline" ? "rn-off" : ""].filter(Boolean).join(" ");
            return (
              <Fragment key={r.id}>
                <tr className={cls || undefined} onClick={() => toggle(r.id)}>
                  <td>
                    <div className="rn-n">
                      <StatusDot runner={r} />
                      <div>
                        <button type="button" className="rn-name" aria-expanded={expanded} onClick={(e) => { e.stopPropagation(); toggle(r.id); }}>
                          {r.name}
                        </button>
                        {r.ephemeral && <> <Badge>{t("runners.ephemeral")}</Badge></>}
                        {sys && <span className="rn-dim rn-sys">{sys}</span>}
                      </div>
                    </div>
                  </td>
                  <td><Meter value={m?.cpuPercent} /></td>
                  <td><Meter value={m && pct(m.memoryUsedBytes, m.memoryTotalBytes)} /></td>
                  <td><Meter value={m && gpuPct(m)} /></td>
                  <td className="rn-num">{r.capacity > 0 ? `${r.running} / ${r.capacity}` : r.running}</td>
                  <td className="rn-dim">{r.lastSeenAt ? <When iso={r.lastSeenAt} /> : "—"}</td>
                  <td className="rn-act">
                    {r.scope.toLowerCase() === mine && <RowMenu scope={scope} runner={r} />}
                  </td>
                </tr>
                {expanded && (
                  <tr className="rn-x">
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
        <MenuItem icon="trash" danger onSelect={() => del.mutate(r.id, { onSuccess: () => notify.ok(t("runners.deleted")), onError: notify.fail })}>
          {t("runners.remove")}
        </MenuItem>
      </Menu>
    </div>
  );
}

function Details({ runner: r }: { runner: ActionsRunner }) {
  const t = useT("actions");
  const m = r.metrics;
  const disk = m && pct(m.diskUsedBytes, m.diskTotalBytes);
  const cpu = m && [m.cpuCount > 0 ? t("runners.d.cores", { n: m.cpuCount }) : "", m.load1 !== undefined ? t("runners.d.load", { n: m.load1.toFixed(1) }) : ""].filter(Boolean).join(" · ");
  return (
    <div className="rn-xg">
      <div>
        <span className="rn-k">{t("runners.d.jobs")}</span>
        {r.jobs.length === 0 ? (
          <p className="ui-note rn-nojob">{t("runners.d.noJobs")}</p>
        ) : (
          <ul className="rn-jobs">
            {r.jobs.map((j) => <JobRow key={j.id} job={j} />)}
          </ul>
        )}
      </div>
      <div>
        <span className="rn-k">{t("runners.d.title")}</span>
        <dl className="rn-dl">
          {m?.memoryUsedBytes !== undefined && m.memoryTotalBytes ? (
            <>
              <dt>RAM</dt>
              <dd className="rn-num">{formatBytes(m.memoryUsedBytes)} / {formatBytes(m.memoryTotalBytes)}</dd>
            </>
          ) : null}
          {cpu ? (
            <>
              <dt>CPU</dt>
              <dd>{cpu}</dd>
            </>
          ) : null}
          {disk !== undefined && (
            <>
              <dt>{t("runners.d.disk")}</dt>
              <dd className="rn-num">{t("runners.d.diskUsed", { n: Math.round(disk) })}</dd>
            </>
          )}
          {m?.gpus.map((g, i) => (
            <Fragment key={`${g.name}-${i}`}>
              <dt>GPU</dt>
              <dd>
                {g.name}
                {g.memoryUsedBytes !== undefined && g.memoryTotalBytes ? <span className="rn-num"> · {formatBytes(g.memoryUsedBytes)} / {formatBytes(g.memoryTotalBytes)}</span> : null}
              </dd>
            </Fragment>
          ))}
          {r.labels.length > 0 && (
            <>
              <dt>{t("runners.labels")}</dt>
              <dd>{r.labels.map((l) => <span key={l} className="rn-lab">{l}</span>)}</dd>
            </>
          )}
          <dt>{t("runners.version")}</dt>
          <dd>
            {r.version || "—"} · {r.scope === "instance" ? t("runners.d.instance") : <span className="ui-mono">{r.scope}</span>}
          </dd>
        </dl>
        {!m && <p className="ui-note">{t("runners.d.noMetrics")}</p>}
      </div>
    </div>
  );
}

function JobRow({ job: j }: { job: RunnerJob }) {
  const t = useT("actions");
  const repo = j.repository;
  const sub = repo && [repo.fullName, [j.workflowName, j.runNumber !== undefined ? `#${j.runNumber}` : ""].filter(Boolean).join(" ")].filter(Boolean).join(" · ");
  return (
    <li className="rn-job">
      <span className="rn-dot rn-dot-busy" aria-hidden="true" />
      <div className="rn-job-m">
        {j.visible && repo && j.runId ? (
          <Link to="/$owner/$name/actions/runs/$runId" params={{ owner: repo.owner, name: repo.name, runId: j.runId }}>
            <b>{j.name || t("runners.d.job")}</b>
          </Link>
        ) : (
          <b>{j.visible ? j.name || t("runners.d.job") : t("runners.d.private")}</b>
        )}
        {j.visible && sub && <span className="rn-dim">{sub}</span>}
      </div>
      {j.startedAt && <span className="rn-dim rn-num">{duration(j.startedAt)}</span>}
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
