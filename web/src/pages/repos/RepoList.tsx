import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMyOrgs, useSession } from "../../api/identity.ts";
import { useCreateRepo, useForkRepo, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, Input, Select } from "../../ui/index.ts";
import { PinButton } from "./PinButton.tsx";
import { Async, useNotify } from "../account/common.tsx";
import type { UseQueryResult } from "@tanstack/react-query";

export const visibilities: Visibility[] = ["private", "internal", "public"];

/** Repository rows of an owner, each linking to the repository page. */
export function RepoRows({ query }: { query: UseQueryResult<Repo[]> }) {
  const t = useT("repos");
  return (
    <Async query={query} empty={t("list.empty")}>
      {(rows) =>
        rows.map((r) => (
          <div key={r.id} className="ui-row acc-wrap">
            <div className="acc-grow">
              <div className="acc-title">
                <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }}>
                  <b>{r.fullName}</b>
                </Link>
                <Badge tone={r.visibility === "public" ? "ok" : "neutral"}>{t(`visibility.${r.visibility}`)}</Badge>
              </div>
              {r.description && <div className="ui-note">{r.description}</div>}
            </div>
            <PinButton owner={r.owner} name={r.name} compact />
          </div>
        ))
      }
    </Async>
  );
}

/** Dialog with the create form; with `org` the repository is created under that organization. */
/** New-repository dialog. Pass `open`/`onOpenChange` to control it from outside (the shell's New menu): then no trigger is rendered. */
export function CreateRepoDialog({ org, open: openProp, onOpenChange }: { org?: string; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRepo(org);
  const [openState, setOpenState] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : openState;
  const setOpen = (o: boolean) => (controlled ? onOpenChange?.(o) : setOpenState(o));
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("private");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), description: description.trim(), visibility },
      {
        onSuccess: (r) => {
          setOpen(false);
          setName("");
          setDescription("");
          notify.ok(t("create.done"));
          void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen} title={t("create.title")} description={t("create.description")} trigger={controlled ? undefined : <Button variant="primary" icon="plus">{t("create.button")}</Button>}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("create.name")} required maxLength={100} autoComplete="off" mono value={name} onChange={(e) => setName(e.target.value)} />
        <Input label={t("create.descriptionLabel")} maxLength={350} value={description} onChange={(e) => setDescription(e.target.value)} />
        <Select label={t("create.visibility")} value={visibility} onValueChange={(v) => setVisibility(v as Visibility)} options={visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))} />
        <p className="ui-note">{t(`visibility.${visibility}.hint`)}</p>
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={create.isPending}>{t("create.submit")}</Button>
        </div>
      </form>
    </Dialog>
  );
}

/** The repositories of the signed-in user. */
export function MyRepos({ repos }: { repos: UseQueryResult<Repo[]> }) {
  const t = useT("repos");
  return (
    <Card title={t("list.title")} actions={<CreateRepoDialog />}>
      <RepoRows query={repos} />
    </Card>
  );
}

/** Fork dialog: the copy goes to the signed-in user or to an organization they manage. */
export function ForkDialog({ repo }: { repo: Repo }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const fork = useForkRepo(repo.owner, repo.name);
  const user = useSession().data;
  const orgs = useMyOrgs().data ?? [];
  const [open, setOpen] = useState(false);
  const [owner, setOwner] = useState(user?.username ?? "");
  const [name, setName] = useState(repo.name);
  // The server checks the permission; this only narrows the choice.
  const owners = [user?.username ?? "", ...orgs.filter((o) => o.role === "owner" || o.role === "admin").map((o) => o.name)].filter(Boolean);

  function submit(e: FormEvent) {
    e.preventDefault();
    fork.mutate(
      { owner, name: name.trim() },
      {
        onSuccess: (r) => {
          setOpen(false);
          notify.ok(t("fork.done"));
          void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen} title={t("fork.title", { name: repo.fullName })} description={t("fork.description")} trigger={<Button icon="branch">{t("fork.button")}</Button>}>
      <form className="acc-form" onSubmit={submit}>
        <Select label={t("fork.owner")} value={owner} onValueChange={setOwner} options={owners.map((o) => ({ value: o, label: o }))} />
        <Input label={t("create.name")} required maxLength={100} autoComplete="off" mono value={name} onChange={(e) => setName(e.target.value)} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={fork.isPending || owner === "" || name.trim() === ""}>{t("fork.submit")}</Button>
        </div>
      </form>
    </Dialog>
  );
}
