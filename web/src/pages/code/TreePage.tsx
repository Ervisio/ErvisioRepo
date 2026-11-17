import { Link, useNavigate } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { canWrite, useTree, type Tree, type TreeEntry } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, EmptyState, Icon, Menu, MenuItem, MenuSeparator } from "../../ui/index.ts";
import { useLayout } from "../../ui/shell/layout.ts";
import { EmptyRepoCard } from "../repos/RepoPage.tsx";
import { Crumb, dirOf, Gate, Initial, RefSwitcher, RepoFrame, shortSha, When } from "./common.tsx";
import { CodeLayout } from "./FileTree.tsx";
import { Readme } from "./Readme.tsx";
import { CloneMenu } from "./RepoHead.tsx";
import { RepoSidebar } from "./RepoSidebar.tsx";

/** Repository home and directory browsing: tree, last commit, README. */
export function TreePage({ owner, name, refName, path = "" }: { owner: string; name: string; refName?: string; path?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code" head={path === ""}>
      {(repo) => <TreeBody repo={repo} refName={refName || repo.defaultBranch} path={path} />}
    </RepoFrame>
  );
}

function TreeBody({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const tree = useTree(owner, name, refName, path);
  const navigate = useNavigate();
  const console_ = useLayout() === "console";
  const isRoot = path === "";
  const switcher = (
    <RefSwitcher
      owner={owner}
      name={name}
      current={refName}
      onPick={(ref) => void navigate({ to: "/$owner/$name", params: { owner, name }, search: { ref, path: path || undefined } })}
    />
  );
  const notFound =
    isRoot && refName === repo.defaultBranch ? (
      <EmptyRepoCard repo={repo} />
    ) : (
      <EmptyState icon="folder" title={t("tree.notFound")} description={t("tree.notFoundHint", { ref: refName })} />
    );
  const files = (
    <Gate query={tree} notFound={notFound}>
      {(data) => (
        <>
          {console_ || !isRoot ? (
            <FileBox repo={repo} tree={data} refName={refName} path={path} />
          ) : (
            <TreeCard repo={repo} tree={data} refName={refName} path={path} />
          )}
          {data.readme && <Readme owner={owner} name={name} refName={refName} path={data.readme} />}
        </>
      )}
    </Gate>
  );
  // A folder: the file tree on the left, the path and its actions on top (round 018 A).
  if (!isRoot) {
    const pick = (ref: string) => void navigate({ to: "/$owner/$name", params: { owner, name }, search: { ref, path } });
    return (
      <CodeLayout
        repo={repo}
        refName={refName}
        path={path}
        isDir
        onPickRef={pick}
        top={
          <>
            <Crumb owner={owner} name={name} refName={refName} path={path} trailing />
            <span className="acc-grow" />
            {canWrite(repo.permission) && tree.data?.ref.kind === "branch" && (
              <Link to="/$owner/$name/new" params={{ owner, name }} search={{ ref: refName, path }} className="ui-btn ui-btn-secondary">
                <Icon name="plus" />
                <span className="cx-lbl">{t("tree.addFile")}</span>
              </Link>
            )}
            <RepoMore repo={repo} refName={refName} path={path} />
          </>
        }
      >
        {files}
      </CodeLayout>
    );
  }
  const content = (
    <>
      {console_ ? (
        <div className="rp-bar">
          {switcher}
          <span className="acc-grow" />
          <CloneMenu repo={repo} refName={refName} />
          <RepoMore repo={repo} refName={refName} path={path} />
        </div>
      ) : (
        <div className="rp-bar">
          {switcher}
          <Crumb owner={owner} name={name} refName={refName} path={path} />
          <span className="acc-grow" />
          <Link to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
            <Button icon="clock">{t("tree.history")}</Button>
          </Link>
          <CloneMenu repo={repo} refName={refName} />
        </div>
      )}
      {files}
    </>
  );
  if (!console_) return content;
  return (
    <div className="rp-home">
      <div className="rp-main">{content}</div>
      <RepoSidebar repo={repo} refName={refName} hasReadme={!!tree.data?.readme} />
    </div>
  );
}

function latest(entries: TreeEntry[]) {
  let best: TreeEntry["lastCommit"];
  for (const e of entries) if (e.lastCommit && (!best || e.lastCommit.date > best.date)) best = e.lastCommit;
  return best;
}

/** Repository actions behind one menu: history, add file, settings. Pin, Watch, Fork and Star sit in the header. */
function RepoMore({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const tr = useT("repos");
  const { owner, name } = repo;
  const writable = canWrite(repo.permission);
  return (
    <Menu
      align="end"
      label={t("more.actions")}
      trigger={<Button icon="dots" className="rp-ib" aria-label={t("more.actions")} title={t("more.actions")} />}
    >
      <MenuItem asChild>
        <Link to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
          <Icon name="clock" />
          {t("tree.history")}
        </Link>
      </MenuItem>
      {writable && (
        <MenuItem asChild>
          <Link to="/$owner/$name/new" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
            <Icon name="plus" />
            {t("tree.addFile")}
          </Link>
        </MenuItem>
      )}
      {repo.permission === "admin" && (
        <>
          <MenuSeparator />
          <MenuItem asChild>
            <Link to="/$owner/$name/settings" params={{ owner, name }}>
              <Icon name="gear" />
              {tr("settings.link")}
            </Link>
          </MenuItem>
        </>
      )}
    </Menu>
  );
}

/** File list (rounds 015 B / 018 A): a surface with the latest commit as a strip over the rows on --bg. */
function FileBox({ repo, tree, refName, path }: { repo: Repo; tree: Tree; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const last = latest(tree.entries);
  return (
    <div className="ui-box rp-files">
      {last && <LastCommit repo={repo} commit={last} refName={refName} path={path} />}
      <div className="ui-in">
        <table className="rp-tbl">
          <caption className="sr-only">{t("tree.files")}</caption>
          <colgroup>
            <col className="rp-c1" />
            <col className="rp-c2" />
            <col className="rp-c3" />
          </colgroup>
          <thead className="sr-only">
            <tr>
              <th scope="col">{t("tree.colName")}</th>
              <th scope="col">{t("tree.colMessage")}</th>
              <th scope="col">{t("tree.colDate")}</th>
            </tr>
          </thead>
          <tbody>
            {path !== "" && (
              <tr className="k-folder">
                <td className="rp-n">
                  <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName, path: dirOf(path) || undefined }} className="rp-nm">
                    <Icon name="folder" className="ui-icon rp-i" />
                    <span>..</span>
                  </Link>
                </td>
                <td className="rp-cm" />
                <td className="rp-tm" />
              </tr>
            )}
            {tree.entries.map((e) => (
              <tr key={e.path} className={e.type === "tree" ? "k-folder" : undefined}>
                <td className="rp-n">
                  <EntryLink owner={owner} name={name} refName={refName} entry={e} lead={<Icon name={e.type === "tree" || e.type === "submodule" ? "folder" : "file"} className="ui-icon rp-i" />} />
                </td>
                <td className="rp-cm">
                  {e.lastCommit && (
                    <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: e.lastCommit.sha }}>
                      {e.lastCommit.summary}
                    </Link>
                  )}
                </td>
                <td className="rp-tm">{e.lastCommit && <When iso={e.lastCommit.date} />}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {tree.entries.length === 0 && <p className="ui-note acc-pad">{t("tree.empty")}</p>}
        {tree.truncated && <p className="ui-note acc-pad">{t("tree.truncated")}</p>}
      </div>
    </div>
  );
}

/** Strip with the latest commit of a folder or file: author, message, sha, time, History. */
export function LastCommit({ repo, commit: c, refName, path }: { repo: Repo; commit: { sha: string; summary: string; author: string; date: string }; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  return (
    <div className="ui-box-h rp-lc">
      <Initial name={c.author} size={22} />
      <span className="rp-lcm">
        <b>{c.author}</b>
        <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: c.sha }}>
          {c.summary}
        </Link>
      </span>
      <Link className="rp-hash" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: c.sha }}>
        {shortSha(c.sha)}
      </Link>
      <span className="ui-tm rp-lct">
        <When iso={c.date} />
      </span>
      <Link className="ui-btn ui-btn-ghost rp-hist" to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
        <Icon name="clock" />
        <span className="cx-lbl">{t("tree.history")}</span>
      </Link>
    </div>
  );
}

function TreeCard({ repo, tree, refName, path }: { repo: Repo; tree: Tree; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const writable = canWrite(repo.permission) && tree.ref.kind === "branch";
  return (
    <Card
      title={t("tree.files")}
      actions={
        writable && (
          <Link to="/$owner/$name/new" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
            <Button size="sm" icon="plus">
              {t("tree.addFile")}
            </Button>
          </Link>
        )
      }
    >
      <TreeList repo={repo} tree={tree} refName={refName} path={path} />
    </Card>
  );
}

function TreeList({ repo, tree, refName, path }: { repo: Repo; tree: Tree; refName: string; path: string }): ReactNode {
  const t = useT("code");
  const { owner, name } = repo;
  const last = latest(tree.entries);
  return (
    <>
      {last && (
        <div className="rp-last">
          <Icon name="commit" />
          <b>{last.author}</b>
          <Link className="rp-last-msg" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: last.sha }}>
            {last.summary}
          </Link>
          <code className="ui-mono">{shortSha(last.sha)}</code>
          <When iso={last.date} />
        </div>
      )}
      <ul className="rp-tree" aria-label={t("tree.files")}>
        {path !== "" && (
          <li className="rp-row k-folder">
            <Icon name="folder" className="ui-icon rp-i" />
            <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName, path: dirOf(path) || undefined }} className="rp-nm">
              ..
            </Link>
          </li>
        )}
        {tree.entries.map((e) => (
          <li key={e.path} className={e.type === "tree" ? "rp-row k-folder" : "rp-row k-file"}>
            <Icon name={e.type === "tree" || e.type === "submodule" ? "folder" : "file"} className="ui-icon rp-i" />
            <EntryLink owner={owner} name={name} refName={refName} entry={e} />
            {e.lastCommit ? (
              <Link className="rp-msg" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: e.lastCommit.sha }}>
                {e.lastCommit.summary}
              </Link>
            ) : (
              <span className="rp-msg" />
            )}
            {e.lastCommit ? <When iso={e.lastCommit.date} /> : <span />}
          </li>
        ))}
      </ul>
      {tree.entries.length === 0 && <p className="ui-note acc-pad">{t("tree.empty")}</p>}
      {tree.truncated && <p className="ui-note acc-pad">{t("tree.truncated")}</p>}
    </>
  );
}

function EntryLink({ owner, name, refName, entry: e, lead }: { owner: string; name: string; refName: string; entry: TreeEntry; lead?: ReactNode }) {
  const t = useT("code");
  if (e.type === "tree") {
    return (
      <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName, path: e.path }} className="rp-nm">
        {lead}
        <span>{e.name}</span>
      </Link>
    );
  }
  if (e.type === "submodule") {
    return (
      <span className="rp-nm" title={t("tree.submodule")}>
        {lead}
        <span>
          {e.name} <code className="ui-mono">@{shortSha(e.oid)}</code>
        </span>
      </span>
    );
  }
  return (
    <Link to="/$owner/$name/blob" params={{ owner, name }} search={{ ref: refName, path: e.path }} className="rp-nm">
      {lead}
      <span>{e.name}</span>
    </Link>
  );
}
