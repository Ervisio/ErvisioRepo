import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { canWrite, PAGE_SIZE, useContributors, useLanguages } from "../../api/code.ts";
import { useReleases } from "../../api/releases.ts";
import type { Repo } from "../../api/repos.ts";
import { currentLocale } from "../../i18n/locale.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Icon, type IconName } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { When } from "./common.tsx";

const SHOWN_CONTRIBUTORS = 12;
const visibilityIcon: Record<Repo["visibility"], IconName> = { private: "lock", internal: "shield", public: "users" };

/** Right column of the repository home: About, Releases, Contributors and Languages. */
export function RepoSidebar({ repo, refName, hasReadme }: { repo: Repo; refName: string; hasReadme: boolean }) {
  return (
    <aside className="rp-side">
      <About repo={repo} hasReadme={hasReadme} />
      <Releases repo={repo} />
      <Contributors repo={repo} refName={refName} />
      <Languages repo={repo} refName={refName} />
    </aside>
  );
}

function Group({ title, count, to, children }: { title: string; count?: string; to?: ReactNode; children: ReactNode }) {
  return (
    <section className="rp-sg">
      <h2>
        {to ?? title}
        {count !== undefined && <span className="rp-cnt">{count}</span>}
      </h2>
      {children}
    </section>
  );
}

function About({ repo, hasReadme }: { repo: Repo; hasReadme: boolean }) {
  const t = useT("code");
  const tr = useT("repos");
  return (
    <Group title={t("side.about")}>
      {repo.description ? <p className="rp-sd">{repo.description}</p> : <p className="rp-sd rp-none">{t("side.noDescription")}</p>}
      <ul className="rp-sf">
        {hasReadme && (
          <li>
            <Icon name="book" />
            <a href="#readme">{t("side.readme")}</a>
          </li>
        )}
        <li>
          <Icon name={visibilityIcon[repo.visibility]} />
          <span>{tr(`visibility.${repo.visibility}`)}</span>
        </li>
        <li>
          <Icon name="branch" />
          <span>
            {t("side.defaultBranch")} <b>{repo.defaultBranch}</b>
          </span>
        </li>
        <li>
          <Icon name="star" />
          <span>
            <b>{repo.starsCount}</b> {t(repo.starsCount === 1 ? "side.stars.one" : "side.stars.other")}
          </span>
        </li>
      </ul>
    </Group>
  );
}

function Releases({ repo }: { repo: Repo }) {
  const t = useT("code");
  const tl = useT("releases");
  const { owner, name } = repo;
  const list = useReleases(owner, name, 0);
  if (list.isError) return null;
  const rows = list.data?.filter((r) => !r.draft);
  const latest = rows?.find((r) => r.latest) ?? rows?.[0];
  // The list is paged without a total: a full page means "at least this many".
  const capped = (list.data?.length ?? 0) === PAGE_SIZE;
  const total = rows ? `${rows.length}${capped ? "+" : ""}` : undefined;
  const more = (rows?.length ?? 0) - 1;
  const all = (
    <Link to="/$owner/$name/releases" params={{ owner, name }}>
      {t("side.releases")}
      {rows && rows.length > 0 && <span className="rp-cnt">{total}</span>}
    </Link>
  );
  return (
    <Group title={t("side.releases")} to={all}>
      {rows &&
        (latest ? (
          <>
            <Link className="rp-rel" to="/$owner/$name/releases/tag/$tag" params={{ owner, name, tag: latest.tag }}>
              <Icon name="tag" />
              <span className="rp-rel-t">
                <span className="rp-rel-n">
                  <b>{latest.name || latest.tag}</b>
                  {latest.latest && <Badge tone="ok">{tl("badge.latest")}</Badge>}
                </span>
                <When iso={latest.publishedAt ?? latest.createdAt} />
              </span>
            </Link>
            {more > 0 && (
              <Link className="rp-more" to="/$owner/$name/releases" params={{ owner, name }}>
                {t(more === 1 ? "side.moreReleases.one" : "side.moreReleases.other", { count: capped ? `${more}+` : more })}
              </Link>
            )}
          </>
        ) : (
          <>
            <p className="rp-sd rp-none">{t("side.noReleases")}</p>
            {canWrite(repo.permission) && (
              <Link className="rp-more" to="/$owner/$name/releases/new" params={{ owner, name }}>
                {t("side.createRelease")}
              </Link>
            )}
          </>
        ))}
    </Group>
  );
}

function Contributors({ repo, refName }: { repo: Repo; refName: string }) {
  const t = useT("code");
  const q = useContributors(repo.owner, repo.name, refName, SHOWN_CONTRIBUTORS);
  if (!q.data || q.data.items.length === 0) return null;
  const { total, items } = q.data;
  const rest = total - items.length;
  return (
    <Group title={t("side.contributors")} count={String(total)}>
      <ul className="rp-avs">
        {items.map((c, i) => {
          const label = c.username && c.username !== c.name ? `${c.name} (@${c.username})` : c.name;
          return (
            <li key={i} title={label}>
              {c.username ? (
                <Link to="/$owner" params={{ owner: c.username }}>
                  <Avatar user={{ username: c.name, avatarUrl: c.avatarUrl }} size="md" />
                  <span className="sr-only">{label}</span>
                </Link>
              ) : (
                <>
                  <Avatar user={{ username: c.name, avatarUrl: c.avatarUrl }} size="md" />
                  <span className="sr-only">{label}</span>
                </>
              )}
            </li>
          );
        })}
      </ul>
      {rest > 0 && <p className="rp-sd rp-none">{t(rest === 1 ? "side.moreContributors.one" : "side.moreContributors.other", { count: rest })}</p>}
    </Group>
  );
}

function Languages({ repo, refName }: { repo: Repo; refName: string }) {
  const t = useT("code");
  const q = useLanguages(repo.owner, repo.name, refName);
  if (!q.data || q.data.items.length === 0) return null;
  const nf = new Intl.NumberFormat(currentLocale(), { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  // The server folds the small languages into an entry named "Other".
  const items = q.data.items.map((l) => {
    const name = l.name === "Other" ? t("side.languagesOther") : l.name;
    return { ...l, name, label: `${name} ${nf.format(l.percent)}%` };
  });
  return (
    <Group title={t("side.languages")}>
      <div className="rp-lang-bar" role="img" aria-label={t("side.languagesList", { list: items.map((l) => l.label).join(", ") })}>
        {items.map((l) => (
          <span key={l.name} title={l.label} style={{ flexGrow: l.bytes, background: l.color }} />
        ))}
      </div>
      <ul className="rp-lang-key">
        {items.map((l) => (
          <li key={l.name}>
            <i style={{ background: l.color }} />
            <b>{l.name}</b>
            <span>{nf.format(l.percent)}%</span>
          </li>
        ))}
      </ul>
    </Group>
  );
}
