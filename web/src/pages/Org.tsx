import { useState, type FormEvent } from "react";
import {
  useCancelInvite,
  useCreateTeam,
  useDeleteTeam,
  useInviteMember,
  useOrg,
  useOrgInvites,
  useOrgMembers,
  useOrgTeams,
  useRemoveMember,
  useSession,
  useSetMemberRole,
  useUpdateOrg,
  type Org,
  type OrgRole,
  type Permission,
} from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Input, Page, Select, Table, TableBody, TableHead, Tabs, TabsContent, TabsList, TabsTrigger, Td, Th, Tr } from "../ui/index.ts";
import { useOwnerRepos } from "../api/repos.ts";
import { orgHooks } from "../api/settings.ts";
import { CreateRepoDialog, RepoRows } from "./repos/RepoList.tsx";
import { Entries, Runners } from "./actions/Settings.tsx";
import { Webhooks } from "./settings/Webhooks.tsx";
import { Async, formatDate, Secret, useNotify } from "./account/common.tsx";

const roles: OrgRole[] = ["owner", "admin", "member"];
const permissions: Permission[] = ["none", "read", "triage", "write", "maintain", "admin"];

function Members({ org, canManage }: { org: string; canManage: boolean }) {
  const t = useT("org");
  const notify = useNotify();
  const members = useOrgMembers(org);
  const setRole = useSetMemberRole(org);
  const remove = useRemoveMember(org);
  const roleOptions = roles.map((r) => ({ value: r, label: t(`role.${r}`) }));

  return (
    <Card title={t("members.title")}>
      <Async query={members} empty={t("members.empty")}>
        {(rows) => (
          <Table>
            <TableHead>
              <Tr>
                <Th>{t("members.user")}</Th>
                <Th>{t("members.role")}</Th>
                <Th>{t("members.since")}</Th>
                {canManage && <Th><span className="sr-only">{t("members.actions")}</span></Th>}
              </Tr>
            </TableHead>
            <TableBody>
              {rows.map((m) => (
                <Tr key={m.username}>
                  <Td>
                    <b>{m.displayName || m.username}</b> <span className="ui-mono">@{m.username}</span>
                  </Td>
                  <Td>
                    {canManage ? (
                      <Select
                        label={t("members.roleOf", { user: m.username })}
                        value={m.role}
                        options={roleOptions}
                        onValueChange={(role) =>
                          setRole.mutate({ username: m.username, role: role as OrgRole }, { onSuccess: () => notify.ok(t("members.roleSaved")), onError: notify.fail })
                        }
                      />
                    ) : (
                      <Badge>{t(`role.${m.role}`)}</Badge>
                    )}
                  </Td>
                  <Td>{formatDate(m.since)}</Td>
                  {canManage && (
                    <Td>
                      <Button size="sm" variant="ghost" aria-label={t("members.remove", { user: m.username })} onClick={() => remove.mutate(m.username, { onSuccess: () => notify.ok(t("members.removed")), onError: notify.fail })}>
                        {t("members.removeButton")}
                      </Button>
                    </Td>
                  )}
                </Tr>
              ))}
            </TableBody>
          </Table>
        )}
      </Async>
    </Card>
  );
}

function Teams({ org, canManage }: { org: string; canManage: boolean }) {
  const t = useT("org");
  const notify = useNotify();
  const teams = useOrgTeams(org);
  const create = useCreateTeam(org);
  const del = useDeleteTeam(org);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { name: name.trim(), description: description.trim() || undefined },
      {
        onSuccess: () => {
          setName("");
          setDescription("");
          notify.ok(t("teams.created"));
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <>
      <Card title={t("teams.title")}>
        <Async query={teams} empty={t("teams.empty")}>
          {(rows) =>
            rows.map((tm) => (
              <div key={tm.name} className="ui-row acc-wrap">
                <div className="acc-grow">
                  <div className="acc-title">{tm.name}</div>
                  {tm.description && <div className="ui-note">{tm.description}</div>}
                </div>
                <span className="ui-note">{t("teams.members", { count: tm.members })}</span>
                {canManage && (
                  <Button size="sm" variant="ghost" aria-label={t("teams.delete", { name: tm.name })} onClick={() => del.mutate(tm.name, { onSuccess: () => notify.ok(t("teams.deleted")), onError: notify.fail })}>
                    {t("teams.deleteButton")}
                  </Button>
                )}
              </div>
            ))
          }
        </Async>
      </Card>
      {canManage && (
        <Card title={t("teams.new")}>
          <form className="acc-form" onSubmit={submit}>
            <Input label={t("teams.name")} required maxLength={39} value={name} onChange={(e) => setName(e.target.value)} />
            <Input label={t("teams.description")} maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />
            <div className="acc-actions">
              <Button variant="primary" type="submit" disabled={create.isPending}>{t("teams.create")}</Button>
            </div>
          </form>
        </Card>
      )}
    </>
  );
}

function Invites({ org, canManage }: { org: string; canManage: boolean }) {
  const t = useT("org");
  const notify = useNotify();
  const invites = useOrgInvites(org, canManage);
  const [link, setLink] = useState<string>();
  const invite = useInviteMember(org);
  const cancel = useCancelInvite(org);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<OrgRole>("member");

  function submit(e: FormEvent) {
    e.preventDefault();
    invite.mutate(
      { email: email.trim(), role },
      {
        onSuccess: (inv) => {
          setEmail("");
          // Without mail delivery the server returns the token so the link can be shared by hand.
          setLink(inv.token ? `${window.location.origin}/org-invite?token=${encodeURIComponent(inv.token)}` : undefined);
          notify.ok(t("invites.sent"));
        },
        onError: notify.fail,
      },
    );
  }

  return (
    <>
      <Card title={t("invites.title")}>
        {link && <Secret label={t("invites.link")} value={link} />}
        <Async query={invites} empty={t("invites.empty")}>
          {(rows) =>
            rows.map((i) => (
              <div key={i.id} className="ui-row acc-wrap">
                <div className="acc-grow">
                  <div className="acc-title">{i.email}</div>
                  <div className="ui-note">
                    {t("invites.expires", { date: formatDate(i.expiresAt) })}
                    {i.invitedBy ? ` · ${t("invites.by", { user: i.invitedBy })}` : ""}
                    {i.teams.length > 0 ? ` · ${i.teams.join(", ")}` : ""}
                  </div>
                </div>
                <Badge>{t(`role.${i.role}`)}</Badge>
                {canManage && (
                  <Button size="sm" variant="ghost" aria-label={t("invites.cancel", { email: i.email })} onClick={() => cancel.mutate(i.id, { onSuccess: () => notify.ok(t("invites.cancelled")), onError: notify.fail })}>
                    {t("invites.cancelButton")}
                  </Button>
                )}
              </div>
            ))
          }
        </Async>
      </Card>
      {canManage && (
        <Card title={t("invites.new")}>
          <form className="acc-form" onSubmit={submit}>
            <Input label={t("invites.email")} type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
            <Select label={t("members.role")} value={role} onValueChange={(v) => setRole(v as OrgRole)} options={roles.map((r) => ({ value: r, label: t(`role.${r}`) }))} />
            <div className="acc-actions">
              <Button variant="primary" type="submit" disabled={invite.isPending}>{t("invites.send")}</Button>
            </div>
          </form>
        </Card>
      )}
    </>
  );
}

function OrgRepos({ org }: { org: string }) {
  const t = useT("org");
  const repos = useOwnerRepos(org, "org");
  return (
    <Card title={t("list.title")} actions={<CreateRepoDialog org={org} />}>
      <RepoRows query={repos} />
    </Card>
  );
}

function Settings({ org }: { org: Org }) {
  const t = useT("org");
  const notify = useNotify();
  const update = useUpdateOrg(org.name);
  const [displayName, setDisplayName] = useState(org.displayName);
  const [description, setDescription] = useState(org.description);
  const [base, setBase] = useState<Permission>(org.basePermission);
  const [require2fa, setRequire2fa] = useState(org.require2fa);

  function submit(e: FormEvent) {
    e.preventDefault();
    update.mutate(
      { displayName: displayName.trim(), description: description.trim(), basePermission: base, require2fa },
      { onSuccess: () => notify.ok(t("settings.saved")), onError: notify.fail },
    );
  }

  return (
    <Card title={t("settings.title")}>
      <form className="acc-form" onSubmit={submit}>
        <Input label={t("settings.displayName")} maxLength={80} value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
        <Input label={t("settings.description")} maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />
        <Select
          label={t("settings.basePermission")}
          value={base}
          onValueChange={(v) => setBase(v as Permission)}
          options={permissions.map((p) => ({ value: p, label: t(`permission.${p}`) }))}
        />
        <label className="acc-toggle">
          <input type="checkbox" checked={require2fa} onChange={(e) => setRequire2fa(e.target.checked)} />
          <span>{t("settings.require2fa")}</span>
        </label>
        <p className="ui-note">{t("settings.require2faHint")}</p>
        <div className="acc-actions">
          <Button variant="primary" type="submit" disabled={update.isPending}>{t("settings.save")}</Button>
        </div>
      </form>
    </Card>
  );
}

/** Organization page: members, teams and invitations. Owners can manage; others read. */
export function OrgPage({ name }: { name: string }) {
  const t = useT("org");
  const tc = useT("common");
  const org = useOrg(name);
  const instanceAdmin = useSession().data?.role === "admin";

  if (org.isPending) {
    return (
      <Page section="pr" title={name}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (org.isError) {
    return (
      <Page section="pr" title={name}>
        <div role="alert">
          <EmptyState icon="alert" title={t("error")} description={org.error.message} action={<Button onClick={() => void org.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      </Page>
    );
  }
  const o = org.data;
  // The server enforces the rules; this only hides controls. Instance admins act as owners.
  const isOwner = o.role === "owner" || instanceAdmin;
  const canManage = isOwner || o.role === "admin";
  return (
    <Page
      section="pr"
      title={o.displayName || o.name}
      subtitle={`@${o.name}`}
      actions={
        <>
          {o.verified && <Badge tone="ok">{t("verified")}</Badge>}
          {o.role && <Badge>{t(`role.${o.role}`)}</Badge>}
        </>
      }
    >
      {o.description && <p className="acc-desc">{o.description}</p>}
      <Tabs defaultValue="repos">
        <TabsList aria-label={t("tabs")}>
          <TabsTrigger value="repos">{t("tab.repos")}</TabsTrigger>
          <TabsTrigger value="members">{t("tab.members")}</TabsTrigger>
          <TabsTrigger value="teams">{t("tab.teams")}</TabsTrigger>
          {canManage && <TabsTrigger value="invites">{t("tab.invites")}</TabsTrigger>}
          {isOwner && <TabsTrigger value="hooks">{t("tab.hooks")}</TabsTrigger>}
          {isOwner && <TabsTrigger value="secrets">{t("tab.secrets")}</TabsTrigger>}
          {isOwner && <TabsTrigger value="variables">{t("tab.variables")}</TabsTrigger>}
          {isOwner && <TabsTrigger value="runners">{t("tab.runners")}</TabsTrigger>}
          {isOwner && <TabsTrigger value="settings">{t("tab.settings")}</TabsTrigger>}
        </TabsList>
        <TabsContent value="repos"><OrgRepos org={o.name} /></TabsContent>
        <TabsContent value="members"><Members org={o.name} canManage={canManage} /></TabsContent>
        <TabsContent value="teams"><Teams org={o.name} canManage={canManage} /></TabsContent>
        {canManage && <TabsContent value="invites"><Invites org={o.name} canManage={canManage} /></TabsContent>}
        {isOwner && <TabsContent value="hooks"><Webhooks base={orgHooks(o.name)} scope="org" /></TabsContent>}
        {isOwner && <TabsContent value="secrets"><Entries scope={{ kind: "org", org: o.name }} kind="secrets" /></TabsContent>}
        {isOwner && <TabsContent value="variables"><Entries scope={{ kind: "org", org: o.name }} kind="variables" /></TabsContent>}
        {isOwner && <TabsContent value="runners"><Runners scope={{ kind: "org", org: o.name }} /></TabsContent>}
        {isOwner && <TabsContent value="settings"><Settings org={o} /></TabsContent>}
      </Tabs>
    </Page>
  );
}
