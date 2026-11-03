import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import {
  useCollaborators,
  useCreateIssue,
  useIssue,
  useIssues,
  useIssueTemplates,
  useLabels,
  useLockIssue,
  useMilestones,
  useUpdateIssue,
  type Issue,
  type IssueFilter,
  type IssueTemplate,
} from "../../api/issues.ts";
import { useSession } from "../../api/identity.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, EmptyState, Icon, Input, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, Pager, RepoFrame, When } from "../code/common.tsx";
import { Avatar, Body, Composer, FilterGroup, FilterItem, LabelChip, ListLayoutSwitch, Meter, StateIcon, StatePill, WorkList, type WorkState } from "../work/common.tsx";
import { AssigneesBlock, canTriage, Conversation, LabelsBlock, LockButton, MilestoneBlock, NotifyBlock, ParticipantsBlock } from "../work/Conversation.tsx";
import { loadListOrder, saveListOrder, useListLayout, type ListLayout } from "../work/layout.ts";

export type IssueSearch = {
  state?: "closed" | "all";
  q?: string;
  labels?: string;
  milestone?: string;
  assignee?: string;
  author?: string;
  mentioned?: string;
  sort?: "updated" | "comments";
  page?: number;
};

const issueState = (i: Issue): WorkState => (i.state === "closed" ? "closed" : "open");

/** Open, closed or all issues of a repository with filters, in the user's list layout. */
export function IssuesPage({ owner, name, search }: { owner: string; name: string; search: IssueSearch }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue">
      {(repo) => <IssueList repo={repo} search={search} />}
    </RepoFrame>
  );
}

function IssueList({ repo, search }: { repo: Repo; search: IssueSearch }) {
  const t = useT("issues");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const layout = useListLayout();
  const me = useSession().data?.username;
  const page = search.page ?? 1;
  const filter: IssueFilter = {
    state: search.state ?? "open",
    q: search.q,
    labels: search.labels ? search.labels.split(",") : undefined,
    milestone: search.milestone,
    assignee: search.assignee,
    author: search.author,
    mentioned: search.mentioned,
    sort: search.sort,
    page,
  };
  const list = useIssues(owner, name, filter);
  const labels = useLabels(owner, name);
  const milestones = useMilestones(owner, name);
  const [q, setQ] = useState(search.q ?? "");

  const go = (patch: Partial<IssueSearch>) =>
    void navigate({ to: "/$owner/$name/issues", params: { owner, name }, search: { ...search, page: undefined, ...patch } });

  const numbers = list.data?.items.map((i) => i.number).join(",");
  useEffect(() => {
    if (numbers !== undefined) saveListOrder("issues", owner, name, numbers ? numbers.split(",").map(Number) : []);
  }, [numbers, owner, name]);

  const open = (i: Issue) => void navigate({ to: "/$owner/$name/issues/$number", params: { owner, name, number: String(i.number) } });
  const stateLabel = (i: Issue) => t(`state.${issueState(i)}`);
  const onLabel = (l: string) => {
    const cur = filter.labels ?? [];
    const next = cur.includes(l) ? cur.filter((x) => x !== l) : [...cur, l];
    go({ labels: next.length ? next.join(",") : undefined });
  };

  const views: { id: string; active: boolean; patch: Partial<IssueSearch> }[] = [
    { id: "open", active: !search.assignee && !search.mentioned && !search.author && !search.state, patch: { state: undefined, assignee: undefined, mentioned: undefined, author: undefined } },
    ...(me
      ? [
          { id: "assigned", active: search.assignee === me, patch: { assignee: me, mentioned: undefined, author: undefined } },
          { id: "mentioned", active: search.mentioned === me, patch: { mentioned: me, assignee: undefined, author: undefined } },
          { id: "created", active: search.author === me, patch: { author: me, assignee: undefined, mentioned: undefined } },
        ]
      : []),
  ];

  const filters = (
    <>
      <FilterGroup title={t("filters.views")}>
        {views.map((v) => (
          <FilterItem key={v.id} active={v.active} onClick={() => go(v.patch)}>
            {t(`view.${v.id}`)}
          </FilterItem>
        ))}
      </FilterGroup>
      <FilterGroup title={t("filters.labels")}>
        {(labels.data ?? []).length === 0 && <p className="ui-note">{t("filters.noLabels")}</p>}
        {(labels.data ?? []).map((l) => (
          <label key={l.id} className="acc-check wk-fcheck">
            <input type="checkbox" checked={(filter.labels ?? []).includes(l.name)} onChange={() => onLabel(l.name)} />
            <LabelChip label={l} />
          </label>
        ))}
      </FilterGroup>
      <FilterGroup title={t("filters.milestones")}>
        {(milestones.data ?? []).length === 0 && <p className="ui-note">{t("filters.noMilestones")}</p>}
        {(milestones.data ?? []).map((m) => (
          <FilterItem key={m.id} active={search.milestone === m.title} onClick={() => go({ milestone: search.milestone === m.title ? undefined : m.title })}>
            <span className="wk-fms">
              {m.title}
              <Meter open={m.openIssues} closed={m.closedIssues} label={t("filters.progress", { title: m.title })} />
            </span>
          </FilterItem>
        ))}
      </FilterGroup>
    </>
  );

  function submit(e: FormEvent) {
    e.preventDefault();
    go({ q: q.trim() || undefined });
  }

  return (
    <>
      <div className="rp-bar wk-bar">
        <div className="ui-tablist" role="group" aria-label={t("list.stateFilter")}>
          {(["open", "closed", "all"] as const).map((s) => {
            const active = (search.state ?? "open") === s;
            return (
              <button key={s} type="button" className="ui-tab" data-state={active ? "active" : "inactive"} aria-pressed={active} onClick={() => go({ state: s === "open" ? undefined : s })}>
                {t(`list.${s}`)}
              </button>
            );
          })}
        </div>
        <form className="wk-search" role="search" onSubmit={submit}>
          <Icon name="search" />
          <input id="wk-search" className="ui-input" type="search" aria-label={t("list.search")} placeholder={t("list.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} />
        </form>
        {layout !== "b" && (
          <>
            <Select
              label={t("list.label")}
              className="wk-fsel"
              value={filter.labels?.[0] ?? "-"}
              onValueChange={(v) => go({ labels: v === "-" ? undefined : v })}
              options={[{ value: "-", label: t("list.anyLabel") }, ...(labels.data ?? []).map((l) => ({ value: l.name, label: l.name }))]}
            />
            <Select
              label={t("list.milestone")}
              className="wk-fsel"
              value={search.milestone ?? "-"}
              onValueChange={(v) => go({ milestone: v === "-" ? undefined : v })}
              options={[{ value: "-", label: t("list.anyMilestone") }, ...(milestones.data ?? []).map((m) => ({ value: m.title, label: m.title }))]}
            />
          </>
        )}
        <Select
          label={t("list.sort")}
          className="wk-fsel"
          value={search.sort ?? "created"}
          onValueChange={(v) => go({ sort: v === "created" ? undefined : (v as IssueSearch["sort"]) })}
          options={(["created", "updated", "comments"] as const).map((s) => ({ value: s, label: t(`sort.${s}`) }))}
        />
        <span className="acc-grow" />
        <ListLayoutSwitch />
        {me && (
          <Link to="/$owner/$name/issues/new" params={{ owner, name }}>
            <Button variant="primary" icon="plus">
              {t("list.new")}
            </Button>
          </Link>
        )}
      </div>
      <Gate query={list}>
        {(res) => (
          <>
            <WorkList
              layout={layout}
              items={res.items}
              getKey={(i) => i.id}
              label={t("list.aria")}
              searchId="wk-search"
              filters={filters}
              onOpen={open}
              empty={<EmptyState icon="issue" title={t("list.empty")} description={t("list.emptyHint")} />}
              row={(i, l) => <IssueRow repo={repo} issue={i} layout={l} stateLabel={stateLabel(i)} />}
              preview={(i) => <IssuePreview repo={repo} issue={i} />}
            />
            <Pager page={page} hasMore={res.hasMore} onPage={(n) => go({ ...search, page: n > 1 ? n : undefined })} />
          </>
        )}
      </Gate>
    </>
  );
}

function IssueRow({ repo, issue: i, layout, stateLabel }: { repo: Repo; issue: Issue; layout: ListLayout; stateLabel: string }) {
  const t = useT("issues");
  const state = issueState(i);
  const meta = (
    <span className="wk-meta">
      <span>#{i.number}</span>
      <span>{i.state === "open" ? t("row.opened", { user: i.author }) : t("row.closedBy", { user: i.closedBy || i.author })}</span>
      <When iso={i.state === "open" ? i.createdAt : (i.closedAt ?? i.updatedAt)} />
      {i.milestone && (
        <span className="wk-meta-ms">
          <Icon name="clock" />
          {i.milestone.title}
        </span>
      )}
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
  const assignees = (
    <span className="wk-avatars">
      {i.assignees.slice(0, 3).map((u) => (
        <span key={u} title={u}>
          <Avatar user={u} size={20} />
        </span>
      ))}
    </span>
  );
  if (layout === "c") {
    return (
      <span className="wk-ci-in">
        <StateIcon kind="issue" state={state} label={stateLabel} />
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
    <Link className="wk-title" to="/$owner/$name/issues/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
      {i.title}
    </Link>
  );
  if (layout === "b") {
    return (
      <div className="wk-rb">
        <StateIcon kind="issue" state={state} label={stateLabel} />
        <div className="wk-rb-main">
          <div className="wk-rb-l1">
            {link}
            {labels}
          </div>
          {meta}
        </div>
        <div className="wk-rb-side">
          {assignees}
          {comments}
        </div>
      </div>
    );
  }
  return (
    <div className="wk-ra">
      <StateIcon kind="issue" state={state} label={stateLabel} />
      <div className="wk-ra-main">
        {link}
        {labels}
        {meta}
      </div>
      {assignees}
      {comments}
      <When iso={i.updatedAt} />
    </div>
  );
}

function IssuePreview({ repo, issue: i }: { repo: Repo; issue: Issue }) {
  const t = useT("issues");
  return (
    <div className="wk-pv-in">
      <div className="wk-pv-head">
        <StatePill state={issueState(i)} label={t(`state.${issueState(i)}`)} />
        <span className="ui-note">#{i.number}</span>
        <span className="acc-grow" />
        <Link to="/$owner/$name/issues/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
          <Button size="sm">{t("preview.open")}</Button>
        </Link>
      </div>
      <h3 className="wk-pv-title">{i.title}</h3>
      <p className="wk-meta">
        <span>{t("row.opened", { user: i.author })}</span>
        <When iso={i.createdAt} />
      </p>
      {i.labels.length > 0 && (
        <div className="wk-labels">
          {i.labels.map((l) => (
            <LabelChip key={l.id} label={l} />
          ))}
        </div>
      )}
      <Body owner={repo.owner} name={repo.name} refName={repo.defaultBranch} source={i.body} />
    </div>
  );
}

/* Detail. */

export function IssuePage({ owner, name, number }: { owner: string; name: string; number: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue">
      {(repo) => <IssueLoader repo={repo} number={number} />}
    </RepoFrame>
  );
}

function IssueLoader({ repo, number }: { repo: Repo; number: number }) {
  const t = useT("issues");
  const issue = useIssue(repo.owner, repo.name, number);
  return (
    <Gate query={issue} notFound={<EmptyState icon="issue" title={t("detail.notFound")} />}>
      {(i) => <IssueDetail repo={repo} issue={i} />}
    </Gate>
  );
}

/** Title editing and state line shared by issue and pull request headers. */
export function WorkHeader({ title, number, canEdit, onRename, saving, pill, meta, actions }: {
  title: string;
  number: number;
  canEdit: boolean;
  onRename: (title: string) => void;
  saving: boolean;
  pill: ReactNode;
  meta: ReactNode;
  actions?: ReactNode;
}) {
  const t = useT("issues");
  const [draft, setDraft] = useState<string | undefined>(undefined);
  return (
    <header className="wk-dh">
      {draft === undefined ? (
        <div className="wk-dh-title">
          <h2>
            {title} <span className="wk-num">#{number}</span>
          </h2>
          <span className="acc-grow" />
          {actions}
          {canEdit && (
            <Button size="sm" icon="edit" onClick={() => setDraft(title)}>
              {t("detail.editTitle")}
            </Button>
          )}
        </div>
      ) : (
        <form
          className="acc-inline wk-dh-title"
          onSubmit={(e) => {
            e.preventDefault();
            if (draft.trim()) onRename(draft.trim());
            setDraft(undefined);
          }}
        >
          <Input label={t("detail.titleLabel")} className="acc-grow" value={draft} autoFocus maxLength={256} onChange={(e) => setDraft(e.target.value)} />
          <Button type="submit" disabled={saving || !draft.trim()}>
            {t("detail.saveTitle")}
          </Button>
          <Button variant="ghost" onClick={() => setDraft(undefined)}>
            {t("detail.cancel")}
          </Button>
        </form>
      )}
      <div className="wk-dh-meta">
        {pill}
        {meta}
      </div>
    </header>
  );
}

/** Previous / next navigation through the last list (split view layout). */
export function StepNav({ kind, repo, number }: { kind: "issues" | "pulls"; repo: Repo; number: number }) {
  const t = useT("issues");
  const navigate = useNavigate();
  const [order] = useState(() => loadListOrder(kind, repo.owner, repo.name));
  const pos = order.indexOf(number);
  const prev = pos > 0 ? order[pos - 1] : undefined;
  const next = pos >= 0 && pos < order.length - 1 ? order[pos + 1] : undefined;
  const to = kind === "issues" ? "/$owner/$name/issues/$number" : "/$owner/$name/pulls/$number";
  const list = kind === "issues" ? "/$owner/$name/issues" : "/$owner/$name/pulls";
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const el = e.target;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (el instanceof HTMLElement && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName))) return;
      const n = e.key === "k" ? prev : e.key === "j" ? next : undefined;
      if (n !== undefined) void navigate({ to, params: { owner: repo.owner, name: repo.name, number: String(n) } });
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [prev, next, navigate, to, repo.owner, repo.name]);
  if (pos < 0) return null;
  return (
    <nav className="wk-pnav" aria-label={t("step.label")}>
      <Link to={list} params={{ owner: repo.owner, name: repo.name }} className="wk-pnav-back">
        {t(`step.${kind}`)}
      </Link>
      <span className="ui-note">{t("step.position", { pos: pos + 1, total: order.length })}</span>
      <span className="acc-grow" />
      <Button size="sm" variant="ghost" disabled={prev === undefined} onClick={() => prev !== undefined && void navigate({ to, params: { owner: repo.owner, name: repo.name, number: String(prev) } })}>
        {t("step.prev")} <kbd>k</kbd>
      </Button>
      <Button size="sm" variant="ghost" disabled={next === undefined} onClick={() => next !== undefined && void navigate({ to, params: { owner: repo.owner, name: repo.name, number: String(next) } })}>
        {t("step.next")} <kbd>j</kbd>
      </Button>
    </nav>
  );
}

/** Detail frame: sidebar (A), symmetric strip (B), or step navigation plus sidebar (C). */
export function DetailLayout({ kind, repo, number, header, strip, sidebar, children }: {
  kind: "issues" | "pulls";
  repo: Repo;
  number: number;
  header: ReactNode;
  strip: ReactNode;
  sidebar: ReactNode;
  children: ReactNode;
}) {
  const layout = useListLayout();
  if (layout === "b") {
    return (
      <div className="wk-detail wk-detail-b">
        {header}
        <div className="wk-strip">{strip}</div>
        {children}
      </div>
    );
  }
  return (
    <div className="wk-detail">
      {layout === "c" && <StepNav kind={kind} repo={repo} number={number} />}
      {header}
      <div className="wk-dgrid">
        <div className="wk-dmain">{children}</div>
        <aside className="wk-side">{sidebar}</aside>
      </div>
    </div>
  );
}

function IssueDetail({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("issues");
  const notify = useNotify();
  const me = useSession().data?.username;
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const lock = useLockIssue(repo.owner, repo.name, issue.number);
  const state = issueState(issue);
  const canEdit = (me !== undefined && me === issue.author) || canTriage(repo.permission);
  const blocks = [
    <AssigneesBlock key="a" repo={repo} issue={issue} />,
    <LabelsBlock key="l" repo={repo} issue={issue} />,
    <MilestoneBlock key="m" repo={repo} issue={issue} />,
    <NotifyBlock key="n" repo={repo} />,
  ];
  return (
    <DetailLayout
      kind="issues"
      repo={repo}
      number={issue.number}
      header={
        <WorkHeader
          title={issue.title}
          number={issue.number}
          canEdit={canEdit}
          saving={update.isPending}
          onRename={(title) => update.mutate({ title }, { onError: notify.fail })}
          pill={<StatePill state={state} label={t(`state.${state}`)} />}
          meta={
            <span className="wk-meta">
              <span>{t("detail.opened", { user: issue.author })}</span>
              <When iso={issue.createdAt} />
              <span>{t("row.comments", { count: issue.commentCount })}</span>
            </span>
          }
          actions={<LockButton repo={repo} issue={issue} busy={lock.isPending} onLock={(l, reason) => lock.mutate({ lock: l, reason }, { onError: notify.fail })} />}
        />
      }
      strip={[
        <StripState key="s" label={t("strip.state")} value={<StatePill state={state} label={t(`state.${state}`)} />} />,
        ...blocks.slice(0, 3),
        <div key="p" className="wk-side-b">
          <div className="wk-side-h">
            <h4>{t("strip.linked")}</h4>
          </div>
          <p className="ui-note">{t("strip.linkedNone")}</p>
        </div>,
        blocks[3],
      ]}
      sidebar={
        <>
          {blocks}
          <ParticipantsBlock repo={repo} issue={issue} />
        </>
      }
    >
      <Conversation
        repo={repo}
        issue={issue}
        canClose={canEdit}
        stateBusy={update.isPending}
        onState={(s, reason) => update.mutate({ state: s, stateReason: reason }, { onError: notify.fail })}
        closeLabels={{ close: t("detail.close"), reopen: t("detail.reopen"), withComment: t("detail.closeWithComment") }}
      />
    </DetailLayout>
  );
}

export function StripState({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="wk-side-b">
      <div className="wk-side-h">
        <h4>{label}</h4>
      </div>
      <div className="wk-side-c">{value}</div>
    </div>
  );
}

/* New issue. */

export function NewIssuePage({ owner, name }: { owner: string; name: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue">
      {(repo) => <NewIssue repo={repo} />}
    </RepoFrame>
  );
}

function NewIssue({ repo }: { repo: Repo }) {
  const t = useT("issues");
  const templates = useIssueTemplates(repo.owner, repo.name);
  const [picked, setPicked] = useState<IssueTemplate | null | undefined>(undefined);
  const list = templates.data?.templates ?? [];
  const blank = templates.data?.config.blankIssuesEnabled ?? true;
  if (templates.isPending) return <p className="ui-note acc-pad">{t("new.loading")}</p>;
  if (picked === undefined && list.length > 0) {
    return (
      <Card title={t("new.chooseTemplate")}>
        <ul className="rp-list wk-tpls">
          {list.map((x) => (
            <li key={x.fileName} className="ui-row">
              <div className="acc-grow">
                <b>{x.name}</b>
                {x.about && <p className="ui-note">{x.about}</p>}
              </div>
              <Button onClick={() => setPicked(x)}>{t("new.useTemplate")}</Button>
            </li>
          ))}
          {templates.data?.config.contactLinks.map((c) => (
            <li key={c.url} className="ui-row">
              <div className="acc-grow">
                <b>{c.name}</b>
                {c.about && <p className="ui-note">{c.about}</p>}
              </div>
              <a href={c.url} target="_blank" rel="noreferrer noopener">
                <Button>{t("new.openLink")}</Button>
              </a>
            </li>
          ))}
          {blank && (
            <li className="ui-row">
              <div className="acc-grow">
                <b>{t("new.blank")}</b>
                <p className="ui-note">{t("new.blankHint")}</p>
              </div>
              <Button onClick={() => setPicked(null)}>{t("new.useBlank")}</Button>
            </li>
          )}
        </ul>
      </Card>
    );
  }
  return <IssueForm key={picked?.fileName ?? "blank"} repo={repo} template={picked ?? undefined} />;
}

function formBody(tpl: IssueTemplate): string {
  // Issue forms are rendered as a Markdown skeleton: one heading per input element.
  const parts: string[] = [];
  for (const el of tpl.form ?? []) {
    const attrs = (el.attributes ?? {}) as { label?: unknown; value?: unknown; description?: unknown };
    if (el.type === "markdown") continue;
    if (typeof attrs.label === "string") parts.push(`### ${attrs.label}\n\n${typeof attrs.value === "string" ? attrs.value : ""}`);
  }
  return parts.join("\n\n");
}

function IssueForm({ repo, template }: { repo: Repo; template?: IssueTemplate }) {
  const t = useT("issues");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateIssue(repo.owner, repo.name);
  const triage = canTriage(repo.permission);
  const labels = useLabels(repo.owner, repo.name);
  const milestones = useMilestones(repo.owner, repo.name);
  const people = useCollaborators(repo.owner, repo.name, triage);
  const [title, setTitle] = useState(template?.title ?? "");
  const [body, setBody] = useState(template ? (template.kind === "form" ? formBody(template) : template.body) : "");
  const [picked, setPicked] = useState<string[]>(template?.labels ?? []);
  const [assignees, setAssignees] = useState<string[]>(template?.assignees ?? []);
  const [milestone, setMilestone] = useState("-");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      {
        title: title.trim(),
        body,
        labels: triage ? picked : undefined,
        assignees: triage ? assignees : undefined,
        milestoneId: triage && milestone !== "-" ? milestone : undefined,
      },
      {
        onSuccess: (i) => void navigate({ to: "/$owner/$name/issues/$number", params: { owner: repo.owner, name: repo.name, number: String(i.number) } }),
        onError: notify.fail,
      },
    );
  }

  const toggle = (list: string[], v: string) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);

  return (
    <form className="wk-dgrid" onSubmit={submit}>
      <div className="wk-dmain wk-new">
        <Input label={t("new.title")} required autoFocus maxLength={256} value={title} onChange={(e) => setTitle(e.target.value)} />
        <Composer owner={repo.owner} name={repo.name} refName={repo.defaultBranch} value={body} onChange={setBody} label={t("new.body")} rows={12}>
          <Link to="/$owner/$name/issues" params={{ owner: repo.owner, name: repo.name }}>
            <Button variant="ghost">{t("new.cancel")}</Button>
          </Link>
          <Button variant="primary" type="submit" disabled={create.isPending || !title.trim()}>
            {t("new.submit")}
          </Button>
        </Composer>
      </div>
      {triage && (
        <aside className="wk-side">
          <div className="wk-side-b">
            <div className="wk-side-h">
              <h4>{t("new.labels")}</h4>
            </div>
            {(labels.data ?? []).length === 0 && <p className="ui-note">{t("new.noLabels")}</p>}
            {(labels.data ?? []).map((l) => (
              <label key={l.id} className="acc-check wk-fcheck">
                <input type="checkbox" checked={picked.includes(l.name)} onChange={() => setPicked(toggle(picked, l.name))} />
                <LabelChip label={l} />
              </label>
            ))}
          </div>
          <div className="wk-side-b">
            <div className="wk-side-h">
              <h4>{t("new.assignees")}</h4>
            </div>
            {(people.data ?? []).length === 0 && <p className="ui-note">{t("new.noPeople")}</p>}
            {(people.data ?? []).map((p) => (
              <label key={p.username} className="acc-check wk-fcheck">
                <input type="checkbox" checked={assignees.includes(p.username)} onChange={() => setAssignees(toggle(assignees, p.username))} />
                <Avatar user={p.username} size={20} />
                {p.username}
              </label>
            ))}
          </div>
          <div className="wk-side-b">
            <Select
              label={t("new.milestone")}
              value={milestone}
              onValueChange={setMilestone}
              options={[{ value: "-", label: t("new.noMilestone") }, ...(milestones.data ?? []).map((m) => ({ value: m.id, label: m.title }))]}
            />
          </div>
        </aside>
      )}
    </form>
  );
}
