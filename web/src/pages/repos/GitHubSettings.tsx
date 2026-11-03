import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { ApiError } from "../../api/http.ts";
import {
  useCheckConnection,
  useConnections,
  useDeleteConnection,
  useMyImports,
  usePutSync,
  useRepoImport,
  useResolveSync,
  useRunSync,
  useSaveConnection,
  useStartImport,
  useSyncLink,
  useSyncRefs,
  type GitHubConnection,
  type GitHubConnectionCreate,
  type GitHubImportCreate,
  type GitHubSyncLink,
  type GitHubSyncLinkPut,
} from "../../api/settings.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, DialogClose, Input, Select, Table, TableBody, TableHead, Td, Th, Tr, type BadgeTone } from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "../account/common.tsx";
import { Gate, Pager } from "../code/common.tsx";
import { splitList } from "./RulesSettings.tsx";

const tone: Record<string, BadgeTone> = { ok: "ok", done: "ok", pending: "warn", running: "info", diverged: "warn", error: "err" };
const isNotFound = (e: unknown) => e instanceof ApiError && e.status === 404;
const importKinds = ["labels", "milestones", "issues", "pulls", "releases"] as const;

/** GitHub section of the repository settings: connections, sync link and import. */
export function GitHubSettings({ owner, name }: { owner: string; name: string }) {
  return (
    <>
      <SyncLink owner={owner} name={name} />
      <ImportStatus owner={owner} name={name} />
      <Connections owner={owner} />
      <MyImports />
    </>
  );
}

function Connections({ owner }: { owner: string }) {
  const t = useT("github");
  const notify = useNotify();
  const list = useConnections(owner);
  const remove = useDeleteConnection();
  const check = useCheckConnection();
  const save = useSaveConnection();
  const [adding, setAdding] = useState(false);
  const [importing, setImporting] = useState<GitHubConnection | undefined>(undefined);
  const [secret, setSecret] = useState<string | undefined>(undefined);

  return (
    <Card title={t("conn.title")} actions={<Button icon="plus" onClick={() => setAdding(true)}>{t("conn.add")}</Button>}>
      <p className="ui-note acc-pad">{t("conn.hint", { owner })}</p>
      {secret && <Secret label={t("conn.secretOnce")} value={secret} />}
      <Async query={list} empty={t("conn.empty")}>
        {(rows) => (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("conn.name")}</Th>
                <Th>{t("conn.kind")}</Th>
                <Th>{t("conn.webhook")}</Th>
                <Th>
                  <span className="sr-only">{t("conn.actions")}</span>
                </Th>
              </Tr>
            </TableHead>
            <TableBody>
              {rows.map((c) => (
                <Tr key={c.id}>
                  <Td>
                    <b>{c.name}</b>
                    <div className="ui-note ui-mono">{c.baseUrl}</div>
                  </Td>
                  <Td>
                    <Badge>{t(`kind.${c.kind}`)}</Badge>
                  </Td>
                  <Td>
                    <span className="ui-mono wk-break">{c.webhookUrl}</span>
                  </Td>
                  <Td>
                    <span className="wk-rowbtns">
                      <Button
                        size="sm"
                        disabled={check.isPending}
                        onClick={() =>
                          check.mutate(c.id, {
                            onSuccess: (r) => (r.ok ? notify.ok(t("conn.checkOk")) : notify.fail(new Error(r.detail ?? t("conn.checkFail")))),
                            onError: notify.fail,
                          })
                        }
                      >
                        {t("conn.check")}
                      </Button>
                      <Button size="sm" onClick={() => setImporting(c)}>
                        {t("import.start")}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          window.confirm(t("conn.rotateConfirm")) &&
                          save.mutate({ id: c.id, body: { rotateWebhookSecret: true } }, { onSuccess: (r) => setSecret(r.webhookSecret), onError: notify.fail })
                        }
                      >
                        {t("conn.rotate")}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        icon="trash"
                        aria-label={t("conn.deleteOf", { name: c.name })}
                        onClick={() => window.confirm(t("conn.deleteConfirm", { name: c.name })) && remove.mutate(c.id, { onSuccess: () => notify.ok(t("conn.deleted")), onError: notify.fail })}
                      />
                    </span>
                  </Td>
                </Tr>
              ))}
            </TableBody>
          </Table>
        )}
      </Async>
      <Dialog open={adding} onOpenChange={setAdding} title={t("conn.newTitle")} description={t("conn.newHint")}>
        {adding && (
          <NewConnection
            owner={owner}
            done={(c) => {
              setAdding(false);
              setSecret(c.webhookSecret);
            }}
          />
        )}
      </Dialog>
      <Dialog open={importing !== undefined} onOpenChange={(o) => !o && setImporting(undefined)} title={t("import.title")} description={t("import.hint")}>
        {importing && <StartImport owner={owner} conn={importing} />}
      </Dialog>
    </Card>
  );
}

function NewConnection({ owner, done }: { owner: string; done: (c: GitHubConnection) => void }) {
  const t = useT("github");
  const notify = useNotify();
  const save = useSaveConnection();
  const [v, setV] = useState<GitHubConnectionCreate>({ owner, name: "", kind: "token", baseUrl: "https://github.com" });
  const set = (p: Partial<GitHubConnectionCreate>) => setV((x) => ({ ...x, ...p }));
  const num = (s: string) => (s.trim() === "" ? undefined : Number(s));

  function submit(e: FormEvent) {
    e.preventDefault();
    save.mutate({ body: { ...v, name: v.name.trim() } }, { onSuccess: (c) => (notify.ok(t("conn.saved")), done(c)), onError: notify.fail });
  }

  return (
    <form className="acc-form" onSubmit={submit}>
      <Input label={t("conn.name")} required value={v.name} onChange={(e) => set({ name: e.target.value })} />
      <Select label={t("conn.kind")} value={v.kind} onValueChange={(k) => set({ kind: k as GitHubConnectionCreate["kind"] })} options={(["token", "app"] as const).map((k) => ({ value: k, label: t(`kind.${k}`) }))} />
      <Input label={t("conn.baseUrl")} type="url" mono hint={t("conn.baseUrlHint")} value={v.baseUrl ?? ""} onChange={(e) => set({ baseUrl: e.target.value })} />
      {v.kind === "token" ? (
        <Input label={t("conn.token")} type="password" required autoComplete="off" hint={t("conn.tokenHint")} value={v.token ?? ""} onChange={(e) => set({ token: e.target.value })} />
      ) : (
        <>
          <Input label={t("conn.appId")} inputMode="numeric" required value={v.appId === undefined ? "" : String(v.appId)} onChange={(e) => set({ appId: num(e.target.value) })} />
          <Input label={t("conn.installationId")} inputMode="numeric" required value={v.installationId === undefined ? "" : String(v.installationId)} onChange={(e) => set({ installationId: num(e.target.value) })} />
          <div className="ui-field">
            <label>
              {t("conn.privateKey")}
              <textarea className="ui-input ui-mono acc-area" required rows={6} spellCheck={false} value={v.privateKey ?? ""} onChange={(e) => set({ privateKey: e.target.value })} />
            </label>
          </div>
        </>
      )}
      <div className="acc-actions">
        <DialogClose asChild>
          <Button variant="ghost">{t("cancel")}</Button>
        </DialogClose>
        <Button variant="primary" type="submit" disabled={save.isPending}>
          {t("conn.create")}
        </Button>
      </div>
    </form>
  );
}

function StartImport({ owner, conn }: { owner: string; conn: GitHubConnection }) {
  const t = useT("github");
  const notify = useNotify();
  const navigate = useNavigate();
  const start = useStartImport();
  const [v, setV] = useState<GitHubImportCreate>({ connectionId: conn.id, remoteOwner: "", remoteName: "", owner, name: "", visibility: "private", include: [...importKinds] });
  const set = (p: Partial<GitHubImportCreate>) => setV((x) => ({ ...x, ...p }));
  const include = v.include ?? [];

  function submit(e: FormEvent) {
    e.preventDefault();
    start.mutate(
      { ...v, remoteOwner: v.remoteOwner.trim(), remoteName: v.remoteName.trim(), name: v.name?.trim() || undefined },
      {
        onSuccess: (imp) => {
          notify.ok(t("import.started"));
          const [o, n] = imp.repoFullName.split("/");
          if (o && n) void navigate({ to: "/$owner/$name/settings", params: { owner: o, name: n }, search: { section: "github" } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <form className="acc-form" onSubmit={submit}>
      <Input label={t("remoteOwner")} required mono value={v.remoteOwner} onChange={(e) => set({ remoteOwner: e.target.value })} />
      <Input label={t("remoteName")} required mono value={v.remoteName} onChange={(e) => set({ remoteName: e.target.value })} />
      <Input label={t("import.name")} mono hint={t("import.nameHint")} value={v.name ?? ""} onChange={(e) => set({ name: e.target.value })} />
      <fieldset className="wk-fieldset">
        <legend>{t("import.include")}</legend>
        {importKinds.map((k) => (
          <label key={k} className="acc-check">
            <input type="checkbox" checked={include.includes(k)} onChange={() => set({ include: include.includes(k) ? include.filter((x) => x !== k) : [...include, k] })} />
            <span>{t(`import.kind.${k}`)}</span>
          </label>
        ))}
      </fieldset>
      <div className="acc-actions">
        <DialogClose asChild>
          <Button variant="ghost">{t("cancel")}</Button>
        </DialogClose>
        <Button variant="primary" type="submit" disabled={start.isPending}>
          {t("import.submit")}
        </Button>
      </div>
    </form>
  );
}

function ImportStatus({ owner, name }: { owner: string; name: string }) {
  const t = useT("github");
  const imp = useRepoImport(owner, name);
  if (!imp.data) return null;
  const d = imp.data;
  return (
    <Card title={t("import.statusTitle")}>
      <div className="acc-pad wk-gh-status">
        <Badge tone={tone[d.status]}>{t(`import.status.${d.status}`)}</Badge>
        <span className="ui-mono">
          {d.remoteOwner}/{d.remoteName}
        </span>
        {d.finishedAt ? <span className="ui-note">{t("import.finished", { when: formatDate(d.finishedAt) })}</span> : d.startedAt && <span className="ui-note">{t("import.startedAt", { when: formatDate(d.startedAt) })}</span>}
      </div>
      {d.lastError && <p className="ui-note acc-pad wk-dl-err">{d.lastError}</p>}
    </Card>
  );
}

/** The imports the viewer started, on any repository it can still read. */
function MyImports() {
  const t = useT("github");
  const [page, setPage] = useState(1);
  const list = useMyImports(page);
  return (
    <Card title={t("imports.title")}>
      <p className="ui-note acc-pad">{t("imports.hint")}</p>
      <Gate query={list}>
        {(res) =>
          res.items.length === 0 ? (
            <p className="ui-note acc-pad">{t("imports.empty")}</p>
          ) : (
            <>
              <Table>
                <TableHead>
                  <Tr>
                    <Th>{t("imports.repo")}</Th>
                    <Th>{t("imports.source")}</Th>
                    <Th>{t("imports.status")}</Th>
                    <Th>{t("imports.created")}</Th>
                  </Tr>
                </TableHead>
                <TableBody>
                  {res.items.map((i) => {
                    const [o = "", n = ""] = i.repoFullName.split("/");
                    return (
                      <Tr key={i.id}>
                        <Td>
                          <Link className="ui-mono" to="/$owner/$name/settings" params={{ owner: o, name: n }} search={{ section: "github" }}>
                            {i.repoFullName}
                          </Link>
                        </Td>
                        <Td>
                          <span className="ui-mono">
                            {i.remoteOwner}/{i.remoteName}
                          </span>
                        </Td>
                        <Td>
                          <Badge tone={tone[i.status]}>{t(`import.status.${i.status}`)}</Badge>
                          {i.lastError && <div className="ui-note wk-dl-err">{i.lastError}</div>}
                        </Td>
                        <Td>{formatDate(i.createdAt)}</Td>
                      </Tr>
                    );
                  })}
                </TableBody>
              </Table>
              <Pager page={page} hasMore={res.hasMore} onPage={setPage} />
            </>
          )
        }
      </Gate>
    </Card>
  );
}

function SyncLink({ owner, name }: { owner: string; name: string }) {
  const t = useT("github");
  const tc = useT("common");
  const link = useSyncLink(owner, name);
  if (link.isPending) return <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>;
  if (link.isError && !isNotFound(link.error)) return <p role="alert" className="ui-note acc-pad">{link.error.message}</p>;
  const l = link.data;
  return (
    <Card title={t("sync.title")}>
      <p className="ui-note acc-pad">{t("sync.hint")}</p>
      {l && <SyncStatus owner={owner} name={name} link={l} />}
      <SyncForm key={l?.updatedAt ?? "new"} owner={owner} name={name} link={l} />
    </Card>
  );
}

function SyncStatus({ owner, name, link }: { owner: string; name: string; link: GitHubSyncLink }) {
  const t = useT("github");
  const notify = useNotify();
  const run = useRunSync(owner, name);
  const refs = useSyncRefs(owner, name, true);
  const resolve = useResolveSync(owner, name);
  const bad = (refs.data ?? []).filter((r) => r.status !== "ok");

  return (
    <div className="wk-gh-sync">
      <div className="wk-gh-status">
        <Badge tone={tone[link.status]}>{t(`sync.status.${link.status}`)}</Badge>
        {!link.enabled && <Badge tone="warn">{t("sync.paused")}</Badge>}
        {link.readOnly && <Badge>{t("sync.readOnly")}</Badge>}
        {link.lastSuccessAt && <span className="ui-note">{t("sync.lastOk", { when: formatDate(link.lastSuccessAt) })}</span>}
        {link.nextAttemptAt && <span className="ui-note">{t("sync.next", { when: formatDate(link.nextAttemptAt) })}</span>}
        <span className="acc-grow" />
        <Button size="sm" icon="upload" disabled={run.isPending} onClick={() => run.mutate(undefined, { onSuccess: () => notify.ok(t("sync.queued")), onError: notify.fail })}>
          {t("sync.run")}
        </Button>
      </div>
      {link.lastError && <p className="ui-note wk-dl-err">{t("sync.error", { error: link.lastError, count: link.failures })}</p>}
      {link.lfsSkipped && <p className="ui-note">{t("sync.lfs")}</p>}
      {bad.length > 0 && (
        <ul className="rp-list wk-rules">
          {bad.map((r) => (
            <li key={r.ref} className="wk-rule">
              <Badge tone={tone[r.status]}>{t(`sync.status.${r.status}`)}</Badge>
              <span className="ui-mono">{r.ref}</span>
              <span className="ui-note">
                {r.lastError ||
                  t("sync.refShas", { local: r.localSha.slice(0, 7), remote: r.remoteSha.slice(0, 7) })}
              </span>
              <span className="acc-grow" />
              {r.status === "diverged" &&
                (["keep_ervisio", "keep_github", "merge_request"] as const).map((res) => (
                  <Button
                    key={res}
                    size="sm"
                    disabled={resolve.isPending}
                    onClick={() => resolve.mutate({ ref: r.ref, resolution: res }, { onSuccess: () => notify.ok(t("sync.resolved")), onError: notify.fail })}
                  >
                    {t(`sync.resolve.${res}`)}
                  </Button>
                ))}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SyncForm({ owner, name, link }: { owner: string; name: string; link?: GitHubSyncLink }) {
  const t = useT("github");
  const notify = useNotify();
  const conns = useConnections(owner);
  const put = usePutSync(owner, name);
  const [v, setV] = useState<GitHubSyncLinkPut>({
    connectionId: link?.connectionId ?? "",
    mode: link?.mode ?? "backup",
    remoteOwner: link?.remoteOwner ?? "",
    remoteName: link?.remoteName ?? name,
    prune: link?.prune ?? false,
    createMissing: link?.createMissing ?? false,
    intervalMinutes: link?.intervalMinutes ?? 10,
    enabled: link?.enabled ?? true,
  });
  const [trust, setTrust] = useState((link?.trustGithub ?? []).join(", "));
  const set = (p: Partial<GitHubSyncLinkPut>) => setV((x) => ({ ...x, ...p }));
  const options = (conns.data ?? []).map((c) => ({ value: c.id, label: c.name }));
  const connectionId = v.connectionId || options[0]?.value || "";

  function submit(e: FormEvent) {
    e.preventDefault();
    const body = { ...v, connectionId, remoteOwner: v.remoteOwner.trim(), remoteName: v.remoteName.trim(), trustGithub: v.mode === "sync" ? splitList(trust) : [] };
    put.mutate(body, { onSuccess: () => notify.ok(t("sync.saved")), onError: notify.fail });
  }

  if (options.length === 0 && !link) return <p className="ui-note acc-pad">{t("sync.needConnection")}</p>;
  return (
    <form className="acc-form acc-pad" onSubmit={submit}>
      <Select label={t("sync.connection")} value={connectionId} onValueChange={(c) => set({ connectionId: c })} options={options} />
      <Select label={t("sync.mode")} value={v.mode} onValueChange={(m) => set({ mode: m as GitHubSyncLinkPut["mode"] })} options={(["mirror", "backup", "sync"] as const).map((m) => ({ value: m, label: t(`sync.modes.${m}`) }))} />
      <p className="ui-note">{t(`sync.modes.${v.mode}.hint`)}</p>
      <Input label={t("remoteOwner")} required mono value={v.remoteOwner} onChange={(e) => set({ remoteOwner: e.target.value })} />
      <Input label={t("remoteName")} required mono value={v.remoteName} onChange={(e) => set({ remoteName: e.target.value })} />
      <Input label={t("sync.interval")} type="number" min={1} value={String(v.intervalMinutes)} onChange={(e) => set({ intervalMinutes: Math.max(1, Number(e.target.value) || 1) })} />
      {v.mode === "sync" && <Input label={t("sync.trust")} hint={t("sync.trustHint")} mono value={trust} onChange={(e) => setTrust(e.target.value)} />}
      <label className="acc-check">
        <input type="checkbox" checked={v.prune} onChange={(e) => set({ prune: e.target.checked })} />
        <span>{t("sync.prune")}</span>
      </label>
      {v.mode !== "mirror" && (
        <label className="acc-check">
          <input type="checkbox" checked={v.createMissing} onChange={(e) => set({ createMissing: e.target.checked })} />
          <span>{t("sync.createMissing")}</span>
        </label>
      )}
      <label className="acc-check">
        <input type="checkbox" checked={v.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
        <span>{t("sync.enabled")}</span>
      </label>
      <div className="acc-actions">
        {link && (
          <Button variant="ghost" disabled={put.isPending} onClick={() => window.confirm(t("sync.unlinkConfirm")) && put.mutate(null, { onSuccess: () => notify.ok(t("sync.unlinked")), onError: notify.fail })}>
            {t("sync.unlink")}
          </Button>
        )}
        <Button variant="primary" type="submit" disabled={put.isPending || !connectionId}>
          {link ? t("sync.save") : t("sync.link")}
        </Button>
      </div>
    </form>
  );
}
