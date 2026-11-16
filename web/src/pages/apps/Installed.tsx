import { useState, type ReactNode } from "react";
import {
  useAcceptPermissions,
  useInstallation,
  useInstallations,
  useSuspendInstallation,
  useUninstall,
  useUpdateInstallation,
  type AppInstallation,
  type AppsOwner,
  type RepositorySelection,
} from "../../api/apps.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, ConfirmDialog, DangerRow, DangerZone, EmptyState, SettingsSection } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "../account/common.tsx";
import { Initial, PermissionDiff, PermissionSummary, RepoSelection } from "./common.tsx";

/** Links inside the installed apps section: the list or one installation. */
export type InstalledNav = {
  link: (installation: number | undefined, props: { className?: string; children: ReactNode }) => ReactNode;
  go: (installation: number | undefined) => void;
};

/** Apps installed on an account or organization: the list, or one installation to configure. */
export function InstalledApps({ owner, installation, nav }: { owner: AppsOwner; installation?: number; nav: InstalledNav }) {
  if (installation !== undefined) return <InstallationPage key={installation} id={installation} nav={nav} />;
  return <InstalledList owner={owner} nav={nav} />;
}

function InstalledList({ owner, nav }: { owner: AppsOwner; nav: InstalledNav }) {
  const t = useT("apps");
  const list = useInstallations(owner);
  return (
    <SettingsSection title={t("installed.title")} description={t("installed.hint")}>
      <Async query={list} empty={t("installed.empty")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((i) => (
              <div key={i.id} className="st-item">
                <Initial name={i.app.name} />
                <div className="st-item-m">
                  <b>{i.app.name}</b> <span className="ui-note ui-mono">{i.app.botLogin}</span>
                  <div className="ui-note">
                    {i.repositorySelection === "all" ? t("repos.all") : t("repos.selected")} · {t("installations.since", { date: formatDate(i.createdAt) })}
                  </div>
                </div>
                <span className="st-item-a">
                  {i.suspendedAt && <Badge tone="warn">{t("installed.suspended")}</Badge>}
                  {i.pending && <Badge tone="info">{t("installed.pending")}</Badge>}
                  {nav.link(i.id, { className: "ui-btn ui-btn-secondary ui-btn-sm", children: t("installed.configure") })}
                </span>
              </div>
            ))}
          </div>
        )}
      </Async>
    </SettingsSection>
  );
}

function InstallationPage({ id, nav }: { id: number; nav: InstalledNav }) {
  const t = useT("apps");
  const tc = useT("common");
  const q = useInstallation(id);
  if (q.isPending) return <p role="status" className="ui-note">{tc("state.loading")}</p>;
  if (q.isError) {
    return (
      <div role="alert">
        <EmptyState
          icon="alert"
          title={t("installed.loadError")}
          description={q.error.message}
          action={nav.link(undefined, { className: "ui-btn ui-btn-secondary", children: t("installed.back") })}
        />
      </div>
    );
  }
  return <InstallationDetail inst={q.data} nav={nav} />;
}

function InstallationDetail({ inst, nav }: { inst: AppInstallation; nav: InstalledNav }) {
  const t = useT("apps");
  const notify = useNotify();
  const accept = useAcceptPermissions(inst.id);
  const suspend = useSuspendInstallation(inst.id);
  const uninstall = useUninstall(inst.id);
  return (
    <>
      <SettingsSection title={inst.app.name} description={nav.link(undefined, { children: t("installed.back") })}>
        <div className="oa-head">
          <Initial name={inst.app.name} size="lg" />
          <p className="ui-note">
            <span className="ui-mono">{inst.app.botLogin}</span> · {t("installed.on", { account: inst.account.name })} ·{" "}
            {t("installations.since", { date: formatDate(inst.createdAt) })}
            {inst.suspendedAt && <> · <Badge tone="warn">{t("installed.suspendedOn", { date: formatDate(inst.suspendedAt) })}</Badge></>}
          </p>
        </div>
      </SettingsSection>
      {inst.pending && (
        <SettingsSection title={t("pending.title")} description={t("pending.hint")}>
          <div className="acc-block" role="status">
            <PermissionDiff
              current={inst.permissions}
              requested={inst.requestedPermissions ?? inst.permissions}
              events={inst.events}
              requestedEvents={inst.requestedEvents ?? inst.events}
            />
            <div className="acc-actions">
              <Button
                variant="primary"
                disabled={accept.isPending}
                onClick={() => accept.mutate(undefined, { onSuccess: () => notify.ok(t("pending.accepted")), onError: notify.fail })}
              >
                {t("pending.accept")}
              </Button>
            </div>
          </div>
        </SettingsSection>
      )}
      <SettingsSection title={t("installed.permissions")}>
        <PermissionSummary perms={inst.permissions} events={inst.events} />
      </SettingsSection>
      <RepoAccess key={inst.updatedAt} inst={inst} />
      <DangerZone title={t("danger.title")}>
        <DangerRow
          title={inst.suspendedAt ? t("installed.unsuspend") : t("installed.suspend")}
          description={inst.suspendedAt ? t("installed.unsuspendHint") : t("installed.suspendHint")}
          action={
            <Button
              variant={inst.suspendedAt ? "secondary" : "danger"}
              disabled={suspend.isPending}
              onClick={() => suspend.mutate(!inst.suspendedAt, {
                onSuccess: () => notify.ok(inst.suspendedAt ? t("installed.unsuspended") : t("installed.suspendedDone")),
                onError: notify.fail,
              })}
            >
              {inst.suspendedAt ? t("installed.unsuspend") : t("installed.suspend")}
            </Button>
          }
        />
        <DangerRow
          title={t("installed.uninstall")}
          description={t("installed.uninstallHint")}
          action={
            <ConfirmDialog
              trigger={<Button variant="danger">{t("installed.uninstall")}</Button>}
              title={t("installed.uninstallTitle", { app: inst.app.name })}
              description={t("installed.uninstallHint")}
              confirmLabel={t("installed.uninstall")}
              pending={uninstall.isPending}
              onConfirm={() => uninstall.mutate(undefined, { onSuccess: () => { notify.ok(t("installed.uninstalled")); nav.go(undefined); }, onError: notify.fail })}
            />
          }
        />
      </DangerZone>
    </>
  );
}

function RepoAccess({ inst }: { inst: AppInstallation }) {
  const t = useT("apps");
  const notify = useNotify();
  const update = useUpdateInstallation(inst.id);
  const [selection, setSelection] = useState<RepositorySelection>(inst.repositorySelection);
  const [repos, setRepos] = useState<string[]>(() => (inst.repositories ?? []).map((r) => r.name));
  const invalid = selection === "selected" && repos.length === 0;
  return (
    <SettingsSection title={t("repos.title")} description={t("repos.hint")}>
      <div className="st-card">
        <RepoSelection account={inst.account} selection={selection} repos={repos} onChange={(s, r) => { setSelection(s); setRepos(r); }} />
        <div className="acc-actions">
          <Button
            variant="primary"
            disabled={update.isPending || invalid}
            onClick={() => update.mutate(
              { repositorySelection: selection, ...(selection === "selected" ? { repositories: repos } : {}) },
              { onSuccess: () => notify.ok(t("repos.saved")), onError: notify.fail },
            )}
          >
            {t("repos.save")}
          </Button>
          {invalid && <span className="ui-note">{t("repos.pickOne")}</span>}
        </div>
      </div>
    </SettingsSection>
  );
}
