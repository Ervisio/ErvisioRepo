import { useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { canWrite, useBlob, useEditFile, useTree, type BlobFile, type ResolvedRef } from "../../api/code.ts";
import { ApiError } from "../../api/http.ts";
import { useSession } from "../../api/identity.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, EmptyState, Icon, Input, Menu, MenuItem } from "../../ui/index.ts";
import type { EditorOptions, Indent } from "../../ui/code/cm.ts";
import { DiffBody } from "../../ui/code/DiffView.tsx";
import { Editor } from "../../ui/code/Editor.tsx";
import { lineDiff } from "../../ui/code/linediff.ts";
import { useNotify } from "../account/common.tsx";
import { CommitFields, emptyTarget, targetBody } from "./CommitForm.tsx";
import { splitLines } from "./BlobPage.tsx";
import { Crumb, Gate, RepoFrame } from "./common.tsx";
import { CodeLayout } from "./FileTree.tsx";

/** Web editor: `edit` changes the file at `path`, `new` creates a file inside directory `path`. */
export function EditPage({ owner, name, refName, path = "", mode }: { owner: string; name: string; refName?: string; path?: string; mode: "edit" | "new" }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code" head={false}>
      {(repo) =>
        mode === "edit" ? (
          <EditExisting repo={repo} refName={refName || repo.defaultBranch} path={path} />
        ) : (
          <EditNew repo={repo} refName={refName || repo.defaultBranch} dir={path} />
        )
      }
    </RepoFrame>
  );
}

function NotAllowed() {
  const t = useT("code");
  return <EmptyState icon="lock" title={t("edit.notAllowed")} description={t("edit.notAllowedHint")} />;
}

/** Branch name without refs/heads/, as the editor shows it and the API takes it. */
const shortBranch = (name: string) => name.replace(/^refs\/heads\//, "");

const writable = (repo: Repo, ref: ResolvedRef) => canWrite(repo.permission) && ref.kind === "branch";

function EditExisting({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const blob = useBlob(repo.owner, repo.name, refName, path);
  return (
    <Gate query={blob}>
      {(b) =>
        !writable(repo, b.ref) || b.binary || b.truncated || b.lfs || b.content === undefined ? (
          <NotAllowed />
        ) : (
          <EditorForm repo={repo} branch={shortBranch(b.ref.name)} dir="" existing={b} refetch={async () => (await blob.refetch()).data} />
        )
      }
    </Gate>
  );
}

function EditNew({ repo, refName, dir }: { repo: Repo; refName: string; dir: string }) {
  const tree = useTree(repo.owner, repo.name, refName, dir);
  return <Gate query={tree}>{(tr) => (writable(repo, tr.ref) ? <EditorForm repo={repo} branch={shortBranch(tr.ref.name)} dir={dir} /> : <NotAllowed />)}</Gate>;
}

/** Problem with a new file name, or undefined when it is acceptable. */
function nameProblem(name: string): "empty" | "invalid" | undefined {
  const n = name.trim();
  if (n === "") return "empty";
  if (n.startsWith("/") || n.endsWith("/") || n.split("/").some((s) => s === "" || s === "." || s === ".." || s === ".git")) return "invalid";
  return undefined;
}

const INDENTS: Indent[] = ["tab", 2, 4];

function EditorForm({ repo, branch, dir, existing, refetch }: {
  repo: Repo;
  branch: string;
  dir: string;
  existing?: BlobFile;
  refetch?: () => Promise<BlobFile | undefined>;
}) {
  const t = useT("code");
  const notify = useNotify();
  const navigate = useNavigate();
  const session = useSession();
  const edit = useEditFile(repo.owner, repo.name);
  const [base, setBase] = useState({ sha: existing?.oid, content: existing?.content ?? "" });
  const [doc, setDoc] = useState(base.content);
  const [resetKey, setResetKey] = useState(0);
  const [fileName, setFileName] = useState("");
  const [target, setTarget] = useState(emptyTarget);
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState(false);
  const [options, setOptions] = useState<EditorOptions>({ indent: "tab", wrap: false });
  const [conflict, setConflict] = useState(false);
  const path = existing ? existing.path : [dir, fileName.trim()].filter(Boolean).join("/");
  const shownDir = existing ? existing.path.split("/").slice(0, -1).join("/") : dir;
  const problem = existing ? undefined : nameProblem(fileName);
  const fallback = existing ? t("edit.updateDefault", { path }) : t("edit.createDefault", { path });
  const changes = useMemo(
    () => (preview ? lineDiff(path || "untitled", existing ? splitLines(base.content) : [], splitLines(doc), !existing) : undefined),
    [preview, path, existing, base.content, doc],
  );

  const openSave = () => {
    if (!target.newBranch) setTarget((v) => ({ ...v, newBranch: `${session.data?.username ?? "patch"}-patch-1` }));
    setSaving(true);
  };
  const send = (sha: string | undefined) =>
    edit.mutate(
      { op: existing ? "update" : "create", path, content: doc, encoding: "utf-8", sha, ...targetBody(target, branch, fallback) },
      {
        onSuccess: (res) => {
          setSaving(false);
          notify.ok(t("edit.committed"));
          if (target.mode === "branch") {
            void navigate({ to: "/$owner/$name/compare", params: { owner: repo.owner, name: repo.name }, search: { base: branch, head: res.branch } });
            return;
          }
          void navigate({ to: "/$owner/$name/blob", params: { owner: repo.owner, name: repo.name }, search: { ref: res.branch, path } });
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409 && existing) {
            setSaving(false);
            setConflict(true);
          } else if (e instanceof ApiError && e.status === 409) notify.fail(new Error(t("edit.exists", { path })));
          else notify.fail(e);
        },
      },
    );

  const loadLatest = async () => {
    const latest = await refetch?.();
    setConflict(false);
    if (!latest) return;
    const content = latest.content ?? "";
    setBase({ sha: latest.oid, content });
    setDoc(content);
    setResetKey((k) => k + 1);
  };
  const overwrite = async () => {
    const latest = await refetch?.();
    setConflict(false);
    if (!latest) return;
    setBase((b) => ({ ...b, sha: latest.oid }));
    send(latest.oid);
  };
  const cancel = () =>
    void navigate(
      existing
        ? { to: "/$owner/$name/blob", params: { owner: repo.owner, name: repo.name }, search: { ref: branch, path } }
        : { to: "/$owner/$name", params: { owner: repo.owner, name: repo.name }, search: { ref: branch, path: dir || undefined } },
    );

  const unchanged = !!existing && doc === base.content;
  const invalidTarget = target.mode === "branch" && !target.newBranch.trim();
  return (
    <CodeLayout
      repo={repo}
      refName={branch}
      path={existing ? path : dir}
      isDir={!existing}
      minimal
      top={
        <>
          <Crumb owner={repo.owner} name={repo.name} refName={branch} path={shownDir} trailing linkLast />
          {existing ? (
            <span className="ed-name ui-mono">{existing.path.split("/").pop()}</span>
          ) : (
            <Input
              className="ed-name-field"
              label={t("edit.fileName")}
              hideLabel
              mono
              required
              placeholder={t("edit.fileNamePlaceholder")}
              value={fileName}
              error={fileName !== "" && problem === "invalid" ? t("edit.fileNameInvalid") : undefined}
              onChange={(e) => setFileName(e.target.value)}
            />
          )}
          <span className="ui-note">
            {t("edit.inBranch")} <code className="ui-mono">{branch}</code>
          </span>
          <span className="acc-grow" />
          <Button onClick={cancel}>{t("edit.cancel")}</Button>
          <Button variant="primary" disabled={problem !== undefined || unchanged} onClick={openSave}>
            {t("edit.save")}
          </Button>
        </>
      }
    >
      <div className="ui-box">
        <div className="cx-code">
          <div className="cx-tb">
            <div className="ui-seg" role="group" aria-label={t("edit.viewLabel")}>
              <button type="button" className="ui-segi" aria-pressed={!preview} onClick={() => setPreview(false)}>
                {t("edit.viewEdit")}
              </button>
              <button type="button" className="ui-segi" aria-pressed={preview} onClick={() => setPreview(true)}>
                {t("edit.viewChanges")}
              </button>
            </div>
            <span className="acc-grow" />
            {!preview && (
              <>
                <Menu
                  align="end"
                  label={t("edit.indent")}
                  trigger={
                    <Button variant="ghost">
                      {t("edit.indentValue", { value: options.indent === "tab" ? t("edit.indentTab") : t("edit.indentSpaces", { count: options.indent }) })}
                      <Icon name="down" />
                    </Button>
                  }
                >
                  {INDENTS.map((i) => (
                    <MenuItem key={i} icon={options.indent === i ? "check" : undefined} onSelect={() => setOptions((o) => ({ ...o, indent: i }))}>
                      {i === "tab" ? t("edit.indentTab") : t("edit.indentSpaces", { count: i })}
                    </MenuItem>
                  ))}
                </Menu>
                <Button variant="ghost" aria-pressed={options.wrap} onClick={() => setOptions((o) => ({ ...o, wrap: !o.wrap }))}>
                  {options.wrap ? t("edit.wrapOn") : t("edit.wrapOff")}
                </Button>
              </>
            )}
          </div>
          <div hidden={preview}>
            <Editor
              initial={base.content}
              path={path || "untitled"}
              label={t("edit.editorLabel", { path: path || t("edit.titleNew") })}
              loadingText={t("edit.loading")}
              onChange={setDoc}
              resetKey={resetKey}
              options={options}
            />
          </div>
          {changes && (changes.hunks.length === 0 ? <p className="ui-note acc-pad">{t("edit.noChanges")}</p> : <DiffBody file={changes} />)}
        </div>
      </div>
      <Dialog
        open={saving}
        onOpenChange={setSaving}
        title={t("edit.saveTitle")}
        footer={
          <>
            <DialogClose asChild>
              <Button>{t("edit.cancel")}</Button>
            </DialogClose>
            <Button variant="primary" disabled={invalidTarget || edit.isPending} onClick={() => send(base.sha)}>
              {edit.isPending ? t("edit.committing") : t("edit.saveConfirm")}
            </Button>
          </>
        }
      >
        <CommitFields value={target} onChange={setTarget} branch={branch} placeholder={fallback} pull />
      </Dialog>
      <Dialog
        open={conflict}
        onOpenChange={setConflict}
        title={t("edit.conflictTitle")}
        description={t("edit.conflictHint", { path })}
        footer={
          <>
            <Button onClick={() => void loadLatest()}>{t("edit.loadLatest")}</Button>
            <Button variant="danger" onClick={() => void overwrite()}>
              {t("edit.overwrite")}
            </Button>
          </>
        }
      >
        <p className="ui-note">{t("edit.conflictBody")}</p>
      </Dialog>
    </CodeLayout>
  );
}
