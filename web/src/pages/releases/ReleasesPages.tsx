import { Link, useNavigate } from "@tanstack/react-router";
import { useRef, useState, type FormEvent } from "react";
import { canWrite, PAGE_SIZE } from "../../api/code.ts";
import { useSession } from "../../api/identity.ts";
import { useSetSubjectSubscription, useSubjectSubscription } from "../../api/notifications.ts";
import type { Repo } from "../../api/repos.ts";
import {
  assetDownloadUrl,
  uploadAsset,
  useCreateRelease,
  useDeleteAsset,
  useDeleteRelease,
  useInvalidateReleases,
  useRelease,
  useReleases,
  useUpdateRelease,
  type Release,
} from "../../api/releases.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Badge, Button, Card, Dialog, DialogClose, EmptyState, Input, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
import { formatDate, useNotify } from "../account/common.tsx";
import { formatBytes, Gate, Pager, RepoFrame, shortSha, When } from "../code/common.tsx";
import { useInAppLinks, useMarkdownContext } from "../code/Readme.tsx";

function ReleaseBadges({ r }: { r: Release }) {
  const t = useT("releases");
  return (
    <span className="ui-inline">
      {r.latest && <Badge tone="ok">{t("badge.latest")}</Badge>}
      {r.draft && <Badge tone="warn">{t("badge.draft")}</Badge>}
      {r.prerelease && <Badge tone="info">{t("badge.prerelease")}</Badge>}
    </span>
  );
}

/** Release notes rendered with links resolved against the tag. */
function Notes({ repo, r }: { repo: Repo; r: Release }) {
  const ctx = useMarkdownContext(repo.owner, repo.name, r.commit || r.tag, "");
  const onClick = useInAppLinks();
  if (!r.body.trim()) return null;
  return (
    <div onClick={onClick}>
      <Markdown source={r.body} ctx={ctx} className="rp-md" />
    </div>
  );
}

/** Paginated release list, newest first. */
export function ReleasesPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  const t = useT("releases");
  return (
    <RepoFrame owner={owner} name={name} tab="releases" section="pkg">
      {(repo) => (
        <>
          {canWrite(repo.permission) && (
            <div className="rp-bar">
              <span className="acc-grow" />
              <Link to="/$owner/$name/releases/new" params={{ owner, name }}>
                <Button variant="primary" icon="plus">
                  {t("list.new")}
                </Button>
              </Link>
            </div>
          )}
          <ReleaseList repo={repo} page={page} />
        </>
      )}
    </RepoFrame>
  );
}

function ReleaseList({ repo, page }: { repo: Repo; page: number }) {
  const t = useT("releases");
  const { owner, name } = repo;
  const navigate = useNavigate();
  const list = useReleases(owner, name, page - 1);
  return (
    <Gate query={list}>
      {(rows) =>
        rows.length === 0 && page === 1 ? (
          <EmptyState icon="pkg" title={t("list.empty")} description={t("list.emptyHint")} />
        ) : (
          <>
            {rows.map((r) => (
              <Card
                key={r.id}
                className="rl-card"
                title={r.name || r.tag}
                actions={<ReleaseBadges r={r} />}
              >
                <div className="rl-meta">
                  <code className="ui-mono">{r.tag}</code>
                  {r.author && <span>{r.author}</span>}
                  <When iso={r.publishedAt ?? r.createdAt} />
                  <span>{t("list.assets", { count: r.assets.length })}</span>
                  <span className="acc-grow" />
                  <Link to="/$owner/$name/releases/$id" params={{ owner, name, id: r.id }}>
                    {t("list.open")}
                  </Link>
                </div>
                <div className="rl-body">
                  <Notes repo={repo} r={r} />
                </div>
              </Card>
            ))}
            <Pager
              page={page}
              hasMore={rows.length === PAGE_SIZE}
              onPage={(n) => void navigate({ to: "/$owner/$name/releases", params: { owner, name }, search: { page: n > 1 ? n : undefined } })}
            />
          </>
        )
      }
    </Gate>
  );
}

/** One release: notes, assets, and writer actions (edit, delete, upload). */
export function ReleasePage({ owner, name, id }: { owner: string; name: string; id: string }) {
  return (
    <RepoFrame owner={owner} name={name} tab="releases" section="pkg">
      {(repo) => <ReleaseDetail repo={repo} id={id} />}
    </RepoFrame>
  );
}

function ReleaseDetail({ repo, id }: { repo: Repo; id: string }) {
  const t = useT("releases");
  const { owner, name } = repo;
  const rel = useRelease(owner, name, id);
  const writer = canWrite(repo.permission);
  return (
    <Gate query={rel} notFound={<EmptyState icon="pkg" title={t("detail.notFound")} />}>
      {(r) => (
        <>
          <Card
            title={r.name || r.tag}
            actions={
              <>
                <ReleaseBadges r={r} />
                <ReleaseSubscription repo={repo} id={r.id} />
                {writer && (
                  <>
                    <Link to="/$owner/$name/releases/$id/edit" params={{ owner, name, id: r.id }}>
                      <Button size="sm" icon="edit">
                        {t("detail.edit")}
                      </Button>
                    </Link>
                    <DeleteRelease repo={repo} r={r} />
                  </>
                )}
              </>
            }
          >
            <dl className="ui-kv acc-pad">
              <dt>{t("field.tag")}</dt>
              <dd>
                <Link to="/$owner/$name" params={{ owner, name }} search={{ ref: r.tag }} className="ui-mono">
                  {r.tag}
                </Link>
              </dd>
              {r.commit && (
                <>
                  <dt>{t("detail.commit")}</dt>
                  <dd>
                    <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.commit }} className="ui-mono">
                      {shortSha(r.commit)}
                    </Link>
                  </dd>
                </>
              )}
              {r.author && (
                <>
                  <dt>{t("detail.author")}</dt>
                  <dd>{r.author}</dd>
                </>
              )}
              <dt>{r.publishedAt ? t("detail.published") : t("detail.created")}</dt>
              <dd>{formatDate(r.publishedAt ?? r.createdAt)}</dd>
            </dl>
            <div className="rl-body">
              <Notes repo={repo} r={r} />
            </div>
          </Card>
          <Assets repo={repo} r={r} writer={writer} />
        </>
      )}
    </Gate>
  );
}

/** The viewer's subscription to the notifications about this release. */
function ReleaseSubscription({ repo, id }: { repo: Repo; id: string }) {
  const t = useT("releases");
  const notify = useNotify();
  const signedIn = useSession().data !== undefined;
  const ref = { owner: repo.owner, name: repo.name, release: id };
  const subject = useSubjectSubscription(ref, signedIn);
  const set = useSetSubjectSubscription(ref);
  if (!subject.data) return null;
  const subscribed = subject.data.subscription.subscribed;
  return (
    <Button
      size="sm"
      variant="ghost"
      icon="bell"
      disabled={set.isPending}
      aria-pressed={subscribed}
      title={t(subscribed ? "sub.subscribedHint" : "sub.unsubscribedHint")}
      onClick={() => set.mutate(!subscribed, { onSuccess: (s) => notify.ok(t(s.subscription.subscribed ? "sub.subscribedOk" : "sub.unsubscribedOk")), onError: notify.fail })}
    >
      {t(subscribed ? "sub.unsubscribe" : "sub.subscribe")}
    </Button>
  );
}

function DeleteRelease({ repo, r }: { repo: Repo; r: Release }) {
  const t = useT("releases");
  const notify = useNotify();
  const navigate = useNavigate();
  const del = useDeleteRelease(repo.owner, repo.name, r.id);
  return (
    <Dialog
      trigger={
        <Button size="sm" variant="danger" icon="trash">
          {t("detail.delete")}
        </Button>
      }
      title={t("detail.deleteTitle", { name: r.name || r.tag })}
      description={t("detail.deleteHint")}
      footer={
        <>
          <DialogClose asChild>
            <Button>{t("form.cancel")}</Button>
          </DialogClose>
          <Button
            variant="danger"
            disabled={del.isPending}
            onClick={() =>
              del.mutate(undefined, {
                onSuccess: () => {
                  notify.ok(t("detail.deleted"));
                  void navigate({ to: "/$owner/$name/releases", params: { owner: repo.owner, name: repo.name } });
                },
                onError: notify.fail,
              })
            }
          >
            {t("detail.delete")}
          </Button>
        </>
      }
    />
  );
}

function Assets({ repo, r, writer }: { repo: Repo; r: Release; writer: boolean }) {
  const t = useT("releases");
  const notify = useNotify();
  const delAsset = useDeleteAsset(repo.owner, repo.name, r.id);
  return (
    <Card title={t("assets.title", { count: r.assets.length })}>
      {r.assets.length === 0 ? (
        <p className="ui-note acc-pad">{t("assets.empty")}</p>
      ) : (
        <Table>
          <TableHead>
            <Tr>
              <Th>{t("assets.name")}</Th>
              <Th>{t("assets.size")}</Th>
              <Th>{t("assets.downloads")}</Th>
              <Th>
                <span className="sr-only">{t("assets.actions")}</span>
              </Th>
            </Tr>
          </TableHead>
          <TableBody>
            {r.assets.map((a) => (
              <Tr key={a.id}>
                <Td>
                  <a href={assetDownloadUrl(repo.owner, repo.name, r.id, a.id)} download={a.name} className="ui-mono">
                    {a.name}
                  </a>
                  {a.label && <div className="ui-note">{a.label}</div>}
                  <div className="ui-note ui-mono rl-sha" title={a.sha256}>
                    sha256:{a.sha256.slice(0, 16)}…
                  </div>
                </Td>
                <Td>{formatBytes(a.size)}</Td>
                <Td>{a.downloadCount}</Td>
                <Td>
                  {writer && (
                    <Button
                      size="sm"
                      variant="ghost"
                      icon="trash"
                      aria-label={t("assets.delete", { name: a.name })}
                      disabled={delAsset.isPending}
                      onClick={() => delAsset.mutate(a.id, { onSuccess: () => notify.ok(t("assets.deleted")), onError: notify.fail })}
                    />
                  )}
                </Td>
              </Tr>
            ))}
          </TableBody>
        </Table>
      )}
      {writer && <Upload repo={repo} id={r.id} />}
    </Card>
  );
}

function Upload({ repo, id }: { repo: Repo; id: string }) {
  const t = useT("releases");
  const notify = useNotify();
  const invalidate = useInvalidateReleases(repo.owner, repo.name);
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [label, setLabel] = useState("");
  const [progress, setProgress] = useState<number | null>(null);
  const abort = useRef<AbortController | null>(null);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!file) return;
    const ctl = new AbortController();
    abort.current = ctl;
    setProgress(0);
    uploadAsset(repo.owner, repo.name, id, file, label.trim(), setProgress, ctl.signal).then(
      (a) => {
        notify.ok(t("upload.done", { name: a.name }));
        setFile(null);
        setLabel("");
        if (input.current) input.current.value = "";
        void invalidate();
      },
      (err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) notify.fail(err);
      },
    ).finally(() => {
      abort.current = null;
      setProgress(null);
    });
  };
  const busy = progress !== null;
  return (
    <form className="acc-form acc-pad" onSubmit={submit}>
      <div className="ui-field">
        <label>
          {t("upload.file")}
          <input ref={input} type="file" className="ui-input" disabled={busy} onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </label>
      </div>
      <Input label={t("upload.label")} hint={t("upload.labelHint")} value={label} disabled={busy} onChange={(e) => setLabel(e.target.value)} />
      {busy && (
        <div className="rl-progress" role="status">
          <progress max={1} value={progress} aria-label={t("upload.progress")} />
          <span className="ui-note">{Math.round(progress * 100)}%</span>
        </div>
      )}
      <div className="acc-actions">
        {busy ? (
          <Button onClick={() => abort.current?.abort()}>{t("upload.abort")}</Button>
        ) : (
          <Button type="submit" variant="primary" icon="upload" disabled={!file}>
            {t("upload.submit")}
          </Button>
        )}
        {file && !busy && <span className="ui-note">{formatBytes(file.size)}</span>}
      </div>
    </form>
  );
}

/** Create (`id` undefined) or edit a release. */
export function ReleaseFormPage({ owner, name, id }: { owner: string; name: string; id?: string }) {
  const t = useT("releases");
  return (
    <RepoFrame owner={owner} name={name} tab="releases" section="pkg">
      {(repo) =>
        !canWrite(repo.permission) ? (
          <EmptyState icon="lock" title={t("form.notAllowed")} />
        ) : id ? (
          <EditRelease repo={repo} id={id} />
        ) : (
          <ReleaseForm repo={repo} />
        )
      }
    </RepoFrame>
  );
}

function EditRelease({ repo, id }: { repo: Repo; id: string }) {
  const rel = useRelease(repo.owner, repo.name, id);
  return <Gate query={rel}>{(r) => <ReleaseForm repo={repo} existing={r} />}</Gate>;
}

function ReleaseForm({ repo, existing }: { repo: Repo; existing?: Release }) {
  const t = useT("releases");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRelease(repo.owner, repo.name);
  const update = useUpdateRelease(repo.owner, repo.name, existing?.id ?? "");
  const [tag, setTag] = useState(existing?.tag ?? "");
  const [target, setTarget] = useState(existing?.target || repo.defaultBranch);
  const [title, setTitle] = useState(existing?.name ?? "");
  const [body, setBody] = useState(existing?.body ?? "");
  const [draft, setDraft] = useState(existing?.draft ?? false);
  const [prerelease, setPrerelease] = useState(existing?.prerelease ?? false);
  const pending = create.isPending || update.isPending;
  // The tag of a published release is fixed; only drafts can still move it.
  const tagLocked = !!existing && !existing.draft;
  const done = (r: Release) => {
    notify.ok(existing ? t("form.saved") : t("form.created"));
    void navigate({ to: "/$owner/$name/releases/$id", params: { owner: repo.owner, name: repo.name, id: r.id } });
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const fields = { name: title.trim(), body, draft, prerelease, target: target.trim() };
    if (existing) update.mutate({ ...fields, ...(tagLocked ? {} : { tag: tag.trim() }) }, { onSuccess: done, onError: notify.fail });
    else create.mutate({ tag: tag.trim(), ...fields }, { onSuccess: done, onError: notify.fail });
  };
  return (
    <Card title={existing ? t("form.editTitle", { name: existing.name || existing.tag }) : t("form.newTitle")}>
      <form className="acc-form acc-pad" onSubmit={submit}>
        <Input label={t("field.tag")} hint={t("field.tagHint")} mono required disabled={tagLocked} value={tag} onChange={(e) => setTag(e.target.value)} />
        <Input label={t("field.target")} hint={t("field.targetHint")} mono disabled={tagLocked} value={target} onChange={(e) => setTarget(e.target.value)} />
        <Input label={t("field.name")} value={title} onChange={(e) => setTitle(e.target.value)} />
        <div className="ui-field">
          <label>
            {t("field.body")}
            <textarea className="ui-input acc-area" rows={10} value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
        </div>
        <label className="acc-check">
          <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} />
          <span>{t("field.draft")}</span>
        </label>
        <label className="acc-check">
          <input type="checkbox" checked={prerelease} onChange={(e) => setPrerelease(e.target.checked)} />
          <span>{t("field.prerelease")}</span>
        </label>
        <div className="acc-actions">
          <Button
            onClick={() =>
              void navigate(
                existing
                  ? { to: "/$owner/$name/releases/$id", params: { owner: repo.owner, name: repo.name, id: existing.id } }
                  : { to: "/$owner/$name/releases", params: { owner: repo.owner, name: repo.name } },
              )
            }
          >
            {t("form.cancel")}
          </Button>
          <Button type="submit" variant="primary" disabled={pending || !tag.trim()}>
            {existing ? t("form.save") : t("form.create")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
