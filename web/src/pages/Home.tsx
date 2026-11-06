import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useRunners } from "../api/actions.ts";
import { useSession } from "../api/identity.ts";
import { useUserActivity, useUserIssues, useUserPins, useUserRuns, type ActivityItem, type UserIssue, type UserIssueFilter } from "../api/personal.ts";
import { useMyRepos } from "../api/repos.ts";
import { useMeta } from "../api/useMeta.ts";
import { useStorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Card, Icon, Page, Tabs, TabsList, TabsTrigger, type IconName } from "../ui/index.ts";
import type { Section } from "../ui/shell/nav.tsx";
import { When } from "./code/common.tsx";

// Dashboard built from existing endpoints only. Each block hides itself or shows an empty note
// when its data is missing, so a failing endpoint never breaks the page.

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

type AttentionTab = Extract<UserIssueFilter, "assigned" | "review_requested"> | "failed";
const attentionTabs: AttentionTab[] = ["assigned", "review_requested", "failed"];
const sizeAttention = 6;

function issueLink(i: Pick<UserIssue, "owner" | "repo" | "number" | "kind">) {
  return i.kind === "pull"
    ? { to: "/$owner/$name/pulls/$number" as const, params: { owner: i.owner, name: i.repo, number: String(i.number) } }
    : { to: "/$owner/$name/issues/$number" as const, params: { owner: i.owner, name: i.repo, number: String(i.number) } };
}

function Attention() {
  const t = useT("home");
  const assigned = useUserIssues({ filter: "assigned", state: "open", perPage: sizeAttention });
  const reviews = useUserIssues({ filter: "review_requested", state: "open", perPage: sizeAttention });
  const failed = useUserRuns({ status: "failure", perPage: sizeAttention });
  const [picked, setPicked] = useState<AttentionTab | null>(null);
  const counts: Record<AttentionTab, number | undefined> = {
    assigned: assigned.data?.totalCount,
    review_requested: reviews.data?.totalCount,
    failed: failed.data?.totalCount,
  };
  if (assigned.isError && reviews.isError && failed.isError) return null;
  const active = picked ?? attentionTabs.find((k) => (counts[k] ?? 0) > 0) ?? "assigned";
  const total = attentionTabs.reduce((n, k) => n + (counts[k] ?? 0), 0);
  const query = active === "assigned" ? assigned : active === "review_requested" ? reviews : failed;
  const issues = active === "assigned" ? assigned.data?.items : active === "review_requested" ? reviews.data?.items : undefined;
  const runs = active === "failed" ? failed.data?.runs : undefined;
  const empty = !(issues?.length || runs?.length);
  return (
    <Card title={t("dash.attention")} actions={total > 0 ? <span className="hm-n">{total}</span> : undefined}>
      <Tabs className="hm-pad" value={active} onValueChange={(v) => setPicked(attentionTabs.find((k) => k === v) ?? null)}>
        <TabsList aria-label={t("dash.attention")}>
          {attentionTabs.map((k) => (
            <TabsTrigger key={k} value={k}>{t(`dash.tab.${k}`)}{counts[k] ? ` ${counts[k]}` : ""}</TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      {query.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : query.isError ? (
        <p className="ui-note hm-pad" role="alert">{t("dash.loadError")}</p>
      ) : empty ? (
        <p className="ui-note hm-pad">{t(`dash.empty.${active}`)}</p>
      ) : (
        <>
          {issues?.map((i) => (
            <Link key={`${i.fullName}#${i.number}`} {...issueLink(i)} className={`hm-row sec-${i.kind === "pull" ? "pr" : "issue"}`}>
              <span className="sb-kt"><Icon name={i.kind === "pull" ? "pr" : "issue"} /></span>
              <span className="hm-txt">
                <b>{i.title}</b>
                <small>{i.fullName} #{i.number}</small>
              </span>
              <When iso={i.updatedAt} />
            </Link>
          ))}
          {runs?.map((r) => (
            <a key={r.id} href={r.url} className="hm-row sec-act">
              <span className="sb-kt"><Icon name="play" /></span>
              <span className="hm-txt">
                <b>{r.title || r.workflowName}</b>
                <small>{r.fullName} · {r.workflowName} · {r.branch}</small>
              </span>
              <When iso={r.updatedAt} />
            </a>
          ))}
        </>
      )}
    </Card>
  );
}

function dayKey(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toDateString();
}

const activityIcon: Record<ActivityItem["kind"], IconName> = { push: "commit", issue: "issue", pull_request: "pr", release: "tag", comment: "issue" };
const activitySection: Record<ActivityItem["kind"], Section> = { push: "code", issue: "issue", pull_request: "pr", release: "pkg", comment: "issue" };

function activityLink(a: ActivityItem) {
  const params = { owner: a.owner, name: a.repo };
  if (a.number > 0) {
    return a.kind === "pull_request"
      ? { to: "/$owner/$name/pulls/$number" as const, params: { ...params, number: String(a.number) } }
      : { to: "/$owner/$name/issues/$number" as const, params: { ...params, number: String(a.number) } };
  }
  return a.kind === "release" ? { to: "/$owner/$name/releases" as const, params } : { to: "/$owner/$name/commits" as const, params };
}

function Activity() {
  const t = useT("home");
  const actionText = (a: ActivityItem) => {
    const key = `dash.act.${a.kind}.${a.action}`;
    const text = t(key);
    return text === key ? a.action : text;
  };
  const feed = useUserActivity({ offset: 0, limit: 10 });
  if (feed.isError) return null;
  const items = feed.data ?? [];
  const now = new Date();
  const yesterday = new Date(now.getTime() - 86_400_000);
  const label = (key: string, iso: string) =>
    key === now.toDateString() ? t("dash.today") : key === yesterday.toDateString() ? t("dash.yesterday") : new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "long" });
  const groups: { key: string; label: string; rows: ActivityItem[] }[] = [];
  for (const a of items) {
    const key = dayKey(a.createdAt);
    const last = groups[groups.length - 1];
    if (last && last.key === key) last.rows.push(a);
    else groups.push({ key, label: label(key, a.createdAt), rows: [a] });
  }
  return (
    <Card title={t("dash.activity")}>
      {feed.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : items.length === 0 ? (
        <p className="ui-note hm-pad">{t("dash.activityEmpty")}</p>
      ) : (
        groups.map((g) => (
          <section key={g.key} aria-label={g.label}>
            <h4 className="hm-day">{g.label}</h4>
            {g.rows.map((a) => (
              <Link key={a.id} {...activityLink(a)} className={`hm-act sec-${activitySection[a.kind]}`}>
                <Icon name={activityIcon[a.kind]} />
                <p>
                  <b>{a.actor || t("dash.act.someone")}</b> {actionText(a)} <b>{a.fullName}</b>
                  {a.title && <small>{a.number > 0 ? `#${a.number} ` : ""}{a.title}</small>}
                </p>
                <When iso={a.createdAt} />
              </Link>
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
  const pins = useUserPins();
  if (repos.isError) return null;
  const all = repos.data ?? [];
  const pinned = pins.data ?? [];
  const pinnedNames = new Set(pinned.map((r) => r.fullName));
  const recent = [...all].filter((r) => !pinnedNames.has(r.fullName)).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  const rows = [...pinned, ...recent].slice(0, Math.max(5, pinned.length));
  return (
    <Card title={t("dash.repos")} actions={all.length > rows.length ? <Link to="/repos" className="hm-link">{t("dash.allRepos", { count: all.length })}</Link> : undefined}>
      {repos.isPending ? (
        <p className="ui-note hm-pad" role="status">{t("dash.loading")}</p>
      ) : rows.length === 0 ? (
        <p className="ui-note hm-pad">{t("dash.reposEmpty")}</p>
      ) : (
        rows.map((r) => (
          <Link key={r.fullName} to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="hm-row hm-row-sm sec-code">
            <span className="sb-kt"><Icon name={pinnedNames.has(r.fullName) ? "star" : r.visibility === "public" ? "book" : "lock"} /></span>
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
