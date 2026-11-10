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
              <section className="rp-files">
                <TreeList repo={repo} tree={data} refName={refName} path={path} />
              </section>
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
