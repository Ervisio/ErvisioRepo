import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useRunners } from "../api/actions.ts";
import { useSession } from "../api/identity.ts";
import { useUserActivity, useUserIssues, useUserPins, useUserRuns, type ActivityItem, type UserIssue, type UserIssueFilter } from "../api/personal.ts";
import { useMyRepos } from "../api/repos.ts";
import { useStorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Badge, BotMark, Button, Icon, Input, Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/index.ts";
import { When } from "./code/common.tsx";
import { CreateRepoDialog } from "./repos/RepoList.tsx";

// Dashboard built from existing endpoints only. Each block hides itself or shows an empty note
// when its data is missing, so a failing endpoint never breaks the page.

export function Home() {
  const t = useT("home");
  const admin = useSession().data?.role === "admin";
  // page-body-aside is what widens .tb-main in the console shell; hm-grid reshapes it.
  return (
    <div className="page sec-code hm">
      <h1 className="sr-only">{t("dash.title")}</h1>
      <div className="page-body page-body-aside hm-grid">
        <aside className="hm-side" aria-label={t("dash.repos")}>
          <Repos />
        </aside>
        <div className="hm-main">
          {admin && <AdminWarning />}
          <Attention />
          <Activity />
        </div>
      </div>
    </div>
  );
}

const maxRepos = 7;

function Repos() {
  const t = useT("home");
  const repos = useMyRepos();
  const pins = useUserPins();
  const [creating, setCreating] = useState(false);
  const [query, setQuery] = useState("");
  if (repos.isError) return null;
  const all = repos.data ?? [];
  const pinned = pins.data ?? [];
  const pinnedNames = new Set(pinned.map((r) => r.fullName));
  const recent = all.filter((r) => !pinnedNames.has(r.fullName)).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
  const q = query.trim().toLowerCase();
  const matches = [...pinned, ...recent].filter((r) => r.fullName.toLowerCase().includes(q));
  const rows = matches.slice(0, maxRepos);
  return (
    <>
      <div className="hm-head">
        <h2>{t("dash.repos")}</h2>
        <Button variant="primary" size="sm" icon="plus" className="hm-new" onClick={() => setCreating(true)}>{t("dash.new")}</Button>
        <CreateRepoDialog open={creating} onOpenChange={setCreating} />
      </div>
      {repos.isPending ? (
        <p className="ui-note" role="status">{t("dash.loading")}</p>
      ) : all.length === 0 ? (
        <p className="hm-note">{t("dash.reposEmpty")}</p>
      ) : (
        <>
          <Input className="hm-find" type="search" label={t("dash.find")} hideLabel placeholder={t("dash.find")} autoComplete="off" value={query} onChange={(e) => setQuery(e.target.value)} />
          {rows.length === 0 ? (
            <p className="hm-note">{t("dash.noMatch")}</p>
          ) : (
            <ul className="hm-repos" data-filtered={q ? "" : undefined}>
              {rows.map((r) => (
                <li key={r.fullName}>
                  <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="hm-repo">
                    <Icon name={r.visibility === "public" ? "book" : "lock"} />
                    <span>{r.owner}/<b>{r.name}</b></span>
                    {pinnedNames.has(r.fullName) && (
                      <>
                        <Icon name="star" />
                        <span className="sr-only">{t("dash.pinned")}</span>
                      </>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {all.length > rows.length && <Link to="/repos" className="hm-all">{t("dash.allRepos", { count: all.length })}</Link>}
        </>
      )}
    </>
  );
}

function AdminWarning() {
  const t = useT("home");
  const runners = useRunners({ kind: "instance" });
  const storage = useStorageStatus();
  const warnings = storage.data?.warnings ?? [];
  const problems = [
    ...(warnings.length > 0 ? [t("dash.storageWarnings", { count: warnings.length })] : []),
    ...(runners.data && !runners.data.some((r) => r.online) ? [t("dash.noRunner")] : []),
  ];
  if (problems.length === 0) return null;
  return (
    <Link to="/admin" className="hm-warn">
      <Icon name="alert" />
      <span>{problems.join(" · ")}</span>
      <span className="hm-warn-go">{t("dash.details")}</span>
    </Link>
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
    <Tabs className="hm-wait" value={active} onValueChange={(v) => setPicked(attentionTabs.find((k) => k === v) ?? null)}>
      <div className="hm-top">
        <h2>{t("dash.attention")}</h2>
        <TabsList className="hm-tabs" aria-label={t("dash.attention")}>
          {attentionTabs.map((k) => (
            <TabsTrigger key={k} className="hm-tab" value={k}>
              {t(`dash.tab.${k}`)}
              {counts[k] ? <i>{counts[k]}</i> : null}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      <TabsContent value={active} className="hm-box">
        {query.isPending ? (
          <p className="hm-empty" role="status">{t("dash.loading")}</p>
        ) : query.isError ? (
          <p className="hm-empty" role="alert">{t("dash.loadError")}</p>
        ) : empty ? (
          total === 0 ? (
            <div className="hm-empty">
              <p>{t("dash.waitingEmpty")}</p>
              <p>{t("dash.waitingHint")}</p>
            </div>
          ) : (
            <p className="hm-empty">{t(`dash.empty.${active}`)}</p>
          )
        ) : (
          <>
            {issues?.map((i) => (
              <Link key={`${i.fullName}#${i.number}`} {...issueLink(i)} className={`hm-row sec-${i.kind === "pull" ? "pr" : "issue"}`}>
                <Icon name={i.kind === "pull" ? "pr" : "issue"} />
                <span className="hm-body">
                  <b>{i.title}</b>
                  <small>{i.fullName} #{i.number}</small>
                </span>
                <When iso={i.updatedAt} />
              </Link>
            ))}
            {runs?.map((r) => (
              <a key={r.id} href={r.url} className="hm-row hm-fail">
                <Icon name="x" />
                <span className="hm-body">
                  <b>{r.title || r.workflowName}</b>
                  <small>{r.fullName} · {r.workflowName} · {r.branch}</small>
                </span>
                <When iso={r.updatedAt} />
              </a>
            ))}
          </>
        )}
      </TabsContent>
    </Tabs>
  );
}

function dayKey(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toDateString();
}

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
    <section className="hm-act" aria-labelledby="hm-act-h">
      <h2 id="hm-act-h">{t("dash.activity")}</h2>
      {feed.isPending ? (
        <p className="hm-note" role="status">{t("dash.loading")}</p>
      ) : items.length === 0 ? (
        <p className="hm-note">{t("dash.activityEmpty")}</p>
      ) : (
        groups.map((g) => (
          <section key={g.key} aria-label={g.label}>
            <h3 className="hm-day">{g.label}</h3>
            {g.rows.map((a) => (
              // No actor means the server itself did it: the GitHub sync or an import.
              <Link key={a.id} {...activityLink(a)} className="hm-line">
                <span>
                  {a.actor ? <><b>{a.actor}</b> <BotMark login={a.actor} /></> : <><b>ErvisioSync</b> <Badge>{t("dash.act.bot")}</Badge></>} {actionText(a)} <b>{a.fullName}</b>
                </span>
                <When iso={a.createdAt} />
              </Link>
            ))}
          </section>
        ))
      )}
    </section>
  );
}
