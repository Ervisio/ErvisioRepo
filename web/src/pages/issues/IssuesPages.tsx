import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useState, type CSSProperties, type FormEvent, type ReactNode } from "react";
import {
  useCollaborators,
  useCreateIssue,
  useDeleteIssue,
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
  type Milestone,
} from "../../api/issues.ts";
import { useSession } from "../../api/identity.ts";
import { useLinkedPulls } from "../../api/pulls.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, ConfirmDialog, EmptyState, Icon, Input, MenuItem, MenuLabel, MenuSeparator, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, Pager, RepoFrame } from "../code/common.tsx";
import { Avatar, Composer, FilterChip, IconTrigger, LabelChip, ListFrame, ListSearch, RowGroups, SideBlock, StateIcon, WorkRow, type WorkState } from "../work/common.tsx";
import { AssigneesMenu, canTriage, Conversation, IssueMeta, LabelsMenu, LockMenuItems, MilestoneMenu } from "../work/Conversation.tsx";

export type IssueSearch = {
  state?: "closed" | "all";
  q?: string;
  labels?: string;
  milestone?: string;
  assignee?: string;
  author?: string;
  mentioned?: string;
  sort?: "updated" | "comments";
  group?: "none";
  page?: number;
};

const issueState = (i: Issue): WorkState => (i.state === "closed" ? "closed" : "open");

/** Open, closed or all issues of a repository: saved views, filter chips, rows grouped by milestone. */
export function IssuesPage({ owner, name, search }: { owner: string; name: string; search: IssueSearch }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue" head={false}>
      {(repo) => <IssueList repo={repo} search={search} />}
    </RepoFrame>
  );
}

type IssueView = { id: "open" | "assigned" | "created" | "mentioned" | "closed"; icon: IconName; search: IssueSearch };

/** Groups list items by milestone, items without one last; `flat` keeps a single untitled group. */
export function byMilestone<T>(items: T[], ms: (x: T) => Milestone | null | undefined, none: string, flat: boolean): { key: string; title?: ReactNode; items: T[] }[] {
  if (flat) return [{ key: "all", items }];
  const groups = new Map<string, { key: string; title: ReactNode; items: T[] }>();
  for (const x of items) {
    const m = ms(x);
    const key = m?.id ?? "";
    const g = groups.get(key) ?? { key: key || "none", title: <><Icon name="clock" />{m?.title ?? none}</>, items: [] as T[] };
    g.items.push(x);
    groups.set(key, g);
  }
  const noMs = groups.get("");
  groups.delete("");
  return [...groups.values(), ...(noMs ? [noMs] : [])];
}

function IssueList({ repo, search }: { repo: Repo; search: IssueSearch }) {
  const t = useT("issues");
  const { owner, name } = repo;
  const navigate = useNavigate();
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
  const people = useCollaborators(owner, name, canTriage(repo.permission));

  const go = (patch: Partial<IssueSearch>) =>
    void navigate({ to: "/$owner/$name/issues", params: { owner, name }, search: { ...search, page: undefined, ...patch } });

  const persons = { assignee: undefined, author: undefined, mentioned: undefined };
  const views: IssueView[] = [
    { id: "open", icon: "issue", search: {} },
    ...(me
      ? ([
          { id: "assigned", icon: "user", search: { assignee: me } },
          { id: "created", icon: "plus", search: { author: me } },
          { id: "mentioned", icon: "eye", search: { mentioned: me } },
        ] as IssueView[])
      : []),
    { id: "closed", icon: "check", search: { state: "closed" } },
  ];
  const isView = (v: IssueView) =>
    search.state === v.search.state && search.assignee === v.search.assignee && search.author === v.search.author && search.mentioned === v.search.mentioned;
  const current = views.find(isView);
  const plain = current?.id === "open" && !search.q && !search.labels && !search.milestone;
  const picked = filter.labels ?? [];
  const toggleLabel = (l: string) => {
    const next = picked.includes(l) ? picked.filter((x) => x !== l) : [...picked, l];
    return next.length ? next.join(",") : undefined;
  };
  const assignees = Array.from(new Set([...(me ? [me] : []), ...(people.data ?? []).map((p) => p.username)]));

  return (
    <ListFrame
      viewsLabel={t("filters.views")}
      views={
        <>
          {views.map((v) => (
            <Link
              key={v.id}
              className="wk-vw-a"
              to="/$owner/$name/issues"
              params={{ owner, name }}
              search={{ ...v.search, labels: search.labels, milestone: search.milestone, group: search.group }}
              aria-current={current === v ? "page" : undefined}
            >
              <Icon name={v.icon} />
              {t(`view.${v.id}`)}
              {v.id === "open" && !!repo.openIssuesCount && <span className="wk-vw-c">{repo.openIssuesCount}</span>}
            </Link>
          ))}
          {(labels.data ?? []).length > 0 && <h3 className="wk-vw-h">{t("filters.labels")}</h3>}
          {(labels.data ?? []).map((l) => (
            <Link
              key={l.id}
              className="wk-vw-a wk-vw-lab"
              to="/$owner/$name/issues"
              params={{ owner, name }}
              search={{ ...search, page: undefined, labels: toggleLabel(l.name) }}
              aria-current={picked.includes(l.name) ? "true" : undefined}
            >
              <i className="wk-vw-dot" style={{ "--c": `#${l.color.replace(/^#/, "")}` } as CSSProperties} aria-hidden="true" />
              {l.name}
            </Link>
          ))}
        </>
      }
      title={current ? t(`view.${current.id}`) : t("list.aria")}
      count={plain && repo.openIssuesCount !== undefined ? t("list.count", { count: repo.openIssuesCount }) : undefined}
      actions={
        <>
          <FilterChip label={t("list.milestone")} value={search.milestone}>
            <MenuItem icon={!search.milestone ? "check" : undefined} onSelect={() => go({ milestone: undefined })}>
              {t("list.anyMilestone")}
            </MenuItem>
            {(milestones.data ?? []).map((m) => (
              <MenuItem key={m.id} icon={search.milestone === m.title ? "check" : undefined} onSelect={() => go({ milestone: m.title })}>
                {m.title}
              </MenuItem>
            ))}
          </FilterChip>
          {me && (
            <Link to="/$owner/$name/issues/new" params={{ owner, name }}>
              <Button variant="primary" icon="plus">
                {t("list.new")}
              </Button>
            </Link>
          )}
        </>
      }
      toolbar={
        <>
          <ListSearch key={search.q ?? ""} label={t("list.search")} placeholder={t("list.searchPlaceholder")} initial={search.q ?? ""} onSearch={(q) => go({ q: q || undefined })} />
          <FilterChip label={t("list.label")} value={picked.length ? picked.join(", ") : undefined}>
            {(labels.data ?? []).length === 0 && <MenuLabel>{t("filters.noLabels")}</MenuLabel>}
            {(labels.data ?? []).map((l) => (
              <MenuItem key={l.id} icon={picked.includes(l.name) ? "check" : undefined} onSelect={() => go({ labels: toggleLabel(l.name) })}>
                <LabelChip label={l} />
              </MenuItem>
            ))}
          </FilterChip>
          <FilterChip label={t("list.assignee")} value={search.assignee}>
            <MenuItem icon={!search.assignee ? "check" : undefined} onSelect={() => go({ assignee: undefined })}>
              {t("list.anyAssignee")}
            </MenuItem>
            {assignees.map((u) => (
              <MenuItem key={u} icon={search.assignee === u ? "check" : undefined} onSelect={() => go({ ...persons, assignee: u })}>
                {u}
              </MenuItem>
            ))}
          </FilterChip>
          <FilterChip label={t("list.sort")} value={t(`sort.${search.sort ?? "created"}`)}>
            {(["created", "updated", "comments"] as const).map((x) => (
              <MenuItem key={x} icon={(search.sort ?? "created") === x ? "check" : undefined} onSelect={() => go({ sort: x === "created" ? undefined : x })}>
                {t(`sort.${x}`)}
              </MenuItem>
            ))}
          </FilterChip>
          <FilterChip label={t("list.group")} value={t(search.group === "none" ? "group.none" : "group.milestone")}>
            {(["milestone", "none"] as const).map((x) => (
              <MenuItem key={x} icon={(search.group ?? "milestone") === x ? "check" : undefined} onSelect={() => go({ group: x === "none" ? "none" : undefined })}>
                {t(`group.${x}`)}
              </MenuItem>
            ))}
          </FilterChip>
        </>
      }
    >
      <Gate query={list}>
        {(res) =>
          res.items.length === 0 ? (
            <EmptyState icon="issue" title={t("list.empty")} description={t("list.emptyHint")} />
          ) : (
            <>
              <RowGroups
                label={t("list.aria")}
                groups={byMilestone(res.items, (i) => i.milestone, t("group.noMilestone"), search.group === "none")}
                getKey={(i) => i.id}
                row={(i) => <IssueRow repo={repo} issue={i} />}
              />
              <Pager page={page} hasMore={res.hasMore} onPage={(n) => go({ page: n > 1 ? n : undefined })} />
            </>
          )
        }
      </Gate>
    </ListFrame>
  );
}

function IssueRow({ repo, issue: i }: { repo: Repo; issue: Issue }) {
  const t = useT("issues");
  const state = issueState(i);
  return (
    <WorkRow
      icon={<StateIcon kind="issue" state={state} label={t(`state.${state}`)} />}
      number={i.number}
      link={
        <Link className="wk-ir-t" to="/$owner/$name/issues/$number" params={{ owner: repo.owner, name: repo.name, number: String(i.number) }}>
          {i.title}
        </Link>
      }
      tags={i.labels.map((l) => (
        <LabelChip key={l.id} label={l} />
      ))}
      side={
        i.commentCount > 0 && (
          <span className="wk-cnt" aria-label={t("row.comments", { count: i.commentCount })}>
            <Icon name="comment" />
            {i.commentCount}
          </span>
        )
      }
      people={<People users={i.assignees} />}
    />
  );
}

/** Overlapping avatars, at most three. */
export function People({ users }: { users: string[] }) {
  if (users.length === 0) return null;
  return (
    <span className="wk-avatars" aria-label={users.join(", ")}>
      {users.slice(0, 3).map((u) => (
        <span key={u} title={u}>
          <Avatar user={u} size={20} />
        </span>
      ))}
    </span>
  );
}

/* Detail. */

export function IssuePage({ owner, name, number }: { owner: string; name: string; number: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue" head={false}>
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

/** Title block shared by issue and pull request pages: breadcrumb, 30 px title with rename, optional meta row and tabs. */
export function WorkHeader({ kind, repo, title, number, canEdit, onRename, saving, meta, tabs }: {
  kind: "issues" | "pulls";
  repo: Repo;
  title: string;
  number: number;
  canEdit: boolean;
  onRename: (title: string) => void;
  saving: boolean;
  meta?: ReactNode;
  tabs?: ReactNode;
}) {
  const t = useT("issues");
  const [draft, setDraft] = useState<string | undefined>(undefined);
  return (
    <header className="wk-dh">
      <nav className="wk-bc" aria-label={t("detail.crumb")}>
        <Link to={kind === "issues" ? "/$owner/$name/issues" : "/$owner/$name/pulls"} params={{ owner: repo.owner, name: repo.name }}>
          {t(`crumb.${kind}`)}
        </Link>
        <span aria-hidden="true">/</span>
        <span className="ui-mono" aria-current="page">
          #{number}
        </span>
      </nav>
      {draft === undefined ? (
        <div className="wk-dh-title">
          <h2>{title}</h2>
          {canEdit && <IconTrigger icon="edit" label={t("detail.editTitle")} onClick={() => setDraft(title)} />}
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
      {meta && <div className="wk-dh-meta">{meta}</div>}
      {tabs}
    </header>
  );
}

/** Detail frame: header over the conversation (left) and the sticky Properties panel (right). */
export function DetailLayout({ header, sidebar, children }: { header: ReactNode; sidebar: ReactNode; children: ReactNode }) {
  const t = useT("issues");
  return (
    <div className="wk-detail">
      {header}
      <div className="wk-dgrid">
        <div className="wk-dmain">{children}</div>
        <aside className="wk-props" aria-label={t("detail.side")}>
          {sidebar}
        </aside>
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
  const del = useDeleteIssue(repo.owner, repo.name, issue.number);
  const navigate = useNavigate();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const isAdmin = repo.permission === "admin";
  const state = issueState(issue);
  const canEdit = (me !== undefined && me === issue.author) || canTriage(repo.permission);
  const setState = (s: "open" | "closed", reason?: string) => update.mutate({ state: s, stateReason: reason }, { onError: notify.fail });
  return (
    <DetailLayout
      header={
        <WorkHeader
          kind="issues"
          repo={repo}
          title={issue.title}
          number={issue.number}
          canEdit={canEdit}
          saving={update.isPending}
          onRename={(title) => update.mutate({ title }, { onError: notify.fail })}
        />
      }
      sidebar={
        <IssueMeta
          repo={repo}
          issue={issue}
          before={
            <SideBlock title={t("side.state")}>
              <span className="wk-prow-state">
                <StateIcon kind="issue" state={state} label={t(`state.${state}`)} />
                {t(`state.${state}`)}
              </span>
            </SideBlock>
          }
          linked={<LinkedPullsBlock repo={repo} issue={issue} />}
          openedLabel={t("side.openedBy")}
          menu={
            canTriage(repo.permission) && (
              <>
                <LockMenuItems repo={repo} issue={issue} onLock={(l, reason) => lock.mutate({ lock: l, reason }, { onError: notify.fail })} />
                {isAdmin && (
                  <>
                    <MenuSeparator />
                    <MenuItem icon="trash" danger onSelect={() => setConfirmDelete(true)}>
                      {t("detail.delete")}
                    </MenuItem>
                  </>
                )}
              </>
            )
          }
        />
      }
    >
      <Conversation
        repo={repo}
        issue={issue}
        canClose={canEdit}
        stateBusy={update.isPending}
        onState={setState}
        closeLabels={{ close: t("detail.close"), reopen: t("detail.reopen"), withComment: t("detail.closeWithComment") }}
      />
      {isAdmin && (
        <ConfirmDialog
          open={confirmDelete}
          onOpenChange={setConfirmDelete}
          title={t("detail.deleteTitle")}
          description={t("detail.deleteBody", { number: issue.number })}
          confirmLabel={t("detail.deleteConfirm")}
          pending={del.isPending}
          onConfirm={() =>
            del.mutate(undefined, {
              onSuccess: () => void navigate({ to: "/$owner/$name/issues", params: { owner: repo.owner, name: repo.name } }),
              onError: notify.fail,
            })
          }
        />
      )}
    </DetailLayout>
  );
}

/** "Development": pull requests whose body or comments reference the issue; left out when there are none. */
function LinkedPullsBlock({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("issues");
  const linked = useLinkedPulls(repo.owner, repo.name, issue.number);
  const items = linked.data ?? [];
  if (items.length === 0) return null;
  return (
    <SideBlock title={t("side.linked")}>
      <ul className="wk-linked">
        {items.map((p) => {
          const [owner = repo.owner, name = repo.name] = p.repo.split("/");
          const state: WorkState = p.state === "open" && p.draft ? "draft" : p.state;
          const local = owner === repo.owner && name === repo.name;
          return (
            <li key={`${p.repo}#${p.number}`}>
              <StateIcon kind="pull" state={state} label={t(`side.linkedState.${state}`)} />
              <Link to="/$owner/$name/pulls/$number" params={{ owner, name, number: String(p.number) }} title={p.title}>
                {local ? `#${p.number}` : `${p.repo}#${p.number}`} {p.title}
              </Link>
            </li>
          );
        })}
      </ul>
    </SideBlock>
  );
}

/* New issue. */

export function NewIssuePage({ owner, name }: { owner: string; name: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="issues" section="issue" head={false}>
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
      <section className="ui-box wk-tpls">
        <div className="ui-box-h">
          <b>{t("new.chooseTemplate")}</b>
        </div>
        <ul className="ui-in">
          {list.map((x) => (
            <li key={x.fileName} className="ui-li">
              <div className="acc-grow">
                <b>{x.name}</b>
                {x.about && <p className="ui-note">{x.about}</p>}
              </div>
              <Button onClick={() => setPicked(x)}>{t("new.useTemplate")}</Button>
            </li>
          ))}
          {templates.data?.config.contactLinks.map((c) => (
            <li key={c.url} className="ui-li">
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
            <li className="ui-li">
              <div className="acc-grow">
                <b>{t("new.blank")}</b>
                <p className="ui-note">{t("new.blankHint")}</p>
              </div>
              <Button onClick={() => setPicked(null)}>{t("new.useBlank")}</Button>
            </li>
          )}
        </ul>
      </section>
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

/** Centred composer (780 px): borderless title, description, chips for assignees, labels and milestone. */
function IssueForm({ repo, template }: { repo: Repo; template?: IssueTemplate }) {
  const t = useT("issues");
  const tw = useT("work");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateIssue(repo.owner, repo.name);
  const triage = canTriage(repo.permission);
  const labels = useLabels(repo.owner, repo.name);
  const milestones = useMilestones(repo.owner, repo.name);
  const initialBody = template ? (template.kind === "form" ? formBody(template) : template.body) : "";
  const [title, setTitle] = useState(template?.title ?? "");
  const [body, setBody] = useState(initialBody);
  const [picked, setPicked] = useState<string[]>(template?.labels ?? []);
  const [assignees, setAssignees] = useState<string[]>(template?.assignees ?? []);
  const [milestone, setMilestone] = useState("");
  const [another, setAnother] = useState(false);
  const titleId = useId();

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      {
        title: title.trim(),
        body,
        labels: triage ? picked : undefined,
        assignees: triage ? assignees : undefined,
        milestoneId: triage && milestone ? milestone : undefined,
      },
      {
        onSuccess: (i) => {
          if (another) {
            notify.ok(t("new.created", { number: i.number }));
            setTitle(template?.title ?? "");
            setBody(initialBody);
            return;
          }
          void navigate({ to: "/$owner/$name/issues/$number", params: { owner: repo.owner, name: repo.name, number: String(i.number) } });
        },
        onError: notify.fail,
      },
    );
  }

  const chosen = (labels.data ?? []).filter((l) => picked.includes(l.name));
  const chosenMilestone = (milestones.data ?? []).find((m) => m.id === milestone);
  const chip = (icon: IconName, text: ReactNode, label: string) => (
    <button type="button" className="wk-nc-chip" aria-label={label}>
      <Icon name={icon} />
      {text}
    </button>
  );

  return (
    <form className="wk-nc" onSubmit={submit}>
      <Composer
        owner={repo.owner}
        name={repo.name}
        refName={repo.defaultBranch}
        value={body}
        onChange={setBody}
        label={t("new.body")}
        placeholder={t("new.bodyHint")}
        rows={8}
        className="wk-cps-new"
        before={
          <>
            <nav className="wk-bc" aria-label={t("detail.crumb")}>
              <Link to="/$owner/$name/issues" params={{ owner: repo.owner, name: repo.name }}>
                {repo.name}
              </Link>
              <span aria-hidden="true">/</span>
              <span aria-current="page">{t("list.new")}</span>
            </nav>
            <label htmlFor={titleId} className="wk-sr">
              {t("new.title")}
            </label>
            <input
              id={titleId}
              className="wk-nc-title"
              required
              autoFocus
              maxLength={256}
              placeholder={t("new.titleHint")}
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </>
        }
        extra={
          triage && (
            <div className="wk-nc-chips">
              <AssigneesMenu repo={repo} assignees={assignees} onChange={setAssignees} trigger={chip("user", assignees.length ? assignees.join(", ") : tw("side.assignees"), tw("side.editAssignees"))} />
              <LabelsMenu
                repo={repo}
                selected={picked}
                onChange={setPicked}
                trigger={chip("tag", chosen.length ? chosen.map((l) => <LabelChip key={l.id} label={l} />) : tw("side.labels"), tw("side.editLabels"))}
              />
              <MilestoneMenu repo={repo} milestone={chosenMilestone} onChange={setMilestone} trigger={chip("clock", chosenMilestone?.title ?? tw("side.milestone"), tw("side.editMilestone"))} />
            </div>
          )
        }
      >
        <label className="wk-nc-more">
          <input type="checkbox" role="switch" checked={another} onChange={(e) => setAnother(e.target.checked)} />
          {t("new.another")}
        </label>
        <Link to="/$owner/$name/issues" params={{ owner: repo.owner, name: repo.name }}>
          <Button variant="ghost">{t("new.cancel")}</Button>
        </Link>
        <Button variant="primary" type="submit" disabled={create.isPending || !title.trim()}>
          {t("new.submit")}
        </Button>
      </Composer>
    </form>
  );
}
