import { Link } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
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
  type TimelineEvent,
} from "../../api/issues.ts";
import { useRepoSubscription, useSetRepoSubscription, type RepoSubscription } from "../../api/notifications.ts";
import { useSession } from "../../api/identity.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon, Menu, MenuItem, MenuLabel, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, shortSha, When } from "../code/common.tsx";
import { Avatar, Body, CommentBox, Composer, EventRow, LabelChip, Meter, Reactions, SideBlock, type WorkState } from "./common.tsx";

export const canTriage = (p: Repo["permission"] | undefined) => p === "triage" || p === "write" || p === "maintain" || p === "admin";

/** An extra entry merged into the timeline by time (pull request reviews). */
export type TimelineExtra = { key: string; at: string; node: ReactNode };

function EditableBody({ repo, author, body, canEdit, onSave, saving, head, footer, own }: {
  repo: Repo;
  author: string;
  body: string;
  canEdit: boolean;
  onSave: (body: string, done: () => void) => void;
  saving: boolean;
  head: ReactNode;
  footer?: ReactNode;
  own?: boolean;
}) {
  const t = useT("work");
  const [draft, setDraft] = useState<string | undefined>(undefined);
  return (
    <CommentBox
      author={author}
      tone={own ? "author" : undefined}
      head={
        <>
          {head}
          <span className="acc-grow" />
          {canEdit && draft === undefined && (
            <Button size="sm" variant="ghost" icon="edit" aria-label={t("comment.edit")} onClick={() => setDraft(body)} />
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
      own={c.author === issue.author}
      saving={edit.isPending}
      onSave={(body, done) => edit.mutate({ id: c.id, body }, { onSuccess: done, onError: notify.fail })}
      head={
        <>
          <b>{c.author}</b>
          <When iso={c.createdAt} />
          {c.author === issue.author && <span className="wk-tag">{t("comment.authorTag")}</span>}
          {c.editedAt && <span className="ui-note">{t("comment.edited")}</span>}
          {canEdit && (
            <Button
              size="sm"
              variant="ghost"
              icon="trash"
              aria-label={t("comment.delete")}
              disabled={del.isPending}
              onClick={() => {
                if (window.confirm(t("comment.deleteConfirm"))) del.mutate(c.id, { onError: notify.fail });
              }}
            />
          )}
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
export function Conversation({ repo, issue, extra = [], onState, stateBusy, canClose, closeLabels, beforeComposer, primarySubmit = true }: {
  repo: Repo;
  issue: Issue;
  extra?: TimelineExtra[];
  onState: (state: "open" | "closed", reason?: string) => void;
  stateBusy: boolean;
  canClose: boolean;
  closeLabels: { close: string; reopen: string; withComment: string };
  /** Rendered between the timeline and the comment box (the pull request merge box). */
  beforeComposer?: ReactNode;
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
        own
        saving={update.isPending}
        onSave={(body, done) => update.mutate({ body }, { onSuccess: done, onError: notify.fail })}
        head={
          <>
            <b>{issue.author}</b>
            <When iso={issue.createdAt} />
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
      {beforeComposer}
      {canComment ? (
        <div className="wk-cm">
          <Avatar user={me ?? "?"} />
          <div className="wk-cm-box">
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
        </div>
      ) : (
        me === undefined && <p className="ui-note">{t("composer.signIn")}</p>
      )}
    </div>
  );
}

/* Sidebar blocks shared by issues and pull requests. */

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export function AssigneesBlock({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("work");
  const notify = useNotify();
  const triage = canTriage(repo.permission);
  const people = useCollaborators(repo.owner, repo.name, triage);
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const me = useSession().data?.username;
  const set = (assignees: string[]) => update.mutate({ assignees }, { onError: notify.fail });
  const names = Array.from(new Set([...(people.data ?? []).map((p) => p.username), ...issue.assignees, ...(me ? [me] : [])])).sort();
  return (
    <SideBlock
      title={t("side.assignees")}
      action={
        triage && (
          <Menu align="end" trigger={<Button size="sm" variant="ghost" icon="gear" aria-label={t("side.editAssignees")} />}>
            <MenuLabel>{t("side.assignTo")}</MenuLabel>
            {names.map((u) => (
              <MenuItem key={u} icon={issue.assignees.includes(u) ? "check" : undefined} onSelect={() => set(toggle(issue.assignees, u))}>
                {u}
              </MenuItem>
            ))}
          </Menu>
        )
      }
    >
      {issue.assignees.length === 0 ? (
        <p className="ui-note">
          {t("side.noAssignees")}
          {triage && me && !issue.assignees.includes(me) && (
            <>
              {" "}
              <button type="button" className="wk-linkbtn" onClick={() => set([...issue.assignees, me])}>
                {t("side.assignSelf")}
              </button>
            </>
          )}
        </p>
      ) : (
        <ul className="wk-people">
          {issue.assignees.map((u) => (
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

export function LabelsBlock({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("work");
  const notify = useNotify();
  const triage = canTriage(repo.permission);
  const labels = useLabels(repo.owner, repo.name);
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const current = issue.labels.map((l) => l.name);
  return (
    <SideBlock
      title={t("side.labels")}
      action={
        triage && (
          <Menu align="end" trigger={<Button size="sm" variant="ghost" icon="gear" aria-label={t("side.editLabels")} />}>
            {(labels.data ?? []).length === 0 && <MenuLabel>{t("side.noRepoLabels")}</MenuLabel>}
            {(labels.data ?? []).map((l) => (
              <MenuItem key={l.id} icon={current.includes(l.name) ? "check" : undefined} onSelect={() => update.mutate({ labels: toggle(current, l.name) }, { onError: notify.fail })}>
                <LabelChip label={l} />
              </MenuItem>
            ))}
          </Menu>
        )
      }
    >
      {issue.labels.length === 0 ? (
        <p className="ui-note">{t("side.noLabels")}</p>
      ) : (
        <div className="wk-labels">
          {issue.labels.map((l) => (
            <LabelChip key={l.id} label={l} />
          ))}
        </div>
      )}
    </SideBlock>
  );
}

export function MilestoneBlock({ repo, issue }: { repo: Repo; issue: Issue }) {
  const t = useT("work");
  const notify = useNotify();
  const triage = canTriage(repo.permission);
  const milestones = useMilestones(repo.owner, repo.name);
  const update = useUpdateIssue(repo.owner, repo.name, issue.number);
  const m = issue.milestone;
  return (
    <SideBlock
      title={t("side.milestone")}
      action={
        triage && (
          <Menu align="end" trigger={<Button size="sm" variant="ghost" icon="gear" aria-label={t("side.editMilestone")} />}>
            <MenuItem icon={!m ? "check" : undefined} onSelect={() => update.mutate({ milestoneId: "" }, { onError: notify.fail })}>
              {t("side.noMilestone")}
            </MenuItem>
            {(milestones.data ?? []).map((x) => (
              <MenuItem key={x.id} icon={m?.id === x.id ? "check" : undefined} onSelect={() => update.mutate({ milestoneId: x.id }, { onError: notify.fail })}>
                {x.title}
              </MenuItem>
            ))}
          </Menu>
        )
      }
    >
      {m ? (
        <div className="wk-ms">
          <span>{m.title}</span>
          <Meter open={m.openIssues} closed={m.closedIssues} label={t("side.progress", { title: m.title })} />
          <span className="ui-note">{t("side.msCount", { closed: m.closedIssues, total: m.openIssues + m.closedIssues })}</span>
        </div>
      ) : (
        <p className="ui-note">{t("side.noMilestone")}</p>
      )}
    </SideBlock>
  );
}

/** Repository watch level: per-thread subscription has no lookup by issue number yet. */
export function NotifyBlock({ repo }: { repo: Repo }) {
  const t = useT("work");
  const notify = useNotify();
  const sub = useRepoSubscription(repo.owner, repo.name);
  const set = useSetRepoSubscription(repo.owner, repo.name);
  const levels: RepoSubscription["level"][] = ["watching", "participating", "ignoring"];
  return (
    <SideBlock title={t("side.notifications")}>
      <Select
        label={t("side.watchLabel")}
        value={sub.data?.level ?? "participating"}
        disabled={!sub.data || set.isPending}
        onValueChange={(v) => set.mutate(v as RepoSubscription["level"], { onError: notify.fail })}
        options={levels.map((l) => ({ value: l, label: t(`watch.${l}`) }))}
      />
      <p className="ui-note">{t(`watch.${sub.data?.level ?? "participating"}.hint`)}</p>
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

/** Lock or unlock the conversation (triage). */
export function LockButton({ repo, issue, onLock, busy }: { repo: Repo; issue: Issue; onLock: (lock: boolean, reason?: string) => void; busy: boolean }) {
  const t = useT("work");
  if (!canTriage(repo.permission)) return null;
  if (issue.locked) {
    return (
      <Button size="sm" variant="ghost" icon="lock" disabled={busy} onClick={() => onLock(false)}>
        {t("lock.unlock")}
      </Button>
    );
  }
  return (
    <Menu
      align="end"
      trigger={
        <Button size="sm" variant="ghost" icon="lock" disabled={busy}>
          {t("lock.lock")}
          <Icon name="down" />
        </Button>
      }
    >
      <MenuLabel>{t("lock.reasonLabel")}</MenuLabel>
      <MenuItem onSelect={() => onLock(true)}>{t("lock.noReason")}</MenuItem>
      {(["off-topic", "too heated", "resolved", "spam"] as const).map((r) => (
        <MenuItem key={r} onSelect={() => onLock(true, r)}>
          {t(`lock.${r}`)}
        </MenuItem>
      ))}
    </Menu>
  );
}
