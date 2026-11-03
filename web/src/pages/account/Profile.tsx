import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useCreateOrg, useMyOrgs, useUpdateProfile, type User } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, Input, Select } from "../../ui/index.ts";
import { Async, useNotify } from "./common.tsx";
import { Emails } from "./Emails.tsx";

function ProfileCard({ user }: { user: User }) {
  const t = useT("account");
  const notify = useNotify();
  const update = useUpdateProfile();
  const [displayName, setDisplayName] = useState(user.displayName);
  const [avatarUrl, setAvatarUrl] = useState(user.avatarUrl ?? "");
  const [locale, setLocale] = useState<User["locale"]>(user.locale);
  const options = [
    { value: "en", label: t("profile.locale.en") },
    { value: "it", label: t("profile.locale.it") },
  ];

  function submit(e: FormEvent) {
    e.preventDefault();
    update.mutate(
      { displayName: displayName.trim(), avatarUrl: avatarUrl.trim(), locale },
      { onSuccess: () => notify.ok(t("profile.saved")), onError: notify.fail },
    );
  }

  return (
    <Card title={t("profile.title")}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("profile.username")} value={user.username} readOnly mono hint={t("profile.username.hint")} />
        <Input label={t("profile.displayName")} maxLength={100} value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
        <Input
          label={t("profile.avatarUrl")}
          type="url"
          inputMode="url"
          maxLength={2048}
          placeholder="https://"
          hint={t("profile.avatarUrl.hint")}
          value={avatarUrl}
          onChange={(e) => setAvatarUrl(e.target.value)}
        />
        <Select label={t("profile.locale")} value={locale} onValueChange={(v) => setLocale(v as User["locale"])} options={options} />
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={update.isPending}>{t("profile.save")}</Button>
        </div>
      </form>
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
      <Emails />
      <Orgs />
    </>
  );
}
