import { useState } from "react";
import { useApplyUpdate, useCheckUpdates, useSetAutoUpdate, useUpdateStatus, type AvailableRelease, type UpdateStatus } from "../../api/updates.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Badge, Button, Card, ConfirmDialog, EmptyState, SettingRow, Toggle } from "../../ui/index.ts";
import { Secret, useNotify } from "../account/common.tsx";
import { When } from "../code/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";

const STEPS = ["backup", "fetch", "pull", "up", "done"] as const;

/** Release notes of the releases newer than the running one, newest first. */
function Releases({ list }: { list: AvailableRelease[] }) {
  const t = useT("admin");
  return (
    <>
      {list.map((r) => (
        <details key={r.version} className="acc-pad" open={r === list[0]}>
          <summary>
            <strong>v{r.version}</strong>
            {r.name && r.name !== `v${r.version}` && <> · {r.name}</>} · <When iso={r.publishedAt} />
          </summary>
          {r.notes.trim() ? <Markdown source={r.notes} className="rp-md" /> : <p className="ui-note">{t("updates.noNotes")}</p>}
          <p><a href={r.url} target="_blank" rel="noreferrer">{t("updates.onGitHub")}</a></p>
        </details>
      ))}
    </>
  );
}

/** Progress of the update the updater container runs, with the end of its log. */
function Progress({ st }: { st: UpdateStatus }) {
  const t = useT("admin");
  const u = st.updater;
  if (u.pending === "" && u.state !== "running" && u.state !== "done" && u.state !== "failed") return null;
  const target = u.pending || u.version;
  const at = u.pending ? -1 : STEPS.indexOf(u.step as (typeof STEPS)[number]);
  const running = u.pending !== "" || u.state === "running";
  return (
    <Card title={t("updates.progress", { version: target })}>
      <div className="acc-pad" role="status" aria-live="polite">
        {running && <p>{u.pending ? t("updates.waitingUpdater") : t("updates.running")}</p>}
        {u.state === "done" && !running && (
          <p>{st.current === u.version ? t("updates.doneOk", { version: u.version }) : t("updates.doneRestart", { version: u.version })}</p>
        )}
        {u.state === "failed" && !running && <p className="ax-err">{t("updates.failed", { version: u.version })}</p>}
        <ol className="ui-inline" aria-label={t("updates.steps")}>
          {STEPS.map((s, i) => (
            <li key={s}>
              <Badge tone={i < at || (s === "done" && at === i) ? "ok" : i === at ? (u.state === "failed" ? "err" : "info") : "neutral"}>
                {t(`updates.step.${s}`)}
              </Badge>
            </li>
          ))}
        </ol>
      </div>
      {u.log.length > 0 && <pre className="acc-code acc-code-multi" aria-label={t("updates.log")}>{u.log.join("\n")}</pre>}
    </Card>
  );
}

/** Why the web update is not possible, and what to do instead. */
function NoUpdater({ st }: { st: UpdateStatus }) {
  const t = useT("admin");
  if (st.deploy === "docker" && !st.updater.available) return <p className="ui-note acc-pad">{t("updates.noUpdater")}</p>;
  if (st.deploy === "native") return <p className="ui-note acc-pad">{t("updates.native")}</p>;
  if (st.deploy === "") return <p className="ui-note acc-pad">{t("updates.unknownDeploy")}</p>;
  return null;
}

function UpdatesBody({ st }: { st: UpdateStatus }) {
  const t = useT("admin");
  const notify = useNotify();
  const check = useCheckUpdates();
  const auto = useSetAutoUpdate();
  const apply = useApplyUpdate();
  const [confirm, setConfirm] = useState(false);
  const target = st.newer[0];
  const busy = st.updater.state === "running" || st.updater.pending !== "";

  function start() {
    if (!target) return;
    apply.mutate({ version: target.version }, {
      onSuccess: () => { setConfirm(false); notify.ok(t("updates.started")); },
      onError: (e) => { setConfirm(false); notify.fail(e); },
    });
  }

  return (
    <>
      <Card
        title={t("updates.version")}
        actions={st.check && (
          <Button size="sm" disabled={check.isPending} onClick={() => check.mutate(undefined, { onError: notify.fail })}>
            {t("updates.checkNow")}
          </Button>
        )}
      >
        <dl className="ui-kv">
          <div className="ui-kv-row">
            <dt>{t("updates.current")}</dt>
            <dd>
              <span className="ui-inline">
                <span className="ui-mono">{st.release ? `v${st.current}` : st.current}</span>
                {!st.release ? <Badge tone="warn">{t("updates.devBuild")}</Badge>
                  : target ? <Badge tone="warn">{t("updates.available", { version: target.version })}</Badge>
                    : st.checkedAt && <Badge tone="ok">{t("updates.upToDate")}</Badge>}
              </span>
            </dd>
          </div>
          {st.latest && (
            <div className="ui-kv-row">
              <dt>{t("updates.latest")}</dt>
              <dd className="ui-mono">v{st.latest.version}</dd>
            </div>
          )}
          <div className="ui-kv-row">
            <dt>{t("updates.checked")}</dt>
            <dd>{!st.check ? t("updates.checkOff") : st.checkedAt ? <When iso={st.checkedAt} /> : t("updates.never")}</dd>
          </div>
          <div className="ui-kv-row">
            <dt>{t("updates.source")}</dt>
            <dd className="ui-mono">{st.repository}</dd>
          </div>
        </dl>
        {st.checkError && <p role="alert" className="ui-note acc-pad ax-err">{t("updates.checkError", { error: st.checkError })}</p>}
        {target && (
          <div className="acc-actions acc-pad">
            <NoUpdater st={st} />
            <ConfirmDialog
              open={confirm}
              onOpenChange={setConfirm}
              trigger={<Button variant="primary" icon="download" disabled={!st.canApply || busy}>{t("updates.install", { version: target.version })}</Button>}
              title={t("updates.confirmTitle", { version: target.version })}
              description={t("updates.confirmText")}
              confirmLabel={t("updates.install", { version: target.version })}
              pending={apply.isPending}
              onConfirm={start}
            />
          </div>
        )}
      </Card>

      <Progress st={st} />

      {st.deploy === "docker" && (
        <Card title={t("updates.auto")}>
          <SettingRow label={t("updates.auto")} description={t("updates.auto.hint")}>
            <Toggle
              label={t("updates.auto")}
              checked={st.auto}
              disabled={auto.isPending}
              onChange={(on) => auto.mutate({ enabled: on }, { onSuccess: () => notify.ok(t("updates.saved")), onError: notify.fail })}
            />
          </SettingRow>
          {st.auto && !st.updater.available && <p className="ui-note acc-pad">{t("updates.noUpdater")}</p>}
        </Card>
      )}

      {st.newer.length > 0 && (
        <Card title={t("updates.whatsNew")}>
          <Releases list={st.newer} />
        </Card>
      )}

      {st.command && (
        <Card title={t("updates.terminal")}>
          <div className="acc-pad">
            <Secret label={st.deploy === "docker" ? t("updates.terminal.docker") : t("updates.terminal.native")} value={st.command} />
          </div>
        </Card>
      )}
    </>
  );
}

/** Admin > Updates: running version, new releases, the Docker updater and the manual command. */
export function AdminUpdates() {
  const t = useT("admin");
  const q = useUpdateStatus(true);
  return (
    <AdminPane title={t("updates.title")} subtitle={t("updates.subtitle")}>
      {q.data ? (
        <UpdatesBody st={q.data} />
      ) : q.isError ? (
        <div role="alert">
          <EmptyState icon="alert" title={t("updates.loadError")} description={q.error.message} />
        </div>
      ) : (
        <p className="ui-note acc-pad">{t("updates.loading")}</p>
      )}
    </AdminPane>
  );
}
