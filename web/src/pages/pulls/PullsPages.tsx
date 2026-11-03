import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { canWrite, useBranches } from "../../api/code.ts";
import { useSession } from "../../api/identity.ts";
import { useCollaborators } from "../../api/issues.ts";
import {
  useAutoMerge,
  useCreatePull,
  useDismissReview,
  useMerge,
  useMergeability,
  usePull,
  usePullCommits,
  usePulls,
  useRequestReviewers,
  useReviews,
  useUpdatePull,
  type MergeMethod,
  type PullFilter,
  type PullRequest,
  type PullReview,
} from "../../api/pulls.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, EmptyState, Icon, Input, Menu, MenuItem, MenuLabel, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { CommitRow } from "../code/HistoryPages.tsx";
import { Gate, Pager, RepoFrame, shortSha, When } from "../code/common.tsx";
import { DetailLayout, StepNav, StripState, WorkHeader } from "../issues/IssuesPages.tsx";
import { Avatar, Body, CommentBox, Composer, EventRow, FilterGroup, FilterItem, LabelChip, ListLayoutSwitch, SideBlock, StateIcon, StatePill, WorkList, type WorkState } from "../work/common.tsx";
import { AssigneesBlock, canTriage, Conversation, LabelsBlock, MilestoneBlock, NotifyBlock, ParticipantsBlock, type TimelineExtra } from "../work/Conversation.tsx";
import { saveListOrder, useListLayout, type ListLayout } from "../work/layout.ts";
import { PullFiles } from "./PullFiles.tsx";

export type PullSearch = {
  state?: "closed" | "merged" | "all";
  author?: string;
  base?: string;
  sort?: "updated";
  compare?: boolean;
  page?: number;
};
export type PullTab = "commits" | "files";

export const pullState = (p: PullRequest): WorkState => (p.state === "merged" ? "merged" : p.state === "closed" ? "closed" : p.draft ? "draft" : "open");

/* List. */

export function PullsPage({ owner, name, search }: { owner: string; name: string; search: PullSearch }) {
  return (
    <RepoFrame owner={owner} name={name} tab="pulls" section="pr">
      {(repo) => <PullList repo={repo} search={search} />}
    </RepoFrame>
  );
}

function PullList({ repo, search }: { repo: Repo; search: PullSearch }) {
  const t = useT("pulls");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const layout = useListLayout();
  const me = useSession().data?.username;
  const page = search.page ?? 1;
  const filter: PullFilter = { state: search.state ?? "open", author: search.author, base: search.base, sort: search.sort, page };
  const list = usePulls(owner, name, filter);
  const branches = useBranches(owner, name, 0, 100);

  const go = (patch: Partial<PullSearch>) =>
    void navigate({ to: "/$owner/$name/pulls", params: { owner, name }, search: { ...search, page: undefined, ...patch } });

  const numbers = list.data?.items.map((p) => p.issue.number).join(",");
  useEffect(() => {
    if (numbers !== undefined) saveListOrder("pulls", owner, name, numbers ? numbers.split(",").map(Number) : []);
  }, [numbers, owner, name]);

  const open = (p: PullRequest) => void navigate({ to: "/$owner/$name/pulls/$number", params: { owner, name, number: String(p.issue.number) } });
  const branchNames = (branches.data?.items ?? []).map((b) => b.name);

  const filters = (
    <>
      <FilterGroup title={t("filters.views")}>
        <FilterItem active={!search.author} onClick={() => go({ author: undefined })}>
          {t("view.all")}
        </FilterItem>
        {me && (
          <FilterItem active={search.author === me} onClick={() => go({ author: me })}>
            {t("view.created")}
          </FilterItem>
        )}
      </FilterGroup>
      <FilterGroup title={t("filters.base")}>
        <FilterItem active={!search.base} onClick={() => go({ base: undefined })}>
          {t("filters.anyBase")}
        </FilterItem>
        {branchNames.map((b) => (
          <FilterItem key={b} active={search.base === b} onClick={() => go({ base: b })}>
            <span className="ui-mono">{b}</span>
          </FilterItem>
        ))}
      </FilterGroup>
    </>
  );

  return (
    <>
      <div className="rp-bar wk-bar">
        <div className="ui-tablist" role="group" aria-label={t("list.stateFilter")}>
          {(["open", "closed", "merged", "all"] as const).map((s) => {
            const active = (search.state ?? "open") === s;
            return (
              <button key={s} type="button" className="ui-tab" data-state={active ? "active" : "inactive"} aria-pressed={active} onClick={() => go({ state: s === "open" ? undefined : s })}>
                {t(`list.${s}`)}
              </button>
            );
          })}
        </div>
        {layout !== "b" && (
          <Select
            label={t("list.base")}
            className="wk-fsel"
            value={search.base ?? "-"}
            onValueChange={(v) => go({ base: v === "-" ? undefined : v })}
            options={[{ value: "-", label: t("filters.anyBase") }, ...branchNames.map((b) => ({ value: b, label: b }))]}
          />
        )}
        <Select
          label={t("list.sort")}
          className="wk-fsel"
          value={search.sort ?? "created"}
          onValueChange={(v) => go({ sort: v === "updated" ? "updated" : undefined })}
          options={(["created", "updated"] as const).map((s) => ({ value: s, label: t(`sort.${s}`) }))}
        />
        <span className="acc-grow" />
        <ListLayoutSwitch />
        {me && canWrite(repo.permission) && (
          <NewPullDialog repo={repo} branches={branchNames} open={search.compare === true} onOpenChange={(o) => go({ compare: o ? true : undefined })} />
        )}
      </div>
      <Gate query={list}>
        {(res) => (
          <>
            <WorkList
              layout={layout}
              items={res.items}
              getKey={(p) => p.issue.id}
              label={t("list.aria")}
              filters={filters}
              onOpen={open}
              empty={<EmptyState icon="pr" title={t("list.empty")} description={t("list.emptyHint")} />}
              row={(p, l) => <PullRow repo={repo} pr={p} layout={l} />}
              preview={(p) => <PullPreview repo={repo} pr={p} />}
            />
            <Pager page={page} hasMore={res.hasMore} onPage={(n) => go({ ...search, page: n > 1 ? n : undefined })} />
          </>
        )}
      </Gate>
    </>
  );
}

function PullRow({ repo, pr, layout }: { repo: Repo; pr: PullRequest; layout: ListLayout }) {
  const t = useT("pulls");
  const i = pr.issue;
  const state = pullState(pr);
  const icon = <StateIcon kind="pull" state={state} label={t(`state.${state}`)} />;
  const meta = (
    <span className="wk-meta">
      <span>#{i.number}</span>
      <span>{pr.state === "merged" ? t("row.merged", { user: pr.mergedBy || i.author }) : t("row.opened", { user: i.author })}</span>
      <When iso={pr.state === "merged" && pr.mergedAt ? pr.mergedAt : i.createdAt} />
      <span className="ui-mono wk-meta-br">{t("row.branches", { head: pr.head.ref, base: pr.base.ref })}</span>
    </span>
  );
  const labels = i.labels.length > 0 && (
    <span className="wk-labels">
      {i.labels.map((l) => (
        <LabelChip key={l.id} label={l} />
      ))}
    </span>
  );
  const comments = (
    <span className="wk-cnt" aria-label={t("row.comments", { count: i.commentCount })}>
      {i.commentCount > 0 && (
        <>
          <Icon name="book" />
          {i.commentCount}
        </>
      )}
    </span>
  );
  const people = (
    <span className="wk-avatars" aria-label={pr.requestedReviewers.length ? t("row.reviewers", { users: pr.requestedReviewers.join(", ") }) : undefined}>
      {pr.requestedReviewers.slice(0, 3).map((u) => (
        <span key={u} title={u}>
          <Avatar user={u} size={20} />
        </span>
      ))}
    </span>
  );
  if (layout === "c") {
    return (
      <span className="wk-ci-in">
        {icon}
        <span className="wk-ci-main">
          <span className="wk-title">{i.title}</span>
          <span className="wk-meta">
            <span>#{i.number}</span>
            <When iso={i.updatedAt} />
          </span>
        </span>
        {comments}
      </span>
    );
  }
  const link = (
    <Link className="wk-title" to="/$owner/$name/pulls/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
      {i.title}
    </Link>
  );
  if (layout === "b") {
    return (
      <div className="wk-rb">
        {icon}
        <div className="wk-rb-main">
          <div className="wk-rb-l1">
            {link}
            {labels}
          </div>
          {meta}
        </div>
        <div className="wk-rb-side">
          {people}
          {comments}
        </div>
      </div>
    );
  }
  return (
    <div className="wk-ra">
      {icon}
      <div className="wk-ra-main">
        {link}
        {labels}
        {meta}
      </div>
      {people}
      {comments}
      <When iso={i.updatedAt} />
    </div>
  );
}

function PullPreview({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const i = pr.issue;
  const state = pullState(pr);
  return (
    <div className="wk-pv-in">
      <div className="wk-pv-head">
        <StatePill state={state} label={t(`state.${state}`)} />
        <span className="ui-note">#{i.number}</span>
        <span className="acc-grow" />
        <Link to="/$owner/$name/pulls/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
          <Button size="sm">{t("preview.open")}</Button>
        </Link>
      </div>
      <h3 className="wk-pv-title">{i.title}</h3>
      <p className="wk-meta">
        <span>{t("row.opened", { user: i.author })}</span>
        <When iso={i.createdAt} />
        <span className="ui-mono">{t("row.branches", { head: pr.head.ref, base: pr.base.ref })}</span>
      </p>
      <Body owner={repo.owner} name={repo.name} refName={repo.defaultBranch} source={i.body} />
    </div>
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
    <RepoFrame owner={owner} name={name} tab="pulls" section="pr">
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
  const layout = useListLayout();
  const update = useUpdatePull(repo.owner, repo.name, pr.issue.number);
  const i = pr.issue;
  const state = pullState(pr);
  const canEdit = (me !== undefined && me === i.author) || canWrite(repo.permission);
  const params = { owner: repo.owner, name: repo.name, number: String(i.number) };

  const header = (
    <WorkHeader
      title={i.title}
      number={i.number}
      canEdit={canEdit && pr.state !== "merged"}
      saving={update.isPending}
      onRename={(title) => update.mutate({ title }, { onError: notify.fail })}
      pill={<StatePill state={state} label={t(`state.${state}`)} />}
      meta={
        <span className="wk-meta">
          <span>
            {pr.state === "merged"
              ? t("detail.merged", { user: pr.mergedBy, head: pr.head.ref, base: pr.base.ref })
              : t("detail.wants", { user: i.author, head: pr.head.ref, base: pr.base.ref })}
          </span>
          <When iso={pr.state === "merged" && pr.mergedAt ? pr.mergedAt : i.createdAt} />
        </span>
      }
      actions={
        canEdit &&
        pr.state === "open" && (
          <Button size="sm" disabled={update.isPending} onClick={() => update.mutate({ draft: !pr.draft }, { onError: notify.fail })}>
            {pr.draft ? t("detail.ready") : t("detail.toDraft")}
          </Button>
        )
      }
    />
  );

  const tabs = (
    <nav className="ui-tablist wk-tabs" aria-label={t("detail.tabs")}>
      <Link className="ui-tab" to="/$owner/$name/pulls/$number" params={params} search={{}} data-state={tab === undefined ? "active" : "inactive"}>
        <Icon name="book" />
        {t("tab.conversation")}
        <span className="wk-count">{i.commentCount}</span>
      </Link>
      <Link className="ui-tab" to="/$owner/$name/pulls/$number" params={params} search={{ tab: "commits" }} data-state={tab === "commits" ? "active" : "inactive"}>
        <Icon name="commit" />
        {t("tab.commits")}
      </Link>
      <Link className="ui-tab" to="/$owner/$name/pulls/$number" params={params} search={{ tab: "files" }} data-state={tab === "files" ? "active" : "inactive"}>
        <Icon name="file" />
        {t("tab.files")}
      </Link>
    </nav>
  );

  if (tab !== undefined) {
    return (
      <div className="wk-detail">
        {layout === "c" && <StepNav kind="pulls" repo={repo} number={i.number} />}
        {header}
        {tabs}
        {tab === "commits" ? <PullCommits repo={repo} pr={pr} page={page} /> : <PullFiles repo={repo} pr={pr} />}
      </div>
    );
  }

  const blocks = [
    <ReviewersBlock key="r" repo={repo} pr={pr} />,
    <AssigneesBlock key="a" repo={repo} issue={i} />,
    <LabelsBlock key="l" repo={repo} issue={i} />,
    <MilestoneBlock key="m" repo={repo} issue={i} />,
    <NotifyBlock key="n" repo={repo} />,
  ];
  return (
    <DetailLayout
      kind="pulls"
      repo={repo}
      number={i.number}
      header={
        <>
          {header}
          {tabs}
        </>
      }
      strip={[<StripState key="s" label={t("strip.state")} value={<StatePill state={state} label={t(`state.${state}`)} />} />, ...blocks]}
      sidebar={
        <>
          {blocks}
          <ParticipantsBlock repo={repo} issue={i} />
        </>
      }
    >
      <PullConversation repo={repo} pr={pr} canEdit={canEdit} />
    </DetailLayout>
  );
}

function PullConversation({ repo, pr, canEdit }: { repo: Repo; pr: PullRequest; canEdit: boolean }) {
  const t = useT("pulls");
  const notify = useNotify();
  const update = useUpdatePull(repo.owner, repo.name, pr.issue.number);
  const reviews = useReviews(repo.owner, repo.name, pr.issue.number);
  const extra: TimelineExtra[] = (reviews.data ?? [])
    .filter((r) => r.state !== "pending")
    .map((r) => ({ key: `review-${r.id}`, at: r.submittedAt ?? r.createdAt, node: <ReviewItem repo={repo} pr={pr} r={r} /> }));
  if (pr.state === "merged" && pr.mergedAt) {
    extra.push({
      key: "merged",
      at: pr.mergedAt,
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
      beforeComposer={<MergeBox repo={repo} pr={pr} />}
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

function ReviewItem({ repo, pr, r }: { repo: Repo; pr: PullRequest; r: PullReview }) {
  const t = useT("pulls");
  const notify = useNotify();
  const dismiss = useDismissReview(repo.owner, repo.name, pr.issue.number);
  const [message, setMessage] = useState("");
  const [open, setOpen] = useState(false);
  const canDismiss = canWrite(repo.permission) && (r.state === "approved" || r.state === "changes_requested");
  const ic = reviewIcon[r.state];
  return (
    <div className="wk-review">
      <EventRow icon={ic.icon} tone={ic.tone}>
        <b>{r.reviewer}</b> {t(`review.${r.state}`)} <When iso={r.submittedAt ?? r.createdAt} />
        {r.state === "dismissed" && r.dismissedBy && <span className="ui-note"> {t("review.dismissedBy", { user: r.dismissedBy })}</span>}
        {canDismiss && (
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
        )}
      </EventRow>
      {r.body && (
        <CommentBox author={r.reviewer} head={<b>{r.reviewer}</b>}>
          <Body owner={repo.owner} name={repo.name} refName={pr.head.ref} source={r.body} />
        </CommentBox>
      )}
      {r.dismissMessage && <p className="ui-note wk-review-note">{t("review.dismissReason", { message: r.dismissMessage })}</p>}
    </div>
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
          <Menu align="end" trigger={<Button size="sm" variant="ghost" icon="gear" aria-label={t("side.editReviewers")} />}>
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
        <p className="ui-note">{t("side.noReviewers")}</p>
      ) : (
        <ul className="wk-people">
          {names.map((u) => {
            const r = latest.get(u);
            return (
              <li key={u}>
                <Avatar user={u} size={20} />
                {u}
                <span className="acc-grow" />
                {r ? (
                  <span className={`wk-verdict wk-verdict-${r.state}`} title={t(`review.${r.state}`)}>
                    <Icon name={reviewIcon[r.state].icon} />
                    <span className="wk-sr">{t(`review.${r.state}`)}</span>
                  </span>
                ) : (
                  <span className="wk-verdict" title={t("side.awaiting")}>
                    <Icon name="clock" />
                    <span className="wk-sr">{t("side.awaiting")}</span>
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </SideBlock>
  );
}

/* Merge box. */

const methods: MergeMethod[] = ["merge", "squash", "rebase"];

function Check({ ok, children, tone }: { ok: boolean; children: ReactNode; tone?: "warn" }) {
  return (
    <li className={`wk-check ${ok ? "wk-check-ok" : tone === "warn" ? "wk-check-warn" : "wk-check-err"}`}>
      <Icon name={ok ? "check" : tone === "warn" ? "alert" : "x"} />
      <span>{children}</span>
    </li>
  );
}

function MergeBox({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const notify = useNotify();
  const writer = canWrite(repo.permission);
  const open = pr.state === "open";
  const m = useMergeability(repo.owner, repo.name, pr.issue.number, open);
  const merge = useMerge(repo.owner, repo.name, pr.issue.number);
  const auto = useAutoMerge(repo.owner, repo.name, pr.issue.number);
  const [method, setMethod] = useState<MergeMethod>(pr.autoMerge?.method ?? "merge");
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [title, setTitle] = useState("");

  if (pr.state === "merged") {
    return (
      <section className="wk-merge wk-merge-merged" aria-label={t("merge.label")}>
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
      </section>
    );
  }
  if (!open) {
    return (
      <section className="wk-merge" aria-label={t("merge.label")}>
        <div className="wk-merge-head">
          <StateIcon kind="pull" state="closed" label={t("state.closed")} />
          <div>
            <b>{t("merge.closed")}</b>
            <p className="ui-note">{t("merge.closedHint")}</p>
          </div>
        </div>
      </section>
    );
  }

  const data = m.data;
  const ready = data?.canMerge === true && !pr.draft;
  const headLine = pr.draft ? t("merge.draft") : !data ? t("merge.checking") : data.canMerge ? t("merge.ready") : t("merge.blocked");
  return (
    <section className={`wk-merge ${ready ? "wk-merge-ok" : ""}`} aria-label={t("merge.label")}>
      <div className="wk-merge-head">
        <StateIcon kind="pull" state={ready ? "open" : "draft"} label={headLine} />
        <div className="acc-grow">
          <b>{headLine}</b>
          {data?.rule && <p className="ui-note">{t("merge.rule", { rule: data.rule })}</p>}
        </div>
      </div>
      {data && (
        <ul className="wk-checks">
          {data.mergeable === "conflict" ? (
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
          )}
          {data.requiredApprovals > 0 && (
            <Check ok={data.approvals >= data.requiredApprovals}>{t("merge.approvals", { approvals: data.approvals, required: data.requiredApprovals })}</Check>
          )}
          {data.missingChecks.length > 0 && <Check ok={false}>{t("merge.missingChecks", { checks: data.missingChecks.join(", ") })}</Check>}
          {data.behind && (
            <Check ok={false} tone="warn">
              {t("merge.behind", { base: pr.base.ref })}
            </Check>
          )}
          {data.codeOwners.map((c) => (
            <Check key={c.pattern} ok={c.approved}>
              {t(c.approved ? "merge.ownersOk" : "merge.ownersWait", { owners: c.owners.join(", "), pattern: c.pattern })}
            </Check>
          ))}
          {data.codeOwnersErrors.map((e) => (
            <Check key={e} ok={false} tone="warn">
              {t("merge.ownersError", { error: e })}
            </Check>
          ))}
          {data.violations.map((v) => (
            <Check key={v.code + (v.commit ?? "")} ok={false}>
              {v.message}
              {v.commit && <code className="ui-mono"> {shortSha(v.commit)}</code>}
            </Check>
          ))}
        </ul>
      )}
      {pr.autoMerge && (
        <div className="ui-note wk-merge-auto">
          <Icon name="clock" />
          <span className="acc-grow">{t("merge.autoOn", { user: pr.autoMerge.enabledBy, method: t(`method.${pr.autoMerge.method}`) })}</span>
          {writer && (
            <Button size="sm" disabled={auto.isPending} onClick={() => auto.mutate(null, { onError: notify.fail })}>
              {t("merge.autoOff")}
            </Button>
          )}
        </div>
      )}
      {writer && (
        <div className="wk-merge-form">
          <Select label={t("merge.method")} value={method} onValueChange={(v) => setMethod(v as MergeMethod)} options={methods.map((x) => ({ value: x, label: t(`method.${x}`) }))} />
          {method !== "rebase" && (
            <Input label={t("merge.commitTitle")} placeholder={method === "squash" ? `${pr.issue.title} (#${pr.issue.number})` : t("merge.commitTitleHint")} value={title} onChange={(e) => setTitle(e.target.value)} />
          )}
          <label className="acc-check">
            <input type="checkbox" checked={deleteBranch} onChange={(e) => setDeleteBranch(e.target.checked)} />
            {t("merge.deleteBranch", { head: pr.head.ref })}
          </label>
          <div className="acc-actions">
            {!pr.autoMerge && !ready && (
              <Button
                disabled={auto.isPending || pr.draft}
                onClick={() => auto.mutate({ method, deleteBranch, title: title.trim() || undefined }, { onSuccess: () => notify.ok(t("merge.autoEnabled")), onError: notify.fail })}
              >
                {t("merge.autoEnable")}
              </Button>
            )}
            <Button
              variant="primary"
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
          </div>
        </div>
      )}
      {!writer && <p className="ui-note">{t("merge.noPermission")}</p>}
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
            <ul className="rp-list">
              {res.items.map((c) => (
                <CommitRow key={c.sha} repo={repo} c={{ sha: c.sha, summary: c.summary, author: c.author.name, date: c.author.date }} />
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
