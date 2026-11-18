import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useRef, useState, type DragEvent, type FormEvent } from "react";
import { archiveUrl, canWrite, PAGE_SIZE } from "../../api/code.ts";
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
  type ReleaseAsset,
} from "../../api/releases.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Button, Dialog, DialogClose, EmptyState, Icon, Input, Menu, MenuItem } from "../../ui/index.ts";
import { formatDate, useNotify } from "../account/common.tsx";
import { formatBytes, Gate, Pager, RepoFrame, shortSha, When } from "../code/common.tsx";
import { useInAppLinks, useMarkdownContext } from "../code/Readme.tsx";
import { Avatar } from "../work/common.tsx";

/* Releases (redesign 2026-11-17, round 024 B "Ultima in evidenza"): latest release hero, table of the
   others, detail with a sticky info panel, two-column form. Styles: .rel-* in styles/actions.css. */

function ReleaseBadges({ r }: { r: Release }) {
  const t = useT("releases");
  return (
    <>
      {r.latest && <span className="rel-badge rel-latest">{t("badge.latest")}</span>}
      {r.prerelease && <span className="rel-badge rel-pre">{t("badge.prerelease")}</span>}
      {r.draft && <span className="rel-badge rel-draft">{t("badge.draft")}</span>}
    </>
  );
}

/** Release notes rendered with links resolved against the tag. */
function Notes({ repo, r }: { repo: Repo; r: Release }) {
  const ctx = useMarkdownContext(repo.owner, repo.name, r.commit || r.tag, "");
  const onClick = useInAppLinks();
  if (!r.body.trim()) return null;
  return (
    <div onClick={onClick}>
      <Markdown source={r.body} ctx={ctx} className="rel-md" />
    </div>
  );
}

/** Asset of this platform (by OS, then the machine's architecture), for the main download button. */
function platformAsset(assets: ReleaseAsset[]): { asset: ReleaseAsset; os: string } | undefined {
  if (typeof navigator === "undefined") return undefined;
  const ua = navigator.userAgent.toLowerCase();
  const [os, keys] = ua.includes("windows") ? ["Windows", ["windows", "win64", "win"]] : ua.includes("mac") ? ["macOS", ["darwin", "macos", "mac"]] : ua.includes("linux") ? ["Linux", ["linux"]] : ["", []];
  const hits = assets.filter((a) => keys.some((k) => a.name.toLowerCase().includes(k)));
  const arm = /arm|aarch64/.test(ua);
  const asset = hits.find((a) => (arm ? /arm64|aarch64/ : /amd64|x86_64|x64/).test(a.name.toLowerCase())) ?? hits[0];
  return asset && { asset, os };
}

/** Asset rows plus the two source archives of the tag. */
function AssetList({ repo, r, onDelete, compact, sources = true }: { repo: Repo; r: Release; onDelete?: (a: ReleaseAsset) => void; compact?: boolean; sources?: boolean }) {
  const t = useT("releases");
  const { owner, name } = repo;
  return (
    <ul className={compact ? "rel-assets rel-compact" : "rel-assets"}>
      {r.assets.map((a) => (
        <li key={a.id} className="rel-asset">
          <Icon name="pkg" />
          <a className="rel-an" href={assetDownloadUrl(owner, name, r.id, a.id)} download={a.name} title={a.label || `sha256:${a.sha256}`}>{a.name}</a>
          <span className="ui-tm">{formatBytes(a.size)}</span>
          {!compact && <span className="ui-tm" title={t("assets.downloads")}>{a.downloadCount}</span>}
          {onDelete ? (
            <Button size="sm" variant="ghost" icon="trash" aria-label={t("assets.delete", { name: a.name })} onClick={() => onDelete(a)} />
          ) : (
            <a className="ui-iconbtn rel-dl" href={assetDownloadUrl(owner, name, r.id, a.id)} download={a.name} aria-label={t("assets.download", { name: a.name })}><Icon name="download" /></a>
          )}
        </li>
      ))}
      {sources && !r.draft &&
        (["zip", "tar.gz"] as const).map((f) => (
          <li key={f} className="rel-asset">
            <Icon name="file" />
            <a className="rel-an" href={archiveUrl(owner, name, r.tag, f)} download>{t("assets.source", { format: f })}</a>
            <span />
            {!compact && <span />}
            <a className="ui-iconbtn rel-dl" href={archiveUrl(owner, name, r.tag, f)} download aria-label={t("assets.source", { format: f })}><Icon name="download" /></a>
          </li>
        ))}
    </ul>
  );
}

const downloads = (r: Release) => r.assets.reduce((n, a) => n + a.downloadCount, 0);

/** Releases / Tags segmented control. */
function RefSeg({ repo, current }: { repo: Repo; current: "releases" | "tags" }) {
  const t = useT("releases");
  const p = { owner: repo.owner, name: repo.name };
  return (
    <nav className="ui-seg" aria-label={t("list.kind")}>
      <Link className="ui-segi" to="/$owner/$name/releases" params={p} aria-current={current === "releases" ? "page" : undefined}>{t("list.releases")}</Link>
      <Link className="ui-segi" to="/$owner/$name/tags" params={p} aria-current={current === "tags" ? "page" : undefined}>{t("list.tags")}</Link>
    </nav>
  );
}

/** Paginated release list, newest first; on the first page the latest release is the hero. */
export function ReleasesPage({ owner, name, page = 1 }: { owner: string; name: string; page?: number }) {
  const t = useT("releases");
  return (
    <RepoFrame owner={owner} name={name} tab="releases" section="pkg">
      {(repo) => (
        <div className="rel-page">
          <div className="rel-lh">
            <h2>{t("list.title")}</h2>
            <RefSeg repo={repo} current="releases" />
            {canWrite(repo.permission) && (
              <Link to="/$owner/$name/releases/new" params={{ owner, name }} className="rel-push">
                <Button variant="primary" icon="plus">{t("list.new")}</Button>
              </Link>
            )}
          </div>
          <ReleaseList repo={repo} page={page} />
        </div>
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
      {(rows) => {
        if (rows.length === 0 && page === 1) return <EmptyState icon="pkg" title={t("list.empty")} description={t("list.emptyHint")} />;
        const hero = page === 1 ? rows.find((r) => r.latest) : undefined;
        const rest = rows.filter((r) => r !== hero);
        return (
          <>
            {hero && <Hero repo={repo} r={hero} />}
            {rest.length > 0 && (
              <div className="ui-box rel-tw" role="region" aria-label={t("list.others")} tabIndex={0}>
                <table className="ui-in rel-t">
                  <thead>
                    <tr>
                      <th scope="col">{t("list.version")}</th>
                      <th scope="col">{t("field.name")}</th>
                      <th scope="col">{t("detail.author")}</th>
                      <th scope="col" className="rel-r">{t("list.assetsCol")}</th>
                      <th scope="col" className="rel-r">{t("list.date")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rest.map((r) => (
                      <tr key={r.id}>
                        <th scope="row">
                          <Link to="/$owner/$name/releases/tag/$tag" params={{ owner, name, tag: r.tag }} className={r.draft ? "rel-v rel-vd" : "rel-v"}>{r.tag}</Link>
                        </th>
                        <td><span className="rel-tt"><span className="rel-ell">{r.name || r.tag}</span><ReleaseBadges r={{ ...r, latest: false }} /></span></td>
                        <td>{r.author ? <span className="rel-who"><Avatar user={r.author} size={18} />{r.author}</span> : <span className="ui-tm">—</span>}</td>
                        <td className="rel-r ui-tm">{r.assets.length}</td>
                        <td className="rel-r ui-tm">{r.publishedAt ? <When iso={r.publishedAt} /> : "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <Pager
              page={page}
              hasMore={rows.length === PAGE_SIZE}
              onPage={(n) => void navigate({ to: "/$owner/$name/releases", params: { owner, name }, search: { page: n > 1 ? n : undefined } })}
            />
          </>
        );
      }}
    </Gate>
  );
}

function Hero({ repo, r }: { repo: Repo; r: Release }) {
  const t = useT("releases");
  const { owner, name } = repo;
  const mine = platformAsset(r.assets);
  return (
    <section className="rel-hero" aria-label={t("list.latest")}>
      <div className="rel-hl">
        <div className="rel-k">
          <ReleaseBadges r={r} />
          <span className="ui-mono">{r.tag}</span>
          {r.publishedAt && <>· <When iso={r.publishedAt} /></>}
        </div>
        <h3 className="rel-h2">
          <Link to="/$owner/$name/releases/tag/$tag" params={{ owner, name, tag: r.tag }}>{r.name || r.tag}</Link>
        </h3>
        <p className="rel-by">
          {r.author && <><Avatar user={r.author} size={18} />{r.author}</>}
          {r.commit && <> · <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.commit }} className="ui-mono">{shortSha(r.commit)}</Link></>}
        </p>
        <Notes repo={repo} r={r} />
      </div>
      <div className="rel-hr">
        <h4 className="rel-ah">{t("assets.titlePlain")} <span className="ui-tm">{t("assets.downloadsTotal", { count: downloads(r) })}</span></h4>
        <AssetList repo={repo} r={r} compact />
        <div className="rel-main-dl">
          {mine && (
            <a className="ui-btn ui-btn-primary" href={assetDownloadUrl(owner, name, r.id, mine.asset.id)} download={mine.asset.name} title={mine.asset.name}>
              <Icon name="download" />
              {t("assets.forPlatform", { os: mine.os })}
            </a>
          )}
          <Link to="/$owner/$name/releases/tag/$tag" params={{ owner, name, tag: r.tag }} className="ui-btn ui-btn-secondary">{t("list.open")}</Link>
        </div>
      </div>
    </section>
  );
}

/** One release: notes on the left, sticky info panel with assets on the right. */
export function ReleasePage({ owner, name, id, byTag }: { owner: string; name: string; id: string; byTag?: boolean }) {
  return (
    <RepoFrame owner={owner} name={name} tab="releases" section="pkg">
      {(repo) => <ReleaseDetail repo={repo} id={id} byTag={byTag} />}
    </RepoFrame>
  );
}

function ReleaseDetail({ repo, id, byTag }: { repo: Repo; id: string; byTag?: boolean }) {
  const t = useT("releases");
  const { owner, name } = repo;
  const rel = useRelease(owner, name, id, byTag);
  const first = useReleases(owner, name, 0);
  const writer = canWrite(repo.permission);
  const [deleting, setDeleting] = useState(false);
  const [uploading, setUploading] = useState(false);
  return (
    <Gate query={rel} notFound={<EmptyState icon="pkg" title={t("detail.notFound")} />}>
      {(r) => {
        // Previous published release, when it is on the first page: the base of "Compare".
        const rows = first.data ?? [];
        const at = rows.findIndex((x) => x.id === r.id);
        const prev = at >= 0 ? rows.slice(at + 1).find((x) => !x.draft) : undefined;
        return (
          <div className="rel-dg">
            <article className="rel-art">
              <div className="rel-dh">
                <h2>{r.name || r.tag}</h2>
                <ReleaseBadges r={r} />
                {writer && (
                  <span className="rel-push rel-acts">
                    <Link to="/$owner/$name/releases/$id/edit" params={{ owner, name, id: r.id }}>
                      <Button icon="edit">{t("detail.edit")}</Button>
                    </Link>
                    <Menu align="end" label={t("detail.more")} trigger={<Button icon="dots" aria-label={t("detail.more")} title={t("detail.more")} />}>
                      <MenuItem icon="trash" danger onSelect={() => setDeleting(true)}>{t("detail.delete")}</MenuItem>
                    </Menu>
                  </span>
                )}
              </div>
              <p className="rel-sub">
                {r.author && <Avatar user={r.author} size={18} />}
                {r.publishedAt
                  ? t("detail.publishedBy", { author: r.author || "—", when: formatDate(r.publishedAt) })
                  : t("detail.createdBy", { author: r.author || "—", when: formatDate(r.createdAt) })}
              </p>
              <Notes repo={repo} r={r} />
            </article>
            <aside className="ui-box rel-pane" aria-label={t("detail.info")}>
              <dl className="rel-kv">
                <dt>{t("field.tag")}</dt>
                <dd><Link to="/$owner/$name" params={{ owner, name }} search={{ ref: r.tag }} className="ui-mono">{r.tag}</Link></dd>
                {r.commit && (
                  <>
                    <dt>{t("detail.commit")}</dt>
                    <dd>
                      <Link to="/$owner/$name/commit/$sha" params={{ owner, name, sha: r.commit }} className="ui-mono">{shortSha(r.commit)}</Link>
                      <CopyButton value={r.commit} />
                    </dd>
                  </>
                )}
                {r.target && (
                  <>
                    <dt>{t("detail.branch")}</dt>
                    <dd><code className="ax-ref">{r.target}</code></dd>
                  </>
                )}
                {prev && r.commit && (
                  <>
                    <dt>{t("detail.compare")}</dt>
                    <dd><Link to="/$owner/$name/compare" params={{ owner, name }} search={{ base: prev.tag, head: r.tag }}>{prev.tag}…{r.tag}</Link></dd>
                  </>
                )}
                <ReleaseSubscription repo={repo} id={r.id} />
              </dl>
              <div className="ui-in rel-as">
                <h3 className="rel-ah">{t("assets.titlePlain")} <span className="ui-tm">{t("assets.downloadsTotal", { count: downloads(r) })}</span></h3>
                {r.assets.length === 0 && r.draft ? <p className="ui-note rel-none">{t("assets.empty")}</p> : <AssetList repo={repo} r={r} compact />}
              </div>
              {writer && (
                <div className="rel-ft">
                  <Button icon="upload" onClick={() => setUploading(true)}>{t("upload.submit")}</Button>
                  <Button variant="danger" icon="trash" onClick={() => setDeleting(true)}>{t("detail.delete")}</Button>
                </div>
              )}
            </aside>
            {writer && <DeleteRelease repo={repo} r={r} open={deleting} onOpenChange={setDeleting} />}
            {writer && (
              <Dialog open={uploading} onOpenChange={setUploading} title={t("upload.title", { name: r.name || r.tag })}>
                <ManageAssets repo={repo} r={r} />
              </Dialog>
            )}
          </div>
        );
      }}
    </Gate>
  );
}

function CopyButton({ value }: { value: string }) {
  const t = useT("releases");
  const notify = useNotify();
  return (
    <Button
      size="sm"
      variant="ghost"
      icon="copy"
      aria-label={t("detail.copy")}
      title={t("detail.copy")}
      onClick={() => void navigator.clipboard.writeText(value).then(() => notify.ok(t("detail.copied")), notify.fail)}
    />
  );
}

/** The viewer's subscription to the notifications about this release, as a row of the info panel. */
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
    <>
      <dt>{t("detail.notifications")}</dt>
      <dd>
        <Button
          size="sm"
          icon="bell"
          disabled={set.isPending}
          aria-pressed={subscribed}
          title={t(subscribed ? "sub.subscribedHint" : "sub.unsubscribedHint")}
          onClick={() => set.mutate(!subscribed, { onSuccess: (s) => notify.ok(t(s.subscription.subscribed ? "sub.subscribedOk" : "sub.unsubscribedOk")), onError: notify.fail })}
        >
          {t(subscribed ? "sub.unsubscribe" : "sub.subscribe")}
        </Button>
      </dd>
    </>
  );
}

function DeleteRelease({ repo, r, open, onOpenChange }: { repo: Repo; r: Release; open: boolean; onOpenChange: (o: boolean) => void }) {
  const t = useT("releases");
  const notify = useNotify();
  const navigate = useNavigate();
  const del = useDeleteRelease(repo.owner, repo.name, r.id);
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
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

/** Existing assets with delete, plus the drop zone that uploads right away. */
function ManageAssets({ repo, r }: { repo: Repo; r: Release }) {
  const t = useT("releases");
  const notify = useNotify();
  const delAsset = useDeleteAsset(repo.owner, repo.name, r.id);
  const uploader = useUploader(repo);
  return (
    <div className="rel-manage">
      {r.assets.length > 0 && (
        <AssetList
          repo={repo}
          r={r}
          sources={false}
          onDelete={(a) => delAsset.mutate(a.id, { onSuccess: () => notify.ok(t("assets.deleted")), onError: notify.fail })}
        />
      )}
      <DropZone disabled={uploader.busy} onFiles={(files) => void uploader.run(r.id, files)} />
      <UploadRows items={uploader.items} onAbort={uploader.abort} />
    </div>
  );
}

type UploadItem = { file: File; progress: number | null };

/** Sequential uploads of files to a release, with progress per file and a single abort. */
function useUploader(repo: Repo) {
  const t = useT("releases");
  const notify = useNotify();
  const invalidate = useInvalidateReleases(repo.owner, repo.name);
  const [items, setItems] = useState<UploadItem[]>([]);
  const [busy, setBusy] = useState(false);
  const ctl = useRef<AbortController | null>(null);

  async function run(releaseId: string, files: File[]): Promise<boolean> {
    if (files.length === 0) return true;
    setBusy(true);
    setItems(files.map((file) => ({ file, progress: null })));
    const abort = new AbortController();
    ctl.current = abort;
    let ok = true;
    for (const [i, file] of files.entries()) {
      const set = (p: number) => setItems((xs) => xs.map((x, j) => (j === i ? { ...x, progress: p } : x)));
      set(0);
      try {
        const a = await uploadAsset(repo.owner, repo.name, releaseId, file, "", set, abort.signal);
        notify.ok(t("upload.done", { name: a.name }));
      } catch (err) {
        if (!(err instanceof DOMException && err.name === "AbortError")) notify.fail(err);
        ok = false;
        break;
      }
    }
    ctl.current = null;
    setBusy(false);
    setItems([]);
    void invalidate();
    return ok;
  }

  return { items, busy, run, abort: () => ctl.current?.abort() };
}

/** Drop area with a real file input inside its label (keyboard and screen readers use the input). */
function DropZone({ disabled, onFiles }: { disabled?: boolean; onFiles: (files: File[]) => void }) {
  const t = useT("releases");
  const id = useId();
  const [over, setOver] = useState(false);
  const drop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    if (!disabled) onFiles(Array.from(e.dataTransfer.files));
  };
  return (
    <label
      htmlFor={id}
      className={over ? "rel-drop rel-over" : "rel-drop"}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={drop}
    >
      <Icon name="upload" />
      <span>{t("upload.drop")} <b>{t("upload.choose")}</b></span>
      <input
        id={id}
        type="file"
        multiple
        className="sr-only"
        disabled={disabled}
        onChange={(e) => {
          onFiles(Array.from(e.target.files ?? []));
          e.target.value = "";
        }}
      />
    </label>
  );
}

function UploadRows({ items, onAbort, onRemove }: { items: UploadItem[]; onAbort?: () => void; onRemove?: (i: number) => void }) {
  const t = useT("releases");
  if (items.length === 0) return null;
  return (
    <ul className="rel-upl">
      {items.map((x, i) => (
        <li key={`${x.file.name}-${i}`}>
          <span className="ui-mono rel-ell">{x.file.name}</span>
          {x.progress === null ? (
            <span className="ui-tm">{formatBytes(x.file.size)}</span>
          ) : (
            <progress className="rel-prog" max={1} value={x.progress} aria-label={t("upload.progressOf", { name: x.file.name })} />
          )}
          {x.progress !== null && onAbort ? (
            <Button size="sm" variant="ghost" icon="x" aria-label={t("upload.abort")} onClick={onAbort} />
          ) : onRemove ? (
            <Button size="sm" variant="ghost" icon="x" aria-label={t("upload.remove", { name: x.file.name })} onClick={() => onRemove(i)} />
          ) : (
            <span />
          )}
        </li>
      ))}
    </ul>
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

/** Two columns: title, notes (Write / Preview) and assets on the left; tag, target, flags and publish on the right. */
function ReleaseForm({ repo, existing }: { repo: Repo; existing?: Release }) {
  const t = useT("releases");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRelease(repo.owner, repo.name);
  const update = useUpdateRelease(repo.owner, repo.name, existing?.id ?? "");
  const delAsset = useDeleteAsset(repo.owner, repo.name, existing?.id ?? "");
  const uploader = useUploader(repo);
  const ctx = useMarkdownContext(repo.owner, repo.name, existing?.commit || repo.defaultBranch, "");
  const [tag, setTag] = useState(existing?.tag ?? "");
  const [target, setTarget] = useState(existing?.target || repo.defaultBranch);
  const [title, setTitle] = useState(existing?.name ?? "");
  const [body, setBody] = useState(existing?.body ?? "");
  const [prerelease, setPrerelease] = useState(existing?.prerelease ?? false);
  const [preview, setPreview] = useState(false);
  const [queue, setQueue] = useState<File[]>([]);
  const pending = create.isPending || update.isPending || uploader.busy;
  // The tag of a published release is fixed; only drafts can still move it.
  const tagLocked = !!existing && !existing.draft;
  const published = !!existing && !existing.draft;

  const save = (draft: boolean) => {
    if (!tag.trim()) return;
    const fields = { name: title.trim(), body, draft, prerelease, target: target.trim() };
    const done = async (r: Release) => {
      const ok = await uploader.run(r.id, queue);
      if (ok) setQueue([]);
      notify.ok(existing ? t("form.saved") : draft ? t("form.drafted") : t("form.created"));
      void navigate({ to: "/$owner/$name/releases/tag/$tag", params: { owner: repo.owner, name: repo.name, tag: r.tag } });
    };
    const opts = { onSuccess: (r: Release) => void done(r), onError: notify.fail };
    if (existing) update.mutate({ ...fields, ...(tagLocked ? {} : { tag: tag.trim() }) }, opts);
    else create.mutate({ tag: tag.trim(), ...fields }, opts);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save(false);
  };
  const cancel = () =>
    void navigate(
      existing
        ? { to: "/$owner/$name/releases/tag/$tag", params: { owner: repo.owner, name: repo.name, tag: existing.tag } }
        : { to: "/$owner/$name/releases", params: { owner: repo.owner, name: repo.name } },
    );

  return (
    <form className="rel-page" onSubmit={submit}>
      <div className="rel-lh">
        <h2>{existing ? t("form.editTitle", { name: existing.name || existing.tag }) : t("form.newTitle")}</h2>
      </div>
      <div className="rel-ng">
        <div className="ui-box rel-fm">
          <Input label={t("field.name")} value={title} onChange={(e) => setTitle(e.target.value)} />
          <div className="ui-field">
            <div className="rel-nh">
              <label htmlFor="rel-body">{t("field.body")}</label>
              <span className="ui-seg rel-seg" role="group" aria-label={t("field.bodyMode")}>
                <button type="button" className="ui-segi" aria-pressed={!preview} onClick={() => setPreview(false)}>{t("field.write")}</button>
                <button type="button" className="ui-segi" aria-pressed={preview} onClick={() => setPreview(true)}>{t("field.preview")}</button>
              </span>
            </div>
            {preview ? (
              <div className="rel-prev">{body.trim() ? <Markdown source={body} ctx={ctx} className="rel-md" /> : <p className="ui-note">{t("field.nothing")}</p>}</div>
            ) : (
              <textarea id="rel-body" className="ui-input acc-area rel-ta" rows={12} value={body} onChange={(e) => setBody(e.target.value)} />
            )}
            <p className="ui-note">{t("field.bodyHint")}</p>
          </div>
          <div className="ui-field">
            <span className="rel-lbl">{t("assets.titlePlain")}</span>
            {existing && existing.assets.length > 0 && (
              <AssetList
                repo={repo}
                r={existing}
                sources={false}
                onDelete={(a) => delAsset.mutate(a.id, { onSuccess: () => notify.ok(t("assets.deleted")), onError: notify.fail })}
              />
            )}
            <DropZone disabled={pending} onFiles={(files) => setQueue((q) => q.concat(files))} />
            <UploadRows
              items={uploader.busy ? uploader.items : queue.map((file) => ({ file, progress: null }))}
              onAbort={uploader.abort}
              onRemove={(i) => setQueue((q) => q.filter((_, j) => j !== i))}
            />
          </div>
        </div>
        <aside className="rel-side">
          <div className="ui-box rel-sb">
            <Input label={t("field.tag")} hint={tagLocked ? undefined : t("field.tagHint")} mono required disabled={tagLocked} value={tag} onChange={(e) => setTag(e.target.value)} />
            <Input label={t("field.target")} hint={tagLocked ? undefined : t("field.targetHint")} mono disabled={tagLocked} value={target} onChange={(e) => setTarget(e.target.value)} />
          </div>
          <div className="ui-box rel-sb">
            <label className="rel-chk">
              <input type="checkbox" checked={prerelease} onChange={(e) => setPrerelease(e.target.checked)} />
              <span>{t("field.prerelease")}<small>{t("field.prereleaseHint")}</small></span>
            </label>
          </div>
          <div className="rel-pub">
            <Button type="submit" variant="primary" disabled={pending || !tag.trim()}>
              {published ? t("form.save") : t("form.publish")}
            </Button>
            {!published && (
              <Button disabled={pending || !tag.trim()} onClick={() => save(true)}>{t("form.saveDraft")}</Button>
            )}
            <Button variant="ghost" onClick={cancel}>{t("form.cancel")}</Button>
          </div>
        </aside>
      </div>
    </form>
  );
}
