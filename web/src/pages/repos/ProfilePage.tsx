import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useEditFile, useTree } from "../../api/code.ts";
import { useSession, useUserProfile, type UserProfile } from "../../api/identity.ts";
import { ApiError } from "../../api/http.ts";
import type { ActivityItem } from "../../api/personal.ts";
import { useContributions, useProfileActivity, useProfilePins, useUserStarred, type ContributionCalendar } from "../../api/profile.ts";
import { useCreateRepo, useOwnerRepos, useRepo, type Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { BotMark, Button, EmptyState, Icon, Page, type IconName } from "../../ui/index.ts";
import { formatDate, useNotify } from "../account/common.tsx";
import { When } from "../code/common.tsx";
import { Readme } from "../code/Readme.tsx";
import { popular, RepoCards, RepoRows } from "./RepoList.tsx";
import type { UseQueryResult } from "@tanstack/react-query";

export type ProfileTab = "repositories" | "stars";

/** Public profile of a user, GitHub style: avatar, details and organizations on the left; overview, repositories and stars on the right. Reachable signed out. */
export function ProfilePage({ username, tab }: { username: string; tab?: ProfileTab }) {
  const t = useT("repos");
  const tc = useT("common");
  const profile = useUserProfile(username);
  const repos = useOwnerRepos(username, "user");
  const starred = useUserStarred(username, profile.data?.kind === "user");
  const me = useSession().data?.username;

  if (profile.isPending) {
    return (
      <Page section="code" title={username}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (profile.isError) {
    const missing = profile.error instanceof ApiError && profile.error.status === 404;
    return (
      <Page section="code" title={username}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={missing ? t("profile.notFound") : profile.error.message}
            action={missing ? <Link to="/orgs/$org" params={{ org: username }} className="ui-btn ui-btn-secondary">{t("profile.tryOrg")}</Link> : <Button onClick={() => void profile.refetch()}>{tc("action.retry")}</Button>}
          />
        </div>
      </Page>
    );
  }
  const u = profile.data;
  const bot = u.kind === "bot";
  const own = me === u.username;
  const starredRepos: UseQueryResult<Repo[]> = { ...starred, data: starred.data?.map((s) => s.repo) } as UseQueryResult<Repo[]>;
  const tabs: { id: ProfileTab | undefined; icon: IconName; label: string; count?: number }[] = [
    { id: undefined, icon: "book", label: t("profile.tab.overview") },
    { id: "repositories", icon: "folder", label: t("profile.tab.repositories"), count: repos.data?.length },
    { id: "stars", icon: "star", label: t("profile.tab.stars"), count: starred.data?.length },
  ];
  return (
    <Page section="code" title={u.displayName || u.username} quiet>
      <div className="up">
        <ProfileCard user={u} own={own} />
        {bot ? (
          <div>
            <EmptyState icon="info" title={t("profile.botTitle")} description={t("profile.botHint")} />
          </div>
        ) : (
          <div className="up-body">
            <nav className="up-tabs" aria-label={t("profile.tabs")}>
              {tabs.map((x) => (
                <Link key={x.id ?? "overview"} to="/$owner" params={{ owner: u.username }} search={{ tab: x.id }} aria-current={tab === x.id ? "page" : undefined}>
                  <Icon name={x.icon} />
                  {x.label}
                  {x.count !== undefined && x.count > 0 && <span className="ui-n">{x.count}</span>}
                </Link>
              ))}
            </nav>
            {tab === "repositories" ? (
              <RepoRows query={repos} pin={own} />
            ) : tab === "stars" ? (
              <RepoRows query={starredRepos} showOwner pin={false} />
            ) : (
              <>
                <ProfileReadme username={u.username} displayName={u.displayName || u.username} own={own} />
                <Featured username={u.username} repos={repos.data ?? []} own={own} />
                <Contributions username={u.username} />
                <ProfileActivity username={u.username} />
              </>
            )}
          </div>
        )}
      </div>
    </Page>
  );
}

/** Left column: avatar, names, bio, details like GitHub (role, company, place, local time, links, email, joined) and organizations. */
function ProfileCard({ user: u, own }: { user: UserProfile; own: boolean }) {
  const t = useT("repos");
  const [broken, setBroken] = useState("");
  const host = (url: string) => {
    try {
      const x = new URL(url);
      return (x.host + x.pathname).replace(/\/$/, "");
    } catch {
      return url;
    }
  };
  return (
    <aside className="up-card">
      {u.avatarUrl && broken !== u.avatarUrl ? (
        <img className="up-av" src={u.avatarUrl} alt="" onError={() => setBroken(u.avatarUrl)} />
      ) : (
        <span className="up-av" aria-hidden="true">{u.username.slice(0, 1).toUpperCase()}</span>
      )}
      <div className="up-names">
        <p className="up-name">{u.displayName || u.username}</p>
        <p className="up-login">{u.username} <BotMark login={u.username} /></p>
      </div>
      {u.kind === "bot" && u.description && <p className="up-bio">{u.description}</p>}
      {u.bio && <p className="up-bio">{u.bio}</p>}
      {own && <Link to="/settings" className="ui-btn ui-btn-secondary up-edit">{t("profile.edit")}</Link>}
      <ul className="up-ln">
        {u.instanceAdmin && <li><Icon name="shield" />{t("profile.instanceAdmin")}</li>}
        {u.company && <li><Icon name="building" /><span>{u.company}</span></li>}
        {u.location && <li><Icon name="place" /><span>{u.location}</span></li>}
        {u.timezone && <LocalTime zone={u.timezone} />}
        {u.website && <li><Icon name="link" /><a href={u.website} rel="nofollow noopener noreferrer" target="_blank">{host(u.website)}</a></li>}
        {u.publicEmail && <li><Icon name="mail" /><a href={`mailto:${u.publicEmail}`}>{u.publicEmail}</a></li>}
        {u.socialLinks?.map((l) => <li key={l}><Icon name="globe" /><a href={l} rel="nofollow noopener noreferrer me" target="_blank">{host(l)}</a></li>)}
        <li><Icon name="calendar" />{t("profile.joined", { date: formatDate(u.createdAt) })}</li>
      </ul>
      {u.orgs && u.orgs.length > 0 && (
        <section className="up-orgs" aria-labelledby="up-orgs-h">
          <h2 id="up-orgs-h">{t("profile.orgs")}</h2>
          <ul>
            {u.orgs.map((o) => (
              <li key={o.name}>
                <Link to="/orgs/$org" params={{ org: o.name }} className="up-org" title={o.displayName || o.name} aria-label={o.displayName || o.name}>
                  {(o.displayName || o.name).slice(0, 1).toUpperCase()}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </aside>
  );
}

/** The user's local time in their chosen zone, refreshed every minute. */
function LocalTime({ zone }: { zone: string }) {
  const t = useT("repos");
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(id);
  }, []);
  let text: string;
  try {
    const time = new Intl.DateTimeFormat(undefined, { timeZone: zone, hour: "2-digit", minute: "2-digit" }).format(now);
    const offset = new Intl.DateTimeFormat("en", { timeZone: zone, timeZoneName: "shortOffset" }).formatToParts(now).find((p) => p.type === "timeZoneName")?.value ?? "";
    text = t("profile.localTime", { time, offset });
  } catch {
    return null; // a zone this browser does not know
  }
  return <li><Icon name="clock" /><span>{text}</span></li>;
}

/** Pinned repositories, or the popular ones when nothing visible is pinned. */
function Featured({ username, repos, own }: { username: string; repos: Repo[]; own: boolean }) {
  const t = useT("repos");
  const pins = useProfilePins(username);
  if (pins.isPending) return null;
  const pinned = pins.data ?? [];
  const list = pinned.length > 0 ? pinned.slice(0, 6) : popular(repos, 6);
  if (list.length === 0) return null;
  return (
    <section className="up-sec" aria-labelledby="up-feat-h">
      <div className="ui-sech">
        <h2 id="up-feat-h">{pinned.length > 0 ? t("profile.pinned") : t("profile.popular")}</h2>
        {own && <Link to="/$owner" params={{ owner: username }} search={{ tab: "repositories" }} className="up-more">{t("profile.customize")}</Link>}
      </div>
      <RepoCards repos={list} showOwner={pinned.some((r) => r.owner !== username)} />
    </section>
  );
}

const DAY = 86_400_000;

/** Parses YYYY-MM-DD as a UTC day. */
const utcDay = (s: string) => Date.parse(`${s}T00:00:00Z`);

/** Contribution calendar: one cell per day, columns are weeks starting on Sunday, darker green for busier days. */
function Contributions({ username }: { username: string }) {
  const t = useT("repos");
  const [year, setYear] = useState<number | undefined>(undefined);
  const last = useContributions(username);
  const picked = useContributions(username, year);
  const cal = (year === undefined ? last : picked).data;
  const years = (last.data?.years ?? []).slice(0, 5);
  if (last.isError) return null;
  return (
    <section className="up-sec up-heat" aria-labelledby="up-heat-h">
      <div className="up-heat-h">
        <h2 id="up-heat-h">{cal ? t(year === undefined ? "contrib.totalLast" : "contrib.totalYear", { count: cal.total, year: year ?? "" }) : t("contrib.title")}</h2>
        {years.length > 0 && (
          <div className="ui-seg" role="group" aria-label={t("contrib.years")}>
            <button type="button" className="ui-segi" aria-pressed={year === undefined} onClick={() => setYear(undefined)}>{t("contrib.last")}</button>
            {years.map((y) => (
              <button key={y} type="button" className="ui-segi" aria-pressed={year === y} onClick={() => setYear(y)}>{y}</button>
            ))}
          </div>
        )}
      </div>
      {cal ? <HeatGrid cal={cal} /> : <p className="ui-note" role="status">{t("contrib.loading")}</p>}
    </section>
  );
}

function HeatGrid({ cal }: { cal: ContributionCalendar }) {
  const t = useT("repos");
  const from = utcDay(cal.from);
  const to = utcDay(cal.to);
  const counts = new Map(cal.days.map((d) => [d.date, d.count]));
  const max = Math.max(0, ...cal.days.map((d) => d.count));
  const lead = new Date(from).getUTCDay();
  const total = lead + Math.round((to - from) / DAY) + 1;
  const weeks = Math.ceil(total / 7);
  const dateFmt = new Intl.DateTimeFormat(undefined, { timeZone: "UTC", day: "numeric", month: "short", year: "numeric" });
  const monthFmt = new Intl.DateTimeFormat(undefined, { timeZone: "UTC", month: "short" });
  const cells = [];
  const months: { col: number; label: string }[] = [];
  for (let i = 0; i < weeks * 7; i++) {
    const at = from + (i - lead) * DAY;
    if (i < lead || at > to) {
      cells.push(<i key={i} className="up-hx" />);
      continue;
    }
    const d = new Date(at);
    const iso = d.toISOString().slice(0, 10);
    const n = counts.get(iso) ?? 0;
    const level = n === 0 ? 0 : Math.min(4, Math.ceil((n / max) * 4));
    cells.push(<i key={i} data-l={level} title={t("contrib.day", { count: n, date: dateFmt.format(d) })} />);
  }
  // A month label sits over the first week that starts in it; a stub month at the start is dropped.
  let prev = -1;
  for (let c = 0; c < weeks; c++) {
    const d = new Date(Math.max(from, from + (c * 7 - lead) * DAY));
    if (d.getUTCMonth() !== prev) months.push({ col: c, label: monthFmt.format(d) });
    prev = d.getUTCMonth();
  }
  if (months.length > 1 && months[1]!.col - months[0]!.col < 3) months.shift();
  return (
    <div className="up-heat-in">
      <div className="up-heat-scroll">
        <div className="up-heat-m" style={{ gridTemplateColumns: `repeat(${weeks}, minmax(0, 1fr))` }} aria-hidden="true">
          {months.map((m) => <span key={m.col} style={{ gridColumn: `${m.col + 1} / span 3` }}>{m.label}</span>)}
        </div>
        <div className="up-heat-g" style={{ gridTemplateColumns: `repeat(${weeks}, minmax(0, 1fr))` }} role="img" aria-label={t("contrib.summary", { count: cal.total, from: dateFmt.format(new Date(from)), to: dateFmt.format(new Date(to)) })}>
          {cells}
        </div>
      </div>
      <p className="up-heat-l" aria-hidden="true">
        {t("contrib.less")}
        {[0, 1, 2, 3, 4].map((l) => <i key={l} data-l={l} />)}
        {t("contrib.more")}
      </p>
    </div>
  );
}

const activityIcon: Record<ActivityItem["kind"], IconName> = { push: "commit", issue: "issue", pull_request: "pr", release: "tag", comment: "comment" };

function activityLink(a: ActivityItem) {
  const params = { owner: a.owner, name: a.repo };
  if (a.number > 0) {
    return a.kind === "pull_request"
      ? { to: "/$owner/$name/pulls/$number" as const, params: { ...params, number: String(a.number) } }
      : { to: "/$owner/$name/issues/$number" as const, params: { ...params, number: String(a.number) } };
  }
  return a.kind === "release" ? { to: "/$owner/$name/releases" as const, params } : { to: "/$owner/$name/commits" as const, params };
}

/** Recent activity of the user in the repositories the visitor can read. */
function ProfileActivity({ username }: { username: string }) {
  const t = useT("repos");
  const feed = useProfileActivity(username, 10);
  if (feed.isError) return null;
  const items = feed.data ?? [];
  const actionText = (a: ActivityItem) => {
    const key = `act.${a.kind}.${a.action}`;
    const text = t(key);
    return text === key ? a.action : text;
  };
  return (
    <section className="up-sec" aria-labelledby="up-act-h">
      <div className="ui-sech">
        <h2 id="up-act-h">{t("profile.activity")}</h2>
      </div>
      {feed.isPending ? (
        <p className="ui-note" role="status">{t("contrib.loading")}</p>
      ) : items.length === 0 ? (
        <p className="ui-note">{t("profile.activityEmpty")}</p>
      ) : (
        <ul className="up-feed">
          {items.map((a) => (
            <li key={a.id}>
              <span className="up-feed-i"><Icon name={activityIcon[a.kind]} /></span>
              <span>
                {actionText(a)} <Link to="/$owner/$name" params={{ owner: a.owner, name: a.repo }}><b>{a.fullName}</b></Link>
                {a.title && (
                  <Link {...activityLink(a)} className="up-feed-t">
                    {a.number > 0 && `#${a.number} `}
                    {a.title}
                  </Link>
                )}
              </span>
              <span className="ui-tm"><When iso={a.createdAt} /></span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

const PROFILE_README = "README.md";

/**
 * Profile README (GitHub special repository): the README of the public repository
 * username/username, above the repositories. The owner gets an edit pencil, or a call to
 * create it when it is missing; other visitors see nothing in that case.
 */
function ProfileReadme({ username, displayName, own }: { username: string; displayName: string; own: boolean }) {
  const t = useT("repos");
  const repo = useRepo(username, username);
  const r = repo.data?.visibility === "public" ? repo.data : undefined;
  const tree = useTree(username, username, r?.defaultBranch ?? "", "", r !== undefined);
  if (repo.isPending || (r && tree.isPending && tree.fetchStatus !== "idle")) return null;
  const missing = repo.isError && repo.error instanceof ApiError && repo.error.status === 404;
  const readme = tree.data?.readme;
  if (r && readme) {
    return (
      <div className="up-readme">
        <Readme
          owner={username}
          name={username}
          refName={r.defaultBranch}
          path={readme}
          label={`${username} / ${readme}`}
          action={
            own && (
              <Link
                to="/$owner/$name/edit"
                params={{ owner: username, name: username }}
                search={{ ref: r.defaultBranch, path: readme }}
                className="ui-btn ui-btn-ghost ui-btn-sm rp-ib"
                aria-label={t("profile.readmeEdit")}
                title={t("profile.readmeEdit")}
              >
                <Icon name="edit" />
              </Link>
            )
          }
        />
      </div>
    );
  }
  // Only the owner is invited, and only when the special repository is missing or public without a README.
  if (!own || !(missing || (r && !readme))) return null;
  return <ReadmeInvite username={username} displayName={displayName} exists={!missing} branch={r?.defaultBranch} />;
}

function ReadmeInvite({ username, displayName, exists, branch }: { username: string; displayName: string; exists: boolean; branch?: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRepo();
  const edit = useEditFile(username, username);
  const busy = create.isPending || edit.isPending;
  async function start() {
    try {
      const ref = exists ? branch : (await create.mutateAsync({ name: username, visibility: "public" })).defaultBranch;
      await edit.mutateAsync({ op: "create", path: PROFILE_README, content: t("profile.readmeSeed", { name: displayName }), encoding: "utf-8", branch: ref });
      await navigate({ to: "/$owner/$name/edit", params: { owner: username, name: username }, search: { ref, path: PROFILE_README } });
    } catch (e) {
      notify.fail(e);
    }
  }
  return (
    <div className="up-invite">
      <Icon name="book" />
      <div>
        <b>{t("profile.readmeTitle")}</b>
        <p>{t("profile.readmeHint", { repo: `${username}/${username}` })}</p>
        <Button variant="primary" size="sm" disabled={busy} onClick={() => void start()}>
          {t("profile.readmeCreate")}
        </Button>
      </div>
    </div>
  );
}
