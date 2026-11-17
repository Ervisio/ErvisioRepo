import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { archiveUrl, canWrite, useBranches, useCommit, useCommitLog, useTags, type Branch, type GitCommit } from "../../api/code.ts";
import { useSession } from "../../api/identity.ts";
import { usePulls, type PullRequest } from "../../api/pulls.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon, Menu, MenuItem, MenuSeparator } from "../../ui/index.ts";
import { DiffView, filePath } from "../../ui/code/DiffView.tsx";
import { CopyButton, Crumb, dayLabel, Gate, Initial, Pager, RefSwitcher, RepoFrame, shortSha, When } from "./common.tsx";

/** Branch heads and tags of the first ref pages, by commit sha (labels on commit rows). */
export type RefLabel = { name: string; tag: boolean };
const REF_LIMIT = 100;

function useRefLabels(owner: string, name: string): Map<string, RefLabel[]> {
  const branches = useBranches(owner, name, 0, REF_LIMIT);
  const tags = useTags(owner, name, 0, REF_LIMIT);
  return useMemo(() => {
    const m = new Map<string, RefLabel[]>();
    const add = (sha: string, r: RefLabel) => m.set(sha, [...(m.get(sha) ?? []), r]);
    for (const b of branches.data?.items ?? []) add(b.commit.sha, { name: b.name, tag: false });
    for (const g of tags.data?.items ?? []) add(g.commit.sha, { name: g.name, tag: true });
    return m;
  }, [branches.data, tags.data]);
}

/** One commit row (round 020): avatar, title, author and time, refs, short sha, copy, browse. */
export function CommitRow({ repo, c, refs }: { repo: Repo; c: { sha: string; summary: string; author: string; date: string }; refs?: RefLabel[] }) {
  const t = useT("code");
  const { owner, name } = repo;
  return (
    <li className="cm-row">
      <Initial name={c.author} size={36} />
      <span className="cm-t">
        <Link className="cm-ttl" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: c.sha }}>
          {c.summary}
        </Link>
        <span className="cm-mt">
          <b>{c.author}</b> · <When iso={c.date} />
          {refs?.map((r) => (
            <span key={`${r.tag ? "t" : "b"}-${r.name}`} className={r.tag ? "ui-chip ui-chip-ok ui-mono" : "ui-chip ui-chip-acc ui-mono"}>
              {r.name}
            </span>
          ))}
        </span>
      </span>
      <span className="cm-act">
        <Link className="cm-sha" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: c.sha }}>
          {shortSha(c.sha)}
        </Link>
        <CopyButton value={c.sha} label={t("commits.copySha")} />
        <Link className="ui-btn ui-btn-ghost rp-ib" to="/$owner/$name" params={{ owner, name }} search={{ ref: c.sha }} aria-label={t("commits.browseAt")} title={t("commits.browseAt")}>
          <Icon name="code" />
        </Link>
      </span>
    </li>
  );
}

/** History of a ref, optionally limited to one path, as a timeline grouped by day. */
export function CommitsPage({ owner, name, refName, path = "" }: { owner: string; name: string; refName?: string; path?: string; page?: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="commits" head={false}>
      {(repo) => <CommitsBody repo={repo} refName={refName || repo.defaultBranch} path={path} />}
    </RepoFrame>
  );
}

function byDay(commits: GitCommit[]): [string, GitCommit[]][] {
  const m = new Map<string, GitCommit[]>();
  for (const c of commits) {
    const k = dayLabel(c.author.date);
    const g = m.get(k);
    if (g) g.push(c);
    else m.set(k, [c]);
  }
  return [...m.entries()];
}

function CommitsBody({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const log = useCommitLog(owner, name, refName, path);
  const refs = useRefLabels(owner, name);
  const [filter, setFilter] = useState("");
  const [author, setAuthor] = useState("");
  const all = useMemo(() => log.data?.pages.flatMap((p) => p.commits) ?? [], [log.data]);
  const authors = useMemo(() => [...new Set(all.map((c) => c.author.name))].sort((a, b) => a.localeCompare(b)), [all]);
  const q = filter.trim().toLowerCase();
  const shown = all.filter((c) => (!author || c.author.name === author) && (!q || c.message.toLowerCase().includes(q)));
  return (
    <div className="cm-page">
      <div className="cm-top">
        <h1>{t("commits.heading")}</h1>
        <RefSwitcher owner={owner} name={name} current={refName} onPick={(ref) => void navigate({ to: "/$owner/$name/commits", params: { owner, name }, search: { ref, path: path || undefined } })} />
        {path && <Crumb owner={owner} name={name} refName={refName} path={path} linkLast />}
        <span className="acc-grow" />
        <label className="cx-find cm-filter">
          <Icon name="search" />
          <input type="search" value={filter} placeholder={t("commits.filter")} aria-label={t("commits.filter")} onChange={(e) => setFilter(e.target.value)} />
        </label>
        <Menu
          align="end"
          label={t("commits.author")}
          trigger={
            <Button aria-label={t("commits.authorPick", { author: author || t("commits.anyAuthor") })}>
              {author || t("commits.author")}
              <Icon name="down" />
            </Button>
          }
        >
          <MenuItem icon={author === "" ? "check" : undefined} onSelect={() => setAuthor("")}>
            {t("commits.anyAuthor")}
          </MenuItem>
          <MenuSeparator />
          {authors.map((a) => (
            <MenuItem key={a} icon={author === a ? "check" : undefined} onSelect={() => setAuthor(a)}>
              {a}
            </MenuItem>
          ))}
        </Menu>
      </div>
      <Gate query={log}>
        {() =>
          shown.length === 0 ? (
            <p className="ui-note acc-pad">{all.length === 0 ? t("commits.empty") : t("commits.noMatch")}</p>
          ) : (
            <ol className="cm-tl">
              {byDay(shown).map(([day, cs]) => (
                <li key={day}>
                  <h2 className="cm-day">{day}</h2>
                  <ul className="rp-list">
                    {cs.map((c) => (
                      <CommitRow key={c.sha} repo={repo} c={{ sha: c.sha, summary: c.summary, author: c.author.name, date: c.author.date }} refs={refs.get(c.sha)} />
                    ))}
                  </ul>
                </li>
              ))}
            </ol>
          )
        }
      </Gate>
      {log.hasNextPage && (
        <div className="cm-more">
          <Button disabled={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
            {log.isFetchingNextPage ? t("commits.loading") : t("commits.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

/** One commit: big header, then its diff against the first parent in the shared diff layout. */
export function CommitPage({ owner, name, sha }: { owner: string; name: string; sha: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="commits" head={false}>
      {(repo) => <CommitBody repo={repo} sha={sha} />}
    </RepoFrame>
  );
}

function CommitBody({ repo, sha }: { repo: Repo; sha: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const commit = useCommit(owner, name, sha);
  const refs = useRefLabels(owner, name);
  return (
    <Gate query={commit} notFound={<EmptyState icon="commit" title={t("commit.notFound")} />}>
      {({ commit: c, diff }) => {
        const body = c.message.slice(c.summary.length).trim();
        const committer = c.committer.name !== c.author.name || c.committer.email !== c.author.email;
        return (
          <div className="cd-page">
          <DiffView
            diff={diff}
            header={
              <header className="cd-h">
                <Initial name={c.author.name} size={44} />
                <div className="cd-t">
                  <h1>{c.summary}</h1>
                  {body && <p className="cd-body">{body}</p>}
                  <div className="cd-mt">
                    <b title={c.author.email}>{c.author.name}</b>
                    <When iso={c.author.date} />
                    {committer && <span>{t("commit.committedBy", { name: c.committer.name })}</span>}
                    <span className="cd-sha">
                      <code className="ui-mono">{shortSha(c.sha)}</code>
                      <CopyButton value={c.sha} label={t("commits.copySha")} />
                    </span>
                    {c.parents.length > 0 && (
                      <span className="cd-par">
                        {t("commit.parents", { count: c.parents.length })}
                        {c.parents.map((p) => (
                          <Link key={p} to="/$owner/$name/commit/$sha" params={{ owner, name, sha: p }} className="cd-sha ui-mono">
                            {shortSha(p)}
                          </Link>
                        ))}
                      </span>
                    )}
                    {refs.get(c.sha)?.map((r) => (
                      <span key={r.name} className={r.tag ? "ui-chip ui-chip-ok ui-mono" : "ui-chip ui-chip-acc ui-mono"}>
                        {r.name}
                      </span>
                    ))}
                  </div>
                </div>
                <Link className="ui-btn ui-btn-secondary cd-browse" to="/$owner/$name" params={{ owner, name }} search={{ ref: c.sha }}>
                  <Icon name="code" />
                  {t("commits.browse")}
                </Link>
              </header>
            }
            fileActions={(f) =>
              f.status !== "deleted" && (
                <Link
                  to="/$owner/$name/blob"
                  params={{ owner, name }}
                  search={{ ref: c.sha, path: filePath(f) }}
                  className="ui-btn ui-btn-ghost rp-ib"
                  aria-label={t("commit.viewFileAt", { path: filePath(f) })}
                  title={t("commit.viewFile")}
                >
                  <Icon name="file" />
                </Link>
              )
            }
          />
          </div>
        );
      }}
    </Gate>
  );
}

const STALE_DAYS = 90;
type BranchView = "all" | "yours" | "stale";
const branchViews: BranchView[] = ["all", "yours", "stale"];

/** Pull requests of the first page by head branch, open ones first. */
function usePullsByHead(repo: Repo): Map<string, PullRequest> {
  const pulls = usePulls(repo.owner, repo.name, { state: "all", page: 1 });
  return useMemo(() => {
    const m = new Map<string, PullRequest>();
    for (const p of pulls.data?.items ?? []) {
      const head = p.head.ref.replace(/^refs\/heads\//, "");
      const prev = m.get(head);
      if (!prev || (prev.state !== "open" && p.state === "open")) m.set(head, p);
    }
    return m;
  }, [pulls.data]);
}

/** Branch table (round 020): view, search, compare; ahead/behind against the default branch and the PR of each branch. */
export function BranchesPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="branches" head={false}>
      {(repo) => <BranchesBody repo={repo} page={page} />}
    </RepoFrame>
  );
}

function BranchesBody({ repo, page }: { repo: Repo; page: number }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const branches = useBranches(owner, name, page - 1);
  const session = useSession();
  const pulls = usePullsByHead(repo);
  const [view, setView] = useState<BranchView>("all");
  const [q, setQ] = useState("");
  const me = [session.data?.username, session.data?.displayName].filter(Boolean).map((s) => s!.toLowerCase());
  const [staleBefore] = useState(() => Date.now() - STALE_DAYS * 86400000);
  const matches = (b: Branch, v: BranchView) =>
    v === "all" || (v === "yours" ? me.includes(b.commit.author.toLowerCase()) : !b.default && new Date(b.commit.date).getTime() < staleBefore);
  const items = branches.data?.items ?? [];
  const shown = items.filter((b) => matches(b, view) && (!q.trim() || b.name.toLowerCase().includes(q.trim().toLowerCase())));
  return (
    <div className="cm-page br-page">
      <div className="cm-top">
        <h1>{t("branches.title")}</h1>
        <div className="ui-seg" role="group" aria-label={t("branches.view")}>
          {branchViews.map((v) => (
            <button key={v} type="button" className="ui-segi" aria-pressed={view === v} onClick={() => setView(v)}>
              {t(`branches.view.${v}`)} <span className="ui-n">{items.filter((b) => matches(b, v)).length}</span>
            </button>
          ))}
        </div>
        <span className="acc-grow" />
        <label className="cx-find cm-filter">
          <Icon name="search" />
          <input type="search" value={q} placeholder={t("branches.search")} aria-label={t("branches.search")} onChange={(e) => setQ(e.target.value)} />
        </label>
        <Link className="ui-btn ui-btn-secondary" to="/$owner/$name/compare" params={{ owner, name }} search={{ base: repo.defaultBranch }}>
          {t("branches.compare")}
        </Link>
      </div>
      <Gate query={branches}>
        {(p) => (
          <div className="ui-box">
            <div className="ui-in br-wrap" role="region" aria-label={t("branches.title")} tabIndex={0}>
              {shown.length === 0 ? (
                <p className="ui-note acc-pad">{p.items.length === 0 ? t("branches.empty") : t("branches.noMatch")}</p>
              ) : (
                <table className="br-tbl">
                  <caption className="sr-only">{t("branches.title")}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("branches.colBranch")}</th>
                      <th scope="col">{t("branches.colUpdated")}</th>
                      <th scope="col">{t("branches.colAheadBehind")}</th>
                      <th scope="col">{t("branches.colPull")}</th>
                      <th scope="col">
                        <span className="sr-only">{t("branches.colActions")}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((b) => (
                      <BranchRow key={b.name} repo={repo} b={b} pull={pulls.get(b.name)} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
            <Pager page={page} hasMore={p.hasMore} onPage={(n) => void navigate({ to: "/$owner/$name/branches", params: { owner, name }, search: { page: n > 1 ? n : undefined } })} />
          </div>
        )}
      </Gate>
    </div>
  );
}

function BranchRow({ repo, b, pull }: { repo: Repo; b: Branch; pull?: PullRequest }) {
  const t = useT("code");
  const { owner, name } = repo;
  return (
    <tr>
      <th scope="row">
        <Link className="br-name ui-mono" to="/$owner/$name" params={{ owner, name }} search={{ ref: b.name }}>
          {b.name}
        </Link>
        {b.default && <span className="ui-chip">{t("branches.default")}</span>}
      </th>
      <td>
        <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: b.commit.sha }} className="br-cm" title={b.commit.summary}>
          <When iso={b.commit.date} /> · {b.commit.author}
        </Link>
      </td>
      <td className="br-num">
        {b.default || b.ahead === undefined || b.behind === undefined ? (
          <span aria-label={t("branches.none")}>—</span>
        ) : (
          <span title={t("branches.aheadBehind", { ahead: b.ahead, behind: b.behind })}>
            {b.behind} / <b>{b.ahead}</b>
          </span>
        )}
      </td>
      <td>
        {pull ? (
          <Link
            to="/$owner/$name/pulls/$number"
            params={{ owner, name, number: String(pull.issue.number) }}
            className={pull.state === "open" ? "ui-chip ui-chip-ok" : pull.state === "merged" ? "ui-chip ui-chip-rev" : "ui-chip"}
          >
            {t(`branches.pull.${pull.state}`, { number: pull.issue.number })}
          </Link>
        ) : !b.default && canWrite(repo.permission) ? (
          <Link className="ui-btn ui-btn-secondary br-open" to="/$owner/$name/compare" params={{ owner, name }} search={{ base: repo.defaultBranch, head: b.name }}>
            {t("branches.openPull")}
          </Link>
        ) : (
          <span aria-label={t("branches.none")}>—</span>
        )}
      </td>
      <td className="br-act">
        {!b.default && (
          <Link
            className="ui-btn ui-btn-ghost rp-ib"
            to="/$owner/$name/compare"
            params={{ owner, name }}
            search={{ base: repo.defaultBranch, head: b.name }}
            aria-label={t("branches.compareOne", { branch: b.name })}
            title={t("branches.compare")}
          >
            <Icon name="layers" />
          </Link>
        )}
        <CopyButton value={b.name} label={t("branches.copy", { branch: b.name })} />
      </td>
    </tr>
  );
}

/** Tag table with source downloads. */
export function TagsPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="tags" head={false}>
      {(repo) => <TagsBody repo={repo} page={page} />}
    </RepoFrame>
  );
}

function TagsBody({ repo, page }: { repo: Repo; page: number }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const tags = useTags(owner, name, page - 1);
  return (
    <div className="cm-page br-page">
      <div className="cm-top">
        <h1>{t("tags.title")}</h1>
      </div>
      <Gate query={tags}>
        {(p) => (
          <div className="ui-box">
            <div className="ui-in br-wrap" role="region" aria-label={t("tags.title")} tabIndex={0}>
              {p.items.length === 0 ? (
                <p className="ui-note acc-pad">{t("tags.empty")}</p>
              ) : (
                <table className="br-tbl tg-tbl">
                  <caption className="sr-only">{t("tags.title")}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("tags.colTag")}</th>
                      <th scope="col">{t("tags.colMessage")}</th>
                      <th scope="col">{t("tags.colDate")}</th>
                      <th scope="col">
                        <span className="sr-only">{t("tags.colDownloads")}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {p.items.map((g) => (
                      <tr key={g.name}>
                        <th scope="row">
                          <Link className="br-name ui-mono" to="/$owner/$name" params={{ owner, name }} search={{ ref: g.name }}>
                            {g.name}
                          </Link>
                        </th>
                        <td>
                          <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: g.commit.sha }} className="br-cm" title={g.message || g.commit.summary}>
                            {g.annotated && g.message ? g.message.split("\n")[0] : g.commit.summary}
                          </Link>
                        </td>
                        <td className="br-num">
                          <When iso={g.tagger?.date ?? g.commit.date} />
                        </td>
                        <td className="br-act">
                          <a href={archiveUrl(owner, name, g.name, "zip")} download className="ui-btn ui-btn-ghost" aria-label={t("tags.zip", { tag: g.name })}>
                            <Icon name="download" /> zip
                          </a>
                          <a href={archiveUrl(owner, name, g.name, "tar.gz")} download className="ui-btn ui-btn-ghost" aria-label={t("tags.tgz", { tag: g.name })}>
                            tar.gz
                          </a>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
            <Pager page={page} hasMore={p.hasMore} onPage={(n) => void navigate({ to: "/$owner/$name/tags", params: { owner, name }, search: { page: n > 1 ? n : undefined } })} />
          </div>
        )}
      </Gate>
    </div>
  );
}
