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
import { Badge, BotMark, Button, EmptyState } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, RepoFrame, shortSha, When } from "../code/common.tsx";
import { branchOf, duration, isActive, runState, StatusIcon, StatusPill } from "./common.tsx";
import { JobLog } from "./JobLog.tsx";

export function RunPage({ owner, name, runId, job }: { owner: string; name: string; runId: string; job?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="actions" section="act">
      {(repo) => <RunDetail repo={repo} runId={runId} job={job} />}
    </RepoFrame>
  );
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
  const run = useRun(owner, name, runId);
  const jobs = useRunJobs(owner, name, runId, run.data);
  const select = (id: string) => void navigate({ to: "/$owner/$name/actions/runs/$runId", params: { owner, name, runId }, search: { job: id }, replace: true });
  const notFound = <EmptyState icon="play" title={t("run.notFound")} action={<Link to="/$owner/$name/actions" params={{ owner, name }}><Button>{t("run.back")}</Button></Link>} />;

  return (
    <Gate query={run} notFound={notFound}>
      {(r) => (
        <div className="ax-main">
          <RunHeader repo={repo} run={r} />
          <Gate query={jobs}>
            {(list) => {
              const sel = pick(list, job);
              if (list.length === 0) return <p className="ui-note acc-pad">{t("jobs.none")}</p>;
              return (
                <div className="ax-grid">
                  <div className="ax-aside">
                    <JobList jobs={list} selected={sel?.id} onSelect={select} />
                    <RunDetails repo={repo} run={r} />
                  </div>
                  {sel && <JobLog key={sel.id} owner={owner} name={name} job={sel} actions={<JobActions repo={repo} job={sel} />} />}
                </div>
              );
            }}
          </Gate>
        </div>
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
  const active = isActive(r.status);
  const wasFailed = ["failure", "timed_out"].includes(runState(r.status, r.conclusion));
  const rerunBtn = (primary: boolean) => (
    <Button variant={primary ? "primary" : "secondary"} icon="play" disabled={rerun.isPending} onClick={() => act(rerun, t("run.rerunDone"))}>{t("run.rerun")}</Button>
  );

  return (
    <header className="ax-head">
      <nav className="ax-bc" aria-label={t("run.breadcrumb")}>
        <Link to="/$owner/$name/actions" params={{ owner, name }}>{t("run.actions")}</Link>
        <span aria-hidden="true">/</span>
        <Link to="/$owner/$name/actions" params={{ owner, name }} search={{ workflow: r.workflowPath }}>{r.workflowName || r.workflowPath}</Link>
      </nav>
      <div className="ax-tr">
        <h2 className="ax-h1">
          {r.title || r.workflowName}
          <span className="ax-num">#{r.runNumber}</span>
        </h2>
        {writer && (
          <div className="ax-acts">
            {r.needsApproval && <Button variant="primary" icon="check" disabled={approve.isPending} onClick={() => act(approve, t("run.approved"))}>{t("run.approve")}</Button>}
            {active && !r.needsApproval && <Button className="ax-cancel" icon="x" disabled={cancel.isPending} onClick={() => act(cancel, t("run.cancelled"))}>{t("run.cancel")}</Button>}
            {!active && rerunBtn(wasFailed)}
          </div>
        )}
      </div>
      <p className="ax-meta">
        <StatusPill status={r.status} conclusion={r.conclusion} />
        <span>{t("run.eventOn", { event: r.eventAction ? `${r.event} · ${r.eventAction}` : r.event, branch: branchOf(r.ref) })}</span>
        <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }} className="ax-sha">{shortSha(r.sha)}</Link>
        <span>{t("run.startedBy", { actor: r.triggerActor || r.actor })} <BotMark login={r.triggerActor || r.actor} /> <When iso={r.startedAt ?? r.createdAt} /></span>
        {duration(r.startedAt, r.completedAt) && <span>{duration(r.startedAt, r.completedAt)}</span>}
        {r.attempt > 1 && <span>{t("run.attempt", { n: r.attempt })}</span>}
      </p>
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

function JobList({ jobs, selected, onSelect }: { jobs: ActionsJob[]; selected?: string; onSelect: (id: string) => void }) {
  const t = useT("actions");
  const names = new Map(jobs.map((j) => [j.key, j.name]));
  return (
    <section className="ax-box ax-jobs" aria-label={t("jobs.title")}>
      <h2 className="ax-bh">{t("jobs.title")}</h2>
      <ul className="ax-jl">
        {jobs.map((j) => {
          const needs = j.needs.map((n) => names.get(n) ?? n).join(", ");
          return (
            <li key={j.id}>
              <button type="button" className="ax-job" aria-current={j.id === selected ? "true" : undefined} onClick={() => onSelect(j.id)}>
                <StatusIcon status={j.status} conclusion={j.conclusion} bare />
                <span className="ax-jn">{j.name}</span>
                <span className="ax-jd">{duration(j.startedAt, j.completedAt)}</span>
                <span className="ax-js">
                  {t(`state.${runState(j.status, j.conclusion)}`)}
                  {needs && ` · ${t("job.needs", { jobs: needs })}`}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function RunDetails({ repo, run: r }: { repo: Repo; run: ActionsRun }) {
  const t = useT("actions");
  const { owner, name } = repo;
  return (
    <section className="ax-box ax-details" aria-label={t("run.details")}>
      <h2 className="ax-bh">{t("run.details")}</h2>
      <dl className="ax-dl">
        <dt>{t("run.workflow")}</dt>
        <dd>{r.workflowName || r.workflowPath}</dd>
        <dt>{t("run.event")}</dt>
        <dd>{r.event}</dd>
        <dt>{t("run.ref")}</dt>
        <dd><code>{branchOf(r.ref)}</code></dd>
        <dt>{t("run.commit")}</dt>
        <dd><Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }}><code>{shortSha(r.sha)}</code></Link></dd>
      </dl>
    </section>
  );
}
