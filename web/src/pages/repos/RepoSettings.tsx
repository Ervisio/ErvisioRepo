import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useDeleteRepo, useRepo, useTransferRepo, useUpdateRepo, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Dialog, DialogClose, EmptyState, Input, Page, Select } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { repoHooks } from "../../api/settings.ts";
import { Entries, Environments, Runners } from "../actions/Settings.tsx";
import { Webhooks } from "../settings/Webhooks.tsx";
import { GitHubSettings } from "./GitHubSettings.tsx";
import { visibilities } from "./RepoList.tsx";
import { BranchProtections, TagRules } from "./RulesSettings.tsx";

export type SettingsSection = "protection" | "tags" | "hooks" | "github" | "secrets" | "variables" | "environments" | "runners";
const sections: (SettingsSection | undefined)[] = [undefined, "protection", "tags", "hooks", "github", "secrets", "variables", "environments", "runners"];

function General({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const update = useUpdateRepo(repo.owner, repo.name);
  const [name, setName] = useState(repo.name);
  const [description, setDescription] = useState(repo.description);
  const [visibility, setVisibility] = useState<Visibility>(repo.visibility);

  function submit(e: FormEvent) {
    e.preventDefault();
    update.mutate(
      { name: name.trim(), description: description.trim(), visibility },
      {
        onSuccess: (r) => {
          notify.ok(t("settings.saved"));
          if (r.name !== repo.name) void navigate({ to: "/$owner/$name/settings", params: { owner: r.owner, name: r.name }, replace: true });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Card title={t("settings.general")}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("create.name")} required maxLength={100} mono autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} hint={t("settings.renameHint")} />
        <Input label={t("create.descriptionLabel")} maxLength={350} value={description} onChange={(e) => setDescription(e.target.value)} />
        <Select label={t("create.visibility")} value={visibility} onValueChange={(v) => setVisibility(v as Visibility)} options={visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))} />
        <p className="ui-note">{t(`visibility.${visibility}.hint`)}</p>
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={update.isPending}>{t("settings.save")}</Button>
        </div>
      </form>
    </Card>
  );
}

function Transfer({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const transfer = useTransferRepo(repo.owner, repo.name);
  const [open, setOpen] = useState(false);
  const [newOwner, setNewOwner] = useState("");
  const [confirm, setConfirm] = useState("");
  const ready = newOwner.trim() !== "" && confirm === repo.fullName;

  function run() {
    transfer.mutate(
      { newOwner: newOwner.trim() },
      {
        onSuccess: (r) => {
          setOpen(false);
          notify.ok(t("transfer.done"));
          void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Card title={t("transfer.title")}>
      <p className="ui-note acc-pad">{t("transfer.hint")}</p>
      <div className="acc-actions">
        <Dialog
          open={open}
          onOpenChange={setOpen}
          title={t("transfer.dialogTitle", { name: repo.fullName })}
          description={t("transfer.dialogHint")}
          trigger={<Button>{t("transfer.button")}</Button>}
          footer={
            <>
              <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
              <Button variant="danger" disabled={!ready || transfer.isPending} onClick={run}>{t("transfer.confirm")}</Button>
            </>
          }
        >
          <div className="acc-form">
            <Input label={t("transfer.newOwner")} mono autoComplete="off" value={newOwner} onChange={(e) => setNewOwner(e.target.value)} />
            <Input label={t("confirm.type", { name: repo.fullName })} mono autoComplete="off" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </div>
        </Dialog>
      </div>
    </Card>
  );
}

function Danger({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const remove = useDeleteRepo(repo.owner, repo.name);
  const [open, setOpen] = useState(false);
  const [confirm, setConfirm] = useState("");

  function run() {
    remove.mutate(undefined, {
      onSuccess: () => {
        setOpen(false);
        notify.ok(t("delete.done"));
        void navigate({ to: "/repos" });
      },
      onError: notify.fail,
    });
  }

  return (
    <Card title={t("delete.title")}>
      <p className="ui-note acc-pad">{t("delete.hint")}</p>
      <div className="acc-actions">
        <Dialog
          open={open}
          onOpenChange={setOpen}
          title={t("delete.dialogTitle", { name: repo.fullName })}
          description={t("delete.dialogHint")}
          trigger={<Button variant="danger">{t("delete.button")}</Button>}
          footer={
            <>
              <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
              <Button variant="danger" disabled={confirm !== repo.fullName || remove.isPending} onClick={run}>{t("delete.confirm")}</Button>
            </>
          }
        >
          <Input label={t("confirm.type", { name: repo.fullName })} mono autoComplete="off" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Dialog>
      </div>
    </Card>
  );
}

function SectionNav({ owner, name, section }: { owner: string; name: string; section?: SettingsSection }) {
  const t = useT("repos");
  return (
    <nav className="ui-tablist wk-tabs" aria-label={t("settings.sections")}>
      {sections.map((s) => (
        <Link
          key={s ?? "general"}
          className="ui-tab"
          data-state={s === section ? "active" : "inactive"}
          aria-current={s === section ? "page" : undefined}
          to="/$owner/$name/settings"
          params={{ owner, name }}
          search={{ section: s }}
        >
          {t(`settings.section.${s ?? "general"}`)}
        </Link>
      ))}
    </nav>
  );
}

export function RepoSettings({ owner, name, section }: { owner: string; name: string; section?: SettingsSection }) {
  const t = useT("repos");
  const tc = useT("common");
  const repo = useRepo(owner, name);
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
  return (
    <Page section="code" title={t("settings.title")} subtitle={r.fullName} actions={<Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }}><Button>{t("settings.back")}</Button></Link>}>
      {r.permission === "admin" ? (
        <>
          <SectionNav owner={r.owner} name={r.name} section={section} />
          {section === undefined && (
            <>
              <General key={r.updatedAt} repo={r} />
              <Transfer repo={r} />
              <Danger repo={r} />
            </>
          )}
          {section === "protection" && <BranchProtections owner={r.owner} name={r.name} />}
          {section === "tags" && <TagRules owner={r.owner} name={r.name} />}
          {section === "hooks" && <Webhooks base={repoHooks(r.owner, r.name)} scope="repo" />}
          {section === "github" && <GitHubSettings owner={r.owner} name={r.name} />}
          {section === "secrets" && <Entries scope={{ kind: "repo", owner: r.owner, name: r.name }} kind="secrets" />}
          {section === "variables" && <Entries scope={{ kind: "repo", owner: r.owner, name: r.name }} kind="variables" />}
          {section === "environments" && <Environments owner={r.owner} name={r.name} />}
          {section === "runners" && <Runners scope={{ kind: "repo", owner: r.owner, name: r.name }} />}
        </>
      ) : (
        <EmptyState icon="lock" title={t("settings.forbidden")} />
      )}
    </Page>
  );
}
