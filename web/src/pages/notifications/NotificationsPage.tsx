import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { useInbox, useInboxRepos, useMarkAllRead, useMarkDone, useMarkRead, useThreadIgnore, type NotificationThread } from "../../api/notifications.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon, Page, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { Gate, Pager, When } from "../code/common.tsx";
import { FilterGroup, FilterItem, ListLayoutSwitch, WorkList } from "../work/common.tsx";
import { useListLayout, type ListLayout } from "../work/layout.ts";

/** repo (owner/name) shows the threads of one repository, filtered by the server. */
export type InboxSearch = { filter?: "participating" | "all"; page?: number; repo?: string };

const subjectIcon: Record<NotificationThread["subjectType"], IconName> = { issue: "issue", pull_request: "pr", release: "tag", commit: "commit" };

/** Link to the page of a notification subject. */
function SubjectLink({ n, className, children, onClick }: { n: NotificationThread; className?: string; children: ReactNode; onClick?: () => void }) {
  const params = { owner: n.owner, name: n.repo };
  switch (n.subjectType) {
    case "issue":
      return (
        <Link className={className} onClick={onClick} to="/$owner/$name/issues/$number" params={{ ...params, number: String(n.number) }}>
          {children}
        </Link>
      );
    case "pull_request":
      return (
        <Link className={className} onClick={onClick} to="/$owner/$name/pulls/$number" params={{ ...params, number: String(n.number) }}>
          {children}
        </Link>
      );
    case "release":
      return (
        <Link className={className} onClick={onClick} to="/$owner/$name/releases" params={params}>
          {children}
        </Link>
      );
    default:
      return (
        <Link className={className} onClick={onClick} to="/$owner/$name/commits" params={params}>
          {children}
        </Link>
      );
  }
}

export function NotificationsPage({ search }: { search: InboxSearch }) {
  const t = useT("notifications");
  const notify = useNotify();
  const navigate = useNavigate();
  const layout = useListLayout();
  const filter = search.filter ?? "unread";
  const page = search.page ?? 1;
  const repo = search.repo;
  const inbox = useInbox({ filter, page, repo });
  const repoCounts = useInboxRepos(filter);
  const markAll = useMarkAllRead();
  const markRead = useMarkRead();
  const setRepo = (r: string | undefined) => go({ repo: r });
  // The reason filter of the filter column applies to the loaded page only.
  const [reason, setReason] = useState<NotificationThread["reason"] | undefined>(undefined);

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

  return (
    <Page
      section="issue"
      title={t("title")}
      subtitle={t("subtitle")}
      actions={
        <Button icon="check" disabled={markAll.isPending} onClick={() => markAll.mutate(new Date().toISOString(), { onSuccess: () => notify.ok(t("allRead")), onError: notify.fail })}>
          {t("markAll")}
        </Button>
      }
    >
      <div className="rp-bar wk-bar">
        <div className="ui-tablist" role="group" aria-label={t("filter.label")}>
          {(["unread", "participating", "all"] as const).map((f) => (
            <button key={f} type="button" className="ui-tab" data-state={filter === f ? "active" : "inactive"} aria-pressed={filter === f} onClick={() => go({ filter: f === "unread" ? undefined : f })}>
              {t(`filter.${f}`)}
            </button>
          ))}
        </div>
        <span className="acc-grow" />
        <ListLayoutSwitch />
      </div>
      <Gate query={inbox}>
        {(res) => {
          const repos = Array.from(new Set(res.items.map((n) => `${n.owner}/${n.repo}`))).sort();
          const reasons = Array.from(new Set(res.items.map((n) => n.reason))).sort();
          const items = layout === "b" ? res.items.filter((n) => !reason || n.reason === reason) : res.items;
          const counts = repoCounts.data ?? [];
          const countOf = (c: { count: number; unread: number }) => (filter === "unread" ? c.unread : c.count);
          const listed = counts.map((c) => `${c.owner}/${c.repo}`);
          // Keep the selected repository listed even when it has no thread left.
          const filterRepos = repo && !listed.includes(repo) ? [...listed, repo] : listed;
          const empty = <EmptyState icon="bell" title={t(`empty.${filter}`)} description={t("emptyHint")} />;
          const filters = (
            <>
              <FilterGroup title={t("filters.repos")}>
                <FilterItem active={!repo} onClick={() => setRepo(undefined)} count={counts.reduce((a, c) => a + countOf(c), 0)}>
                  {t("filters.allRepos")}
                </FilterItem>
                {filterRepos.map((r) => {
                  const c = counts.find((x) => `${x.owner}/${x.repo}` === r);
                  return (
                    <FilterItem key={r} active={repo === r} onClick={() => setRepo(r)} count={c ? countOf(c) : 0}>
                      {r}
                    </FilterItem>
                  );
                })}
              </FilterGroup>
              <FilterGroup title={t("filters.reasons")}>
                <FilterItem active={!reason} onClick={() => setReason(undefined)}>
                  {t("filters.allReasons")}
                </FilterItem>
                {reasons.map((r) => (
                  <FilterItem key={r} active={reason === r} onClick={() => setReason(r)} count={res.items.filter((n) => n.reason === r).length}>
                    {t(`reason.${r}`)}
                  </FilterItem>
                ))}
              </FilterGroup>
            </>
          );
          const list = (xs: NotificationThread[]) => (
            <WorkList
              layout={layout}
              items={xs}
              getKey={(n) => n.id}
              label={t("list")}
              filters={filters}
              onOpen={openThread}
              empty={empty}
              row={(n, l) => <ThreadRow n={n} layout={l} />}
              preview={(n) => <ThreadPreview n={n} onOpen={() => openThread(n)} />}
            />
          );
          return (
            <>
              {layout === "a" && items.length > 0
                ? repos.map((r) => (
                    <section key={r} className="wk-group">
                      <h3 className="wk-group-h">
                        <Icon name="code" />
                        {r}
                      </h3>
                      {list(items.filter((n) => `${n.owner}/${n.repo}` === r))}
                    </section>
                  ))
                : list(items)}
              <Pager page={page} hasMore={res.hasMore} onPage={(n) => go({ ...search, page: n > 1 ? n : undefined })} />
            </>
          );
        }}
      </Gate>
    </Page>
  );
}

function ThreadActions({ n }: { n: NotificationThread }) {
  const t = useT("notifications");
  const notify = useNotify();
  const markRead = useMarkRead();
  const done = useMarkDone();
  const ignore = useThreadIgnore();
  return (
    <span className="wk-nact">
      {n.unread && (
        <Button size="sm" variant="ghost" icon="check" aria-label={t("action.read")} title={t("action.read")} disabled={markRead.isPending} onClick={() => markRead.mutate(n.id, { onError: notify.fail })} />
      )}
      <Button size="sm" variant="ghost" icon="x" aria-label={t("action.done")} title={t("action.done")} disabled={done.isPending} onClick={() => done.mutate(n.id, { onError: notify.fail })} />
      <Button
        size="sm"
        variant="ghost"
        icon="bell"
        aria-label={t("action.ignore")}
        title={t("action.ignore")}
        disabled={ignore.isPending}
        onClick={() => ignore.mutate({ id: n.id, ignored: true }, { onSuccess: () => notify.ok(t("ignored")), onError: notify.fail })}
      />
    </span>
  );
}

function ThreadRow({ n, layout }: { n: NotificationThread; layout: ListLayout }) {
  const t = useT("notifications");
  const markRead = useMarkRead();
  const icon = (
    <span className={`wk-nicon ${n.unread ? "wk-unread" : ""}`}>
      <Icon name={subjectIcon[n.subjectType]} />
      {n.unread && <span className="wk-sr">{t("unread")}</span>}
    </span>
  );
  const subject = n.number > 0 ? `${n.title} #${n.number}` : n.title;
  if (layout === "c") {
    return (
      <span className="wk-ci-in">
        {icon}
        <span className="wk-ci-main">
          <span className="wk-title">{subject}</span>
          <span className="wk-meta">
            <span>
              {n.owner}/{n.repo}
            </span>
            <When iso={n.updatedAt} />
          </span>
        </span>
      </span>
    );
  }
  return (
    <div className={`wk-ra wk-nrow ${n.unread ? "wk-nrow-unread" : ""}`}>
      {icon}
      <div className="wk-ra-main">
        <SubjectLink n={n} className="wk-title" onClick={() => n.unread && markRead.mutate(n.id)}>
          {subject}
        </SubjectLink>
        <span className="wk-meta">
          {layout === "b" && (
            <span>
              {n.owner}/{n.repo}
            </span>
          )}
          <span className="wk-tag">{t(`reason.${n.reason}`)}</span>
          {n.summary && <span className="wk-nsum">{n.summary}</span>}
        </span>
      </div>
      <When iso={n.updatedAt} />
      <ThreadActions n={n} />
    </div>
  );
}

function ThreadPreview({ n, onOpen }: { n: NotificationThread; onOpen: () => void }) {
  const t = useT("notifications");
  return (
    <div className="wk-pv-in">
      <div className="wk-pv-head">
        <Icon name={subjectIcon[n.subjectType]} />
        <span className="ui-note">
          {n.owner}/{n.repo}
        </span>
        <span className="acc-grow" />
        <ThreadActions n={n} />
      </div>
      <h3 className="wk-pv-title">
        {n.title} {n.number > 0 && <span className="wk-num">#{n.number}</span>}
      </h3>
      <p className="wk-meta">
        <span className="wk-tag">{t(`reason.${n.reason}`)}</span>
        <span>{t(`subject.${n.subjectType}`)}</span>
        <When iso={n.updatedAt} />
      </p>
      {n.summary ? <p>{n.summary}</p> : <p className="ui-note">{t("noSummary")}</p>}
      <div className="acc-actions">
        <Button onClick={onOpen}>{t("action.open")}</Button>
      </div>
    </div>
  );
}
