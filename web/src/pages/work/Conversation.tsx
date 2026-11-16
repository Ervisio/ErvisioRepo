import { Link, useParams } from "@tanstack/react-router";
import { useDeferredValue, useState, type ReactNode } from "react";
import {
  useAddComment,
  useCollaborators,
  useDeleteComment,
  useEditComment,
  useLabels,
  useMilestones,
  useReact,
  useTimeline,
  useUpdateIssue,
  type Issue,
  type IssueComment,
  type Label,
  type Milestone,
  type TimelineEvent,
} from "../../api/issues.ts";
import { useRepoSubscription, useSetRepoSubscription, useSetSubjectSubscription, useSubjectSubscription, type RepoSubscription } from "../../api/notifications.ts";
import { useSession, useUserSearch } from "../../api/identity.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon, Input, Menu, MenuItem, MenuLabel, MenuSeparator } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, shortSha, When } from "../code/common.tsx";
import { Body, CommentBox, Composer, EventRow, IconTrigger, LabelChip, Meter, Reactions, SideBlock, SideDetails, Avatar, type WorkState } from "./common.tsx";

export const canTriage = (p: Repo["permission"] | undefined) => p === "triage" || p === "write" || p === "maintain" || p === "admin";

/** An extra entry merged into the timeline by time (pull request reviews). */
export type TimelineExtra = { key: string; at: string; node: ReactNode };

function EditableBody({ repo, author, body, canEdit, onSave, onDelete, saving, head, footer }: {
  repo: Repo;
  author: string;
  body: string;
  canEdit: boolean;
  onSave: (body: string, done: () => void) => void;
  onDelete?: () => void;
  saving: boolean;
  head: ReactNode;
  footer?: ReactNode;
}) {
  const t = useT("work");
  const [draft, setDraft] = useState<string | undefined>(undefined);
  return (
    <CommentBox
      author={author}
      head={
        <>
          {head}
          <span className="wk-grow" />
          {canEdit && draft === undefined && (
            <Menu align="end" label={t("comment.menu")} trigger={<IconTrigger icon="dots" label={t("comment.menu")} />}>
              <MenuItem icon="edit" onSelect={() => setDraft(body)}>
                {t("comment.edit")}
              </MenuItem>
              {onDelete && (
                <MenuItem icon="trash" danger onSelect={onDelete}>
                  {t("comment.delete")}
                </MenuItem>
              )}
            </Menu>
          )}
        </>
      }
      footer={draft === undefined ? footer : undefined}
    >
      {draft === undefined ? (
        <Body owner={repo.owner} name={repo.name} refName={repo.defaultBranch} source={body} />
      ) : (
        <Composer owner={repo.owner} name={repo.name} refName={repo.defaultBranch} value={draft} onChange={setDraft} label={t("comment.editLabel")} autoFocus>
          <Button variant="ghost" onClick={() => setDraft(undefined)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={saving} onClick={() => onSave(draft, () => setDraft(undefined))}>
            {t("action.save")}
          </Button>
        </Composer>
      )}
    </CommentBox>
  );
}

function CommentItem({ repo, issue, c, me }: { repo: Repo; issue: Issue; c: IssueComment; me?: string }) {
  const t = useT("work");
  const notify = useNotify();
  const edit = useEditComment(repo.owner, repo.name);
  const del = useDeleteComment(repo.owner, repo.name);
  const react = useReact(repo.owner, repo.name, issue.number);
  const mine = me !== undefined && me === c.author;
  const canEdit = mine || canTriage(repo.permission);
  return (
    <EditableBody
      repo={repo}
      author={c.author}
      body={c.body}
      canEdit={canEdit}
      saving={edit.isPending}
      onSave={(body, done) => edit.mutate({ id: c.id, body }, { onSuccess: done, onError: notify.fail })}
      onDelete={() => {
        if (window.confirm(t("comment.deleteConfirm"))) del.mutate(c.id, { onError: notify.fail });
      }}
      head={
        <>
          <b>{c.author}</b>
          <When iso={c.createdAt} />
          {c.author === issue.author && <span className="wk-tag">{t("comment.authorTag")}</span>}
          {c.editedAt && <span className="ui-note">{t("comment.edited")}</span>}
        </>
      }
      footer={
        <Reactions
          groups={c.reactions}
          disabled={me === undefined || issue.locked}
          onToggle={(content, on) => react.mutate({ commentId: c.id, content, on }, { onError: notify.fail })}
        />
      }
    />
  );
}

function EventItem({ e }: { e: TimelineEvent }) {
  const t = useT("work");
  const who = <b>{e.actor}</b>;
  const when = <When iso={e.createdAt} />;
  let icon: Parameters<typeof EventRow>[0]["icon"] = "info";
  let tone: WorkState | undefined;
  let text: ReactNode;
  switch (e.event) {
    case "closed":
      icon = "check";
      tone = "closed";
      text = e.stateReason === "not_planned" ? t("ev.closedNotPlanned") : e.stateReason === "duplicate" ? t("ev.closedDuplicate") : t("ev.closed");
      break;
    case "reopened":
      icon = "issue";
      tone = "open";
      text = t("ev.reopened");
      break;
    case "renamed":
      icon = "edit";
      text = t("ev.renamed", { from: e.renameFrom ?? "", to: e.renameTo ?? "" });
      break;
    case "labeled":
    case "unlabeled":
      icon = "tag";
      text = (
        <>
          {t(e.event === "labeled" ? "ev.labeled" : "ev.unlabeled")} {e.label && <LabelChip label={e.label} />}
        </>
      );
      break;
    case "assigned":
      icon = "check";
      text = e.assignee === e.actor ? t("ev.selfAssigned") : t("ev.assigned", { user: e.assignee ?? "" });
      break;
    case "unassigned":
      icon = "x";
      text = e.assignee === e.actor ? t("ev.selfUnassigned") : t("ev.unassigned", { user: e.assignee ?? "" });
      break;
    case "milestoned":
      icon = "clock";
      text = t("ev.milestoned", { milestone: e.milestone ?? "" });
      break;
    case "demilestoned":
      icon = "clock";
      text = t("ev.demilestoned", { milestone: e.milestone ?? "" });
      break;
    case "locked":
      icon = "lock";
      text = e.lockReason ? t("ev.lockedReason", { reason: t(`lock.${e.lockReason}`) }) : t("ev.locked");
      break;
    case "unlocked":
      icon = "lock";
      text = t("ev.unlocked");
      break;
    case "referenced":
    case "cross-referenced": {
      icon = e.source?.kind === "commit" ? "commit" : "book";
      const s = e.source;
      if (!s || s.redacted) {
        text = t("ev.referencedHidden");
      } else if (s.kind === "commit") {
        text = t("ev.referencedCommit", { commit: shortSha(s.commit ?? "") });
      } else {
        const [o, n] = (s.repo ?? "").split("/");
        const target = s.kind === "pull" ? "/$owner/$name/pulls/$number" : "/$owner/$name/issues/$number";
        text = (
          <>
            {t("ev.referenced")}{" "}
            {o && n && s.number !== undefined ? (
              <Link to={target} params={{ owner: o, name: n, number: String(s.number) }}>
                {s.title || `${s.repo}#${s.number}`}
              </Link>
            ) : (
              s.title
            )}
          </>
        );
      }
      break;
    }
    default:
      text = t("ev.other", { event: e.event });
  }
  return (
    <EventRow icon={icon} tone={tone}>
      {who} {text} {when}
    </EventRow>
  );
}

/**
 * Body, timeline and comment box of an issue or pull request. `onState` changes the open or
 * closed state; `extra` adds entries (reviews) sorted by time with the events.
 */
export function Conversation({ repo, issue, extra = [], onState, stateBusy, canClose, closeLabels, primarySubmit = true }: {
  repo: Repo;
  issue: Issue;
  extra?: TimelineExtra[];
  onState: (state: "open" | "closed", reason?: string) => void;
  stateBusy: boolean;
  canClose: boolean;
  closeLabels: { close: string; reopen: string; withComment: string };
  /** False when the view already has its filled call to action (merge). */
  primarySubmit?: boolean;
}) {
  const t = useT("work");
  const notify = useNotify();
  const me = useSession().data?.username;
  const timeline = useTimeline(repo.owner, repo.name, issue.number);
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const react = useReact(repo.owner, repo.name, issue.number);
  const add = useAddComment(repo.owner, repo.name, issue.number);
  const [draft, setDraft] = useState("");
  const canEditBody = (me !== undefined && me === issue.author) || canTriage(repo.permission);
  const canComment = me !== undefined && (!issue.locked || canTriage(repo.permission));

  function comment(then?: () => void) {
    if (!draft.trim()) {
      then?.();
      return;
    }
    add.mutate(draft, {
      onSuccess: () => {
        setDraft("");
        then?.();
      },
      onError: notify.fail,
    });
  }

  return (
    <div className="wk-conv">
      <EditableBody
        repo={repo}
        author={issue.author}
        body={issue.body}
        canEdit={canEditBody}
        saving={update.isPending}
        onSave={(body, done) => update.mutate({ body }, { onSuccess: done, onError: notify.fail })}
        head={
          <>
            <b>{issue.author}</b>
            <When iso={issue.createdAt} />
            <span className="wk-tag">{t("comment.authorTag")}</span>
            {issue.editedAt && <span className="ui-note">{t("comment.edited")}</span>}
          </>
        }
        footer={
          <Reactions
            groups={issue.reactions}
            disabled={me === undefined || issue.locked}
            onToggle={(content, on) => react.mutate({ content, on }, { onError: notify.fail })}
          />
        }
      />
      <Gate query={timeline}>
        {(events) => {
          const items: TimelineExtra[] = [
            ...events.map((e) => ({
              key: e.id,
              at: e.createdAt,
              node: e.event === "commented" && e.comment ? <CommentItem repo={repo} issue={issue} c={e.comment} me={me} /> : <EventItem e={e} />,
            })),
            ...extra,
          ].sort((a, b) => a.at.localeCompare(b.at));
          return items.map((x) => <div key={x.key}>{x.node}</div>);
        }}
      </Gate>
      {issue.locked && (
        <EventRow icon="lock">{issue.lockReason ? t("locked.reason", { reason: t(`lock.${issue.lockReason}`) }) : t("locked.plain")}</EventRow>
      )}
      {canComment ? (
        <div className="wk-cm wk-cm-new">
          <Composer owner={repo.owner} name={repo.name} refName={repo.defaultBranch} value={draft} onChange={setDraft} label={t("composer.label")}>
            {canClose &&
              (issue.state === "open" ? (
                <Button icon="check" disabled={stateBusy || add.isPending} onClick={() => comment(() => onState("closed", "completed"))}>
                  {draft.trim() ? closeLabels.withComment : closeLabels.close}
                </Button>
              ) : (
                <Button icon="issue" disabled={stateBusy || add.isPending} onClick={() => comment(() => onState("open"))}>
                  {closeLabels.reopen}
                </Button>
              ))}
            <Button variant={primarySubmit ? "primary" : "secondary"} disabled={!draft.trim() || add.isPending} onClick={() => comment()}>
              {t("composer.submit")}
            </Button>
          </Composer>
        </div>
      ) : (
        me === undefined && <p className="ui-note">{t("composer.signIn")}</p>
      )}
    </div>
  );
}

/* Metadata groups of the right column, shared by issues, pull requests and the new issue form. */

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export function AssigneesGroup({ repo, assignees, busy, onChange }: { repo: Repo; assignees: string[]; busy: boolean; onChange: (assignees: string[]) => void }) {
  const t = useT("work");
  const triage = canTriage(repo.permission);
  const people = useCollaborators(repo.owner, repo.name, triage);
  const me = useSession().data?.username;
  const names = Array.from(new Set([...(people.data ?? []).map((p) => p.username), ...assignees, ...(me ? [me] : [])])).sort();
  const [searching, setSearching] = useState(false);
  return (
    <SideBlock
      title={t("side.assignees")}
      action={
        triage && (
          <Menu align="end" trigger={<IconTrigger icon="edit" label={t("side.editAssignees")} />}>
            <MenuLabel>{t("side.assignTo")}</MenuLabel>
            {names.map((u) => (
              <MenuItem key={u} icon={assignees.includes(u) ? "check" : undefined} onSelect={() => onChange(toggle(assignees, u))}>
                {u}
              </MenuItem>
            ))}
            <MenuSeparator />
            <MenuItem icon="search" onSelect={() => setSearching(true)}>
              {t("side.searchPeople")}
            </MenuItem>
          </Menu>
        )
      }
    >
      {searching && triage && <PeopleSearch repo={repo} selected={assignees} busy={busy} onToggle={(u) => onChange(toggle(assignees, u))} onClose={() => setSearching(false)} />}
      {assignees.length === 0 ? (
        <p className="ui-note">
          {t("side.noAssignees")}
          {triage && me && (
            <>
              {" "}
              <button type="button" className="wk-linkbtn" onClick={() => onChange([...assignees, me])}>
                {t("side.assignSelf")}
              </button>
            </>
          )}
        </p>
      ) : (
        <ul className="wk-people">
          {assignees.map((u) => (
            <li key={u}>
              <Avatar user={u} size={20} />
              {u}
            </li>
          ))}
        </ul>
      )}
    </SideBlock>
  );
}

/** Searches the people visible to the viewer (GET /search/users) to toggle them in a list. */
export function PeopleSearch({ repo, selected, busy, onToggle, onClose }: { repo: Repo; selected: string[]; busy: boolean; onToggle: (username: string) => void; onClose: () => void }) {
  const t = useT("work");
  const [q, setQ] = useState("");
  const deferred = useDeferredValue(q.trim());
  const found = useUserSearch(deferred, `${repo.owner}/${repo.name}`);
  return (
    <div className="wk-psearch">
      <Input label={t("side.searchLabel")} autoFocus autoComplete="off" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === "Escape" && onClose()} />
      {deferred !== "" && found.isSuccess && found.data.length === 0 && <p className="ui-note">{t("side.searchNone")}</p>}
      {found.isError && <p className="ui-note">{t("side.searchFailed")}</p>}
      {(found.data ?? []).length > 0 && (
        <ul className="wk-people" aria-label={t("side.searchResults")}>
          {(found.data ?? []).map((u) => (
            <li key={u.username}>
              <button type="button" className="wk-linkbtn wk-psearch-hit" disabled={busy} aria-pressed={selected.includes(u.username)} onClick={() => onToggle(u.username)}>
                <Avatar user={u.username} size={20} />
                <span>{u.username}</span>
                {u.displayName && <span className="ui-note">{u.displayName}</span>}
                {selected.includes(u.username) && <Icon name="check" />}
              </button>
            </li>
          ))}
        </ul>
      )}
      <Button size="sm" variant="ghost" onClick={onClose}>
        {t("side.searchDone")}
      </Button>
    </div>
  );
}

export function LabelsGroup({ repo, selected, onChange }: { repo: Repo; selected: Label[]; onChange: (names: string[]) => void }) {
  const t = useT("work");
  const triage = canTriage(repo.permission);
  const labels = useLabels(repo.owner, repo.name);
  const current = selected.map((l) => l.name);
  return (
    <SideBlock
      title={t("side.labels")}
      action={
        triage && (
          <Menu align="end" trigger={<IconTrigger icon="edit" label={t("side.editLabels")} />}>
            {(labels.data ?? []).length === 0 && <MenuLabel>{t("side.noRepoLabels")}</MenuLabel>}
            {(labels.data ?? []).map((l) => (
              <MenuItem key={l.id} icon={current.includes(l.name) ? "check" : undefined} onSelect={() => onChange(toggle(current, l.name))}>
                <LabelChip label={l} />
              </MenuItem>
            ))}
          </Menu>
        )
      }
    >
      {selected.length === 0 ? (
        <p className="ui-note">{t("side.noLabels")}</p>
      ) : (
        <div className="wk-labels">
          {selected.map((l) => (
            <LabelChip key={l.id} label={l} />
          ))}
        </div>
      )}
    </SideBlock>
  );
}

export function MilestoneGroup({ repo, milestone: m, onChange }: { repo: Repo; milestone: Milestone | undefined; onChange: (id: string) => void }) {
  const t = useT("work");
  const triage = canTriage(repo.permission);
  const milestones = useMilestones(repo.owner, repo.name);
  return (
    <SideBlock
      title={t("side.milestone")}
      action={
        triage && (
          <Menu align="end" trigger={<IconTrigger icon="edit" label={t("side.editMilestone")} />}>
            <MenuItem icon={!m ? "check" : undefined} onSelect={() => onChange("")}>
              {t("side.noMilestone")}
            </MenuItem>
            {(milestones.data ?? []).map((x) => (
              <MenuItem key={x.id} icon={m?.id === x.id ? "check" : undefined} onSelect={() => onChange(x.id)}>
                {x.title}
              </MenuItem>
            ))}
          </Menu>
        )
      }
    >
      {m ? (
        <div className="wk-ms">
          <div className="wk-ms-h">
            <span>{m.title}</span>
            <span className="ui-note">{t("side.msCount", { closed: m.closedIssues, total: m.openIssues + m.closedIssues })}</span>
          </div>
          <Meter open={m.openIssues} closed={m.closedIssues} label={t("side.progress", { title: m.title })} />
        </div>
      ) : (
        <p className="ui-note">{t("side.noMilestone")}</p>
      )}
    </SideBlock>
  );
}

/**
 * Subscription to this issue or pull request and the repository watch level, both reached from
 * the pencil menu. number defaults to the $number route parameter.
 */
export function NotifyBlock({ repo, number }: { repo: Repo; number?: number }) {
  const t = useT("work");
  const notify = useNotify();
  const params: { number?: string } = useParams({ strict: false });
  const n = number ?? Number(params.number);
  const signedIn = useSession().data !== undefined;
  const ref = { owner: repo.owner, name: repo.name, number: n };
  const subject = useSubjectSubscription(ref, signedIn && Number.isInteger(n) && n > 0);
  const setSubject = useSetSubjectSubscription(ref);
  const sub = useRepoSubscription(repo.owner, repo.name, signedIn);
  const set = useSetRepoSubscription(repo.owner, repo.name);
  if (!signedIn) return null;
  const levels: RepoSubscription["level"][] = ["watching", "participating", "ignoring"];
  const subscribed = subject.data?.subscription.subscribed ?? false;
  const level = sub.data?.level ?? "participating";
  return (
    <SideBlock
      title={t("side.notifications")}
      action={
        <Menu align="end" trigger={<IconTrigger icon="edit" label={t("side.editNotifications")} disabled={!subject.data} />}>
          <MenuItem
            icon="bell"
            onSelect={() => setSubject.mutate(!subscribed, { onSuccess: (s) => notify.ok(t(s.subscription.subscribed ? "subject.subscribedOk" : "subject.unsubscribedOk")), onError: notify.fail })}
          >
            {t(subscribed ? "subject.unsubscribe" : "subject.subscribe")}
          </MenuItem>
          {subject.data?.subscription.explicit && (
            <MenuItem icon="x" onSelect={() => setSubject.mutate(null, { onError: notify.fail })}>
              {t("subject.reset")}
            </MenuItem>
          )}
          <MenuSeparator />
          <MenuLabel>{t("side.watchLabel")}</MenuLabel>
          {levels.map((l) => (
            <MenuItem key={l} icon={l === level ? "check" : undefined} onSelect={() => set.mutate(l, { onError: notify.fail })}>
              {t(`watch.${l}`)}
            </MenuItem>
          ))}
        </Menu>
      }
    >
      <p className="ui-note">{subject.data ? t(subscribed ? "subject.subscribedHint" : "subject.unsubscribedHint") : ""}</p>
    </SideBlock>
  );
}

export function ParticipantsBlock({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("work");
  const timeline = useTimeline(repo.owner, repo.name, issue.number);
  const people = Array.from(new Set([issue.author, ...(timeline.data ?? []).filter((e) => e.event === "commented").map((e) => e.actor)]));
  return (
    <SideBlock title={t("side.participants", { count: people.length })}>
      <div className="wk-avatars">
        {people.map((u) => (
          <span key={u} title={u}>
            <Avatar user={u} size={24} />
          </span>
        ))}
      </div>
    </SideBlock>
  );
}

/** One-line digest of the metadata for the collapsed "Details" row on narrow screens. */
export function detailsSummary(t: ReturnType<typeof useT>, d: { reviewers?: number; assignees: number; labels: number; milestone?: string }): string {
  const parts = [
    d.reviewers ? t("side.sumReviewers", { count: d.reviewers }) : "",
    d.assignees ? t("side.sumAssignees", { count: d.assignees }) : "",
    d.labels ? t("side.sumLabels", { count: d.labels }) : "",
    d.milestone ?? "",
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : t("side.sumNone");
}

/** Everything in the right column below the status box of an issue or pull request. */
export function IssueMeta({ repo, issue, reviewers, before, linked }: { repo: Repo; issue: Issue; reviewers?: number; before?: ReactNode; linked?: ReactNode }) {
  const t = useT("work");
  const notify = useNotify();
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const fail = { onError: notify.fail };
  return (
    <SideDetails summary={detailsSummary(t, { reviewers, assignees: issue.assignees.length, labels: issue.labels.length, milestone: issue.milestone?.title })}>
      {before}
      <AssigneesGroup repo={repo} assignees={issue.assignees} busy={update.isPending} onChange={(assignees) => update.mutate({ assignees }, fail)} />
      <LabelsGroup repo={repo} selected={issue.labels} onChange={(labels) => update.mutate({ labels }, fail)} />
      <MilestoneGroup repo={repo} milestone={issue.milestone ?? undefined} onChange={(milestoneId) => update.mutate({ milestoneId }, fail)} />
      {linked}
      <NotifyBlock repo={repo} number={issue.number} />
      <ParticipantsBlock repo={repo} issue={issue} />
    </SideDetails>
  );
}

/** Lock or unlock the conversation (triage): entries of the title menu. */
export function LockMenuItems({ repo, issue, onLock }: { repo: Repo; issue: Issue; onLock: (lock: boolean, reason?: string) => void }) {
  const t = useT("work");
  if (!canTriage(repo.permission)) return null;
  if (issue.locked) {
    return (
      <MenuItem icon="lock" onSelect={() => onLock(false)}>
        {t("lock.unlock")}
      </MenuItem>
    );
  }
  return (
    <>
      <MenuLabel>{t("lock.lock")}</MenuLabel>
      <MenuItem icon="lock" onSelect={() => onLock(true)}>
        {t("lock.noReason")}
      </MenuItem>
      {(["off-topic", "too heated", "resolved", "spam"] as const).map((r) => (
        <MenuItem key={r} icon="lock" onSelect={() => onLock(true, r)}>
          {t(`lock.${r}`)}
        </MenuItem>
      ))}
    </>
  );
}
