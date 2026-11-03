import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { canWrite, useBlob, useEditFile, useTree, type BlobFile, type ResolvedRef } from "../../api/code.ts";
import { ApiError } from "../../api/http.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Dialog, EmptyState, Input } from "../../ui/index.ts";
import { Editor } from "../../ui/code/Editor.tsx";
import { useNotify } from "../account/common.tsx";
import { CommitFields, emptyTarget, targetBody } from "./CommitForm.tsx";
import { Crumb, Gate, RepoFrame } from "./common.tsx";

/** Web editor: `edit` changes the file at `path`, `new` creates a file inside directory `path`. */
export function EditPage({ owner, name, refName, path = "", mode }: { owner: string; name: string; refName?: string; path?: string; mode: "edit" | "new" }) {
  return (
    <RepoFrame owner={owner} name={name} tab="code">
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

const writable = (repo: Repo, ref: ResolvedRef) => canWrite(repo.permission) && ref.kind === "branch";

function EditExisting({ repo, refName, path }: { repo: Repo; refName: string; path: string }) {
  const blob = useBlob(repo.owner, repo.name, refName, path);
  return (
    <Gate query={blob}>
      {(b) =>
        !writable(repo, b.ref) || b.binary || b.truncated || b.lfs || b.content === undefined ? (
          <NotAllowed />
        ) : (
          <EditorForm repo={repo} branch={b.ref.name} dir="" existing={b} refetch={async () => (await blob.refetch()).data} />
        )
      }
    </Gate>
  );
}

function EditNew({ repo, refName, dir }: { repo: Repo; refName: string; dir: string }) {
  const tree = useTree(repo.owner, repo.name, refName, dir);
  return <Gate query={tree}>{(tr) => (writable(repo, tr.ref) ? <EditorForm repo={repo} branch={tr.ref.name} dir={dir} /> : <NotAllowed />)}</Gate>;
}

/** Problem with a new file name, or undefined when it is acceptable. */
function nameProblem(name: string): "empty" | "invalid" | undefined {
  const n = name.trim();
  if (n === "") return "empty";
  if (n.startsWith("/") || n.endsWith("/") || n.split("/").some((s) => s === "" || s === "." || s === ".." || s === ".git")) return "invalid";
  return undefined;
}

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
  const edit = useEditFile(repo.owner, repo.name);
  const [base, setBase] = useState({ sha: existing?.oid, content: existing?.content ?? "" });
  const [doc, setDoc] = useState(base.content);
  const [resetKey, setResetKey] = useState(0);
  const [fileName, setFileName] = useState("");
  const [target, setTarget] = useState(emptyTarget);
  const [conflict, setConflict] = useState(false);
  const path = existing ? existing.path : [dir, fileName.trim()].filter(Boolean).join("/");
  const problem = existing ? undefined : nameProblem(fileName);
  const fallback = existing ? t("edit.updateDefault", { path }) : t("edit.createDefault", { path });

  const send = (sha: string | undefined) =>
    edit.mutate(
      { op: existing ? "update" : "create", path, content: doc, encoding: "utf-8", sha, ...targetBody(target, branch, fallback) },
      {
        onSuccess: (res) => {
          notify.ok(t("edit.committed"));
          void navigate({ to: "/$owner/$name/blob", params: { owner: repo.owner, name: repo.name }, search: { ref: res.branch, path } });
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409 && existing) setConflict(true);
          else if (e instanceof ApiError && e.status === 409) notify.fail(new Error(t("edit.exists", { path })));
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

  const invalid = problem !== undefined || (target.mode === "branch" && !target.newBranch.trim());
  return (
    <>
      <div className="rp-bar">
        <Crumb owner={repo.owner} name={repo.name} refName={branch} path={existing ? path : dir} />
      </div>
      <Card title={existing ? t("edit.titleEdit", { path }) : t("edit.titleNew")}>
        {!existing && (
          <div className="acc-pad">
            <Input
              label={t("edit.fileName")}
              hint={dir ? t("edit.fileNameHint", { dir }) : undefined}
              mono
              required
              value={fileName}
              error={fileName !== "" && problem === "invalid" ? t("edit.fileNameInvalid") : undefined}
              onChange={(e) => setFileName(e.target.value)}
            />
          </div>
        )}
        <Editor initial={base.content} path={path || "untitled"} label={t("edit.editorLabel", { path: path || t("edit.titleNew") })} loadingText={t("edit.loading")} onChange={setDoc} resetKey={resetKey} />
      </Card>
      <Card title={t("edit.commitTitle")}>
        <div className="acc-pad">
          <CommitFields value={target} onChange={setTarget} branch={branch} placeholder={fallback} />
          <div className="acc-actions">
            <Button
              onClick={() =>
                void navigate(
                  existing
                    ? { to: "/$owner/$name/blob", params: { owner: repo.owner, name: repo.name }, search: { ref: branch, path } }
                    : { to: "/$owner/$name", params: { owner: repo.owner, name: repo.name }, search: { ref: branch, path: dir || undefined } },
                )
              }
            >
              {t("edit.cancel")}
            </Button>
            <Button variant="primary" icon="commit" disabled={invalid || edit.isPending || (!!existing && doc === base.content)} onClick={() => send(base.sha)}>
              {edit.isPending ? t("edit.committing") : t("edit.commit")}
            </Button>
          </div>
        </div>
      </Card>
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
    </>
  );
}
