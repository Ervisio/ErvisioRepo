import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { canWrite, rawUrl, useBlame, useBlob, useCommits, useEditFile, type Blame, type BlobFile } from "../../api/code.ts";
import { ApiError } from "../../api/http.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Button, Dialog, DialogClose, Icon, Menu, MenuItem, MenuSeparator } from "../../ui/index.ts";
import { CodeView } from "../../ui/code/CodeView.tsx";
import { highlight, type Token } from "../../ui/code/highlight.ts";
import { useNotify } from "../account/common.tsx";
import { CommitFields, emptyTarget, targetBody } from "./CommitForm.tsx";
import { AuthorName, CopyButton, Crumb, dirOf, formatBytes, Gate, RepoFrame, shortSha, When } from "./common.tsx";
import { CodeLayout } from "./FileTree.tsx";
import { LastCommit } from "./TreePage.tsx";
import { isMarkdown, useInAppLinks, useMarkdownContext } from "./Readme.tsx";

export type BlobMode = "code" | "blame" | "preview";

/** One file: source, blame, Markdown or media; raw and download links. */
export function BlobPage({ owner, name, refName, path, view }: { owner: string; name: string; refName?: string; path: string; view?: BlobMode }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code" head={false}>
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
    <CodeLayout
      repo={repo}
      refName={refName}
      path={path}
      isDir={false}
      onPickRef={(ref) => void navigate({ to: "/$owner/$name/blob", params: { owner, name }, search: { ref, path, view } })}
      top={
        <>
          <Crumb owner={owner} name={name} refName={refName} path={path} />
          <CopyButton value={path} label={t("blob.copyPath")} />
        </>
      }
    >
      <Gate query={blob}>{(b) => <BlobBox repo={repo} blob={b} refName={refName} path={path} view={view} onView={go} />}</Gate>
    </CodeLayout>
  );
}

/** The file's latest commit, from the first page of its history. */
function FileCommit({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const log = useCommits(repo.owner, repo.name, refName, path, 0);
  const c = log.data?.commits[0];
  if (!c) return null;
  return <LastCommit repo={repo} commit={{ sha: c.sha, summary: c.summary, author: c.author.name, authorUser: c.author.user, date: c.author.date }} refName={refName} path={path} />;
}

/** Surface with the latest commit, then the code area: toolbar (view, meta, Raw, copy, download, edit, more) over the content. */
function BlobBox({ repo, blob: b, refName, path, view, onView }: { repo: Repo; blob: BlobFile; refName: string; path: string; view: BlobMode; onView: (v: BlobMode) => void }) {
  const t = useT("code");
  const { owner, name } = repo;
  const [deleting, setDeleting] = useState(false);
  const text = b.content !== undefined && !b.binary;
  const writable = canWrite(repo.permission) && b.ref.kind === "branch";
  const editable = writable && !b.binary && !b.truncated && !b.lfs;
  const raw = rawUrl(owner, name, refName, path);
  const media = mediaKind(b);
  const preview = rawUrl(owner, name, b.ref.commit, b.path, { preview: true });
  const download = media && b.lfs ? preview : raw;
  const showSource = text && !media;
  const meta = [showSource && !b.lfs ? t("blob.lines", { count: splitLines(b.content ?? "").length }) : "", formatBytes(b.lfs?.size ?? b.size)].filter(Boolean).join(" · ");
  return (
    <div className="ui-box rp-blob">
      <FileCommit repo={repo} refName={refName} path={path} />
      <div className="cx-code">
        <div className="cx-tb">
          {showSource && (
            <div className="ui-seg" role="group" aria-label={t("blob.viewLabel")}>
              {(isMarkdown(path) ? (["preview", "code", "blame"] as const) : (["code", "blame"] as const)).map((m) => (
                <button key={m} type="button" className="ui-segi" aria-pressed={view === m} onClick={() => onView(m)}>
                  {t(`blob.view.${m}`)}
                </button>
              ))}
            </div>
          )}
          {!media && <span className="cx-meta">{meta}</span>}
          <span className="acc-grow" />
          <a className="ui-btn ui-btn-ghost" href={raw} target="_blank" rel="noopener">
            {t("blob.raw")}
          </a>
          {showSource && b.content !== undefined && <CopyButton value={b.content} label={t("blob.copyContent")} />}
          <a className="ui-btn ui-btn-ghost rp-ib" href={download} download={path.split("/").pop()} aria-label={t("blob.download")} title={t("blob.download")}>
            <Icon name="download" />
          </a>
          {editable && (
            <Link className="ui-btn ui-btn-ghost rp-ib" to="/$owner/$name/edit" params={{ owner, name }} search={{ ref: refName, path }} aria-label={t("blob.edit")} title={t("blob.edit")}>
              <Icon name="edit" />
            </Link>
          )}
          <Menu align="end" label={t("blob.more")} trigger={<Button icon="dots" variant="ghost" className="rp-ib" aria-label={t("blob.more")} title={t("blob.more")} />}>
            <MenuItem asChild>
              <Link to="/$owner/$name/commits" params={{ owner, name }} search={{ ref: refName, path }}>
                <Icon name="clock" />
                {t("tree.history")}
              </Link>
            </MenuItem>
            {writable && (
              <>
                <MenuSeparator />
                <MenuItem danger icon="trash" onSelect={() => setDeleting(true)}>
                  {t("blob.delete")}
                </MenuItem>
              </>
            )}
          </Menu>
        </div>
        {media ? (
          <MediaPreview key={preview} kind={media} blob={b} src={preview} download={download} />
        ) : (
          <BlobContent repo={repo} blob={b} refName={refName} view={view} />
        )}
      </div>
      {writable && <DeleteFile repo={repo} blob={b} branch={b.ref.name.replace(/^refs\/heads\//, "")} open={deleting} onOpenChange={setDeleting} />}
    </div>
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

function mediaKind(b: BlobFile): "image" | "audio" | "video" | undefined {
  const kind = b.contentType.split("/", 1)[0];
  if (kind === "image" || kind === "audio" || kind === "video") return kind;
}

function MediaPreview({ kind, blob, src, download }: { kind: "image" | "audio" | "video"; blob: BlobFile; src: string; download: string }) {
  const t = useT("code");
  const [failed, setFailed] = useState(false);
  const [showGIF, setShowGIF] = useState(false);
  const gif = blob.contentType === "image/gif";
  const filename = blob.path.split("/").pop();
  const format = filename?.split(".").pop()?.toUpperCase();
  const onError = () => setFailed(true);
  const fallback = (
    <p className="ui-note" role="status">
      {t("blob.mediaUnavailable")} <a href={download} download={filename}>{t("blob.download")}</a>
    </p>
  );
  return (
    <div className="cx-media">
      <div className="cx-media-stage">
        {gif && !failed && (
          <Button onClick={() => setShowGIF(!showGIF)}>{t(showGIF ? "blob.hideGIF" : "blob.showGIF")}</Button>
        )}
        {failed ? fallback : kind === "video" ? (
          <video src={src} controls playsInline preload="metadata" aria-label={filename} onError={onError}>{fallback}</video>
        ) : kind === "audio" ? (
          <audio src={src} controls preload="metadata" aria-label={filename} onError={onError}>{fallback}</audio>
        ) : (!gif || showGIF) && (
          <img src={src} alt={blob.path} onError={onError} />
        )}
      </div>
      <dl className="cx-media-details">
        <div><dt>{t("blob.mediaType")}</dt><dd>{format}</dd></div>
        <div><dt>{t("blob.mediaSize")}</dt><dd>{formatBytes(blob.lfs?.size ?? blob.size)}</dd></div>
      </dl>
    </div>
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
    <div className="rp-rb" onClick={onClick}>
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
            <span className="cv-ann-who">
              <AuthorName name={r.commit.author} user={r.commit.authorUser} />
            </span>
            <When iso={r.commit.date} />
          </>
        );
      }}
    />
  );
}

function DeleteFile({ repo, blob, branch, open, onOpenChange }: { repo: Repo; blob: BlobFile; branch: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT("code");
  const notify = useNotify();
  const edit = useEditFile(repo.owner, repo.name);
  const navigate = useNavigate();
  const [target, setTarget] = useState(emptyTarget);
  const fallback = t("edit.deleteDefault", { path: blob.path });
  const submit = () =>
    edit.mutate(
      { op: "delete", path: blob.path, sha: blob.oid, encoding: "utf-8", ...targetBody(target, branch, fallback) },
      {
        onSuccess: (res) => {
          onOpenChange(false);
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
      onOpenChange={onOpenChange}
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
