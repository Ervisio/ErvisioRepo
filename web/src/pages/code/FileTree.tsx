import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { canWrite, useTreeNames, type Tree, type TreeEntry } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon } from "../../ui/index.ts";
import { RefSwitcher } from "./common.tsx";

const TREE_KEY = "erepo.codeTree";
const NARROW = "(max-width: 900px)";
const FILTER_LIMIT = 50;

function useNarrow(): boolean {
  return useSyncExternalStore(
    (fn) => {
      const mq = window.matchMedia(NARROW);
      mq.addEventListener("change", fn);
      return () => mq.removeEventListener("change", fn);
    },
    () => window.matchMedia(NARROW).matches,
  );
}

function readClosed(): boolean {
  try {
    return localStorage.getItem(TREE_KEY) === "closed";
  } catch {
    return false;
  }
}

/** Directories that contain `path` ("a", "a/b" for "a/b/c"). */
function ancestors(path: string): string[] {
  const parts = path.split("/").filter(Boolean);
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join("/"));
}

/**
 * Code pages with the file tree on the left (round 018 A): a sticky surface with close,
 * Add file, the branch picker, "Go to file" (t) and the folders, lazily loaded. Below
 * 900 px the tree opens from a button in the top row. `top` is that row's content.
 */
export function CodeLayout({ repo, refName, path, isDir, onPickRef, minimal, top, children }: {
  repo: Repo;
  refName: string;
  path: string;
  isDir: boolean;
  onPickRef?: (ref: string) => void;
  /** The editor: no picker, no Add file, no filter. */
  minimal?: boolean;
  top: ReactNode;
  children: ReactNode;
}) {
  const t = useT("code");
  const narrow = useNarrow();
  const [closed, setClosed] = useState(readClosed);
  const [mobileOpen, setMobileOpen] = useState(false);
  const shown = narrow ? mobileOpen : !closed;
  const toggle = (open: boolean) => {
    if (narrow) {
      setMobileOpen(open);
      return;
    }
    setClosed(!open);
    try {
      localStorage.setItem(TREE_KEY, open ? "open" : "closed");
    } catch {
      // storage unavailable: the choice lasts for this page only
    }
  };
  return (
    <div className="cx" data-tree={shown ? "open" : "closed"}>
      {shown && (
        <FileTree
          repo={repo}
          refName={refName}
          path={path}
          isDir={isDir}
          onPickRef={onPickRef}
          minimal={minimal}
          onClose={() => toggle(false)}
        />
      )}
      <div className="cx-main">
        <div className="cx-top">
          {!shown && (
            <Button icon="layers" className="rp-ib" aria-label={t("tree.open")} title={t("tree.open")} aria-expanded={false} onClick={() => toggle(true)} />
          )}
          {top}
        </div>
        {children}
      </div>
    </div>
  );
}

function FileTree({ repo, refName, path, isDir, onPickRef, minimal, onClose }: {
  repo: Repo;
  refName: string;
  path: string;
  isDir: boolean;
  onPickRef?: (ref: string) => void;
  minimal?: boolean;
  onClose: () => void;
}) {
  const t = useT("code");
  const { owner, name } = repo;
  const root = useTreeNames(owner, name, refName, "");
  const [filter, setFilter] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const openByDefault = new Set([...ancestors(path), ...(isDir && path ? [path] : [])]);
  const [toggled, setToggled] = useState<Map<string, boolean>>(() => new Map());
  const isOpen = (dir: string) => toggled.get(dir) ?? openByDefault.has(dir);
  const flip = (dir: string) => setToggled((m) => new Map(m).set(dir, !isOpen(dir)));
  const writable = canWrite(repo.permission) && root.data?.ref.kind === "branch";

  // "t" jumps to the filter, as on GitHub; never while typing somewhere else.
  useEffect(() => {
    if (minimal) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "t" || e.metaKey || e.ctrlKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el && (el.isContentEditable || el.closest("input, textarea, select, [contenteditable]"))) return;
      e.preventDefault();
      input.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [minimal]);

  return (
    <aside className="cx-tree" aria-label={t("tree.files")}>
      <div className="cx-th">
        <Button icon="layers" variant="ghost" className="rp-ib" aria-label={t("tree.close")} title={t("tree.close")} aria-expanded onClick={onClose} />
        <b>{t("tree.files")}</b>
        {!minimal && writable && (
          <Link
            to="/$owner/$name/new"
            params={{ owner, name }}
            search={{ ref: refName, path: (isDir ? path : path.split("/").slice(0, -1).join("/")) || undefined }}
            className="ui-btn ui-btn-secondary rp-ib"
            aria-label={t("tree.addFile")}
            title={t("tree.addFile")}
          >
            <Icon name="plus" />
          </Link>
        )}
      </div>
      {!minimal && (
        <>
          {onPickRef && (
            <div className="cx-ref">
              <RefSwitcher owner={owner} name={name} current={refName} onPick={onPickRef} />
            </div>
          )}
          <label className="cx-find">
            <Icon name="search" />
            <input ref={input} type="search" value={filter} placeholder={t("tree.goToFile")} aria-label={t("tree.goToFile")} onChange={(e) => setFilter(e.target.value)} />
            <kbd aria-hidden="true">t</kbd>
          </label>
        </>
      )}
      {filter.trim() ? (
        <Filtered repo={repo} refName={refName} filter={filter} current={path} />
      ) : (
        <ul className="cx-tl" role="list">
          {root.data && <Entries repo={repo} refName={refName} tree={root.data} level={0} current={path} isOpen={isOpen} onFlip={flip} />}
        </ul>
      )}
      {root.isPending && root.fetchStatus !== "idle" && <p className="ui-note acc-pad">…</p>}
    </aside>
  );
}

function Entries({ repo, refName, tree, level, current, isOpen, onFlip }: {
  repo: Repo;
  refName: string;
  tree: Tree;
  level: number;
  current: string;
  isOpen: (dir: string) => boolean;
  onFlip: (dir: string) => void;
}) {
  return (
    <>
      {tree.entries.map((e) =>
        e.type === "tree" ? (
          <Dir key={e.path} repo={repo} refName={refName} entry={e} level={level} current={current} isOpen={isOpen} onFlip={onFlip} />
        ) : (
          <li key={e.path}>
            <Node repo={repo} refName={refName} entry={e} level={level} current={current} />
          </li>
        ),
      )}
    </>
  );
}

function Dir({ repo, refName, entry, level, current, isOpen, onFlip }: {
  repo: Repo;
  refName: string;
  entry: TreeEntry;
  level: number;
  current: string;
  isOpen: (dir: string) => boolean;
  onFlip: (dir: string) => void;
}) {
  const t = useT("code");
  const open = isOpen(entry.path);
  const sub = useTreeNames(repo.owner, repo.name, refName, entry.path, open);
  return (
    <li>
      <div className="cx-tn" style={{ paddingLeft: 4 + level * 16 }}>
        <button
          type="button"
          className="cx-cv"
          aria-expanded={open}
          aria-label={open ? t("tree.collapse", { name: entry.name }) : t("tree.expand", { name: entry.name })}
          onClick={() => onFlip(entry.path)}
        >
          <Icon name={open ? "down" : "right"} />
        </button>
        <Link
          to="/$owner/$name"
          params={{ owner: repo.owner, name: repo.name }}
          search={{ ref: refName, path: entry.path }}
          className="cx-tlink"
          aria-current={current === entry.path ? "page" : undefined}
        >
          <Icon name="folder" className="ui-icon cx-dir" />
          <span>{entry.name}</span>
        </Link>
      </div>
      {open && sub.data && (
        <ul className="cx-tl" role="list">
          <Entries repo={repo} refName={refName} tree={sub.data} level={level + 1} current={current} isOpen={isOpen} onFlip={onFlip} />
        </ul>
      )}
    </li>
  );
}

function Node({ repo, refName, entry: e, level, current, full }: { repo: Repo; refName: string; entry: TreeEntry; level: number; current: string; full?: boolean }) {
  const label = full ? e.path : e.name;
  const pad = { paddingLeft: 4 + level * 16 + (full ? 0 : 22) };
  if (e.type === "submodule") {
    return (
      <div className="cx-tn" style={pad}>
        <span className="cx-tlink">
          <Icon name="folder" className="ui-icon" />
          <span>{label}</span>
        </span>
      </div>
    );
  }
  return (
    <div className="cx-tn" style={pad}>
      <Link
        to="/$owner/$name/blob"
        params={{ owner: repo.owner, name: repo.name }}
        search={{ ref: refName, path: e.path }}
        className="cx-tlink"
        aria-current={current === e.path ? "page" : undefined}
      >
        <Icon name="file" className="ui-icon" />
        <span>{label}</span>
      </Link>
    </div>
  );
}

/** "Go to file": matches among the folders already loaded in the tree. */
function Filtered({ repo, refName, filter, current }: { repo: Repo; refName: string; filter: string; current: string }) {
  const t = useT("code");
  const qc = useQueryClient();
  const q = filter.trim().toLowerCase();
  const seen = new Map<string, TreeEntry>();
  for (const [, data] of qc.getQueriesData<Tree>({ queryKey: ["code", repo.owner, repo.name, "tree-names", refName] })) {
    for (const e of data?.entries ?? []) if (e.type !== "tree" && e.path.toLowerCase().includes(q)) seen.set(e.path, e);
  }
  const hits = [...seen.values()].sort((a, b) => a.path.localeCompare(b.path)).slice(0, FILTER_LIMIT);
  if (hits.length === 0) return <p className="ui-note acc-pad">{t("tree.noMatch")}</p>;
  return (
    <ul className="cx-tl" role="list">
      {hits.map((e) => (
        <li key={e.path}>
          <Node repo={repo} refName={refName} entry={e} level={0} current={current} full />
        </li>
      ))}
    </ul>
  );
}
