import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { RUNS_PER_PAGE, useDispatch, useRuns, useWorkflows, type ActionsRun, type ActionsWorkflow, type RunFilter } from "../../api/actions.ts";
import { canWrite } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { BotMark, Button, Dialog, DialogClose, EmptyState, Icon, Input, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, Pager, RepoFrame, When } from "../code/common.tsx";
import { branchOf, duration, StatusIcon } from "./common.tsx";

export type RunsSearch = RunFilter;

const events = ["push", "pull_request", "workflow_dispatch", "schedule", "release"];
const statuses = ["queued", "in_progress", "completed", "success", "failure", "cancelled"];

export function RunsPage({ owner, name, search }: { owner: string; name: string; search: RunsSearch }) {
  return (
    <RepoFrame owner={owner} name={name} tab="actions" section="act">
      {(repo) => <Runs repo={repo} search={search} />}
    </RepoFrame>
  );
}

/** Workflow sidebar (round 023 A): all workflows, one entry per workflow, then the management links. */
function WorkflowNav({ repo, search, go }: { repo: Repo; search: RunsSearch; go: (patch: Partial<RunsSearch>) => void }) {
  const t = useT("actions");
  const workflows = useWorkflows(repo.owner, repo.name);
  return (
    <nav className="ax-nav" aria-label={t("workflows.label")}>
      <h2>{t("run.actions")}</h2>
      <button type="button" className="ax-ni" aria-current={!search.workflow ? "page" : undefined} onClick={() => go({ workflow: undefined })}>
        {t("workflows.all")}
      </button>
      {workflows.data?.map((w) => (
        <button key={w.path} type="button" className="ax-ni" aria-current={w.path === search.workflow ? "page" : undefined} title={w.path} onClick={() => go({ workflow: w.path })}>
          <span className="ax-ell">{w.name || w.path}</span>
        </button>
      ))}
      {workflows.isSuccess && workflows.data.length === 0 && <p className="ui-note ax-nnote">{t("workflows.none")}</p>}
      {repo.permission === "admin" && (
        <>
          <h3>{t("nav.manage")}</h3>
          <Link className="ax-ni" to="/$owner/$name/settings" params={{ owner: repo.owner, name: repo.name }} search={{ section: "runners" }}>
            <Icon name="cpu" />
            {t("runners.title")}
          </Link>
        </>
      )}
    </nav>
  );
}

function Runs({ repo, search }: { repo: Repo; search: RunsSearch }) {
  const t = useT("actions");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const workflows = useWorkflows(owner, name);
  const runs = useRuns(owner, name, search);
  const go = (patch: Partial<RunsSearch>) =>
    void navigate({ to: "/$owner/$name/actions", params: { owner, name }, search: { ...search, page: undefined, ...patch } });
  const current = workflows.data?.find((w) => w.path === search.workflow);

  return (
    <div className="ax-layout">
      <WorkflowNav repo={repo} search={search} go={go} />
      <div className="ax-main">
        <div className="ax-lh">
          <h2 className="ax-h2">{current ? current.name || current.path : t("workflows.all")}</h2>
          {current && canWrite(repo.permission) && <DispatchDialog repo={repo} workflow={current} />}
        </div>
        <form
          className="ax-find"
          onSubmit={(e) => {
            e.preventDefault();
            const v = new FormData(e.currentTarget).get("branch");
            go({ branch: typeof v === "string" && v.trim() ? v.trim() : undefined });
          }}
        >
          <Input label={t("filter.branch")} hideLabel name="branch" mono placeholder={t("filter.branchPlaceholder")} defaultValue={search.branch ?? ""} key={search.branch ?? ""} />
          <Button type="submit" icon="search">{t("filter.apply")}</Button>
        </form>
        <section className="ui-box" aria-label={t("runs.title")}>
          <div className="ax-bxh">
            <b>{runs.data ? t("runs.count", { count: runs.data.totalCount }) : t("runs.title")}</b>
            <Select label={t("filter.event")} hideLabel className="ax-flt" value={search.event ?? "_"} onValueChange={(v) => go({ event: v === "_" ? undefined : v })} options={[{ value: "_", label: t("filter.event") }, ...events.map((e) => ({ value: e, label: e }))]} />
            <Select label={t("filter.status")} hideLabel className="ax-flt" value={search.status ?? "_"} onValueChange={(v) => go({ status: v === "_" ? undefined : v })} options={[{ value: "_", label: t("filter.status") }, ...statuses.map((s) => ({ value: s, label: t(`filterStatus.${s}`) }))]} />
          </div>
          <Gate query={runs}>
            {(list) =>
              list.runs.length === 0 ? (
                <div className="ui-in">
                  <EmptyState icon="play" title={t("runs.empty")} description={t("runs.emptyHint")} />
                </div>
              ) : (
                <ul className="ui-in ax-rows">
                  {list.runs.map((r) => (
                    <RunRow key={r.id} repo={repo} run={r} />
                  ))}
                </ul>
              )
            }
          </Gate>
        </section>
        {runs.data && runs.data.runs.length > 0 && (
          <Pager page={search.page ?? 1} hasMore={(search.page ?? 1) * RUNS_PER_PAGE < runs.data.totalCount} onPage={(n) => go({ page: n > 1 ? n : undefined })} />
        )}
      </div>
    </div>
  );
}

function RunRow({ repo, run: r }: { repo: Repo; run: ActionsRun }) {
  const t = useT("actions");
  const { owner, name } = repo;
  const actor = r.triggerActor || r.actor;
  const title = r.title || r.workflowName;
  return (
    <li className="ax-rr">
      <StatusIcon status={r.status} conclusion={r.conclusion} bare />
      <span className="ax-rr-m">
        {r.external ? (
          <a href={r.htmlUrl} target="_blank" rel="noreferrer" className="ax-rr-t" title={t("run.onGitHub")}>{title}</a>
        ) : (
          <Link to="/$owner/$name/actions/runs/$runId" params={{ owner, name, runId: r.id }} className="ax-rr-t">{title}</Link>
        )}
        <span className="ax-rr-s">
          <b>{r.workflowName || r.workflowPath}</b> #{r.runNumber}: {t("run.byLine", { event: r.event, actor })} <BotMark login={actor} />
          {r.attempt > 1 && <> · {t("run.attempt", { n: r.attempt })}</>}
          {r.needsApproval && <> <span className="ui-chip ui-chip-warn">{t("run.needsApproval")}</span></>}
          {r.fork && <> <span className="ui-chip">{t("run.fork")}</span></>}
          {r.external && <> <span className="ui-chip ui-chip-acc">{t("run.github")}</span></>}
        </span>
      </span>
      <span className="ax-rr-ref"><code className="ax-ref">{branchOf(r.ref)}</code></span>
      <span className="ax-rr-tm">
        <span className="ui-tm"><When iso={r.createdAt} /></span>
        <span className="ui-tm">{duration(r.startedAt, r.completedAt)}</span>
      </span>
    </li>
  );
}

/** Start a workflow_dispatch run: a ref and free-form inputs, one KEY=value per line. */
function DispatchDialog({ repo, workflow }: { repo: Repo; workflow: ActionsWorkflow }) {
  const t = useT("actions");
  const notify = useNotify();
  const dispatch = useDispatch(repo.owner, repo.name);
  const [open, setOpen] = useState(false);
  const [ref, setRef] = useState(repo.defaultBranch);
  const [inputs, setInputs] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    const parsed: Record<string, string> = {};
    for (const line of inputs.split("\n")) {
      const i = line.indexOf("=");
      if (i > 0) parsed[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
    dispatch.mutate(
      { workflow: workflow.path, ref: ref.trim() || undefined, inputs: Object.keys(parsed).length ? parsed : undefined },
      { onSuccess: () => (notify.ok(t("dispatch.done")), setOpen(false)), onError: notify.fail },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen} title={t("dispatch.title", { name: workflow.name || workflow.path })} description={t("dispatch.hint")} trigger={<Button variant="primary" icon="play">{t("dispatch.button")}</Button>}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("dispatch.ref")} mono required value={ref} onChange={(e) => setRef(e.target.value)} />
        <div className="ui-field">
          <label htmlFor="ax-inputs">{t("dispatch.inputs")}</label>
          <textarea id="ax-inputs" className="ui-input ui-mono acc-area" rows={4} spellCheck={false} placeholder="environment=staging" value={inputs} onChange={(e) => setInputs(e.target.value)} />
          <p className="ui-note">{t("dispatch.inputsHint")}</p>
        </div>
        <div className="acc-actions">
          <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
          <Button variant="primary" type="submit" disabled={dispatch.isPending}>{t("dispatch.run")}</Button>
        </div>
      </form>
    </Dialog>
  );
}
