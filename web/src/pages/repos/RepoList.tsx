import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent, type ReactNode } from "react";
import { useMyOrgs, useSession } from "../../api/identity.ts";
import { useCreateRepo, useForkRepo, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, Input, Select } from "../../ui/index.ts";
import { formatBytes, When } from "../code/common.tsx";
import { PinButton } from "./PinButton.tsx";
import { Async, useNotify } from "../account/common.tsx";
import type { UseQueryResult } from "@tanstack/react-query";

export const visibilities: Visibility[] = ["private", "internal", "public"];

type TypeFilter = "all" | Visibility;
type SortKey = "updated" | "name";

/** One repository in GitHub style: name, visibility, description, last update and size, an optional pin. */
export function RepoRow({ repo: r, showOwner, pin = true }: { repo: Repo; showOwner?: boolean; pin?: boolean }) {
  const t = useT("repos");
  return (
    <li className="rl-row">
      <div className="rl-main">
        <h3 className="rl-h">
          <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }} className="rl-name">
            {showOwner ? `${r.owner} / ${r.name}` : r.name}
          </Link>
          <span className="rl-vis">{t(`visibility.${r.visibility}`)}</span>
        </h3>
        {r.description && <p className="rl-desc">{r.description}</p>}
        <p className="rl-meta">
          <span>{t("list.updated")} <When iso={r.updatedAt} /></span>
          {r.sizeBytes > 0 && <span>{formatBytes(r.sizeBytes)}</span>}
        </p>
      </div>
      {pin && <PinButton owner={r.owner} name={r.name} compact />}
    </li>
  );
}

/** Repository list with GitHub's filter bar: find by name, type and sort, all on the loaded page. */
export function RepoRows({ query, showOwner, action }: { query: UseQueryResult<Repo[]>; showOwner?: boolean; action?: ReactNode }) {
  const t = useT("repos");
  const [q, setQ] = useState("");
  const [type, setType] = useState<TypeFilter>("all");
  const [sort, setSort] = useState<SortKey>("updated");
  const needle = q.trim().toLowerCase();
  return (
    <div className="rl">
      <div className="rl-bar" role="search">
        <Input className="rl-find" label={t("list.find")} hideLabel type="search" placeholder={t("list.find")} value={q} onChange={(e) => setQ(e.target.value)} />
        <Select
          label={t("list.type")}
          hideLabel
          value={type}
          onValueChange={(v) => setType(v as TypeFilter)}
          options={[{ value: "all", label: t("list.type.all") }, ...visibilities.map((v) => ({ value: v, label: t(`visibility.${v}`) }))]}
        />
        <Select
          label={t("explore.sort")}
          hideLabel
          value={sort}
          onValueChange={(v) => setSort(v === "name" ? "name" : "updated")}
          options={[{ value: "updated", label: t("explore.sort.updated") }, { value: "name", label: t("explore.sort.name") }]}
        />
        {action}
      </div>
      <Async query={query} empty={t("list.empty")}>
        {(rows) => {
          const shown = rows
            .filter((r) => (type === "all" || r.visibility === type) && (!needle || `${r.name} ${r.description}`.toLowerCase().includes(needle)))
            .sort((x, y) => (sort === "name" ? x.name.localeCompare(y.name) : y.updatedAt.localeCompare(x.updatedAt)));
          return shown.length === 0 ? (
            <p className="ui-note rl-none">{t("explore.noMatch")}</p>
          ) : (
            <ul className="rl-list">{shown.map((r) => <RepoRow key={r.id} repo={r} showOwner={showOwner} />)}</ul>
          );
        }}
      </Async>
    </div>
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

/** The repositories of the signed-in user, with their owner since organizations are included. */
export function MyRepos({ repos }: { repos: UseQueryResult<Repo[]> }) {
  return <RepoRows query={repos} showOwner action={<CreateRepoDialog />} />;
}

/** Fork dialog: the copy goes to the signed-in user or to an organization they manage. */
export function ForkDialog({ repo, open: openProp, onOpenChange }: { repo: Repo; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const fork = useForkRepo(repo.owner, repo.name);
  const user = useSession().data;
  const orgs = useMyOrgs().data ?? [];
  const [openState, setOpenState] = useState(false);
  // Controlled when the caller opens it from a menu; it then renders no trigger.
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : openState;
  const setOpen = (v: boolean) => (controlled ? onOpenChange?.(v) : setOpenState(v));
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
    <Dialog open={open} onOpenChange={setOpen} title={t("fork.title", { name: repo.fullName })} description={t("fork.description")} trigger={controlled ? undefined : <Button icon="branch">{t("fork.button")}</Button>}>
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
