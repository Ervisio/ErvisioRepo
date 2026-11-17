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
import { BotMark, Button, EmptyState, Icon } from "../../ui/index.ts";
import { Avatar } from "../work/common.tsx";
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
              const summary = job === SUMMARY || list.length === 0;
              const sel = summary ? undefined : pick(list, job);
              return (
                <div className="ax-grid">
                  <JobNav repo={repo} run={r} jobs={list} selected={sel?.id ?? SUMMARY} onSelect={select} />
                  {sel ? (
                    <JobLog key={sel.id} owner={owner} name={name} job={sel} actions={<JobActions repo={repo} job={sel} />} />
                  ) : (
                    <RunSummary repo={repo} run={r} jobs={list} onSelect={select} />
                  )}
                </div>
              );
            }}
          </Gate>
        </div>
      )}
    </Gate>
  );
}

/** Value of the `job` search param that shows the run summary instead of a job. */
const SUMMARY = "summary";

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
  const actor = r.triggerActor || r.actor;
  const took = duration(r.startedAt, r.completedAt);

  return (
    <header className="ax-head">
      <div className="ax-tr">
        <h2 className="ax-h1">
          {r.title || r.workflowName}{" "}
          <Link className="ax-num" to="/$owner/$name/actions" params={{ owner, name }} search={{ workflow: r.workflowPath }}>
            {r.workflowName || r.workflowPath} #{r.runNumber}
          </Link>
        </h2>
        {writer && (
          <div className="ax-acts">
            {r.needsApproval && <Button variant="primary" icon="check" disabled={approve.isPending} onClick={() => act(approve, t("run.approved"))}>{t("run.approve")}</Button>}
            {active && !r.needsApproval && <Button className="ax-cancel" icon="x" disabled={cancel.isPending} onClick={() => act(cancel, t("run.cancelled"))}>{t("run.cancel")}</Button>}
            {!active && <Button variant={wasFailed ? "primary" : "secondary"} icon="play" disabled={rerun.isPending} onClick={() => act(rerun, t("run.rerunDone"))}>{t("run.rerun")}</Button>}
          </div>
        )}
      </div>
      <p className="ax-meta">
        <StatusPill status={r.status} conclusion={r.conclusion} />
        <span className="ax-who">
          <Avatar user={actor} size={18} />
          {t("run.triggered", { actor, event: r.eventAction ? `${r.event} · ${r.eventAction}` : r.event })} <BotMark login={actor} />
        </span>
        <code className="ax-ref">{branchOf(r.ref)}</code>
        <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }} className="ax-sha">{shortSha(r.sha)}</Link>
        <span>· <When iso={r.startedAt ?? r.createdAt} /></span>
        {took && <span>· {t("run.took", { duration: took })}</span>}
        {r.attempt > 1 && <span>· {t("run.attempt", { n: r.attempt })}</span>}
      </p>
      {r.fork && <p className="ax-forknote">{t("run.forkNote")}</p>}
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
      {job.cancelRequested && isActive(job.status) && <span className="ui-chip ui-chip-warn">{t("job.cancelling")}</span>}
    </div>
  );
}

/** Job column: Summary, the jobs with state and duration, then the workflow file. */
function JobNav({ repo, run: r, jobs, selected, onSelect }: { repo: Repo; run: ActionsRun; jobs: ActionsJob[]; selected: string; onSelect: (id: string) => void }) {
  const t = useT("actions");
  return (
    <nav className="ax-jn" aria-label={t("jobs.title")}>
      <button type="button" className="ax-ni" aria-current={selected === SUMMARY ? "page" : undefined} onClick={() => onSelect(SUMMARY)}>
        <Icon name="home" />
        {t("run.summary")}
      </button>
      <h3>{t("jobs.title")}</h3>
      {jobs.map((j) => (
        <button key={j.id} type="button" className="ax-ni" aria-current={j.id === selected ? "page" : undefined} onClick={() => onSelect(j.id)}>
          <StatusIcon status={j.status} conclusion={j.conclusion} bare />
          <span className="ax-ell">{j.name}</span>
          <span className="ui-tm">{duration(j.startedAt, j.completedAt)}</span>
        </button>
      ))}
      {jobs.length === 0 && <p className="ui-note ax-nnote">{t("jobs.none")}</p>}
      <h3>{t("run.detailsNav")}</h3>
      <Link className="ax-ni" to="/$owner/$name/blob" params={{ owner: repo.owner, name: repo.name }} search={{ ref: r.sha, path: r.workflowPath }}>
        <Icon name="file" />
        {t("run.workflowFile")}
      </Link>
    </nav>
  );
}

function RunSummary({ repo, run: r, jobs, onSelect }: { repo: Repo; run: ActionsRun; jobs: ActionsJob[]; onSelect: (id: string) => void }) {
  const t = useT("actions");
  const { owner, name } = repo;
  const names = new Map(jobs.map((j) => [j.key, j.name]));
  return (
    <section className="ui-box ax-jc" aria-label={t("run.summary")}>
      <div className="ax-jh">
        <h2 className="ax-jt">{t("run.summary")}</h2>
      </div>
      <div className="ui-in ax-sum-in">
        <dl className="ax-dl">
          <dt>{t("run.workflow")}</dt>
          <dd>{r.workflowName || r.workflowPath}</dd>
          <dt>{t("run.event")}</dt>
          <dd>{r.eventAction ? `${r.event} · ${r.eventAction}` : r.event}</dd>
          <dt>{t("run.ref")}</dt>
          <dd><code className="ax-ref">{branchOf(r.ref)}</code></dd>
          <dt>{t("run.commit")}</dt>
          <dd><Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }} className="ui-mono">{shortSha(r.sha)}</Link></dd>
          <dt>{t("runners.status")}</dt>
          <dd><StatusPill status={r.status} conclusion={r.conclusion} /></dd>
        </dl>
        {jobs.length > 0 && (
          <ul className="ax-sjobs">
            {jobs.map((j) => {
              const needs = j.needs.map((n) => names.get(n) ?? n).join(", ");
              return (
                <li key={j.id}>
                  <button type="button" className="ui-li" onClick={() => onSelect(j.id)}>
                    <StatusIcon status={j.status} conclusion={j.conclusion} bare />
                    <span className="ax-ell">{j.name}</span>
                    {needs && <span className="ui-tm">{t("job.needs", { jobs: needs })}</span>}
                    <span className="ui-tm ax-push">{duration(j.startedAt, j.completedAt)}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}
