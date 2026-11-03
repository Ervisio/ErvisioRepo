import { Link, useNavigate } from "@tanstack/react-router";
import { archiveUrl, useBranches, useCommit, useCommits, useTags } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Icon } from "../../ui/index.ts";
import { DiffView, filePath } from "../../ui/code/DiffView.tsx";
import { Crumb, Gate, Pager, RefSwitcher, RepoFrame, shortSha, When } from "./common.tsx";

/** One commit row: summary, author, relative date, short sha. */
export function CommitRow({ repo, c }: { repo: Repo; c: { sha: string; summary: string; author: string; date: string } }) {
  return (
    <li className="rp-crow">
      <Icon name="commit" className="ui-icon rp-i" />
      <Link className="rp-nm" to="/$owner/$name/commit/$sha" params={{ owner: repo.owner, name: repo.name, sha: c.sha }}>
        {c.summary}
      </Link>
      <span className="rp-msg">{c.author}</span>
      <When iso={c.date} />
      <code className="ui-mono">{shortSha(c.sha)}</code>
    </li>
  );
}

/** Paginated history of a ref, optionally limited to one path. */
export function CommitsPage({ owner, name, refName, path = "", page = 1 }: { owner: string; name: string; refName?: string; path?: string; page?: number }) {
  return (
    <RepoFrame owner={owner} name={name} tab="commits">
      {(repo) => <CommitsBody repo={repo} refName={refName || repo.defaultBranch} path={path} page={page} />}
    </RepoFrame>
  );
}

function CommitsBody({ repo, refName, path, page }: { repo: Repo; refName: string; path: string; page: number }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const commits = useCommits(owner, name, refName, path, page - 1);
  const go = (s: { ref: string; page?: number }) =>
    void navigate({ to: "/$owner/$name/commits", params: { owner, name }, search: { ref: s.ref, path: path || undefined, page: s.page && s.page > 1 ? s.page : undefined } });
  return (
    <>
      <div className="rp-bar">
        <RefSwitcher owner={owner} name={name} current={refName} onPick={(ref) => go({ ref })} />
        {path && <Crumb owner={owner} name={name} refName={refName} path={path} />}
        <span className="acc-grow" />
        <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName }}>
          <Button icon="code">{t("commits.browse")}</Button>
        </Link>
      </div>
      <Gate query={commits}>
        {(p) => (
          <Card title={path ? t("commits.titlePath", { path }) : t("commits.title", { ref: refName })}>
            {p.commits.length === 0 ? (
              <p className="ui-note acc-pad">{t("commits.empty")}</p>
            ) : (
              <ul className="rp-list">
                {p.commits.map((c) => (
                  <CommitRow key={c.sha} repo={repo} c={{ sha: c.sha, summary: c.summary, author: c.author.name, date: c.author.date }} />
                ))}
              </ul>
            )}
            <Pager page={page} hasMore={p.hasMore} onPage={(n) => go({ ref: refName, page: n })} />
          </Card>
        )}
      </Gate>
    </>
  );
}

/** One commit: message, metadata and its diff against the first parent. */
export function CommitPage({ owner, name, sha }: { owner: string; name: string; sha: string }) {
  return <RepoFrame owner={owner} name={name} tab="commits">{(repo) => <CommitBody repo={repo} sha={sha} />}</RepoFrame>;
}

function CommitBody({ repo, sha }: { repo: Repo; sha: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const commit = useCommit(owner, name, sha);
  return (
    <Gate query={commit} notFound={<EmptyState icon="commit" title={t("commit.notFound")} />}>
      {({ commit: c, diff }) => {
        const body = c.message.slice(c.summary.length).trim();
        return (
          <>
            <Card
              title={c.summary}
              actions={
                <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: c.sha }}>
                  <Button size="sm" icon="code">
                    {t("commits.browse")}
                  </Button>
                </Link>
              }
            >
              {body && <pre className="rp-pre rp-msgbody">{body}</pre>}
              <dl className="ui-kv acc-pad">
                <dt>{t("commit.author")}</dt>
                <dd>
                  {c.author.name} <span className="ui-note">&lt;{c.author.email}&gt;</span> · <When iso={c.author.date} />
                </dd>
                {(c.committer.name !== c.author.name || c.committer.email !== c.author.email) && (
                  <>
                    <dt>{t("commit.committer")}</dt>
                    <dd>
                      {c.committer.name} · <When iso={c.committer.date} />
                    </dd>
                  </>
                )}
                <dt>{t("commit.sha")}</dt>
                <dd>
                  <code className="ui-mono">{c.sha}</code>
                </dd>
                {c.parents.length > 0 && (
                  <>
                    <dt>{t("commit.parents", { count: c.parents.length })}</dt>
                    <dd className="ui-inline">
                      {c.parents.map((p) => (
                        <Link key={p} to="/$owner/$name/commit/$sha" params={{ owner, name, sha: p }} className="ui-mono">
                          {shortSha(p)}
                        </Link>
                      ))}
                    </dd>
                  </>
                )}
              </dl>
            </Card>
            <DiffView
              diff={diff}
              fileActions={(f) =>
                f.status !== "deleted" && (
                  <Link to="/$owner/$name/blob" params={{ owner, name }} search={{ ref: c.sha, path: filePath(f) }} className="ui-note">
                    {t("commit.viewFile")}
                  </Link>
                )
              }
            />
          </>
        );
      }}
    </Gate>
  );
}

/** Branch list with ahead/behind counts against the default branch. */
export function BranchesPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  return <RepoFrame owner={owner} name={name} tab="branches">{(repo) => <BranchesBody repo={repo} page={page} />}</RepoFrame>;
}

function BranchesBody({ repo, page }: { repo: Repo; page: number }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const branches = useBranches(owner, name, page - 1);
  return (
    <Gate query={branches}>
      {(p) => (
        <Card title={t("branches.title")}>
          {p.items.length === 0 ? (
            <p className="ui-note acc-pad">{t("branches.empty")}</p>
          ) : (
            <ul className="rp-list">
              {p.items.map((b) => (
                <li key={b.name} className="rp-brow">
                  <Icon name="branch" className="ui-icon rp-i" />
                  <Link className="rp-nm ui-mono" to="/$owner/$name" params={{ owner, name }} search={{ ref: b.name }}>
                    {b.name}
                  </Link>
                  <span className="rp-msg">
                    {b.default ? (
                      <Badge tone="section">{t("branches.default")}</Badge>
                    ) : (
                      b.ahead !== undefined &&
                      b.behind !== undefined && <span className="ui-note">{t("branches.aheadBehind", { ahead: b.ahead, behind: b.behind })}</span>
                    )}
                  </span>
                  <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: b.commit.sha }} className="rp-last-msg" title={b.commit.summary}>
                    {b.commit.summary}
                  </Link>
                  <When iso={b.commit.date} />
                  {!b.default ? (
                    <Link to="/$owner/$name/compare" params={{ owner, name }} search={{ base: repo.defaultBranch, head: b.name }} className="ui-note">
                      {t("branches.compare")}
                    </Link>
                  ) : (
                    <span />
                  )}
                </li>
              ))}
            </ul>
          )}
          <Pager page={page} hasMore={p.hasMore} onPage={(n) => void navigate({ to: "/$owner/$name/branches", params: { owner, name }, search: { page: n > 1 ? n : undefined } })} />
        </Card>
      )}
    </Gate>
  );
}

/** Tag list with source downloads. */
export function TagsPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  return <RepoFrame owner={owner} name={name} tab="tags">{(repo) => <TagsBody repo={repo} page={page} />}</RepoFrame>;
}

function TagsBody({ repo, page }: { repo: Repo; page: number }) {
  const t = useT("code");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const tags = useTags(owner, name, page - 1);
  return (
    <Gate query={tags}>
      {(p) => (
        <Card title={t("tags.title")}>
          {p.items.length === 0 ? (
            <p className="ui-note acc-pad">{t("tags.empty")}</p>
          ) : (
            <ul className="rp-list">
              {p.items.map((g) => (
                <li key={g.name} className="rp-tag">
                  <Link className="rp-tag-name ui-mono" to="/$owner/$name" params={{ owner, name }} search={{ ref: g.name }}>
                    {g.name}
                  </Link>
                  <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: g.commit.sha }} className="rp-msg" title={g.message || g.commit.summary}>
                    {g.annotated && g.message ? g.message.split("\n")[0] : g.commit.summary}
                  </Link>
                  <When iso={g.tagger?.date ?? g.commit.date} />
                  <a href={archiveUrl(owner, name, g.name, "zip")} download className="ui-note" aria-label={t("tags.zip", { tag: g.name })}>
                    <Icon name="download" /> zip
                  </a>
                </li>
              ))}
            </ul>
          )}
          <Pager page={page} hasMore={p.hasMore} onPage={(n) => void navigate({ to: "/$owner/$name/tags", params: { owner, name }, search: { page: n > 1 ? n : undefined } })} />
        </Card>
      )}
    </Gate>
  );
}
