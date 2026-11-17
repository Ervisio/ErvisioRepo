import { Link } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { useRunners } from "../api/actions.ts";
import { useSession } from "../api/identity.ts";
import { useUserActivity, useUserIssues, useUserPins, useUserRuns, type ActivityItem, type UserActionsRun, type UserIssue } from "../api/personal.ts";
import { useMyRepos } from "../api/repos.ts";
import { useStorageStatus } from "../api/useStorage.ts";
import { useT } from "../i18n/useT.ts";
import { Badge, BotMark, Icon, Input, type IconName } from "../ui/index.ts";
import { formatBytes, When } from "./code/common.tsx";
import { CreateRepoDialog } from "./repos/RepoList.tsx";
import { Avatar } from "./work/common.tsx";

// Dashboard (round 017 C): four tiles that pick the queue below, the activity feed, and on the right the
// repositories and, for admins, the instance. Built from existing endpoints only; a block whose endpoint
// fails hides itself or shows a note, so one failing endpoint never breaks the page.

type Queue = "review" | "assigned" | "mine" | "failed";
const queues: Queue[] = ["review", "assigned", "mine", "failed"];
const queueSize = 10;
const day = 86_400_000;

export function Home() {
  const t = useT("home");
  const admin = useSession().data?.role === "admin";
  return (
    <div className="page db">
      <h1 className="sr-only">{t("dash.title")}</h1>
      <Board />
      <div className="db-g">
        <div className="db-main">
          <Activity />
        </div>
        <aside className="db-side" aria-label={t("dash.side")}>
          <Repos />
          {admin && <Instance />}
        </aside>
      </div>
    </div>
  );
}

function issueLink(i: Pick<UserIssue, "owner" | "repo" | "number" | "kind">) {
  return i.kind === "pull"
    ? { to: "/$owner/$name/pulls/$number" as const, params: { owner: i.owner, name: i.repo, number: String(i.number) } }
    : { to: "/$owner/$name/issues/$number" as const, params: { owner: i.owner, name: i.repo, number: String(i.number) } };
}

const queueIcon: Record<Queue, { icon: IconName; cls: string }> = {
  review: { icon: "pr", cls: "ui-st-merged" },
  assigned: { icon: "issue", cls: "ui-st-open" },
  mine: { icon: "pr", cls: "ui-st-open" },
  failed: { icon: "play", cls: "ui-st-fail" },
};
const queueChip: Record<Queue, string> = { review: "ui-chip-rev", assigned: "ui-chip-ok", mine: "ui-chip-acc", failed: "ui-chip-err" };

/** Tiles with the four queues and the list of the picked one. */
function Board() {
  const t = useT("home");
  const review = useUserIssues({ filter: "review_requested", state: "open", perPage: queueSize });
  const assigned = useUserIssues({ filter: "assigned", type: "issue", state: "open", perPage: queueSize });
  const mine = useUserIssues({ filter: "created", type: "pr", state: "open", perPage: queueSize });
  const failed = useUserRuns({ status: "failure", perPage: queueSize });
  const [picked, setPicked] = useState<Queue | null>(null);
  const issues: Record<Exclude<Queue, "failed">, typeof review> = { review, assigned, mine };
  const count = (q: Queue) => (q === "failed" ? failed.data?.totalCount : issues[q].data?.totalCount);
  const active = picked ?? queues.find((q) => (count(q) ?? 0) > 0) ?? "review";
  const query = active === "failed" ? failed : issues[active];

  const context = (q: Queue): ReactNode => {
    if (q === "failed") {
      const runs = failed.data?.runs ?? [];
      if (!runs.length) return t("dash.tile.none");
      const recent = runs.filter((r) => Date.now() - new Date(r.updatedAt).getTime() < day).length;
      return t("dash.tile.lastDay", { count: recent });
    }
    const data = issues[q].data;
    const first = data?.items[0];
    if (!data || !first) return t("dash.tile.none");
    if (q === "assigned" && data.totalCount <= data.items.length) {
      return t("dash.tile.repos", { count: new Set(data.items.map((i) => i.fullName)).size });
    }
    return <>{t("dash.tile.latest")} <When iso={first.updatedAt} /></>;
  };

  return (
    <>
      <div className="db-tiles" role="group" aria-label={t("dash.attention")}>
        {queues.map((q) => (
          <button key={q} type="button" className="db-t" aria-pressed={active === q} onClick={() => setPicked(q)}>
            <span className="db-tl">
              <Icon name={queueIcon[q].icon} className={`ui-icon ${queueIcon[q].cls}`} />
              {t(`dash.queue.${q}`)}
            </span>
            <b>{count(q) ?? "–"}</b>
            <span className="db-ts">{context(q)}</span>
          </button>
        ))}
      </div>
      <section className="db-q" aria-labelledby="db-q-h">
        <div className="ui-sech">
          <h2 id="db-q-h">{t(`dash.queue.${active}`)}</h2>
          {count(active) ? <span className="ui-n">{count(active)}</span> : null}
        </div>
        <div className="ui-box">
          <div className="ui-in">
            {query.isPending ? (
              <p className="db-note" role="status">{t("dash.loading")}</p>
            ) : query.isError ? (
              <p className="db-note" role="alert">{t("dash.loadError")}</p>
            ) : active === "failed" ? (
              failed.data?.runs.length ? failed.data.runs.map((r) => <RunRow key={r.id} r={r} />) : <p className="db-note">{t(`dash.empty.${active}`)}</p>
            ) : issues[active].data?.items.length ? (
              issues[active].data.items.map((i) => <IssueRow key={`${i.fullName}#${i.number}`} i={i} q={active} />)
            ) : (
              <p className="db-note">{t(`dash.empty.${active}`)}</p>
            )}
          </div>
        </div>
      </section>
    </>
  );
}

function IssueRow({ i, q }: { i: UserIssue; q: Exclude<Queue, "failed"> }) {
  const t = useT("home");
  return (
    <Link {...issueLink(i)} className="db-r">
      <Icon name={i.kind === "pull" ? "pr" : "issue"} className={`ui-icon ${queueIcon[q].cls}`} />
      <span className="db-rm">
        <span className="db-ttl">{i.title}</span>
        <span className="db-rp">
          <b>{i.fullName}</b> #{i.number}{i.author ? ` · ${i.author}` : ""}
        </span>
      </span>
      <span className="db-rx"><span className={`ui-chip ${queueChip[q]}`}>{t(`dash.chip.${q}`)}</span></span>
      <span className="ui-tm"><When iso={i.updatedAt} /></span>
    </Link>
  );
}

function RunRow({ r }: { r: UserActionsRun }) {
  const t = useT("home");
  return (
    <a href={r.url} className="db-r">
      <Icon name="play" className="ui-icon ui-st-fail" />
      <span className="db-rm">
        <span className="db-ttl">{r.title || r.workflowName}</span>
        <span className="db-rp">
          <b>{r.fullName}</b> · {r.workflowName} #{r.runNumber} · {r.branch}
        </span>
      </span>
      <span className="db-rx"><span className="ui-chip ui-chip-err">{t("dash.chip.failed")}</span></span>
      <span className="ui-tm"><When iso={r.updatedAt} /></span>
    </a>
  );
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
  return (
    <section className="db-act" aria-labelledby="db-act-h">
      <div className="ui-sech">
        <h2 id="db-act-h">{t("dash.activity")}</h2>
      </div>
      {feed.isPending ? (
        <p className="db-note" role="status">{t("dash.loading")}</p>
      ) : items.length === 0 ? (
        <p className="db-note">{t("dash.activityEmpty")}</p>
      ) : (
        <ul className="db-evs">
          {items.map((a) => (
            <li key={a.id} className="db-ev">
              {/* No actor means the server itself did it: the GitHub sync or an import. */}
              <Avatar user={a.actor || "ErvisioSync"} size={24} />
              <span>
                {a.actor ? <><b>{a.actor}</b> <BotMark login={a.actor} /></> : <><b>ErvisioSync</b> <Badge>{t("dash.act.bot")}</Badge></>} {actionText(a)} <b>{a.fullName}</b>
                {a.title && (
                  <Link {...activityLink(a)} className="db-evt">
                    {a.number > 0 && `#${a.number} `}
                    {a.title}
                  </Link>
                )}
                <span className="ui-tm"><When iso={a.createdAt} /></span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
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
    <section aria-labelledby="db-repos-h">
      <div className="ui-sech">
        <h2 id="db-repos-h">{t("dash.repos")}</h2>
        <button type="button" className="db-link" onClick={() => setCreating(true)}>{t("dash.new")}</button>
        <CreateRepoDialog open={creating} onOpenChange={setCreating} />
      </div>
      {repos.isPending ? (
        <p className="db-note" role="status">{t("dash.loading")}</p>
      ) : all.length === 0 ? (
        <p className="db-note">{t("dash.reposEmpty")}</p>
      ) : (
        <>
          <Input className="db-find" type="search" label={t("dash.find")} hideLabel placeholder={t("dash.find")} autoComplete="off" value={query} onChange={(e) => setQuery(e.target.value)} />
          {rows.length === 0 ? (
            <p className="db-note">{t("dash.noMatch")}</p>
          ) : (
            <ul className="db-repos" data-filtered={q ? "" : undefined}>
              {rows.map((r) => (
                <li key={r.fullName}>
                  <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="db-repo">
                    <span className={r.ownerKind === "org" ? "db-ra org" : "db-ra"}><Avatar user={r.owner} size={20} /></span>
                    <b>{r.fullName}</b>
                    {r.visibility !== "public" && <Icon name="lock" />}
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
          {all.length > rows.length && <Link to="/repos" className="db-all">{t("dash.allRepos", { count: all.length })}</Link>}
        </>
      )}
    </section>
  );
}

/** Instance health for admins: runners, running jobs and storage. */
function Instance() {
  const t = useT("home");
  const runners = useRunners({ kind: "instance" });
  const storage = useStorageStatus();
  const list = runners.data ?? [];
  const online = list.filter((r) => r.online).length;
  const running = list.reduce((n, r) => n + r.running, 0);
  const warnings = storage.data?.warnings.length ?? 0;
  const used = storage.data?.policies.reduce((n, p) => n + p.bytes, 0);
  return (
    <section aria-labelledby="db-inst-h">
      <div className="ui-sech">
        <h2 id="db-inst-h">{t("dash.instance")}</h2>
        <Link to="/admin">{t("dash.admin")}</Link>
      </div>
      <ul className="db-ck">
        {runners.data && (
          <li>
            <Icon name={online > 0 ? "check" : "alert"} className={`ui-icon ${online > 0 ? "ui-st-open" : "ui-st-warn"}`} />
            {t("dash.runnersOnline")}
            <span className="ui-tm">{t("dash.ofTotal", { count: online, total: list.length })}</span>
          </li>
        )}
        {runners.data && (
          <li>
            <Icon name="play" className="ui-icon ui-st-info" />
            {t("dash.jobsRunning")}
            <span className="ui-tm">{running}</span>
          </li>
        )}
        {storage.data && (
          <li>
            <Icon name="db" className="ui-icon" />
            {t("dash.storage")}
            {warnings > 0 && <span className="ui-chip ui-chip-warn">{t("dash.storageWarnings", { count: warnings })}</span>}
            <span className="ui-tm">{used !== undefined ? t("dash.used", { size: formatBytes(used) }) : ""}</span>
          </li>
        )}
      </ul>
    </section>
  );
}
