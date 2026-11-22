import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent, type ReactNode } from "react";
import { canWrite, useBranches } from "../../api/code.ts";
import { useSession } from "../../api/identity.ts";
import { useCollaborators } from "../../api/issues.ts";
import {
  useAutoMerge,
  useCreatePull,
  useHeadChecks,
  useHeadStatus,
  useDismissReview,
  useMerge,
  useMergeability,
  usePull,
  usePullCommits,
  usePullFiles,
  usePulls,
  useRequestReviewers,
  useReviews,
  useUpdateBranch,
  useUpdatePull,
  type CheckRun,
  type CommitStatus,
  type MergeMethod,
  type PullFilter,
  type PullMergeability,
  type PullRequest,
  type PullReview,
} from "../../api/pulls.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, EmptyState, Icon, Input, Menu, MenuItem, MenuLabel, Select, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { CommitRow } from "../code/HistoryPages.tsx";
import { CopyButton, Gate, Pager, RepoFrame, shortSha, When } from "../code/common.tsx";
import { DetailLayout, WorkHeader } from "../issues/IssuesPages.tsx";
import { Avatar, Body, CommentBox, Composer, EventRow, FilterChip, IconTrigger, LabelChip, ListFrame, ListSearch, RowGroups, SideBlock, StateIcon, StatePill, StatusBox, WorkRow, type WorkState } from "../work/common.tsx";
import { canTriage, Conversation, IssueMeta, type TimelineExtra } from "../work/Conversation.tsx";
import { PullFiles } from "./PullFiles.tsx";

export type PullSearch = {
  state?: "closed" | "merged" | "all";
  q?: string;
  author?: string;
  reviewRequested?: string;
  base?: string;
  sort?: "updated";
  compare?: boolean;
  page?: number;
};
export type PullTab = "commits" | "checks" | "files";

export const pullState = (p: PullRequest): WorkState => (p.state === "merged" ? "merged" : p.state === "closed" ? "closed" : p.draft ? "draft" : "open");

/* List. */

export function PullsPage({ owner, name, search }: { owner: string; name: string; search: PullSearch }) {
  return (
    <RepoFrame owner={owner} name={name} tab="pulls" section="pr" head={false}>
      {(repo) => <PullList repo={repo} search={search} />}
    </RepoFrame>
  );
}

type PullView = { id: "open" | "reviewRequested" | "created" | "merged" | "closed"; icon: IconName; search: PullSearch };

function PullList({ repo, search }: { repo: Repo; search: PullSearch }) {
  const t = useT("pulls");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const me = useSession().data?.username;
  const page = search.page ?? 1;
  const filter: PullFilter = {
    state: search.state ?? "open",
    q: search.q,
    author: search.author,
    reviewRequested: search.reviewRequested,
    base: search.base,
    sort: search.sort,
    page,
  };
  const list = usePulls(owner, name, filter);
  const branches = useBranches(owner, name, 0, 100);

  const go = (patch: Partial<PullSearch>) =>
    void navigate({ to: "/$owner/$name/pulls", params: { owner, name }, search: { ...search, page: undefined, ...patch } });
  const branchNames = (branches.data?.items ?? []).map((b) => b.name);

  const views: PullView[] = [
    { id: "open", icon: "pr", search: {} },
    ...(me
      ? ([
          { id: "reviewRequested", icon: "eye", search: { reviewRequested: me } },
          { id: "created", icon: "plus", search: { author: me } },
        ] as PullView[])
      : []),
    { id: "merged", icon: "pr", search: { state: "merged" } },
    { id: "closed", icon: "x", search: { state: "closed" } },
  ];
  const current = views.find((v) => search.state === v.search.state && search.author === v.search.author && search.reviewRequested === v.search.reviewRequested);
  const plain = current?.id === "open" && !search.q && !search.base;

  return (
    <ListFrame
      viewsLabel={t("filters.views")}
      views={views.map((v) => (
        <Link
          key={v.id}
          className="wk-vw-a"
          to="/$owner/$name/pulls"
          params={{ owner, name }}
          search={{ ...v.search, base: search.base }}
          aria-current={current === v ? "page" : undefined}
        >
          <Icon name={v.icon} className={v.id === "merged" ? "ui-st-merged" : undefined} />
          {t(`view.${v.id}`)}
          {v.id === "open" && !!repo.openPullsCount && <span className="wk-vw-c">{repo.openPullsCount}</span>}
        </Link>
      ))}
      title={current ? t(`view.${current.id}`) : t("list.aria")}
      count={plain && repo.openPullsCount !== undefined ? t("list.count", { count: repo.openPullsCount }) : undefined}
      actions={
        me &&
        canWrite(repo.permission) && <NewPullDialog repo={repo} branches={branchNames} open={search.compare === true} onOpenChange={(o) => go({ compare: o ? true : undefined })} />
      }
      toolbar={
        <>
          <ListSearch key={search.q ?? ""} label={t("list.search")} placeholder={t("list.searchPlaceholder")} initial={search.q ?? ""} onSearch={(q) => go({ q: q || undefined })} />
          <FilterChip label={t("list.base")} value={search.base}>
            <MenuItem icon={!search.base ? "check" : undefined} onSelect={() => go({ base: undefined })}>
              {t("filters.anyBase")}
            </MenuItem>
            {branchNames.map((b) => (
              <MenuItem key={b} icon={search.base === b ? "check" : undefined} onSelect={() => go({ base: b })}>
                <span className="ui-mono">{b}</span>
              </MenuItem>
            ))}
          </FilterChip>
          <FilterChip label={t("list.sort")} value={t(`sort.${search.sort ?? "created"}`)}>
            {(["created", "updated"] as const).map((x) => (
              <MenuItem key={x} icon={(search.sort ?? "created") === x ? "check" : undefined} onSelect={() => go({ sort: x === "updated" ? "updated" : undefined })}>
                {t(`sort.${x}`)}
              </MenuItem>
            ))}
          </FilterChip>
        </>
      }
    >
      <Gate query={list}>
        {(res) =>
          res.items.length === 0 ? (
            <EmptyState icon="pr" title={t("list.empty")} description={t("list.emptyHint")} />
          ) : (
            <>
              <RowGroups label={t("list.aria")} groups={[{ key: "all", items: res.items }]} getKey={(p) => p.issue.id} row={(p) => <PullRow repo={repo} pr={p} />} />
              <Pager page={page} hasMore={res.hasMore} onPage={(n) => go({ page: n > 1 ? n : undefined })} />
            </>
          )
        }
      </Gate>
    </ListFrame>
  );
}

function PullRow({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const i = pr.issue;
  const state = pullState(pr);
  return (
    <WorkRow
      icon={<StateIcon kind="pull" state={state} label={t(`state.${state}`)} />}
      number={i.number}
      link={
        <Link className="wk-ir-t" to="/$owner/$name/pulls/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
          {i.title}
        </Link>
      }
      tags={
        <>
          {i.labels.map((l) => (
            <LabelChip key={l.id} label={l} />
          ))}
          {pr.draft && pr.state === "open" && <span className="ui-chip">{t("state.draft")}</span>}
          {!pr.draft && pr.state === "open" && pr.requestedReviewers.length > 0 && <span className="ui-chip ui-chip-rev">{t("row.reviewRequested")}</span>}
        </>
      }
      side={
        i.commentCount > 0 && (
          <span className="wk-cnt" aria-label={t("row.comments", { count: i.commentCount })}>
            <Icon name="comment" />
            {i.commentCount}
          </span>
        )
      }
      people={
        <span title={i.author}>
          <Avatar user={i.author} size={20} />
        </span>
      }
    />
  );
}

function NewPullDialog({ repo, branches, open, onOpenChange }: { repo: Repo; branches: string[]; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT("pulls");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreatePull(repo.owner, repo.name);
  const [base, setBase] = useState(repo.defaultBranch);
  const [head, setHead] = useState("");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [draft, setDraft] = useState(false);
  const heads = branches.filter((b) => b !== base);
  const pickedHead = heads.includes(head) ? head : (heads[0] ?? "");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { base, head: pickedHead, title: title.trim(), body, draft },
      {
        onSuccess: (p) => void navigate({ to: "/$owner/$name/pulls/$number", params: { owner: repo.owner, name: repo.name, number: String(p.issue.number) } }),
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      trigger={
        <Button variant="primary" icon="plus">
          {t("new.open")}
        </Button>
      }
      title={t("new.title")}
      description={t("new.description")}
    >
      <form className="acc-form" onSubmit={submit}>
        <div className="acc-inline">
          <Select label={t("new.base")} value={base} onValueChange={setBase} options={branches.map((b) => ({ value: b, label: b }))} />
          <Select
            label={t("new.head")}
            value={pickedHead || "-"}
            onValueChange={setHead}
            disabled={heads.length === 0}
            options={heads.length ? heads.map((b) => ({ value: b, label: b })) : [{ value: "-", label: t("new.noHead") }]}
          />
        </div>
        {pickedHead && (
          <Link className="ui-note" to="/$owner/$name/compare" params={{ owner: repo.owner, name: repo.name }} search={{ base, head: pickedHead }}>
            {t("new.compare")}
          </Link>
        )}
        <Input label={t("new.titleLabel")} required maxLength={256} value={title} onChange={(e) => setTitle(e.target.value)} />
        <Composer owner={repo.owner} name={repo.name} refName={repo.defaultBranch} value={body} onChange={setBody} label={t("new.body")} rows={6} />
        <label className="acc-check">
          <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} />
          {t("new.draft")}
        </label>
        <div className="acc-actions">
          <DialogClose asChild>
            <Button variant="ghost">{t("action.cancel")}</Button>
          </DialogClose>
          <Button type="submit" disabled={create.isPending || !title.trim() || !pickedHead}>
            {draft ? t("new.submitDraft") : t("new.submit")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/* Detail. */

export function PullPage({ owner, name, number, tab, page }: { owner: string; name: string; number: number; tab?: PullTab; page?: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="pulls" section="pr" head={false}>
      {(repo) => <PullLoader repo={repo} number={number} tab={tab} page={page ?? 1} />}
    </RepoFrame>
  );
}

function PullLoader({ repo, number, tab, page }: { repo: Repo; number: number; tab?: PullTab; page: number }) {
  const t = useT("pulls");
  const pr = usePull(repo.owner, repo.name, number);
  return (
    <Gate query={pr} notFound={<EmptyState icon="pr" title={t("detail.notFound")} />}>
      {(p) => <PullDetail repo={repo} pr={p} tab={tab} page={page} />}
    </Gate>
  );
}

function PullDetail({ repo, pr, tab, page }: { repo: Repo; pr: PullRequest; tab?: PullTab; page: number }) {
  const t = useT("pulls");
  const notify = useNotify();
  const me = useSession().data?.username;
  const update = useUpdatePull(repo.owner, repo.name, pr.issue.number);
  const i = pr.issue;
  const canEdit = (me !== undefined && me === i.author) || canWrite(repo.permission);
  const canDraft = canEdit && pr.state === "open";
  const toggleDraft = () => update.mutate({ draft: !pr.draft }, { onError: notify.fail });
  const merged = pr.state === "merged";

  const header = (
    <WorkHeader
      kind="pulls"
      repo={repo}
      title={i.title}
      number={i.number}
      canEdit={canEdit && !merged}
      saving={update.isPending}
      onRename={(title) => update.mutate({ title }, { onError: notify.fail })}
      meta={<PullMeta repo={repo} pr={pr} />}
      tabs={<PullTabs repo={repo} pr={pr} tab={tab} />}
    />
  );

  if (tab === "files") return <PullFiles repo={repo} pr={pr} header={<div className="wk-detail">{header}</div>} />;
  if (tab !== undefined) {
    return (
      <div className="wk-detail">
        {header}
        {tab === "commits" ? <PullCommits repo={repo} pr={pr} page={page} /> : <PullChecks repo={repo} pr={pr} />}
      </div>
    );
  }

  return (
    <DetailLayout
      header={header}
      sidebar={
        <>
          <MergeStatus repo={repo} pr={pr} onReady={canDraft ? toggleDraft : undefined} />
          <IssueMeta
            repo={repo}
            issue={i}
            reviewers={pr.requestedReviewers.length}
            before={<ReviewersBlock repo={repo} pr={pr} />}
            openedLabel={t("side.openedBy")}
            menu={
              canDraft && (
                <MenuItem icon="edit" onSelect={toggleDraft}>
                  {pr.draft ? t("detail.ready") : t("detail.toDraft")}
                </MenuItem>
              )
            }
          />
        </>
      }
    >
      <PullConversation repo={repo} pr={pr} canEdit={canEdit} />
    </DetailLayout>
  );
}

/** "X wants to merge into base from head", the branch copy button and the Code menu. */
function PullMeta({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const ta = useT("account");
  const notify = useNotify();
  const i = pr.issue;
  const merged = pr.state === "merged";
  const state = pullState(pr);
  const [headOwner, headName] = pr.head.repo ? pr.head.repo.split("/") : [repo.owner, repo.name];
  const checkout = `git fetch origin ${pr.head.ref} && git switch ${pr.head.ref}`;
  return (
    <>
      <StatePill kind="pull" state={state} label={t(`state.${state}`)} />
      <span className="wk-dh-who">
        <b>{merged ? pr.mergedBy : i.author}</b> {t(merged ? "detail.mergedInto" : "detail.wantsInto")} <code className="wk-ref">{pr.base.ref}</code> {t("detail.from")}{" "}
        <code className="wk-ref">{pr.head.repo && pr.head.repo !== `${repo.owner}/${repo.name}` ? `${pr.head.repo}:${pr.head.ref}` : pr.head.ref}</code>
      </span>
      <CopyButton value={pr.head.ref} label={t("detail.copyBranch")} className="wk-ib" />
      <span className="wk-dh-r">
        <Menu
          align="end"
          label={t("detail.code")}
          trigger={
            <Button icon="code">
              {t("detail.code")}
              <Icon name="down" />
            </Button>
          }
        >
          {headOwner && headName && (
            <MenuItem icon="folder" asChild>
              <Link to="/$owner/$name" params={{ owner: headOwner, name: headName }} search={{ ref: pr.head.ref }}>
                {t("detail.browse")}
              </Link>
            </MenuItem>
          )}
          <MenuItem icon="terminal" onSelect={() => void navigator.clipboard.writeText(checkout).then(() => notify.ok(ta("copied")), (e: unknown) => notify.fail(e))}>
            {t("detail.copyCheckout")}
          </MenuItem>
        </Menu>
      </span>
    </>
  );
}

/** Conversation / Commits / Checks / Files changed, with counts where the data is at hand. */
function PullTabs({ repo, pr, tab }: { repo: Repo; pr: PullRequest; tab?: PullTab }) {
  const t = useT("pulls");
  const i = pr.issue;
  const params = { owner: repo.owner, name: repo.name, number: String(i.number) };
  const commits = usePullCommits(repo.owner, repo.name, i.number, 1);
  const files = usePullFiles(repo.owner, repo.name, i.number);
  const status = useHeadStatus(repo.owner, repo.name, pr.head.sha);
  const checks = useHeadChecks(repo.owner, repo.name, pr.head.sha);
  const nChecks = status.data && checks.data ? status.data.statuses.length + checks.data.checkRuns.length : undefined;
  const nCommits = commits.data ? (commits.data.hasMore ? `${commits.data.items.length}+` : commits.data.items.length) : undefined;
  const tabs: { id?: PullTab; icon: IconName; label: string; count?: number | string }[] = [
    { icon: "comment", label: t("tab.conversation"), count: i.commentCount },
    { id: "commits", icon: "commit", label: t("tab.commits"), count: nCommits },
    { id: "checks", icon: "check", label: t("tab.checks"), count: nChecks },
    { id: "files", icon: "file", label: t("tab.files"), count: files.data?.length },
  ];
  return (
    <nav className="wk-prt" aria-label={t("detail.tabs")}>
      {tabs.map((x) => (
        <Link key={x.id ?? "conversation"} to="/$owner/$name/pulls/$number" params={params} search={x.id ? { tab: x.id } : {}} aria-current={tab === x.id ? "page" : undefined}>
          <Icon name={x.icon} />
          {x.label}
          {x.count !== undefined && <span className="ui-n">{x.count}</span>}
        </Link>
      ))}
    </nav>
  );
}

function PullConversation({ repo, pr, canEdit }: { repo: Repo; pr: PullRequest; canEdit: boolean }) {
  const t = useT("pulls");
  const notify = useNotify();
  const update = useUpdatePull(repo.owner, repo.name, pr.issue.number);
  const reviews = useReviews(repo.owner, repo.name, pr.issue.number);
  const extra: TimelineExtra[] = (reviews.data ?? [])
    .filter((r) => r.state !== "pending")
    .map((r) => ({ key: `review-${r.id}`, at: r.submittedAt ?? r.createdAt, kind: r.body ? "post" : "event", node: <ReviewItem repo={repo} pr={pr} r={r} /> }));
  if (pr.state === "merged" && pr.mergedAt) {
    extra.push({
      key: "merged",
      at: pr.mergedAt,
      kind: "event",
      node: (
        <EventRow icon="pr" tone="merged">
          {t("ev.merged", { user: pr.mergedBy, commit: shortSha(pr.mergeCommit), base: pr.base.ref })}
        </EventRow>
      ),
    });
  }
  const canMerge = canWrite(repo.permission) && pr.state === "open";
  return (
    <Conversation
      repo={repo}
      issue={pr.issue}
      extra={extra}
      canClose={canEdit && pr.state !== "merged"}
      stateBusy={update.isPending}
      onState={(s) => update.mutate({ state: s }, { onError: notify.fail })}
      closeLabels={{ close: t("detail.close"), reopen: t("detail.reopen"), withComment: t("detail.closeWithComment") }}
      primarySubmit={!canMerge}
    />
  );
}

const reviewIcon: Record<PullReview["state"], { icon: "check" | "alert" | "book" | "x" | "clock"; tone?: WorkState }> = {
  approved: { icon: "check", tone: "open" },
  changes_requested: { icon: "alert", tone: "closed" },
  commented: { icon: "book" },
  dismissed: { icon: "x" },
  pending: { icon: "clock" },
};

const verdictChip: Partial<Record<PullReview["state"], string>> = { approved: "ui-chip ui-chip-ok", changes_requested: "ui-chip ui-chip-err", dismissed: "ui-chip" };

/** A review: a post with a verdict chip when it has a summary, an event row otherwise. */
function ReviewItem({ repo, pr, r }: { repo: Repo; pr: PullRequest; r: PullReview }) {
  const t = useT("pulls");
  const notify = useNotify();
  const dismiss = useDismissReview(repo.owner, repo.name, pr.issue.number);
  const [message, setMessage] = useState("");
  const [open, setOpen] = useState(false);
  const canDismiss = canWrite(repo.permission) && (r.state === "approved" || r.state === "changes_requested");
  const ic = reviewIcon[r.state];
  const dismissDialog = canDismiss && (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <button type="button" className="wk-linkbtn">
          {t("review.dismiss")}
        </button>
      }
      title={t("review.dismissTitle")}
      description={t("review.dismissHint")}
    >
      <form
        className="acc-form"
        onSubmit={(e) => {
          e.preventDefault();
          dismiss.mutate({ id: r.id, message: message.trim() }, { onSuccess: () => setOpen(false), onError: notify.fail });
        }}
      >
        <Input label={t("review.dismissMessage")} required value={message} onChange={(e) => setMessage(e.target.value)} />
        <div className="acc-actions">
          <DialogClose asChild>
            <Button variant="ghost">{t("action.cancel")}</Button>
          </DialogClose>
          <Button type="submit" variant="danger" disabled={dismiss.isPending || !message.trim()}>
            {t("review.dismissConfirm")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
  const notes = (
    <>
      {r.state === "dismissed" && r.dismissedBy && <span className="ui-note"> {t("review.dismissedBy", { user: r.dismissedBy })}</span>}
      {r.dismissMessage && <span className="ui-note"> {t("review.dismissReason", { message: r.dismissMessage })}</span>}
    </>
  );
  if (!r.body) {
    return (
      <EventRow icon={ic.icon} tone={ic.tone}>
        <b>{r.reviewer}</b> {t(`review.${r.state}`)} <When iso={r.submittedAt ?? r.createdAt} />
        {notes}
        {dismissDialog}
      </EventRow>
    );
  }
  return (
    <CommentBox
      author={r.reviewer}
      head={
        <>
          <b>{r.reviewer}</b>
          <span className={verdictChip[r.state] ?? "ui-chip"}>{t(`review.chip.${r.state}`)}</span>
          <When iso={r.submittedAt ?? r.createdAt} />
          {notes}
          <span className="wk-grow" />
          {dismissDialog}
        </>
      }
    >
      <Body owner={repo.owner} name={repo.name} refName={pr.head.ref} source={r.body} />
    </CommentBox>
  );
}

function ReviewersBlock({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const notify = useNotify();
  const triage = canTriage(repo.permission);
  const people = useCollaborators(repo.owner, repo.name, triage);
  const reviews = useReviews(repo.owner, repo.name, pr.issue.number);
  const request = useRequestReviewers(repo.owner, repo.name, pr.issue.number);
  // Latest submitted verdict of each reviewer.
  const latest = new Map<string, PullReview>();
  for (const r of reviews.data ?? []) {
    if (r.state === "pending" || r.state === "commented") continue;
    const cur = latest.get(r.reviewer);
    if (!cur || (r.submittedAt ?? r.createdAt) > (cur.submittedAt ?? cur.createdAt)) latest.set(r.reviewer, r);
  }
  const names = Array.from(new Set([...pr.requestedReviewers, ...latest.keys()])).sort();
  const candidates = (people.data ?? []).map((p) => p.username).filter((u) => u !== pr.issue.author);
  return (
    <SideBlock
      title={t("side.reviewers")}
      action={
        triage &&
        pr.state === "open" && (
          <Menu align="end" trigger={<IconTrigger icon="edit" label={t("side.editReviewers")} />}>
            <MenuLabel>{t("side.requestFrom")}</MenuLabel>
            {candidates.map((u) => {
              const on = pr.requestedReviewers.includes(u);
              return (
                <MenuItem key={u} icon={on ? "check" : undefined} onSelect={() => request.mutate({ add: !on, reviewers: [u] }, { onError: notify.fail })}>
                  {u}
                </MenuItem>
              );
            })}
          </Menu>
        )
      }
    >
      {names.length === 0 ? (
        <span className="wk-prow-none">{t("side.noReviewers")}</span>
      ) : (
        <ul className="wk-people">
          {names.map((u) => {
            const r = latest.get(u);
            return (
              <li key={u}>
                <Avatar user={u} size={20} />
                {u}
                <span className="acc-grow" />
                <span className={`wk-verdict wk-verdict-${r?.state ?? "pending"}`} role="img" aria-label={r ? t(`review.${r.state}`) : t("side.awaiting")} title={r ? t(`review.${r.state}`) : t("side.awaiting")}>
                  <Icon name={r ? reviewIcon[r.state].icon : "clock"} />
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </SideBlock>
  );
}

/* Status box: merge readiness and the merge controls. */

const methods: MergeMethod[] = ["merge", "squash", "rebase"];

function Check({ ok, children, tone }: { ok: boolean; children: ReactNode; tone?: "warn" }) {
  return (
    <li className={`wk-check ${ok ? "wk-check-ok" : tone === "warn" ? "wk-check-warn" : "wk-check-err"}`}>
      <Icon name={ok ? "check" : tone === "warn" ? "alert" : "x"} />
      <span>{children}</span>
    </li>
  );
}

const statusTone = (s: string): "ok" | "warn" | "err" => (s === "success" ? "ok" : s === "pending" ? "warn" : "err");
const runTone = (r: CheckRun): "ok" | "warn" | "err" =>
  r.status !== "completed" ? "warn" : r.conclusion === "success" || r.conclusion === "neutral" || r.conclusion === "skipped" ? "ok" : "err";

type CheckLine = { key: string; tone: "ok" | "warn" | "err"; name: string; detail: string; url: string };

/** Commit statuses and latest check runs of the head commit as one list. */
function useHeadLines(repo: Repo, sha: string): { lines: CheckLine[]; loading: boolean } {
  const t = useT("pulls");
  const status = useHeadStatus(repo.owner, repo.name, sha);
  const checks = useHeadChecks(repo.owner, repo.name, sha);
  const statuses: CommitStatus[] = status.data?.statuses ?? [];
  const runs = checks.data?.checkRuns ?? [];
  const lines: CheckLine[] = [
    ...statuses.map((x) => ({ key: `s:${x.context}`, tone: statusTone(x.state), name: x.context, detail: x.description || t(`checks.state.${x.state}`), url: x.targetUrl })),
    ...runs.map((r) => ({
      key: `r:${r.id}`,
      tone: runTone(r),
      name: r.name,
      detail: r.title || (r.status === "completed" ? t("checks.conclusion", { conclusion: r.conclusion }) : t(`checks.run.${r.status}`)),
      url: r.detailsUrl,
    })),
  ];
  return { lines, loading: status.isPending || checks.isPending };
}

const toneIcon = { ok: "check", warn: "clock", err: "x" } as const;

function CheckRows({ lines, details }: { lines: CheckLine[]; details?: boolean }) {
  const t = useT("pulls");
  return (
    <ul className="wk-cks">
      {lines.map((l) => (
        <li key={l.key} className={`wk-ck wk-check-${l.tone}`}>
          <Icon name={toneIcon[l.tone]} />
          <b>{l.name}</b>
          <span className="wk-ck-d">{l.detail}</span>
          {details && l.url && (
            <a href={l.url} target="_blank" rel="noreferrer noopener">
              {t("checks.details")}
            </a>
          )}
        </li>
      ))}
    </ul>
  );
}

/** The one thing that stops the merge, most actionable first. */
function blockedBy(t: ReturnType<typeof useT>, data: PullMergeability): string {
  if (data.mergeable === "conflict") return t("merge.conflicts", { count: data.conflictFiles.length });
  if (data.mergeable === "unknown") return t("merge.unknown");
  if (data.mergeable === "error") return t("merge.error");
  if (data.missingChecks.length > 0) return t("merge.missingChecks", { checks: data.missingChecks.join(", ") });
  if (data.approvals < data.requiredApprovals) return t("merge.approvals", { approvals: data.approvals, required: data.requiredApprovals });
  const owners = data.codeOwners.find((c) => !c.approved);
  if (owners) return t("merge.ownersWait", { owners: owners.owners.join(", "), pattern: owners.pattern });
  return data.violations[0]?.message ?? t("merge.blocked");
}

function MergeStatus({ repo, pr, onReady }: { repo: Repo; pr: PullRequest; onReady?: () => void }) {
  const t = useT("pulls");
  const notify = useNotify();
  const writer = canWrite(repo.permission);
  const open = pr.state === "open";
  const m = useMergeability(repo.owner, repo.name, pr.issue.number, open);
  const merge = useMerge(repo.owner, repo.name, pr.issue.number);
  const auto = useAutoMerge(repo.owner, repo.name, pr.issue.number);
  const update = useUpdateBranch(repo.owner, repo.name, pr.issue.number);
  // The server checks the push permission on the head repository.
  const me = useSession().data?.username;
  const canUpdate = writer || (me !== undefined && me === pr.issue.author);
  const [method, setMethod] = useState<MergeMethod>(pr.autoMerge?.method ?? "merge");
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [title, setTitle] = useState("");
  const head = useHeadLines(repo, pr.head.sha);

  if (pr.state === "merged") {
    return (
      <StatusBox>
        <div className="wk-merge-head">
          <StateIcon kind="pull" state="merged" label={t("state.merged")} />
          <div>
            <b>{t("merge.done")}</b>
            <p className="ui-note">
              {t("merge.doneHint", { user: pr.mergedBy, base: pr.base.ref })}{" "}
              {pr.mergeCommit && (
                <Link to="/$owner/$name/commit/$sha" params={{ owner: repo.owner, name: repo.name, sha: pr.mergeCommit }} className="ui-mono">
                  {shortSha(pr.mergeCommit)}
                </Link>
              )}
            </p>
          </div>
        </div>
      </StatusBox>
    );
  }
  if (!open) {
    return (
      <StatusBox>
        <div className="wk-merge-head">
          <StateIcon kind="pull" state="closed" label={t("state.closed")} />
          <div>
            <b>{t("merge.closed")}</b>
            <p className="ui-note">{t("merge.closedHint")}</p>
          </div>
        </div>
      </StatusBox>
    );
  }

  const data = m.data;
  const ready = data?.canMerge === true && !pr.draft;
  const why = pr.draft ? undefined : !data ? t("merge.checking") : data.canMerge ? undefined : blockedBy(t, data);
  const passed = head.lines.filter((l) => l.tone === "ok").length;
  return (
    <StatusBox>
      <span className={ready ? "ui-state" : "ui-state ui-state-draft"}>
        <Icon name="pr" />
        {ready ? t("merge.ready") : pr.draft ? t("state.draft") : t("merge.notReady")}
      </span>
      <ul className="wk-checks">
        {pr.draft && (
          <Check ok={false} tone="warn">
            {t("merge.draft")}
            {onReady && (
              <Button size="sm" onClick={onReady}>
                {t("detail.ready")}
              </Button>
            )}
          </Check>
        )}
        {data &&
          (data.mergeable === "conflict" ? (
            <Check ok={false}>
              {t("merge.conflicts", { count: data.conflictFiles.length })}
              {data.conflictFiles.length > 0 && (
                <span className="wk-files">
                  {data.conflictFiles.map((f) => (
                    <code key={f} className="ui-mono">
                      {f}
                    </code>
                  ))}
                </span>
              )}
            </Check>
          ) : data.mergeable === "unknown" ? (
            <Check ok={false} tone="warn">
              {t("merge.unknown")}
            </Check>
          ) : data.mergeable === "error" ? (
            <Check ok={false}>{t("merge.error")}</Check>
          ) : (
            <Check ok>{t("merge.noConflicts")}</Check>
          ))}
        {data && data.requiredApprovals > 0 && (
          <Check ok={data.approvals >= data.requiredApprovals}>{t("merge.approvals", { approvals: data.approvals, required: data.requiredApprovals })}</Check>
        )}
        {data && data.missingChecks.length > 0 && <Check ok={false}>{t("merge.missingChecks", { checks: data.missingChecks.join(", ") })}</Check>}
        {data?.behind && (
          <Check ok={false} tone="warn">
            {t("merge.behind", { base: pr.base.ref })}
            {canUpdate && (
              <Button
                size="sm"
                disabled={update.isPending}
                onClick={() => update.mutate({ method: "merge", expectedHeadSha: pr.head.sha }, { onSuccess: () => notify.ok(t("merge.updated")), onError: notify.fail })}
              >
                {t("merge.updateBranch")}
              </Button>
            )}
          </Check>
        )}
        {data?.codeOwners.map((c) => (
          <Check key={c.pattern} ok={c.approved}>
            {t(c.approved ? "merge.ownersOk" : "merge.ownersWait", { owners: c.owners.join(", "), pattern: c.pattern })}
          </Check>
        ))}
        {data?.codeOwnersErrors.map((e) => (
          <Check key={e} ok={false} tone="warn">
            {t("merge.ownersError", { error: e })}
          </Check>
        ))}
        {data?.violations.map((v) => (
          <Check key={v.code + (v.commit ?? "")} ok={false}>
            {v.message}
            {v.commit && <code className="ui-mono"> {shortSha(v.commit)}</code>}
          </Check>
        ))}
        {head.lines.length > 0 && (
          <Check ok={passed === head.lines.length} tone={head.lines.some((l) => l.tone === "err") ? undefined : "warn"}>
            {t("merge.checksCount", { passed, total: head.lines.length })}
          </Check>
        )}
      </ul>
      {head.lines.length > 0 && <CheckRows lines={head.lines} />}
      {data?.rule && <p className="ui-note">{t("merge.rule", { rule: data.rule })}</p>}
      {pr.autoMerge && (
        <div className="ui-note wk-merge-auto">
          <Icon name="clock" />
          <span className="wk-grow">{t("merge.autoOn", { user: pr.autoMerge.enabledBy, method: t(`method.${pr.autoMerge.method}`) })}</span>
          {writer && (
            <Button size="sm" disabled={auto.isPending} onClick={() => auto.mutate(null, { onError: notify.fail })}>
              {t("merge.autoOff")}
            </Button>
          )}
        </div>
      )}
      {writer ? (
        <>
          <div className="wk-split">
            <Button
              variant="primary"
              className="wk-split-main wk-merge"
              disabled={!ready || merge.isPending}
              onClick={() =>
                merge.mutate(
                  { method, deleteBranch, title: title.trim() || undefined, sha: data?.headSha },
                  { onSuccess: () => notify.ok(t("merge.merged")), onError: notify.fail },
                )
              }
            >
              {t(`merge.do.${method}`)}
            </Button>
            <Menu align="end" label={t("merge.method")} trigger={<Button variant="primary" className="wk-split-more wk-merge" icon="down" aria-label={t("merge.method")} title={t("merge.method")} />}>
              <MenuLabel>{t("merge.method")}</MenuLabel>
              {methods.map((x) => (
                <MenuItem key={x} icon={x === method ? "check" : undefined} onSelect={() => setMethod(x)}>
                  {t(`method.${x}`)}
                </MenuItem>
              ))}
            </Menu>
          </div>
          <label className="acc-check wk-delbr">
            <input type="checkbox" checked={deleteBranch} onChange={(e) => setDeleteBranch(e.target.checked)} />
            {t("merge.deleteBranch", { head: pr.head.ref })}
          </label>
          {why && (
            <p className="wk-why">
              <Icon name="clock" />
              {why}
            </p>
          )}
          {!pr.autoMerge && !ready && (
            <Button
              disabled={auto.isPending || pr.draft}
              onClick={() => auto.mutate({ method, deleteBranch, title: title.trim() || undefined }, { onSuccess: () => notify.ok(t("merge.autoEnabled")), onError: notify.fail })}
            >
              {t("merge.autoEnable")}
            </Button>
          )}
          {method !== "rebase" && (
            <details className="wk-opts">
              <summary>{t("merge.options")}</summary>
              <div className="wk-opts-b">
                <Input label={t("merge.commitTitle")} placeholder={method === "squash" ? `${pr.issue.title} (#${pr.issue.number})` : t("merge.commitTitleHint")} value={title} onChange={(e) => setTitle(e.target.value)} />
              </div>
            </details>
          )}
        </>
      ) : (
        <p className="ui-note">{t("merge.noPermission")}</p>
      )}
    </StatusBox>
  );
}

/* Checks. */

function PullChecks({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const head = useHeadLines(repo, pr.head.sha);
  if (head.loading) return <p className="ui-note acc-pad">{t("checks.loading")}</p>;
  if (head.lines.length === 0) return <EmptyState icon="check" title={t("checks.empty")} description={t("checks.emptyHint", { sha: shortSha(pr.head.sha) })} />;
  const passed = head.lines.filter((l) => l.tone === "ok").length;
  return (
    <section className="ui-box">
      <div className="ui-box-h">
        <b>{t("checks.summary", { passed, total: head.lines.length })}</b>
        <span className="ui-mono ui-tm">{shortSha(pr.head.sha)}</span>
      </div>
      <div className="ui-in">
        <CheckRows lines={head.lines} details />
      </div>
    </section>
  );
}

/* Commits. */

function PullCommits({ repo, pr, page }: { repo: Repo; pr: PullRequest; page: number }) {
  const t = useT("pulls");
  const navigate = useNavigate();
  const commits = usePullCommits(repo.owner, repo.name, pr.issue.number, page);
  return (
    <Gate query={commits}>
      {(res) =>
        res.items.length === 0 ? (
          <EmptyState icon="commit" title={t("commits.empty")} />
        ) : (
          <>
            <ul className="rp-list ui-box wk-commits">
              {res.items.map((c) => (
                <CommitRow key={c.sha} repo={repo} c={c} />
              ))}
            </ul>
            <Pager
              page={page}
              hasMore={res.hasMore}
              onPage={(n) =>
                void navigate({
                  to: "/$owner/$name/pulls/$number",
                  params: { owner: repo.owner, name: repo.name, number: String(pr.issue.number) },
                  search: { tab: "commits", page: n > 1 ? n : undefined },
                })
              }
            />
          </>
        )
      }
    </Gate>
  );
}
