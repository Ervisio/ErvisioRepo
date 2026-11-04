import { Link, useNavigate } from "@tanstack/react-router";
import {
  useCancelJob,
  useReviewDeployment,
  useRun,
  useRunAction,
  useRunJobs,
  type ActionsJob,
  type ActionsRun,
} from "../../api/actions.ts";
import { canWrite } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, EmptyState } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, RepoFrame, shortSha, When } from "../code/common.tsx";
import { ListLayoutSwitch } from "../work/common.tsx";
import { useListLayout } from "../work/layout.ts";
import { branchOf, duration, isActive, runState, StatusIcon, StatusPill } from "./common.tsx";
import { JobLog } from "./JobLog.tsx";

export function RunPage({ owner, name, runId, job }: { owner: string; name: string; runId: string; job?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="actions" section="act">
      {(repo) => <RunDetail repo={repo} runId={runId} job={job} />}
    </RepoFrame>
  );
}

/** Level of each job in the dependency graph: 0 without needs, else one past its deepest need. */
function levels(jobs: ActionsJob[]): ActionsJob[][] {
  const byKey = new Map(jobs.map((j) => [j.key, j]));
  const depth = new Map<string, number>();
  const visit = (j: ActionsJob, seen: Set<string>): number => {
    const known = depth.get(j.key);
    if (known !== undefined) return known;
    if (seen.has(j.key)) return 0;
    seen.add(j.key);
    const d = j.needs.reduce((m, n) => {
      const dep = byKey.get(n);
      return dep ? Math.max(m, visit(dep, seen) + 1) : m;
    }, 0);
    depth.set(j.key, d);
    return d;
  };
  const cols: ActionsJob[][] = [];
  for (const j of jobs) (cols[visit(j, new Set())] ??= []).push(j);
  return cols.filter(Boolean);
}

/** Job shown by default: the one the URL names, else the first failure, else the first running job. */
function pick(jobs: ActionsJob[], id?: string): ActionsJob | undefined {
  return (
    jobs.find((j) => j.id === id) ??
    jobs.find((j) => ["failure", "timed_out"].includes(runState(j.status, j.conclusion))) ??
    jobs.find((j) => j.status === "in_progress") ??
    jobs[0]
  );
}

function RunDetail({ repo, runId, job }: { repo: Repo; runId: string; job?: string }) {
  const t = useT("actions");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const layout = useListLayout();
  const run = useRun(owner, name, runId);
  const jobs = useRunJobs(owner, name, runId, run.data);
  const select = (id: string) => void navigate({ to: "/$owner/$name/actions/runs/$runId", params: { owner, name, runId }, search: { job: id }, replace: true });
  const notFound = <EmptyState icon="play" title={t("run.notFound")} action={<Link to="/$owner/$name/actions" params={{ owner, name }}><Button>{t("run.back")}</Button></Link>} />;

  return (
    <Gate query={run} notFound={notFound}>
      {(r) => (
        <>
          <RunHeader repo={repo} run={r} />
          <Gate query={jobs}>
            {(list) => {
              const sel = pick(list, job);
              const log = sel ? <JobLog key={sel.id} owner={owner} name={name} job={sel} /> : null;
              if (list.length === 0) return <p className="ui-note acc-pad">{t("jobs.none")}</p>;
              return (
                <>
                  <JobActions repo={repo} job={sel} />
                  {layout === "a" && (
                    <>
                      <JobGraph jobs={list} selected={sel?.id} onSelect={select} />
                      {log}
                    </>
                  )}
                  {layout === "b" && (
                    <div className="ax-split-b">
                      <JobGraph jobs={list} selected={sel?.id} onSelect={select} compact />
                      {log}
                    </div>
                  )}
                  {layout === "c" && (
                    <div className="ax-split-c">
                      <Gantt run={r} jobs={list} selected={sel?.id} onSelect={select} />
                      {log}
                    </div>
                  )}
                </>
              );
            }}
          </Gate>
        </>
      )}
    </Gate>
  );
}

function RunHeader({ repo, run: r }: { repo: Repo; run: ActionsRun }) {
  const t = useT("actions");
  const notify = useNotify();
  const { owner, name } = repo;
  const rerun = useRunAction(owner, name, r.id, "rerun");
  const cancel = useRunAction(owner, name, r.id, "cancel");
  const approve = useRunAction(owner, name, r.id, "approve");
  const writer = canWrite(repo.permission);
  const act = (m: typeof rerun, done: string) => m.mutate(undefined, { onSuccess: () => notify.ok(done), onError: notify.fail });

  return (
    <header className="ax-head">
      <div className="ax-head-top">
        <StatusPill status={r.status} conclusion={r.conclusion} />
        <h2 className="ax-h2">{r.title || r.workflowName}</h2>
        <span className="ax-count">#{r.runNumber}</span>
        <span className="acc-grow" />
        <ListLayoutSwitch />
        {writer && r.needsApproval && <Button variant="primary" icon="check" disabled={approve.isPending} onClick={() => act(approve, t("run.approved"))}>{t("run.approve")}</Button>}
        {writer && isActive(r.status) && !r.needsApproval && <Button variant="danger" icon="x" disabled={cancel.isPending} onClick={() => act(cancel, t("run.cancelled"))}>{t("run.cancel")}</Button>}
        {writer && !isActive(r.status) && <Button icon="play" disabled={rerun.isPending} onClick={() => act(rerun, t("run.rerunDone"))}>{t("run.rerun")}</Button>}
      </div>
      <dl className="ax-kv">
        <div><dt>{t("run.workflow")}</dt><dd>{r.workflowName || r.workflowPath}</dd></div>
        <div><dt>{t("run.event")}</dt><dd>{r.event}{r.eventAction ? ` · ${r.eventAction}` : ""}</dd></div>
        <div><dt>{t("run.ref")}</dt><dd><code className="ax-tag">{branchOf(r.ref)}</code></dd></div>
        <div><dt>{t("run.commit")}</dt><dd><Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }} className="ui-mono">{shortSha(r.sha)}</Link></dd></div>
        <div><dt>{t("run.actor")}</dt><dd>{r.triggerActor || r.actor}</dd></div>
        <div><dt>{t("run.started")}</dt><dd><When iso={r.startedAt ?? r.createdAt} /></dd></div>
        <div><dt>{t("run.duration")}</dt><dd>{duration(r.startedAt, r.completedAt) || "–"}</dd></div>
        {r.attempt > 1 && <div><dt>{t("run.attemptLabel")}</dt><dd>{r.attempt}</dd></div>}
      </dl>
      {r.fork && <Badge tone="warn">{t("run.forkNote")}</Badge>}
    </header>
  );
}

/** Cancel the selected job, or review its deployment to an environment. */
function JobActions({ repo, job }: { repo: Repo; job?: ActionsJob }) {
  const t = useT("actions");
  const notify = useNotify();
  const cancel = useCancelJob(repo.owner, repo.name);
  const review = useReviewDeployment(repo.owner, repo.name);
  if (!job || !canWrite(repo.permission)) return null;
  const awaiting = job.environment !== "" && job.status === "waiting" && !job.approved;
  if (!awaiting && !isActive(job.status)) return null;
  const opts = (done: string) => ({ onSuccess: () => notify.ok(done), onError: notify.fail });
  return (
    <div className="ax-jobact" role="group" aria-label={t("job.actions", { job: job.name })}>
      {awaiting && (
        <>
          <span>{t("job.awaiting", { env: job.environment })}</span>
          <Button size="sm" variant="primary" icon="check" disabled={review.isPending} onClick={() => review.mutate({ jobId: job.id, approve: true }, opts(t("job.approved")))}>{t("job.approve")}</Button>
          <Button size="sm" variant="danger" icon="x" disabled={review.isPending} onClick={() => review.mutate({ jobId: job.id, approve: false }, opts(t("job.rejected")))}>{t("job.reject")}</Button>
        </>
      )}
      {isActive(job.status) && !job.cancelRequested && (
        <Button size="sm" icon="x" disabled={cancel.isPending} onClick={() => cancel.mutate(job.id, opts(t("job.cancelled")))}>{t("job.cancel")}</Button>
      )}
      {job.cancelRequested && isActive(job.status) && <Badge tone="warn">{t("job.cancelling")}</Badge>}
    </div>
  );
}

function JobCard({ job, selected, onSelect }: { job: ActionsJob; selected: boolean; onSelect: (id: string) => void }) {
  const t = useT("actions");
  return (
    <button type="button" className="ax-job" aria-current={selected ? "true" : undefined} onClick={() => onSelect(job.id)}>
      <StatusIcon status={job.status} conclusion={job.conclusion} />
      <span className="ax-job-main">
        <b>{job.name}</b>
        {job.runsOn.length > 0 && <span className="ax-job-sub">{job.runsOn.join(", ")}</span>}
        {job.needs.length > 0 && <span className="ax-job-sub">{t("job.needs", { jobs: job.needs.join(", ") })}</span>}
      </span>
      <span className="ax-job-dur">{duration(job.startedAt, job.completedAt)}</span>
    </button>
  );
}

/** Layouts A and B: jobs in columns left to right, one column per dependency level. */
function JobGraph({ jobs, selected, onSelect, compact }: { jobs: ActionsJob[]; selected?: string; onSelect: (id: string) => void; compact?: boolean }) {
  const t = useT("actions");
  return (
    <div className={compact ? "ax-graph ax-graph-compact" : "ax-graph"} role="group" aria-label={t("jobs.graph")}>
      {levels(jobs).map((col, i) => (
        <ol key={i} className="ax-col">
          {col.map((j) => (
            <li key={j.id}>
              <JobCard job={j} selected={j.id === selected} onSelect={onSelect} />
            </li>
          ))}
        </ol>
      ))}
    </div>
  );
}

/** Layout C: job list with a bar per job on the time scale of the run. */
function Gantt({ run, jobs, selected, onSelect }: { run: ActionsRun; jobs: ActionsJob[]; selected?: string; onSelect: (id: string) => void }) {
  const t = useT("actions");
  const stamps = jobs.flatMap((j) => [j.startedAt, j.completedAt]).filter((x): x is string => !!x).map(Date.parse);
  const start = Math.min(...stamps, Date.parse(run.startedAt ?? run.createdAt) || Infinity);
  const end = Math.max(...stamps, start + 1000);
  const span = Math.max(end - start, 1000);
  return (
    <ul className="ax-gantt" aria-label={t("jobs.gantt")}>
      {jobs.map((j) => {
        const a = j.startedAt ? Date.parse(j.startedAt) : undefined;
        const b = j.completedAt ? Date.parse(j.completedAt) : end;
        const s = runState(j.status, j.conclusion);
        return (
          <li key={j.id}>
            <button type="button" className="ax-grow" aria-current={j.id === selected ? "true" : undefined} onClick={() => onSelect(j.id)}>
              <StatusIcon status={j.status} conclusion={j.conclusion} />
              <span className="ax-grow-name">{j.name}</span>
              <span className="ax-bar-track" aria-hidden="true">
                {a !== undefined && <i className={`ax-bar ax-b-${s}`} style={{ left: `${((a - start) / span) * 100}%`, width: `${Math.max(((b - a) / span) * 100, 1.5)}%` }} />}
              </span>
              <span className="ax-job-dur">{duration(j.startedAt, j.completedAt)}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
