import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useCreateOrg, useMyOrgs, type User } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input } from "../../ui/index.ts";
import { Async, useNotify } from "./common.tsx";

function ProfileCard({ user }: { user: User }) {
  const t = useT("account");
  return (
    <Card title={t("profile.title")}>
      <div className="acc-form">
        <Input label={t("profile.username")} value={user.username} readOnly mono hint={t("profile.username.hint")} />
        <Input label={t("profile.displayName")} value={user.displayName} readOnly />
        <Input
          label={t("profile.email")}
          value={user.email ?? ""}
          readOnly
          hint={user.emailVerified ? t("profile.emailVerified") : t("profile.emailUnverified")}
        />
      </div>
    </Card>
  );
}

function Orgs() {
  const t = useT("account");
  const notify = useNotify();
  const orgs = useMyOrgs();
  const create = useCreateOrg();
  const [name, setName] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim() },
      {
        onSuccess: () => {
          setName("");
          notify.ok(t("orgs.created"));
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <Card title={t("orgs.title")}>
      <Async query={orgs} empty={t("orgs.empty")}>
        {(rows) =>
          rows.map((o) => (
            <Link key={o.name} to="/orgs/$org" params={{ org: o.name }} className="ui-row">
              <span className="acc-grow">{o.displayName || o.name}</span>
              <span className="ui-mono">{o.name}</span>
              {o.role && <Badge>{t(`orgs.role.${o.role}`)}</Badge>}
            </Link>
          ))
        }
      </Async>
      <form className="acc-inline" onSubmit={submit}>
        <Input label={t("orgs.new")} mono required maxLength={39} value={name} onChange={(e) => setName(e.target.value)} />
        <Button type="submit" icon="plus" disabled={create.isPending}>{t("orgs.create")}</Button>
      </form>
    </Card>
  );
}

export function Profile({ user }: { user: User }) {
  return (
    <>
      <ProfileCard user={user} />
      <Orgs />
    </>
  );
}
