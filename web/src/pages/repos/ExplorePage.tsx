import { Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { request } from "../../api/http.ts";
import { useMyOrgs, useSession, type UserSummary } from "../../api/identity.ts";
import { useExploreRepos } from "../../api/personal.ts";
import { useWeeklyActivity } from "../../api/profile.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Input, Page, Select } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Pager } from "../code/common.tsx";
import { RepoRow } from "./RepoList.tsx";

export type ExploreView = "orgs" | "users";
export type ExploreSearch = { q?: string; sort?: "name" | "stars"; view?: ExploreView; page?: number };

const PER_PAGE = 20;

/** Explore: public repositories (reachable signed out), the organizations and the people the visitor can see. Filters live in the URL. */
export function ExplorePage({ search }: { search: ExploreSearch }) {
  const t = useT("repos");
  const navigate = useNavigate();
  const view = search.view;
  const [draft, setDraft] = useState(search.q ?? "");
  const go = (next: ExploreSearch) => void navigate({ to: "/explore", search: next });
  const views: { id: ExploreView | undefined; label: string }[] = [
    { id: undefined, label: t("explore.view.repos") },
    { id: "orgs", label: t("explore.view.orgs") },
    { id: "users", label: t("explore.view.users") },
  ];

  function submit(e: FormEvent) {
    e.preventDefault();
    go({ q: draft.trim() || undefined, sort: search.sort, view });
  }

  return (
    <Page section="code" title={t("explore.title")} quiet>
      <header className="px-h">
        <p className="px-t" aria-hidden="true">{t("explore.title")}</p>
        <nav className="ui-seg" aria-label={t("explore.views")}>
          {views.map((v) => (
            <Link key={v.id ?? "repos"} to="/explore" search={{ view: v.id }} className="ui-segi" aria-current={view === v.id ? "page" : undefined} onClick={() => setDraft("")}>
              {v.label}
            </Link>
          ))}
        </nav>
      </header>
      <div className="px-grid">
        <div className="px-main">
          <form className="rl-bar" role="search" onSubmit={submit}>
            <Input
              className="rl-find"
              label={t(view === "users" ? "explore.searchUsers" : view === "orgs" ? "explore.searchOrgs" : "explore.search")}
              hideLabel
              type="search"
              placeholder={t(view === "users" ? "explore.searchUsersHint" : view === "orgs" ? "explore.searchOrgs" : "explore.searchHint")}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
            />
            {view === undefined && (
              <Select
                label={t("explore.sort")}
                hideLabel
                value={search.sort ?? "updated"}
                onValueChange={(v) => go({ q: search.q, sort: v === "name" || v === "stars" ? v : undefined })}
                options={[{ value: "updated", label: t("explore.sort.updated") }, { value: "name", label: t("explore.sort.name") }, { value: "stars", label: t("explore.sort.stars") }]}
              />
            )}
            <Button type="submit" icon="search">{t("explore.go")}</Button>
          </form>
          {view === "orgs" ? <OrgResults q={search.q} /> : view === "users" ? <UserResults q={search.q} /> : <RepoResults search={search} onPage={(n) => go({ ...search, page: n > 1 ? n : undefined })} />}
        </div>
        <ExploreAside />
      </div>
    </Page>
  );
}

function RepoResults({ search, onPage }: { search: ExploreSearch; onPage: (n: number) => void }) {
  const t = useT("repos");
  const tc = useT("common");
  const page = search.page ?? 1;
  const list = useExploreRepos({ q: search.q, sort: search.sort ?? "updated", page, perPage: PER_PAGE });
  const activity = useWeeklyActivity(list.data?.items.map((r) => r.id) ?? []);
  if (list.isPending) return <p role="status" className="ui-note rl-none">{tc("state.loading")}</p>;
  if (list.isError) {
    return (
      <div role="alert">
        <EmptyState icon="alert" title={list.error.message} action={<Button onClick={() => void list.refetch()}>{tc("action.retry")}</Button>} />
      </div>
    );
  }
  return (
    <>
      {list.data.items.length === 0 ? (
        <p className="ui-note rl-none">{search.q ? t("explore.noMatch") : t("explore.empty")}</p>
      ) : (
        <ul className="rl-list">{list.data.items.map((r) => <RepoRow key={r.id} repo={r} showOwner pin={false} weeks={activity.data?.get(r.id)} />)}</ul>
      )}
      <Pager page={page} hasMore={page * PER_PAGE < list.data.totalCount} onPage={onPage} />
    </>
  );
}

/** The organizations the visitor can see: the ones they belong to. */
function OrgResults({ q }: { q?: string }) {
  const t = useT("repos");
  const tc = useT("common");
  const signedIn = !!useSession().data;
  const orgs = useMyOrgs();
  if (!signedIn) return <p className="ui-note rl-none">{t("explore.signIn")}</p>;
  if (orgs.isPending) return <p role="status" className="ui-note rl-none">{tc("state.loading")}</p>;
  const needle = (q ?? "").toLowerCase();
  const rows = (orgs.data ?? []).filter((o) => !needle || `${o.name} ${o.displayName} ${o.description}`.toLowerCase().includes(needle));
  if (rows.length === 0) return <p className="ui-note rl-none">{needle ? t("explore.noMatch") : t("explore.noOrgs")}</p>;
  return (
    <ul className="rl-list">
      {rows.map((o) => (
        <li key={o.name} className="rl-row px-who">
          <AvatarImg src={o.avatarUrl} className="px-org">{(o.displayName || o.name).slice(0, 1).toUpperCase()}</AvatarImg>
          <div className="rl-main">
            <h3 className="rl-h">
              <Link to="/orgs/$org" params={{ org: o.name }} className="rl-name">{o.displayName || o.name}</Link>
              <span className="rl-o">{o.name}</span>
            </h3>
            {o.description && <p className="rl-desc">{o.description}</p>}
          </div>
        </li>
      ))}
    </ul>
  );
}

/** People search over the user directory the visitor may see. */
function UserResults({ q }: { q?: string }) {
  const t = useT("repos");
  const tc = useT("common");
  const signedIn = !!useSession().data;
  const term = (q ?? "").trim();
  const users = useQuery({
    queryKey: ["users", "search", term],
    enabled: signedIn && term !== "",
    queryFn: () => request<UserSummary[]>("GET", `/search/users?q=${encodeURIComponent(term)}&limit=50`),
  });
  if (!signedIn) return <p className="ui-note rl-none">{t("explore.signIn")}</p>;
  if (term === "") return <p className="ui-note rl-none">{t("explore.usersHint")}</p>;
  if (users.isPending) return <p role="status" className="ui-note rl-none">{tc("state.loading")}</p>;
  const rows = users.data ?? [];
  if (rows.length === 0) return <p className="ui-note rl-none">{t("explore.noUsers")}</p>;
  return (
    <ul className="rl-list">
      {rows.map((u) => (
        <li key={u.username} className="rl-row px-who">
          <Avatar user={u} size="xl" />
          <div className="rl-main">
            <h3 className="rl-h">
              <Link to="/$owner" params={{ owner: u.username }} className="rl-name">{u.displayName || u.username}</Link>
              <span className="rl-o">{u.username}</span>
            </h3>
          </div>
        </li>
      ))}
    </ul>
  );
}

/** Side column: the most starred public repositories and the visitor's organizations. */
function ExploreAside() {
  const t = useT("repos");
  const top = useExploreRepos({ sort: "stars", perPage: 5 });
  const signedIn = !!useSession().data;
  const orgs = useMyOrgs();
  const starred = (top.data?.items ?? []).filter((r) => r.starsCount > 0);
  const mine = signedIn ? (orgs.data ?? []).slice(0, 6) : [];
  if (starred.length === 0 && mine.length === 0) return <aside className="px-side" />;
  return (
    <aside className="px-side">
      {starred.length > 0 && (
        <section className="ui-box" aria-labelledby="px-top-h">
          <h2 className="ui-box-h" id="px-top-h"><b>{t("explore.topStarred")}</b></h2>
          <ul className="ui-in">
            {starred.map((r) => (
              <li key={r.id}>
                <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="ui-li">
                  <AvatarImg src={r.avatarUrl || r.ownerAvatarUrl} className="px-org px-sm">{r.owner.slice(0, 1).toUpperCase()}</AvatarImg>
                  <span className="px-n">{r.fullName}</span>
                  <span className="ui-tm">★ {r.starsCount}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      {mine.length > 0 && (
        <section className="ui-box" aria-labelledby="px-orgs-h">
          <h2 className="ui-box-h" id="px-orgs-h"><b>{t("explore.view.orgs")}</b></h2>
          <ul className="ui-in">
            {mine.map((o) => (
              <li key={o.name}>
                <Link to="/orgs/$org" params={{ org: o.name }} className="ui-li">
                  <AvatarImg src={o.avatarUrl} className="px-org px-sm">{(o.displayName || o.name).slice(0, 1).toUpperCase()}</AvatarImg>
                  <span className="px-n">{o.displayName || o.name}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </aside>
  );
}
