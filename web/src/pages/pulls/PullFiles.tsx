import { useState, type FormEvent } from "react";
import { canWrite } from "../../api/code.ts";
import { useSession } from "../../api/identity.ts";
import {
  suggestionOf,
  useAddReviewComment,
  useApplySuggestion,
  useCreateReview,
  useDeleteReview,
  useDeleteReviewComment,
  useEditReviewComment,
  usePullDiff,
  useResolveThread,
  useReviewComments,
  useReviews,
  useSubmitReview,
  type PullRequest,
  type PullReviewComment,
} from "../../api/pulls.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, DialogClose, Input, Select } from "../../ui/index.ts";
import { DiffView, filePath } from "../../ui/code/DiffView.tsx";
import { useNotify } from "../account/common.tsx";
import { Gate, When } from "../code/common.tsx";
import { Body, CommentBox, Composer } from "../work/common.tsx";

type Thread = { root: PullReviewComment; replies: PullReviewComment[] };
type Side = "LEFT" | "RIGHT";

/** Groups review comments into threads: replies point at any comment of their thread. */
function threadsOf(comments: PullReviewComment[]): Map<string, Thread[]> {
  const byId = new Map(comments.map((c) => [c.id, c]));
  const rootOf = (c: PullReviewComment): PullReviewComment => {
    let cur = c;
    for (let i = 0; i < 50 && cur.inReplyTo; i++) {
      const up = byId.get(cur.inReplyTo);
      if (!up) break;
      cur = up;
    }
    return cur;
  };
  const threads = new Map<string, Thread>();
  for (const c of comments) {
    const root = rootOf(c);
    const th = threads.get(root.id) ?? { root, replies: [] };
    if (c.id !== root.id) th.replies.push(c);
    threads.set(root.id, th);
  }
  const byPath = new Map<string, Thread[]>();
  for (const th of threads.values()) {
    th.replies.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
    const list = byPath.get(th.root.path) ?? [];
    list.push(th);
    byPath.set(th.root.path, list);
  }
  for (const list of byPath.values()) list.sort((a, b) => (a.root.line ?? a.root.originalLine) - (b.root.line ?? b.root.originalLine));
  return byPath;
}

const anchor = (path: string) => `wk-th-${path.replace(/[^A-Za-z0-9_-]/g, "_")}`;

export function PullFiles({ repo, pr }: { repo: Repo; pr: PullRequest }) {
  const t = useT("pulls");
  const me = useSession().data?.username;
  const diff = usePullDiff(repo.owner, repo.name, pr.issue.number);
  const comments = useReviewComments(repo.owner, repo.name, pr.issue.number);
  const reviews = useReviews(repo.owner, repo.name, pr.issue.number);
  const [compose, setCompose] = useState<string | undefined>(undefined);
  const pending = (reviews.data ?? []).find((r) => r.state === "pending" && r.reviewer === me);
  const writer = canWrite(repo.permission);
  const canComment = me !== undefined && writer && pr.state !== "closed";
  const byPath = threadsOf(comments.data ?? []);
  const pendingCount = (comments.data ?? []).filter((c) => c.pending && c.reviewId === pending?.id).length;

  function startComment(path: string) {
    setCompose(path);
    requestAnimationFrame(() => document.getElementById("wk-newcomment")?.scrollIntoView({ block: "center" }));
  }

  return (
    <Gate query={diff}>
      {(d) => {
        const paths = d.files.map(filePath);
        return (
          <div className="wk-files-tab">
            <div className="rp-bar">
              {pending && (
                <span className="ui-note">
                  <Badge tone="warn">{t("review.pendingBadge")}</Badge> {t("review.pendingCount", { count: pendingCount })}
                </span>
              )}
              <span className="acc-grow" />
              {canComment && <SubmitReview repo={repo} pr={pr} pendingId={pending?.id} me={me} />}
            </div>
            <DiffView
              diff={d}
              fileActions={(f) => {
                const p = filePath(f);
                const n = byPath.get(p)?.length ?? 0;
                return (
                  <>
                    {n > 0 && (
                      <a className="wk-linkbtn" href={`#${anchor(p)}`}>
                        {t("files.threads", { count: n })}
                      </a>
                    )}
                    {canComment && (
                      <Button size="sm" variant="ghost" icon="book" onClick={() => startComment(p)}>
                        {t("files.comment")}
                      </Button>
                    )}
                  </>
                );
              }}
            />
            <Card title={t("files.reviewComments")} className="wk-threads">
              {compose !== undefined && (
                <NewComment repo={repo} pr={pr} paths={paths} path={compose} pending={pending !== undefined} onDone={() => setCompose(undefined)} />
              )}
              {byPath.size === 0 && compose === undefined && <p className="ui-note acc-pad">{t("files.noComments")}</p>}
              {Array.from(byPath.entries()).map(([path, threads]) => (
                <section key={path} id={anchor(path)} className="wk-th-file">
                  <h4 className="ui-mono">{path}</h4>
                  {threads.map((th) => (
                    <ThreadView key={th.root.id} repo={repo} pr={pr} th={th} me={me} canComment={canComment} />
                  ))}
                </section>
              ))}
            </Card>
          </div>
        );
      }}
    </Gate>
  );
}

function NewComment({ repo, pr, paths, path, pending, onDone }: { repo: Repo; pr: PullRequest; paths: string[]; path: string; pending: boolean; onDone: () => void }) {
  const t = useT("pulls");
  const notify = useNotify();
  const add = useAddReviewComment(repo.owner, repo.name, pr.issue.number);
  const start = useCreateReview(repo.owner, repo.name, pr.issue.number);
  const [file, setFile] = useState(path);
  const [side, setSide] = useState<Side>("RIGHT");
  const [line, setLine] = useState("");
  const [startLine, setStartLine] = useState("");
  const [body, setBody] = useState("");
  const n = Number(line);
  const s = startLine ? Number(startLine) : undefined;
  const valid = Number.isInteger(n) && n > 0 && body.trim() !== "" && (s === undefined || (Number.isInteger(s) && s > 0 && s < n));
  const busy = add.isPending || start.isPending;

  function single(e: FormEvent) {
    e.preventDefault();
    add.mutate({ path: file, side, line: n, startLine: s, body, commitSha: pr.head.sha }, { onSuccess: onDone, onError: notify.fail });
  }
  function startReview() {
    start.mutate({ commitSha: pr.head.sha, comments: [{ path: file, side, line: n, startLine: s, body }] }, { onSuccess: onDone, onError: notify.fail });
  }

  return (
    <form id="wk-newcomment" className="acc-form wk-newcomment" onSubmit={single}>
      <div className="acc-inline">
        <Select label={t("files.path")} value={file} onValueChange={setFile} options={paths.map((p) => ({ value: p, label: p }))} />
        <Select
          label={t("files.side")}
          value={side}
          onValueChange={(v) => setSide(v === "LEFT" ? "LEFT" : "RIGHT")}
          options={[
            { value: "RIGHT", label: t("files.sideRight") },
            { value: "LEFT", label: t("files.sideLeft") },
          ]}
        />
        <Input label={t("files.startLine")} hint={t("files.startLineHint")} inputMode="numeric" value={startLine} onChange={(e) => setStartLine(e.target.value.replace(/\D/g, ""))} />
        <Input label={t("files.line")} required inputMode="numeric" value={line} onChange={(e) => setLine(e.target.value.replace(/\D/g, ""))} />
      </div>
      <Composer owner={repo.owner} name={repo.name} refName={pr.head.ref} value={body} onChange={setBody} label={t("files.body")} placeholder={t("files.bodyHint")} autoFocus>
        <Button variant="ghost" onClick={onDone}>
          {t("action.cancel")}
        </Button>
        {pending ? (
          <Button type="submit" disabled={!valid || busy}>
            {t("files.addToReview")}
          </Button>
        ) : (
          <>
            <Button type="submit" disabled={!valid || busy}>
              {t("files.single")}
            </Button>
            <Button disabled={!valid || busy} onClick={startReview}>
              {t("files.startReview")}
            </Button>
          </>
        )}
      </Composer>
    </form>
  );
}

function ThreadView({ repo, pr, th, me, canComment }: { repo: Repo; pr: PullRequest; th: Thread; me?: string; canComment: boolean }) {
  const t = useT("pulls");
  const notify = useNotify();
  const resolve = useResolveThread(repo.owner, repo.name);
  const add = useAddReviewComment(repo.owner, repo.name, pr.issue.number);
  const [reply, setReply] = useState<string | undefined>(undefined);
  const [open, setOpen] = useState(!th.root.resolved);
  const r = th.root;
  const line = r.line ?? r.originalLine;
  const start = r.startLine ?? r.originalStartLine ?? undefined;
  const hunk = r.diffHunk.split("\n").slice(-4).join("\n");
  return (
    <div className={`wk-thread ${r.resolved ? "wk-thread-resolved" : ""}`}>
      <div className="wk-thread-head">
        <button type="button" className="wk-linkbtn" aria-expanded={open} onClick={() => setOpen(!open)}>
          {start ? t("files.lines", { start, end: line }) : t("files.lineN", { line })}
        </button>
        <span className="ui-note">{r.side === "LEFT" ? t("files.sideLeft") : t("files.sideRight")}</span>
        {r.outdated && <Badge tone="warn">{t("files.outdated")}</Badge>}
        {r.resolved && <Badge tone="ok">{t("files.resolved")}</Badge>}
        {r.pending && <Badge tone="info">{t("review.pendingBadge")}</Badge>}
        <span className="acc-grow" />
        {canComment && !r.pending && (
          <Button size="sm" variant="ghost" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: r.id, resolved: !r.resolved }, { onError: notify.fail })}>
            {r.resolved ? t("files.unresolve") : t("files.resolve")}
          </Button>
        )}
      </div>
      {open && (
        <>
          {hunk && <pre className="ui-mono wk-hunk">{hunk}</pre>}
          {[r, ...th.replies].map((c) => (
            <ReviewComment key={c.id} repo={repo} pr={pr} c={c} me={me} canApply={canWrite(repo.permission) && pr.state === "open" && !r.outdated && !r.resolved} />
          ))}
          {canComment &&
            (reply === undefined ? (
              <button type="button" className="wk-reply-open" onClick={() => setReply("")}>
                {t("files.reply")}
              </button>
            ) : (
              <Composer owner={repo.owner} name={repo.name} refName={pr.head.ref} value={reply} onChange={setReply} label={t("files.replyLabel")} rows={3} autoFocus>
                <Button variant="ghost" onClick={() => setReply(undefined)}>
                  {t("action.cancel")}
                </Button>
                <Button disabled={!reply.trim() || add.isPending} onClick={() => add.mutate({ body: reply, inReplyTo: r.id }, { onSuccess: () => setReply(undefined), onError: notify.fail })}>
                  {t("files.replySubmit")}
                </Button>
              </Composer>
            ))}
        </>
      )}
    </div>
  );
}

function ReviewComment({ repo, pr, c, me, canApply }: { repo: Repo; pr: PullRequest; c: PullReviewComment; me?: string; canApply: boolean }) {
  const t = useT("pulls");
  const notify = useNotify();
  const edit = useEditReviewComment(repo.owner, repo.name);
  const del = useDeleteReviewComment(repo.owner, repo.name);
  const apply = useApplySuggestion(repo.owner, repo.name);
  const [draft, setDraft] = useState<string | undefined>(undefined);
  const mine = me !== undefined && me === c.author;
  const suggestion = suggestionOf(c.body);
  return (
    <CommentBox
      author={c.author}
      head={
        <>
          <b>{c.author}</b>
          <When iso={c.createdAt} />
          {c.updatedAt !== c.createdAt && <span className="ui-note">{t("files.edited")}</span>}
          <span className="acc-grow" />
          {(mine || canWrite(repo.permission)) && draft === undefined && (
            <>
              {mine && <Button size="sm" variant="ghost" icon="edit" aria-label={t("files.edit")} onClick={() => setDraft(c.body)} />}
              <Button
                size="sm"
                variant="ghost"
                icon="trash"
                aria-label={t("files.delete")}
                disabled={del.isPending}
                onClick={() => {
                  if (window.confirm(t("files.deleteConfirm"))) del.mutate(c.id, { onError: notify.fail });
                }}
              />
            </>
          )}
        </>
      }
      footer={
        suggestion !== undefined &&
        canApply &&
        draft === undefined && (
          <div className="wk-suggest">
            <Button size="sm" icon="check" disabled={apply.isPending} onClick={() => apply.mutate({ id: c.id }, { onSuccess: () => notify.ok(t("files.applied")), onError: notify.fail })}>
              {t("files.apply")}
            </Button>
          </div>
        )
      }
    >
      {draft === undefined ? (
        <Body owner={repo.owner} name={repo.name} refName={pr.head.ref} source={c.body} />
      ) : (
        <Composer owner={repo.owner} name={repo.name} refName={pr.head.ref} value={draft} onChange={setDraft} label={t("files.editLabel")} autoFocus>
          <Button variant="ghost" onClick={() => setDraft(undefined)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={edit.isPending || !draft.trim()} onClick={() => edit.mutate({ id: c.id, body: draft }, { onSuccess: () => setDraft(undefined), onError: notify.fail })}>
            {t("action.save")}
          </Button>
        </Composer>
      )}
    </CommentBox>
  );
}

type ReviewEvent = "comment" | "approve" | "request_changes";

function SubmitReview({ repo, pr, pendingId, me }: { repo: Repo; pr: PullRequest; pendingId?: string; me?: string }) {
  const t = useT("pulls");
  const notify = useNotify();
  const create = useCreateReview(repo.owner, repo.name, pr.issue.number);
  const submit = useSubmitReview(repo.owner, repo.name, pr.issue.number);
  const discard = useDeleteReview(repo.owner, repo.name, pr.issue.number);
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState("");
  const [event, setEvent] = useState<ReviewEvent>("comment");
  const own = me === pr.issue.author;
  const busy = create.isPending || submit.isPending || discard.isPending;
  const done = () => {
    setOpen(false);
    setBody("");
    notify.ok(t("review.submitted"));
  };

  function send(e: FormEvent) {
    e.preventDefault();
    if (pendingId) submit.mutate({ id: pendingId, event, body: body || undefined }, { onSuccess: done, onError: notify.fail });
    else create.mutate({ event, body: body || undefined, commitSha: pr.head.sha }, { onSuccess: done, onError: notify.fail });
  }

  return (
    <>
      {pendingId && (
        <Button
          variant="ghost"
          disabled={busy}
          onClick={() => {
            if (window.confirm(t("review.discardConfirm"))) discard.mutate(pendingId, { onError: notify.fail });
          }}
        >
          {t("review.discard")}
        </Button>
      )}
      <Dialog
        open={open}
        onOpenChange={setOpen}
        trigger={<Button variant="primary">{pendingId ? t("review.finish") : t("review.open")}</Button>}
        title={t("review.title")}
        description={t("review.hint")}
      >
        <form className="acc-form" onSubmit={send}>
          <Composer owner={repo.owner} name={repo.name} refName={pr.head.ref} value={body} onChange={setBody} label={t("review.body")} rows={5} />
          <fieldset className="wk-radios">
            <legend>{t("review.verdict")}</legend>
            {(["comment", "approve", "request_changes"] as const).map((ev) => (
              <label key={ev} className="acc-check">
                <input type="radio" name="event" value={ev} checked={event === ev} disabled={own && ev !== "comment"} onChange={() => setEvent(ev)} />
                <span>
                  <b>{t(`review.ev.${ev}`)}</b>
                  <span className="ui-note"> {t(`review.ev.${ev}.hint`)}</span>
                </span>
              </label>
            ))}
            {own && <p className="ui-note">{t("review.ownHint")}</p>}
          </fieldset>
          <div className="acc-actions">
            <DialogClose asChild>
              <Button variant="ghost">{t("action.cancel")}</Button>
            </DialogClose>
            <Button type="submit" disabled={busy || (event !== "approve" && !body.trim() && !pendingId)}>
              {t("review.submit")}
            </Button>
          </div>
        </form>
      </Dialog>
    </>
  );
}
