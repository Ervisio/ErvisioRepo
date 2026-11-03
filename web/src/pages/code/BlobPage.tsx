import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { canWrite, rawUrl, useBlame, useBlob, useEditFile, type Blame, type BlobFile } from "../../api/code.ts";
import { ApiError } from "../../api/http.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Button, Card, Dialog, DialogClose } from "../../ui/index.ts";
import { CodeView } from "../../ui/code/CodeView.tsx";
import { highlight, type Token } from "../../ui/code/highlight.ts";
import { useNotify } from "../account/common.tsx";
import { CommitFields, emptyTarget, targetBody } from "./CommitForm.tsx";
import { Crumb, dirOf, formatBytes, Gate, RefSwitcher, RepoFrame, shortSha, When } from "./common.tsx";
import { isMarkdown, useInAppLinks, useMarkdownContext } from "./Readme.tsx";

export type BlobMode = "code" | "blame" | "preview";

/** One file: highlighted source, blame, or rendered Markdown; raw and download links. */
export function BlobPage({ owner, name, refName, path, view }: { owner: string; name: string; refName?: string; path: string; view?: BlobMode }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code">
      {(repo) => <BlobBody repo={repo} refName={refName || repo.defaultBranch} path={path} view={view ?? (isMarkdown(path) ? "preview" : "code")} />}
    </RepoFrame>
  );
}

/** Splits file content into lines; a final newline does not add an empty line. */
export function splitLines(s: string): string[] {
  const lines = s.split("\n");
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return lines.map((l) => (l.endsWith("\r") ? l.slice(0, -1) : l));
}

/** Tokens for `code`, computed lazily; undefined until ready or when not highlightable. */
export function useHighlight(code: string | undefined, path: string): Token[][] | undefined {
  const [state, setState] = useState<{ code?: string; tokens?: Token[][] }>({});
  useEffect(() => {
    if (code === undefined) return;
    let live = true;
    highlight(code, path).then(
      (tokens) => live && setState({ code, tokens }),
      () => live && setState({ code }),
    );
    return () => {
      live = false;
    };
  }, [code, path]);
  return state.code === code ? state.tokens : undefined;
}

function lineFromHash(): number | undefined {
  const m = /^#L(\d+)$/.exec(window.location.hash);
  return m ? Number(m[1]) : undefined;
}

function BlobBody({ repo, refName, path, view }: { repo: Repo; refName: string; path: string; view: BlobMode }) {
  const t = useT("code");
  const { owner, name } = repo;
  const blob = useBlob(owner, name, refName, path);
  const navigate = useNavigate();
  const go = (v: BlobMode) => void navigate({ to: "/$owner/$name/blob", params: { owner, name }, search: { ref: refName, path, view: v } });
  return (
    <>
      <div className="rp-bar">
        <RefSwitcher
          owner={owner}
          name={name}
          current={refName}
          onPick={(ref) => void navigate({ to: "/$owner/$name/blob", params: { owner, name }, search: { ref, path, view } })}
        />
        <Crumb owner={owner} name={name} refName={refName} path={path} />
      </div>
      <Gate query={blob}>
        {(b) => (
          <Card
            className="rp-file"
            title={undefined}
            actions={undefined}
          >
            <div className="rp-fh">
              <span className="ui-mono">{formatBytes(b.size)}</span>
              {b.content !== undefined && !b.binary && <span className="ui-note">{t("blob.lines", { count: splitLines(b.content).length })}</span>}
              <span className="acc-grow" />
              {!b.binary && b.content !== undefined && (
                <div className="ui-tablist" role="group" aria-label={t("blob.viewLabel")}>
                  {(isMarkdown(path) ? (["preview", "code", "blame"] as const) : (["code", "blame"] as const)).map((m) => (
                    <button key={m} type="button" className="ui-tab" data-state={view === m ? "active" : "inactive"} aria-pressed={view === m} onClick={() => go(m)}>
                      {t(`blob.view.${m}`)}
                    </button>
                  ))}
                </div>
              )}
              <Link to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path }}>
                <Button size="sm" icon="clock">
                  {t("tree.history")}
                </Button>
              </Link>
              <a className="ui-btn ui-btn-secondary ui-btn-sm" href={rawUrl(owner, name, refName, path)} target="_blank" rel="noopener">
                {t("blob.raw")}
              </a>
              <a className="ui-btn ui-btn-secondary ui-btn-sm" href={rawUrl(owner, name, refName, path)} download={path.split("/").pop()} aria-label={t("blob.download")}>
                {t("blob.download")}
              </a>
              {canWrite(repo.permission) && b.ref.kind === "branch" && (
                <>
                  {!b.binary && !b.truncated && !b.lfs && (
                    <Link to="/$owner/$name/edit" params={{ owner, name }} search={{ ref: refName, path }}>
                      <Button size="sm" icon="edit">
                        {t("blob.edit")}
                      </Button>
                    </Link>
                  )}
                  <DeleteFile repo={repo} blob={b} branch={b.ref.name} />
                </>
              )}
            </div>
            <BlobContent repo={repo} blob={b} refName={refName} view={view} />
          </Card>
        )}
      </Gate>
    </>
  );
}

function BlobContent({ repo, blob: b, refName, view }: { repo: Repo; blob: BlobFile; refName: string; view: BlobMode }) {
  const t = useT("code");
  const { owner, name } = repo;
  const raw = rawUrl(owner, name, refName, b.path);
  if (b.lfs) {
    return (
      <p className="ui-note acc-pad">
        {t("blob.lfs", { size: formatBytes(b.lfs.size) })} <code className="ui-mono">{b.lfs.oid}</code>
      </p>
    );
  }
  if (b.binary || b.content === undefined) {
    if (b.contentType.startsWith("image/")) {
      return (
        <div className="rp-img">
          <img src={raw} alt={b.path} />
        </div>
      );
    }
    return (
      <p className="ui-note acc-pad">
        {t("blob.binary")}{" "}
        <a href={raw} download={b.path.split("/").pop()}>
          {t("blob.download")}
        </a>
      </p>
    );
  }
  return (
    <>
      {b.truncated && (
        <p className="ui-note acc-pad" role="status">
          {t("blob.truncated")}{" "}
          <a href={raw} target="_blank" rel="noopener">
            {t("blob.raw")}
          </a>
        </p>
      )}
      {view === "blame" ? (
        <BlameView repo={repo} refName={refName} path={b.path} />
      ) : view === "preview" ? (
        <Preview repo={repo} refName={refName} path={b.path} content={b.content} />
      ) : (
        <Source content={b.content} path={b.path} />
      )}
    </>
  );
}

function Source({ content, path }: { content: string; path: string }) {
  const t = useT("code");
  const lines = useMemo(() => splitLines(content), [content]);
  const tokens = useHighlight(content, path);
  const [sel, setSel] = useState(lineFromHash);
  return (
    <CodeView
      label={t("blob.codeLabel", { path })}
      lines={lines}
      tokens={tokens}
      selected={sel}
      onSelect={(n) => {
        setSel(n);
        window.history.replaceState(window.history.state, "", `#L${n}`);
      }}
    />
  );
}

function Preview({ repo, refName, path, content }: { repo: Repo; refName: string; path: string; content: string }) {
  const ctx = useMarkdownContext(repo.owner, repo.name, refName, path);
  const onClick = useInAppLinks();
  return (
    <div onClick={onClick}>
      <Markdown source={content} ctx={ctx} className="rp-md" />
    </div>
  );
}

function BlameView({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const t = useT("code");
  const blame = useBlame(repo.owner, repo.name, refName, path, true);
  return <Gate query={blame}>{(b) => <BlameLines repo={repo} blame={b} path={path} label={t("blob.blameLabel", { path })} />}</Gate>;
}

function BlameLines({ repo, blame, path, label }: { repo: Repo; blame: Blame; path: string; label: string }) {
  // For every line, the range that starts on it (or -1 for continuation lines).
  const starts = useMemo(() => {
    const a = new Int32Array(blame.lines.length).fill(-1);
    blame.ranges.forEach((r, i) => {
      if (r.startLine >= 1 && r.startLine <= a.length) a[r.startLine - 1] = i;
    });
    return a;
  }, [blame]);
  const content = useMemo(() => blame.lines.join("\n"), [blame]);
  const tokens = useHighlight(content, path);
  return (
    <CodeView
      label={label}
      lines={blame.lines}
      tokens={tokens}
      annotate={(i) => {
        const r = blame.ranges[starts[i] ?? -1];
        if (!r) return null;
        return (
          <>
            <Link to="/$owner/$name/commit/$sha" params={{ owner: repo.owner, name: repo.name, sha: r.commit.sha }} title={r.commit.summary}>
              {shortSha(r.commit.sha)}
            </Link>
            <span className="cv-ann-who">{r.commit.author}</span>
            <When iso={r.commit.date} />
          </>
        );
      }}
    />
  );
}

function DeleteFile({ repo, blob, branch }: { repo: Repo; blob: BlobFile; branch: string }) {
  const t = useT("code");
  const notify = useNotify();
  const edit = useEditFile(repo.owner, repo.name);
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [target, setTarget] = useState(emptyTarget);
  const fallback = t("edit.deleteDefault", { path: blob.path });
  const submit = () =>
    edit.mutate(
      { op: "delete", path: blob.path, sha: blob.oid, encoding: "utf-8", ...targetBody(target, branch, fallback) },
      {
        onSuccess: (res) => {
          setOpen(false);
          notify.ok(t("edit.deleted"));
          const dir = dirOf(blob.path);
          void navigate({ to: "/$owner/$name", params: { owner: repo.owner, name: repo.name }, search: { ref: res.branch, path: dir || undefined } });
        },
        onError: (e) => notify.fail(e instanceof ApiError && e.status === 409 ? new Error(t("edit.staleDelete")) : e),
      },
    );
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <Button size="sm" variant="danger" icon="trash">
          {t("blob.delete")}
        </Button>
      }
      title={t("edit.deleteTitle", { path: blob.path })}
      description={t("edit.deleteHint")}
      footer={
        <>
          <DialogClose asChild>
            <Button>{t("edit.cancel")}</Button>
          </DialogClose>
          <Button variant="danger" disabled={edit.isPending || (target.mode === "branch" && !target.newBranch.trim())} onClick={submit}>
            {t("edit.deleteConfirm")}
          </Button>
        </>
      }
    >
      <CommitFields value={target} onChange={setTarget} branch={branch} placeholder={fallback} />
    </Dialog>
  );
}
