import { Link } from "@tanstack/react-router";
import type { UseQueryResult } from "@tanstack/react-query";
import { useSession } from "../../api/identity.ts";
import { useUserStarred } from "../../api/profile.ts";
import { useMyRepos, type Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Page } from "../../ui/index.ts";
import { CreateRepoDialog, RepoRows } from "./RepoList.tsx";

export type MyReposView = "personal" | "orgs" | "stars";

/** "Your repositories": every repository of the signed-in user, split into personal, organization and starred ones. */
export function MyReposPage({ view }: { view?: MyReposView }) {
  const t = useT("repos");
  const me = useSession().data?.username ?? "";
  const repos = useMyRepos();
  const starred = useUserStarred(me);
  const all = repos.data ?? [];
  const personal = all.filter((r) => r.owner === me);
  const orgs = all.filter((r) => r.ownerKind === "org");
  const stars = starred.data?.map((s) => s.repo);
  const views: { id: MyReposView | undefined; label: string; count?: number }[] = [
    { id: undefined, label: t("mine.all"), count: repos.data?.length },
    { id: "personal", label: t("mine.personal"), count: repos.data && personal.length },
    { id: "orgs", label: t("mine.orgs"), count: repos.data && orgs.length },
    { id: "stars", label: t("mine.stars"), count: stars?.length },
  ];
  const pick = (rows: Repo[] | undefined, source: UseQueryResult<unknown>) => ({ ...source, data: rows }) as UseQueryResult<Repo[]>;
  const query = view === "stars" ? pick(stars, starred) : view === "personal" ? pick(repos.data && personal, repos) : view === "orgs" ? pick(repos.data && orgs, repos) : repos;
  return (
    <Page section="code" title={t("mine.title")} quiet>
      <header className="px-h">
        <p className="px-t" aria-hidden="true">{t("mine.title")}</p>
        <nav className="ui-seg" aria-label={t("mine.views")}>
          {views.map((v) => (
            <Link key={v.id ?? "all"} to="/repos" search={{ view: v.id }} className="ui-segi" aria-current={view === v.id ? "page" : undefined}>
              {v.label}
              {v.count !== undefined && <span className="ui-n">{v.count}</span>}
            </Link>
          ))}
        </nav>
        <span className="px-new"><CreateRepoDialog /></span>
      </header>
      <RepoRows key={view ?? "all"} query={query} showOwner pin={view !== "stars"} />
    </Page>
  );
}
