import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { RUNS_PER_PAGE, useDispatch, useRuns, useWorkflows, type ActionsRun, type ActionsWorkflow, type RunFilter } from "../../api/actions.ts";
import { canWrite } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Dialog, DialogClose, EmptyState, Icon, Input, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, Pager, RepoFrame, shortSha, When } from "../code/common.tsx";
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
      <nav className="ax-side" aria-label={t("workflows.label")}>
        <h2 className="ax-side-h">{t("workflows.title")}</h2>
        <button type="button" className="ax-side-i" aria-current={!search.workflow ? "page" : undefined} onClick={() => go({ workflow: undefined })}>
          <Icon name="layers" />
          {t("workflows.all")}
        </button>
        {workflows.data?.map((w) => (
          <button key={w.path} type="button" className="ax-side-i" aria-current={w.path === search.workflow ? "page" : undefined} title={w.path} onClick={() => go({ workflow: w.path })}>
            <Icon name="play" />
            <span>{w.name || w.path}</span>
          </button>
        ))}
        {workflows.isSuccess && workflows.data.length === 0 && <p className="ui-note ax-side-note">{t("workflows.none")}</p>}
      </nav>
      <div className="ax-main">
        <div className="rp-bar">
          <h2 className="ax-h2">{current ? current.name || current.path : t("runs.title")}</h2>
          {runs.data && <span className="ax-count">{t("runs.count", { count: runs.data.totalCount })}</span>}
          <span className="acc-grow" />
          {current && canWrite(repo.permission) && <DispatchDialog repo={repo} workflow={current} />}
        </div>
        <form
          className="ax-filters"
          onSubmit={(e) => {
            e.preventDefault();
            const v = new FormData(e.currentTarget).get("branch");
            go({ branch: typeof v === "string" && v.trim() ? v.trim() : undefined });
          }}
        >
          <Select label={t("filter.event")} value={search.event ?? "_"} onValueChange={(v) => go({ event: v === "_" ? undefined : v })} options={[{ value: "_", label: t("filter.any") }, ...events.map((e) => ({ value: e, label: e }))]} />
          <Select label={t("filter.status")} value={search.status ?? "_"} onValueChange={(v) => go({ status: v === "_" ? undefined : v })} options={[{ value: "_", label: t("filter.any") }, ...statuses.map((s) => ({ value: s, label: t(`filterStatus.${s}`) }))]} />
          <Input label={t("filter.branch")} name="branch" mono defaultValue={search.branch ?? ""} key={search.branch ?? ""} />
          <Button type="submit" icon="search">{t("filter.apply")}</Button>
        </form>
        <Gate query={runs}>
          {(list) =>
            list.runs.length === 0 ? (
              <EmptyState icon="play" title={t("runs.empty")} description={t("runs.emptyHint")} />
            ) : (
              <>
                <ul className="ax-rows">
                  {list.runs.map((r) => (
                    <RunRow key={r.id} repo={repo} run={r} />
                  ))}
                </ul>
                <Pager page={search.page ?? 1} hasMore={(search.page ?? 1) * RUNS_PER_PAGE < list.totalCount} onPage={(n) => go({ page: n > 1 ? n : undefined })} />
              </>
            )
          }
        </Gate>
      </div>
    </div>
  );
}

function RunRow({ repo, run: r }: { repo: Repo; run: ActionsRun }) {
  const t = useT("actions");
  const { owner, name } = repo;
  return (
    <li className="ax-row">
      <StatusIcon status={r.status} conclusion={r.conclusion} />
      <span className="ax-row-main">
        <Link to="/$owner/$name/actions/runs/$runId" params={{ owner, name, runId: r.id }} className="ax-row-title">
          {r.title || r.workflowName}
        </Link>
        <span className="ax-row-meta">
          <span>{r.workflowName || r.workflowPath}</span>
          <span>#{r.runNumber}</span>
          <span>{r.event}</span>
          <code className="ax-tag">{branchOf(r.ref)}</code>
          <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.sha }} className="ui-mono">{shortSha(r.sha)}</Link>
          <span>{r.triggerActor || r.actor}</span>
          {r.attempt > 1 && <span>{t("run.attempt", { n: r.attempt })}</span>}
          {r.needsApproval && <Badge tone="warn">{t("run.needsApproval")}</Badge>}
          {r.fork && <Badge>{t("run.fork")}</Badge>}
        </span>
      </span>
      <span className="ax-row-time">
        <When iso={r.createdAt} />
        <span>{duration(r.startedAt, r.completedAt)}</span>
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
