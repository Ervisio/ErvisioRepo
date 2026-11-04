import { Link } from "@tanstack/react-router";
import { useRunners } from "../api/actions.ts";
import { useSession } from "../api/identity.ts";
import { useInbox, type NotificationThread } from "../api/notifications.ts";
import { useMyRepos } from "../api/repos.ts";
import { useMeta } from "../api/useMeta.ts";
import { useStorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Card, Icon, Page } from "../ui/index.ts";
import type { Section } from "../ui/shell/nav.tsx";
import { When } from "./code/common.tsx";
import { SubjectLink, subjectIcon } from "./notifications/NotificationsPage.tsx";

// Dashboard built from existing endpoints only. Each block hides itself or shows an empty note
// when its data is missing, so a failing endpoint never breaks the page.

const attentionReasons = new Set<NotificationThread["reason"]>(["assigned", "review_requested", "mentioned"]);
const subjectSection: Record<NotificationThread["subjectType"], Section> = { issue: "issue", pull_request: "pr", release: "pkg", commit: "code" };

export function Home() {
  const t = useT("home");
  const admin = useSession().data?.role === "admin";
  const today = new Date().toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
  return (
    <Page
      title={t("dash.title")}
      subtitle={today}
      aside={
        <>
          <RecentRepos />
          {admin && <InstanceHealth />}
        </>
      }
    >
      <Attention />
      <Activity />
    </Page>
  );
}

function Attention() {
  const t = useT("home");
  const tn = useT("notifications");
  const inbox = useInbox({ filter: "unread", page: 1 });
  if (inbox.isError) return null;
  const rows = (inbox.data?.items ?? []).filter((n) => attentionReasons.has(n.reason));
  return (
    <Card title={t("dash.attention")} actions={rows.length > 0 ? <span className="hm-n">{rows.length}</span> : undefined}>
      {inbox.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : rows.length === 0 ? (
        <p className="ui-note hm-pad">{t("dash.attentionEmpty")}</p>
      ) : (
        rows.map((n) => (
          <SubjectLink key={n.id} n={n} className={`hm-row sec-${subjectSection[n.subjectType]}`}>
            <span className="sb-kt"><Icon name={subjectIcon[n.subjectType]} /></span>
            <span className="hm-txt">
              <b>{n.title}</b>
              <small>{n.owner}/{n.repo}{n.number ? ` #${n.number}` : ""}</small>
            </span>
            <span className="hm-pill">{tn(`reason.${n.reason}`)}</span>
          </SubjectLink>
        ))
      )}
    </Card>
  );
}

function dayKey(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toDateString();
}

function Activity() {
  const t = useT("home");
  const inbox = useInbox({ filter: "all", page: 1 });
  if (inbox.isError) return null;
  const items = (inbox.data?.items ?? []).slice(0, 10);
  const now = new Date();
  const yesterday = new Date(now.getTime() - 86_400_000);
  const label = (key: string, iso: string) =>
    key === now.toDateString() ? t("dash.today") : key === yesterday.toDateString() ? t("dash.yesterday") : new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "long" });
  const groups: { key: string; label: string; rows: NotificationThread[] }[] = [];
  for (const n of items) {
    const key = dayKey(n.updatedAt);
    const last = groups[groups.length - 1];
    if (last && last.key === key) last.rows.push(n);
    else groups.push({ key, label: label(key, n.updatedAt), rows: [n] });
  }
  return (
    <Card title={t("dash.activity")} actions={items.length > 0 ? <Link to="/notifications" search={{ filter: "all" }} className="hm-link">{t("dash.showAll")}</Link> : undefined}>
      {inbox.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : items.length === 0 ? (
        <p className="ui-note hm-pad">{t("dash.activityEmpty")}</p>
      ) : (
        groups.map((g) => (
          <section key={g.key} aria-label={g.label}>
            <h4 className="hm-day">{g.label}</h4>
            {g.rows.map((n) => (
              <SubjectLink key={n.id} n={n} className={`hm-act sec-${subjectSection[n.subjectType]}`}>
                <Icon name={subjectIcon[n.subjectType]} />
                <p>
                  <b>{n.owner}/{n.repo}</b> {n.title}
                  {n.summary && <small>{n.summary}</small>}
                </p>
                <When iso={n.updatedAt} />
              </SubjectLink>
            ))}
          </section>
        ))
      )}
    </Card>
  );
}

function RecentRepos() {
  const t = useT("home");
  const repos = useMyRepos();
  if (repos.isError) return null;
  const all = repos.data ?? [];
  const rows = [...all].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)).slice(0, 5);
  return (
    <Card title={t("dash.repos")} actions={all.length > rows.length ? <Link to="/repos" className="hm-link">{t("dash.allRepos", { count: all.length })}</Link> : undefined}>
      {repos.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : rows.length === 0 ? (
        <p className="ui-note hm-pad">{t("dash.reposEmpty")}</p>
      ) : (
        rows.map((r) => (
          <Link key={r.fullName} to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="hm-row hm-row-sm sec-code">
            <span className="sb-kt"><Icon name={r.visibility === "public" ? "book" : "lock"} /></span>
            <span className="hm-txt">
              <b>{r.fullName}</b>
              <small><When iso={r.updatedAt} /></small>
            </span>
          </Link>
        ))
      )}
    </Card>
  );
}

function InstanceHealth() {
  const t = useT("home");
  const runners = useRunners({ kind: "instance" });
  const storage = useStorageStatus();
  const meta = useMeta();
  const online = runners.data?.filter((r) => r.online).length ?? 0;
  const warnings = storage.data?.warnings ?? [];
  return (
    <Card title={t("dash.health")} className="hm-health" actions={<Link to="/admin" className="hm-link">{t("dash.details")}</Link>}>
      <div className="hm-stat">
        {runners.data && (
          <Link to="/admin/runners">
            <b>{online}/{runners.data.length}</b>
            <small>{t("dash.runnersOnline")}</small>
          </Link>
        )}
        {meta.data && (
          <Link to="/admin">
            <b>{meta.data.version}</b>
            <small>{t("version")}</small>
          </Link>
        )}
      </div>
      {warnings.length > 0 && (
        <Link to="/admin/storage" className="hm-warn">
          <Icon name="alert" />
          <span>
            <b>{t("dash.storageWarnings", { count: warnings.length })}</b>
            {warnings[0]?.message}
          </span>
        </Link>
      )}
      {!runners.data && !meta.data && warnings.length === 0 && <p className="ui-note hm-pad">{t("dash.healthEmpty")}</p>}
    </Card>
  );
}
