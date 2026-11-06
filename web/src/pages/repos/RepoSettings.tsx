import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useSession } from "../../api/identity.ts";
import { useTiering } from "../../api/useStorage.ts";
import { useDeleteRepo, useRepo, useTransferRepo, useUpdateRepo, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import {
  Badge,
  Button,
  ConfirmDialog,
  DangerRow,
  DangerZone,
  EmptyState,
  Input,
  Page,
  SaveBar,
  Select,
  SettingRow,
  SettingsCard,
  SettingsLayout,
  SettingsSection,
  useDraft,
  type SettingsNavGroup,
  type SettingsNavItem,
} from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { formatBytes } from "../code/common.tsx";
import { repoHooks } from "../../api/settings.ts";
import { Entries, Environments, Runners } from "../actions/Settings.tsx";
import { Webhooks } from "../settings/Webhooks.tsx";
import { Collaborators, Labels, Milestones } from "./RepoAccess.tsx";
import { GitHubSettings } from "./GitHubSettings.tsx";
import { visibilities } from "./RepoList.tsx";
import { BranchProtections, TagRules } from "./RulesSettings.tsx";

export const repoSettingsSections = ["collaborators", "labels", "milestones", "protection", "tags", "hooks", "github", "secrets", "variables", "environments", "runners", "storage"] as const;
export type RepoSettingsSection = (typeof repoSettingsSections)[number];

function General({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const update = useUpdateRepo(repo.owner, repo.name);
  const draft = useDraft({ name: repo.name, description: repo.description, visibility: repo.visibility as Visibility });
  const v = draft.value;

  function save() {
    update.mutate(
      { name: v.name.trim(), description: v.description.trim(), visibility: v.visibility },
      {
        onSuccess: (r) => {
          draft.rebase({ name: r.name, description: r.description, visibility: r.visibility });
          notify.ok(t("settings.saved"));
          if (r.name !== repo.name) void navigate({ to: "/$owner/$name/settings", params: { owner: r.owner, name: r.name }, replace: true });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <SettingsSection title={t("settings.general")}>
      <SettingsCard>
        <SettingRow label={t("create.name")} description={t("settings.renameHint")} dirty={draft.isDirty("name")} was={draft.initial.name}>
          <Input label={t("create.name")} hideLabel required maxLength={100} mono autoComplete="off" value={v.name} onChange={(e) => draft.set("name", e.target.value)} />
        </SettingRow>
        <SettingRow label={t("create.descriptionLabel")} dirty={draft.isDirty("description")} was={draft.initial.description}>
          <Input label={t("create.descriptionLabel")} hideLabel maxLength={350} value={v.description} onChange={(e) => draft.set("description", e.target.value)} />
        </SettingRow>
        <SettingRow label={t("create.visibility")} description={t(`visibility.${v.visibility}.hint`)} dirty={draft.isDirty("visibility")} was={t(`visibility.${draft.initial.visibility}`)}>
          <Select label={t("create.visibility")} hideLabel value={v.visibility} onValueChange={(x) => draft.set("visibility", x as Visibility)} options={visibilities.map((x) => ({ value: x, label: t(`visibility.${x}`) }))} />
        </SettingRow>
      </SettingsCard>
      <SaveBar draft={draft} labels={{ name: t("create.name"), description: t("create.descriptionLabel"), visibility: t("create.visibility") }} onSave={save} saving={update.isPending} />
    </SettingsSection>
  );
}

/** Transfer and delete, kept apart at the bottom of the General page. */
function Danger({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const transfer = useTransferRepo(repo.owner, repo.name);
  const remove = useDeleteRepo(repo.owner, repo.name);
  const [newOwner, setNewOwner] = useState("");

  return (
    <DangerZone title={t("danger.title")}>
      <DangerRow
        title={t("transfer.title")}
        description={t("transfer.hint")}
        action={
          <ConfirmDialog
            trigger={<Button>{t("transfer.button")}</Button>}
            title={t("transfer.dialogTitle", { name: repo.fullName })}
            description={t("transfer.dialogHint")}
            confirmLabel={t("transfer.confirm")}
            typeToConfirm={repo.fullName}
            pending={transfer.isPending || newOwner.trim() === ""}
            onConfirm={() =>
              transfer.mutate(
                { newOwner: newOwner.trim() },
                {
                  onSuccess: (r) => {
                    notify.ok(t("transfer.done"));
                    void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
                  },
                  onError: notify.fail,
                },
              )
            }
          >
            <Input label={t("transfer.newOwner")} mono autoComplete="off" value={newOwner} onChange={(e) => setNewOwner(e.target.value)} />
          </ConfirmDialog>
        }
      />
      <DangerRow
        title={t("delete.title")}
        description={t("delete.hint")}
        action={
          <ConfirmDialog
            trigger={<Button variant="danger">{t("delete.button")}</Button>}
            title={t("delete.dialogTitle", { name: repo.fullName })}
            description={t("delete.dialogHint")}
            confirmLabel={t("delete.confirm")}
            typeToConfirm={repo.fullName}
            pending={remove.isPending}
            onConfirm={() =>
              remove.mutate(undefined, {
                onSuccess: () => {
                  notify.ok(t("delete.done"));
                  void navigate({ to: "/repos" });
                },
                onError: notify.fail,
              })
            }
          />
        }
      />
    </DangerZone>
  );
}

/** Hot-tier controls for instance admins: pin keeps the repository local, evict moves it to the remote tier now. */
function Storage({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const tiering = useTiering();
  const state = tiering.data?.state ?? repo.storageState;
  const run = (op: "pin" | "unpin" | "evict") =>
    tiering.mutate({ owner: repo.owner, name: repo.name, op }, { onSuccess: () => notify.ok(t(`storage.${op}Done`)), onError: notify.fail });

  return (
    <SettingsSection title={t("settings.section.storage")} description={t("storage.hint")}>
      <SettingsCard>
        <SettingRow label={t("storage.state")} description={tiering.data && (tiering.data.pinned ? t("storage.pinned") : t("storage.unpinned"))}>
          <Badge tone={state === "hot" ? "ok" : state === "degraded" ? "err" : "neutral"}>{t(`storage.stateValue.${state}`)}</Badge>
        </SettingRow>
        <SettingRow label={t("storage.size")}>
          <span className="ui-mono">{formatBytes(tiering.data?.sizeBytes ?? repo.sizeBytes)}</span>
        </SettingRow>
        <SettingRow label={t("storage.pin")} description={t("storage.pinHint")}>
          <span className="ui-inline">
            <Button disabled={tiering.isPending} onClick={() => run("pin")}>{t("storage.pinButton")}</Button>
            <Button variant="ghost" disabled={tiering.isPending} onClick={() => run("unpin")}>{t("storage.unpinButton")}</Button>
          </span>
        </SettingRow>
        <SettingRow label={t("storage.evict")} description={t("storage.evictHint")}>
          <Button disabled={tiering.isPending || state !== "hot"} onClick={() => run("evict")}>{t("storage.evictButton")}</Button>
        </SettingRow>
      </SettingsCard>
    </SettingsSection>
  );
}

function useGroups(instanceAdmin: boolean): SettingsNavGroup[] {
  const t = useT("repos");
  const item = (id: RepoSettingsSection | "general", icon: SettingsNavItem["icon"]): SettingsNavItem => ({ id, icon, label: t(`settings.section.${id}`) });
  return [
    { title: t("settings.group.repo"), items: [item("general", "gear"), item("collaborators", "users"), ...(instanceAdmin ? [item("storage", "db")] : [])] },
    { title: t("settings.group.issues"), items: [item("labels", "layers"), item("milestones", "clock")] },
    { title: t("settings.group.code"), items: [item("protection", "shield"), item("tags", "tag"), item("hooks", "bell"), item("github", "branch")] },
    { title: t("settings.group.actions"), items: [item("secrets", "lock"), item("variables", "code"), item("environments", "grid"), item("runners", "cpu")] },
  ];
}

export function RepoSettings({ owner, name, section }: { owner: string; name: string; section?: RepoSettingsSection }) {
  const t = useT("repos");
  const tc = useT("common");
  const repo = useRepo(owner, name);
  const instanceAdmin = useSession().data?.role === "admin";
  const groups = useGroups(instanceAdmin);
  const title = `${owner}/${name}`;

  if (repo.isPending) {
    return (
      <Page title={title}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (repo.isError) {
    return (
      <Page title={title}>
        <div role="alert">
          <EmptyState icon="alert" title={t("error")} description={repo.error.message} action={<Button onClick={() => void repo.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      </Page>
    );
  }
  const r = repo.data;
  const scope = { kind: "repo" as const, owner: r.owner, name: r.name };
  return (
    <Page section="code" title={t("settings.title")} subtitle={r.fullName}>
      {r.permission === "admin" ? (
        <SettingsLayout
          label={t("settings.sections")}
          groups={groups}
          active={section ?? "general"}
          renderLink={(it, props) => (
            <Link to="/$owner/$name/settings" params={{ owner: r.owner, name: r.name }} search={{ section: it.id === "general" ? undefined : (it.id as RepoSettingsSection) }} {...props} />
          )}
        >
          {section === undefined && (
            <>
              <General key={r.updatedAt} repo={r} />
              <Danger repo={r} />
            </>
          )}
          {section === "collaborators" && <Collaborators owner={r.owner} name={r.name} />}
          {section === "labels" && <Labels owner={r.owner} name={r.name} />}
          {section === "milestones" && <Milestones owner={r.owner} name={r.name} />}
          {section === "protection" && <BranchProtections owner={r.owner} name={r.name} />}
          {section === "tags" && <TagRules owner={r.owner} name={r.name} />}
          {section === "hooks" && <Webhooks base={repoHooks(r.owner, r.name)} scope="repo" />}
          {section === "github" && <GitHubSettings owner={r.owner} name={r.name} />}
          {section === "secrets" && <Entries scope={scope} kind="secrets" />}
          {section === "variables" && <Entries scope={scope} kind="variables" />}
          {section === "environments" && <Environments owner={r.owner} name={r.name} />}
          {section === "runners" && <Runners scope={scope} />}
          {section === "storage" && instanceAdmin && <Storage repo={r} />}
        </SettingsLayout>
      ) : (
        <EmptyState icon="lock" title={t("settings.forbidden")} />
      )}
    </Page>
  );
}
