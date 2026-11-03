import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useCreateRepo, type Repo, type Visibility } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Dialog, Input, Select } from "../../ui/index.ts";
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
          </div>
        ))
      }
    </Async>
  );
}

/** Dialog with the create form; with `org` the repository is created under that organization. */
export function CreateRepoDialog({ org }: { org?: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRepo(org);
  const [open, setOpen] = useState(false);
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
    <Dialog open={open} onOpenChange={setOpen} title={t("create.title")} description={t("create.description")} trigger={<Button variant="primary" icon="plus">{t("create.button")}</Button>}>
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
