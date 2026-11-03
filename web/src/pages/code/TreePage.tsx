import { Link, useNavigate } from "@tanstack/react-router";
import { canWrite, useTree, type Tree, type TreeEntry } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, EmptyState, Icon } from "../../ui/index.ts";
import { CloneDialog, EmptyRepoCard } from "../repos/RepoPage.tsx";
import { ArchiveMenu, Crumb, dirOf, Gate, RefSwitcher, RepoFrame, shortSha, When } from "./common.tsx";
import { Readme } from "./Readme.tsx";

/** Repository home and directory browsing: tree, last commit, README. */
export function TreePage({ owner, name, refName, path = "" }: { owner: string; name: string; refName?: string; path?: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code">
      {(repo) => <TreeBody repo={repo} refName={refName || repo.defaultBranch} path={path} />}
    </RepoFrame>
  );
}

function TreeBody({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const tree = useTree(owner, name, refName, path);
  const navigate = useNavigate();
  const isRoot = path === "";
  const notFound =
    isRoot && refName === repo.defaultBranch ? (
      <EmptyRepoCard repo={repo} />
    ) : (
      <EmptyState icon="folder" title={t("tree.notFound")} description={t("tree.notFoundHint", { ref: refName })} />
    );
  return (
    <>
      <div className="rp-bar">
        <RefSwitcher
          owner={owner}
          name={name}
          current={refName}
          onPick={(ref) => void navigate({ to: "/$owner/$name", params: { owner, name }, search: { ref, path: path || undefined } })}
        />
        <Crumb owner={owner} name={name} refName={refName} path={path} />
        <span className="acc-grow" />
        <Link to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
          <Button icon="clock">{t("tree.history")}</Button>
        </Link>
        <CloneDialog repo={repo} />
        <ArchiveMenu owner={owner} name={name} refName={refName} />
      </div>
      <Gate query={tree} notFound={notFound}>
        {(data) => (
          <>
            <TreeCard repo={repo} tree={data} refName={refName} path={path} />
            {data.readme && <Readme owner={owner} name={name} refName={refName} path={data.readme} />}
          </>
        )}
      </Gate>
    </>
  );
}

function latest(entries: TreeEntry[]) {
  let best: TreeEntry["lastCommit"];
  for (const e of entries) if (e.lastCommit && (!best || e.lastCommit.date > best.date)) best = e.lastCommit;
  return best;
}

function TreeCard({ repo, tree, refName, path }: { repo: Repo; tree: Tree; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const last = latest(tree.entries);
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
    </Card>
  );
}

function EntryLink({ owner, name, refName, entry: e }: { owner: string; name: string; refName: string; entry: TreeEntry }) {
  const t = useT("code");
  if (e.type === "tree") {
    return (
      <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: refName, path: e.path }} className="rp-nm">
        {e.name}
      </Link>
    );
  }
  if (e.type === "submodule") {
    return (
      <span className="rp-nm" title={t("tree.submodule")}>
        {e.name} <code className="ui-mono">@{shortSha(e.oid)}</code>
      </span>
    );
  }
  return (
    <Link to="/$owner/$name/blob" params={{ owner, name }} search={{ ref: refName, path: e.path }} className="rp-nm">
      {e.name}
    </Link>
  );
}
