import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import { canWrite, useTree, type Tree, type TreeEntry } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { archiveUrl } from "../../api/code.ts";
import { Button, Card, EmptyState, Icon, Menu, MenuItem, MenuSeparator } from "../../ui/index.ts";
import { useLayout } from "../../ui/shell/layout.ts";
import { usePinToggle } from "../repos/PinButton.tsx";
import { ForkDialog } from "../repos/RepoList.tsx";
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
  return (
    <>
      {console_ && isRoot && repo.description && <p className="rp-desc">{repo.description}</p>}
      {console_ ? (
        <div className="rp-bar">
          {switcher}
          {path !== "" && <Crumb owner={owner} name={name} refName={refName} path={path} />}
          <span className="acc-grow" />
          <CloneDialog repo={repo} primary />
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
          <CloneDialog repo={repo} />
          <ArchiveMenu owner={owner} name={name} refName={refName} />
        </div>
      )}
      <Gate query={tree} notFound={notFound}>
        {(data) => (
          <>
            {console_ ? (
              <FileBox repo={repo} tree={data} refName={refName} path={path} />
            ) : (
              <TreeCard repo={repo} tree={data} refName={refName} path={path} />
            )}
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

/** Repository actions behind one menu: history, archive, add file, pin, fork, settings. */
function RepoMore({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const tr = useT("repos");
  const { owner, name } = repo;
  const pin = usePinToggle(owner, name);
  const [forking, setForking] = useState(false);
  const writable = canWrite(repo.permission);
  return (
    <>
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
        {(["zip", "tar.gz"] as const).map((f) => (
          <MenuItem key={f} asChild>
            <a href={archiveUrl(owner, name, refName, f)} download>
              <Icon name="download" />
              {t(`archive.${f === "zip" ? "zip" : "tgz"}`)}
            </a>
          </MenuItem>
        ))}
        <MenuSeparator />
        {pin && (
          <MenuItem icon="star" onSelect={pin.toggle}>
            {pin.label}
          </MenuItem>
        )}
        <MenuItem icon="branch" onSelect={() => setForking(true)}>
          {tr("fork.button")}
        </MenuItem>
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
      <ForkDialog repo={repo} open={forking} onOpenChange={setForking} />
    </>
  );
}

/** Console file browser: the latest commit as a header strip over a fixed-column table. */
function FileBox({ repo, tree, refName, path }: { repo: Repo; tree: Tree; refName: string; path: string }) {
  const t = useT("code");
  const { owner, name } = repo;
  const last = latest(tree.entries);
  return (
    <div className="rp-box">
      {last && (
        <div className="rp-head">
          <span className="rp-who">
            <span className="rp-av" aria-hidden="true">
              {last.author.slice(0, 1).toUpperCase()}
            </span>
            <b>{last.author}</b>
          </span>
          <Link className="rp-hmsg" to="/$owner/$name/commit/$sha" params={{ owner, name, sha: last.sha }}>
            {last.summary}
          </Link>
          <span className="rp-hm">
            <code className="rp-hash">{shortSha(last.sha)}</code>
            <When iso={last.date} />
            <Link className="rp-hist" to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path: path || undefined }}>
              <Icon name="clock" />
              {t("tree.history")}
            </Link>
          </span>
        </div>
      )}
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
