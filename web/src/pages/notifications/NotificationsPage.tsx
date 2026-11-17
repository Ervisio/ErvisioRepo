import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { useIssue } from "../../api/issues.ts";
import { useInbox, useMarkAllRead, useMarkDone, useMarkRead, useThreadIgnore, useUnreadCount, type NotificationThread } from "../../api/notifications.ts";
import { usePull } from "../../api/pulls.ts";
import { useRepo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon, Input, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Pager, When } from "../code/common.tsx";
import { Body } from "../work/common.tsx";

/**
 * Inbox views: unread (default), review requests, assignments, everything. Review and Assigned read every
 * thread and keep those with that reason, on the loaded page. repo (owner/name) is filtered by the server.
 */
export type InboxView = "review" | "assigned" | "all";
export type InboxSearch = { filter?: InboxView; page?: number; repo?: string };

const subjectIcon: Record<NotificationThread["subjectType"], IconName> = { issue: "issue", pull_request: "pr", release: "tag", commit: "commit" };

const views = ["unread", "review", "assigned", "all"] as const;
const viewReason: Partial<Record<(typeof views)[number], NotificationThread["reason"]>> = { review: "review_requested", assigned: "assigned" };

const reasonChip: Partial<Record<NotificationThread["reason"], string>> = {
  review_requested: "ui-chip-rev",
  assigned: "ui-chip-ok",
  mentioned: "ui-chip-acc",
};

// Below this width only the list shows, and a row opens its subject directly.
const narrow = () => window.matchMedia("(max-width: 720px)").matches;

/** Notifications as a mail client (round 017 C): the list on the left, a preview of the picked thread on the right. */
export function NotificationsPage({ search }: { search: InboxSearch }) {
  const t = useT("notifications");
  const notify = useNotify();
  const navigate = useNavigate();
  const view = search.filter ?? "unread";
  const page = search.page ?? 1;
  const repo = search.repo;
  const inbox = useInbox({ filter: view === "unread" ? "unread" : "all", page, repo });
  const unread = useUnreadCount().data?.unread;
  const markAll = useMarkAllRead();
  const markRead = useMarkRead();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<string | null>(null);

  const go = (patch: Partial<InboxSearch>) => void navigate({ to: "/notifications", search: { ...search, page: undefined, ...patch } });
  const openThread = (n: NotificationThread) => {
    if (n.unread) markRead.mutate(n.id);
    switch (n.subjectType) {
      case "issue":
        void navigate({ to: "/$owner/$name/issues/$number", params: { owner: n.owner, name: n.repo, number: String(n.number) } });
        break;
      case "pull_request":
        void navigate({ to: "/$owner/$name/pulls/$number", params: { owner: n.owner, name: n.repo, number: String(n.number) } });
        break;
      case "release":
        void navigate({ to: "/$owner/$name/releases", params: { owner: n.owner, name: n.repo } });
        break;
      default:
        void navigate({ to: "/$owner/$name/commits", params: { owner: n.owner, name: n.repo } });
    }
  };

  const reason = viewReason[view];
  const q = query.trim().toLowerCase();
  const items = (inbox.data?.items ?? []).filter(
    (n) => (!reason || n.reason === reason) && (!q || `${n.owner}/${n.repo} ${n.title}`.toLowerCase().includes(q)),
  );
  const current = items.find((n) => n.id === picked) ?? items[0];

  return (
    <div className="page nt">
      <div className="nt-l">
        <div className="nt-tb">
          <div className="nt-row">
            <h1>{t("title")}</h1>
            <Button
              variant="ghost"
              icon="check"
              aria-label={t("markAll")}
              title={t("markAll")}
              disabled={markAll.isPending}
              onClick={() => markAll.mutate(new Date().toISOString(), { onSuccess: () => notify.ok(t("allRead")), onError: notify.fail })}
            />
            <Link to="/settings" search={{ section: "notifications" }} className="ui-iconbtn" aria-label={t("settings")} title={t("settings")}>
              <Icon name="gear" />
            </Link>
          </div>
          <div className="ui-seg" role="group" aria-label={t("filter.label")}>
            {views.map((v) => (
              <button key={v} type="button" className="ui-segi" aria-pressed={view === v} onClick={() => go({ filter: v === "unread" ? undefined : v })}>
                {t(`view.${v}`)}
                {v === "unread" && unread ? <span className="nt-c">{unread}</span> : null}
              </button>
            ))}
          </div>
          <Input type="search" label={t("find")} hideLabel placeholder={t("find")} autoComplete="off" value={query} onChange={(e) => setQuery(e.target.value)} />
          {repo && (
            <span className="ui-chip nt-repo">
              {repo}
              <button type="button" aria-label={t("clearRepo")} title={t("clearRepo")} onClick={() => go({ repo: undefined })}>
                <Icon name="x" />
              </button>
            </span>
          )}
        </div>
        <div className="nt-ls">
          {inbox.isPending ? (
            <p className="nt-note" role="status">{t("loading")}</p>
          ) : inbox.isError ? (
            <p className="nt-note" role="alert">{t("loadError")}</p>
          ) : items.length === 0 ? (
            <EmptyState icon="bell" title={t(`empty.${view}`)} description={t("emptyHint")} />
          ) : (
            <ul aria-label={t("list")}>
              {items.map((n) => (
                <li key={n.id}>
                  <button
                    type="button"
                    className={n.unread ? "nt-r" : "nt-r read"}
                    aria-current={current?.id === n.id ? "true" : undefined}
                    onClick={() => (narrow() ? openThread(n) : setPicked(n.id))}
                  >
                    <span className="ui-udot" />
                    {n.unread && <span className="sr-only">{t("unread")}</span>}
                    <Icon name={subjectIcon[n.subjectType]} />
                    <span className="nt-rm">
                      <span className="nt-top">
                        <span className="nt-rp">{n.owner}/{n.repo}{n.number > 0 && ` #${n.number}`}</span>
                        <span className="ui-tm"><When iso={n.updatedAt} /></span>
                      </span>
                      <span className="nt-ttl">{n.title}</span>
                      <span className={`ui-chip ${reasonChip[n.reason] ?? ""}`}>{t(`reason.${n.reason}`)}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {inbox.data && <Pager page={page} hasMore={inbox.data.hasMore} onPage={(p) => go({ page: p > 1 ? p : undefined })} />}
        </div>
      </div>
      <section className="nt-p" aria-label={t("preview")}>
        {current ? <Preview key={current.id} n={current} onOpen={() => openThread(current)} /> : <p className="nt-note">{t("pick")}</p>}
      </section>
    </div>
  );
}

function Preview({ n, onOpen }: { n: NotificationThread; onOpen: () => void }) {
  const t = useT("notifications");
  const notify = useNotify();
  const markRead = useMarkRead();
  const done = useMarkDone();
  const ignore = useThreadIgnore();
  return (
    <>
      <div className="nt-ph">
        <h2>
          {n.title} {n.number > 0 && <span>#{n.number}</span>}
        </h2>
        <span className="nt-act">
          {n.unread && (
            <Button icon="eye" aria-label={t("action.read")} title={t("action.read")} disabled={markRead.isPending} onClick={() => markRead.mutate(n.id, { onError: notify.fail })} />
          )}
          <Button icon="check" disabled={done.isPending} onClick={() => done.mutate(n.id, { onError: notify.fail })}>
            {t("action.done")}
          </Button>
          <Button
            icon="bell"
            aria-label={t("action.ignore")}
            title={t("action.ignore")}
            disabled={ignore.isPending}
            onClick={() => ignore.mutate({ id: n.id, ignored: true }, { onSuccess: () => notify.ok(t("ignored")), onError: notify.fail })}
          />
          <Button variant="primary" onClick={onOpen}>{t("action.open")}</Button>
        </span>
      </div>
      {n.subjectType === "issue" ? <IssueDetail n={n} /> : n.subjectType === "pull_request" ? <PullDetail n={n} /> : <Summary n={n} />}
    </>
  );
}

/** Box with the reason (or the server summary) and the time, around `children`. */
function Summary({ n, children }: { n: NotificationThread; children?: ReactNode }) {
  const t = useT("notifications");
  return (
    <div className="ui-box nt-box">
      <div className="ui-box-h">
        <b>{t(`subject.${n.subjectType}`)}</b> · {n.summary || t(`reason.${n.reason}`)} · <When iso={n.updatedAt} />
      </div>
      {children && <div className="ui-in nt-in">{children}</div>}
    </div>
  );
}

function IssueDetail({ n }: { n: NotificationThread }) {
  const t = useT("notifications");
  const issue = useIssue(n.owner, n.repo, n.number).data;
  const branch = useRepo(n.owner, n.repo).data?.defaultBranch ?? "HEAD";
  if (!issue) return <Summary n={n} />;
  const open = issue.state === "open";
  return (
    <>
      <div className="nt-meta">
        <span className={open ? "ui-state" : "ui-state ui-state-merged"}>
          <Icon name={open ? "issue" : "check"} />
          {t(`state.${issue.state}`)}
        </span>
        <span>
          <b>{issue.author || t("ghost")}</b> {t("opened")} <When iso={issue.createdAt} />
        </span>
      </div>
      <Summary n={n}>
        <Body owner={n.owner} name={n.repo} refName={branch} source={issue.body} />
      </Summary>
    </>
  );
}

function PullDetail({ n }: { n: NotificationThread }) {
  const t = useT("notifications");
  const pull = usePull(n.owner, n.repo, n.number).data;
  const branch = useRepo(n.owner, n.repo).data?.defaultBranch ?? "HEAD";
  if (!pull) return <Summary n={n} />;
  const cls = pull.draft && pull.state === "open" ? "ui-state ui-state-draft" : pull.state === "open" ? "ui-state" : pull.state === "merged" ? "ui-state ui-state-merged" : "ui-state ui-state-closed";
  return (
    <>
      <div className="nt-meta">
        <span className={cls}>
          <Icon name="pr" />
          {t(pull.draft && pull.state === "open" ? "state.draft" : `state.${pull.state}`)}
        </span>
        <span>
          <b>{pull.issue.author || t("ghost")}</b> {t("wantsMerge")} <code className="ui-mono">{pull.base.ref}</code> {t("from")} <code className="ui-mono">{pull.head.ref}</code>
        </span>
      </div>
      <Summary n={n}>
        <Body owner={n.owner} name={n.repo} refName={branch} source={pull.issue.body} />
      </Summary>
    </>
  );
}
