import { Link, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import {
  useCancelInvite,
  useCreateTeam,
  useDeleteOrg,
  useDeleteTeam,
  useInviteMember,
  useOrg,
  useOrgInvites,
  useOrgMembers,
  useOrgTeams,
  useRemoveMember,
  useRemoveTeamMember,
  useSession,
  useSetMemberRole,
  useSetTeamMember,
  useTeamMembers,
  useOrgAvatar,
  useUpdateOrg,
  useUpdateTeam,
  type Org,
  type OrgRole,
  type Permission,
  type Team,
  type TeamRole,
} from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  DangerRow,
  DangerZone,
  Dialog,
  DialogClose,
  EmptyState,
  Icon,
  Input,
  Page,
  SaveBar,
  Select,
  SettingRow,
  SettingsCard,
  SettingsLayout,
  SettingsSection,
  Table,
  TableBody,
  TableHead,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Td,
  Th,
  Toggle,
  Tr,
  useDraft,
  type SettingsNavGroup,
  type SettingsNavItem,
} from "../ui/index.ts";
import { useLayout } from "../ui/shell/layout.ts";
import { Avatar } from "../ui/shell/ConsoleShell.tsx";
import { AvatarImg } from "../ui/AvatarImg.tsx";
import { ImagePicker } from "./settings/ImagePicker.tsx";
import { useOwnerRepos } from "../api/repos.ts";
import { orgHooks } from "../api/settings.ts";
import { CreateRepoDialog, popular, RepoCards, RepoRows, type RepoLanguages } from "./repos/RepoList.tsx";
import { useOrgLanguages } from "../api/profile.ts";
import { Entries } from "./actions/Settings.tsx";
import { Runners } from "./actions/Runners.tsx";
import { Webhooks } from "./settings/Webhooks.tsx";
import { Async, formatDate, Secret, useNotify } from "./account/common.tsx";
import { canRegisterApps } from "../api/oauth.ts";
import { useMeta } from "../api/useMeta.ts";
import { OAuthAppsSection, type AppsNav, type AppTarget } from "./oauth/OAuthApps.tsx";
import { AppsSection, type AppsNav as GhAppsNav, type AppTarget as GhAppTarget } from "./apps/Apps.tsx";
import { InstalledApps, type InstalledNav } from "./apps/Installed.tsx";
import { isOrgSettingsTab, orgTabs, type OrgSettingsTab, type OrgTab } from "../ui/shell/nav.tsx";

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
                      <ConfirmDialog
                        trigger={<Button variant="ghost" aria-label={t("members.remove", { user: m.username })}>{t("members.removeButton")}</Button>}
                        title={t("members.removeTitle", { user: m.username })}
                        description={t("members.removeHint")}
                        confirmLabel={t("members.removeButton")}
                        pending={remove.isPending}
                        onConfirm={() => remove.mutate(m.username, { onSuccess: () => notify.ok(t("members.removed")), onError: notify.fail })}
                      />
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

const teamRoles: TeamRole[] = ["maintainer", "member"];

function TeamMembers({ org, team, canManage }: { org: string; team: string; canManage: boolean }) {
  const t = useT("org");
  const notify = useNotify();
  const members = useTeamMembers(org, team);
  const set = useSetTeamMember(org, team);
  const remove = useRemoveTeamMember(org, team);
  const [user, setUser] = useState("");
  const [role, setRole] = useState<TeamRole>("member");
  const options = teamRoles.map((r) => ({ value: r, label: t(`teams.role.${r}`) }));

  function add(e: FormEvent) {
    e.preventDefault();
    const username = user.trim();
    set.mutate({ username, role }, { onSuccess: () => { setUser(""); notify.ok(t("teams.memberAdded", { user: username })); }, onError: notify.fail });
  }

  return (
    <div className="acc-form">
      <Async query={members} empty={t("teams.noMembers")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((m) => (
              <div key={m.username} className="st-item">
                <div className="st-item-m">
                  <b>{m.displayName || m.username}</b> <span className="ui-mono">@{m.username}</span>
                </div>
                {canManage ? (
                  <div className="st-item-a">
                    <Select
                      hideLabel
                      label={t("teams.roleOf", { user: m.username })}
                      value={m.role}
                      options={options}
                      onValueChange={(r) => set.mutate({ username: m.username, role: r as TeamRole }, { onError: notify.fail })}
                    />
                    <Button variant="ghost" icon="trash" aria-label={t("teams.removeMember", { user: m.username })} onClick={() => remove.mutate(m.username, { onSuccess: () => notify.ok(t("teams.memberRemoved")), onError: notify.fail })} />
                  </div>
                ) : (
                  <Badge>{t(`teams.role.${m.role}`)}</Badge>
                )}
              </div>
            ))}
          </div>
        )}
      </Async>
      {canManage && (
        <form className="st-add" onSubmit={add}>
          <Input label={t("teams.addMember")} mono required autoComplete="off" value={user} onChange={(e) => setUser(e.target.value)} />
          <Select className="st-narrow" label={t("members.role")} value={role} onValueChange={(r) => setRole(r as TeamRole)} options={options} />
          <Button type="submit" disabled={set.isPending || user.trim() === ""}>{t("teams.add")}</Button>
        </form>
      )}
    </div>
  );
}

function EditTeam({ org, team }: { org: string; team: Team }) {
  const t = useT("org");
  const notify = useNotify();
  const update = useUpdateTeam(org);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(team.name);
  const [description, setDescription] = useState(team.description);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => { setOpen(o); if (o) { setName(team.name); setDescription(team.description); } }}
      title={t("teams.editTitle", { name: team.name })}
      trigger={<Button variant="ghost" icon="edit" aria-label={t("teams.edit", { name: team.name })} />}
      footer={
        <>
          <DialogClose asChild><Button variant="ghost">{t("cancel")}</Button></DialogClose>
          <Button
            variant="primary"
            disabled={update.isPending || name.trim() === ""}
            onClick={() => update.mutate({ team: team.name, body: { name: name.trim(), description: description.trim() } }, { onSuccess: () => { setOpen(false); notify.ok(t("teams.saved")); }, onError: notify.fail })}
          >
            {t("settings.save")}
          </Button>
        </>
      }
    >
      <div className="acc-form">
        <Input label={t("teams.name")} required maxLength={39} value={name} onChange={(e) => setName(e.target.value)} />
        <Input label={t("teams.description")} maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />
      </div>
    </Dialog>
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
          {(rows) => (
            <div className="st-list">
              {rows.map((tm) => (
                <div key={tm.name} className="st-item">
                  <div className="st-item-m">
                    <div className="acc-title">{tm.name}</div>
                    {tm.description && <div className="ui-note">{tm.description}</div>}
                  </div>
                  <div className="st-item-a">
                    <Dialog
                      title={t("teams.membersTitle", { name: tm.name })}
                      trigger={<Button variant="ghost" icon="users">{t("teams.members", { count: tm.members })}</Button>}
                    >
                      <TeamMembers org={org} team={tm.name} canManage={canManage} />
                    </Dialog>
                    {canManage && <EditTeam org={org} team={tm} />}
                    {canManage && (
                      <ConfirmDialog
                        trigger={<Button variant="ghost" icon="trash" aria-label={t("teams.delete", { name: tm.name })} />}
                        title={t("teams.deleteTitle", { name: tm.name })}
                        description={t("teams.deleteHint")}
                        confirmLabel={t("teams.deleteButton")}
                        pending={del.isPending}
                        onConfirm={() => del.mutate(tm.name, { onSuccess: () => notify.ok(t("teams.deleted")), onError: notify.fail })}
                      />
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
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
                  <ConfirmDialog
                    trigger={<Button variant="ghost" aria-label={t("invites.cancel", { email: i.email })}>{t("invites.cancelButton")}</Button>}
                    title={t("invites.cancelTitle", { email: i.email })}
                    confirmLabel={t("invites.cancelConfirm")}
                    pending={cancel.isPending}
                    onConfirm={() => cancel.mutate(i.id, { onSuccess: () => notify.ok(t("invites.cancelled")), onError: notify.fail })}
                  />
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

/** Repositories tab: popular repositories as cards, then the full list with language filter and activity. */
function OrgRepos({ org }: { org: string }) {
  const t = useT("org");
  const repos = useOwnerRepos(org, "org");
  const langs = useOrgLanguages(org);
  const languages: RepoLanguages = new Map((langs.data?.repos ?? []).map((r) => [`${org}/${r.name}`, { language: r.language, color: r.color }]));
  const top = popular(repos.data ?? [], 4);
  return (
    <>
      {top.length > 0 && (
        <section className="oh-sec" aria-labelledby="oh-pop-h">
          <div className="ui-sech"><h2 id="oh-pop-h">{t("repos.popular")}</h2></div>
          <RepoCards repos={top} languages={languages} />
        </section>
      )}
      <section aria-labelledby="oh-repos-h">
        <div className="ui-sech"><h2 id="oh-repos-h">{t("repos.title")}</h2></div>
        <RepoRows query={repos} languages={languages} />
      </section>
    </>
  );
}

/** GitHub-style organization header: square avatar, name, description, members and verified mark; Settings and New repository on the right. */
function OrgHead({ org, isOwner, canCreate, onTab }: { org: Org; isOwner: boolean; canCreate: boolean; onTab: (tab: OrgTab) => void }) {
  const t = useT("org");
  const members = useOrgMembers(org.name);
  return (
    <header className="oh">
      <AvatarImg src={org.avatarUrl} className="oh-av">{(org.displayName || org.name).slice(0, 1).toUpperCase()}</AvatarImg>
      <div className="oh-m">
        <p className="oh-name">{org.displayName || org.name}</p>
        {org.description && <p className="oh-desc">{org.description}</p>}
        <p className="oh-meta">
          <span className="ui-mono">@{org.name}</span>
          {members.data && <span><Icon name="users" />{t("members.count", { count: members.data.length })}</span>}
          {org.verified && <span className="ui-chip ui-chip-ok"><Icon name="check" />{t("verified")}</span>}
          {org.role && <span className="ui-chip">{t(`role.${org.role}`)}</span>}
        </p>
      </div>
      <div className="oh-act">
        {isOwner && <Button icon="gear" onClick={() => onTab("settings")}>{t("tab.settings")}</Button>}
        {canCreate && <CreateRepoDialog org={org.name} />}
      </div>
    </header>
  );
}

/** Side column of the repositories tab: people in a row, top languages with a split bar, teams with member counts. */
function OrgAside({ org, canManage, onTab }: { org: string; canManage: boolean; onTab: (tab: OrgTab) => void }) {
  const t = useT("org");
  const members = useOrgMembers(org);
  const teams = useOrgTeams(org);
  const langs = useOrgLanguages(org);
  const items = langs.data?.items.filter((l) => l.percent > 0) ?? [];
  const shown = items.slice(0, 6);
  const rest = items.slice(6).reduce((a, l) => a + l.percent, 0);
  return (
    <>
      {members.data && members.data.length > 0 && (
        <section className="oh-side" aria-labelledby="oh-ppl-h">
          <h2 id="oh-ppl-h">
            {t("members.title")} <span className="ui-n">{members.data.length}</span>
            {canManage && <button type="button" className="oh-link" onClick={() => onTab("invites")}>{t("invites.short")}</button>}
          </h2>
          <ul className="oh-people">
            {members.data.slice(0, 24).map((m) => (
              <li key={m.username}>
                <Link to="/$owner" params={{ owner: m.username }} title={m.displayName || m.username} aria-label={m.displayName || m.username}>
                  <Avatar user={{ username: m.username, avatarUrl: "" }} size="lg" />
                </Link>
              </li>
            ))}
          </ul>
          {members.data.length > 24 && <button type="button" className="oh-link" onClick={() => onTab("members")}>{t("members.all", { count: members.data.length })}</button>}
        </section>
      )}
      {shown.length > 0 && (
        <section className="oh-side" aria-labelledby="oh-lang-h">
          <h2 id="oh-lang-h">{t("languages.title")}</h2>
          <div className="oh-bar" aria-hidden="true">
            {shown.map((l) => <i key={l.name} style={{ width: `${l.percent}%`, background: l.color }} />)}
          </div>
          <ul className="oh-langs">
            {shown.map((l) => (
              <li key={l.name}>
                <i style={{ background: l.color }} aria-hidden="true" />
                <span>{l.name === "Other" ? t("languages.other") : l.name}</span>
                <span className="oh-pct">{l.percent.toFixed(1)}%</span>
              </li>
            ))}
            {rest > 0 && (
              <li>
                <i className="oh-rest" aria-hidden="true" />
                <span>{t("languages.other")}</span>
                <span className="oh-pct">{rest.toFixed(1)}%</span>
              </li>
            )}
          </ul>
        </section>
      )}
      {teams.data && teams.data.length > 0 && (
        <section className="oh-side" aria-labelledby="oh-teams-h">
          <h2 id="oh-teams-h">{t("teams.title")} <span className="ui-n">{teams.data.length}</span></h2>
          <ul className="oh-teams">
            {teams.data.slice(0, 8).map((x) => (
              <li key={x.id}>
                <button type="button" className="ui-li" onClick={() => onTab("teams")}>
                  <Icon name="users" />
                  <span className="oh-tn">{x.name}</span>
                  <span className="ui-tm">{x.members}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function Settings({ org }: { org: Org }) {
  const t = useT("org");
  const notify = useNotify();
  const navigate = useNavigate();
  const update = useUpdateOrg(org.name);
  const del = useDeleteOrg(org.name);
  const avatar = useOrgAvatar(org.name);
  const draft = useDraft({ displayName: org.displayName, description: org.description, basePermission: org.basePermission as Permission, require2fa: org.require2fa });
  const v = draft.value;
  const permLabel = (p: Permission) => t(`permission.${p}`);

  function save() {
    const next = { displayName: v.displayName.trim(), description: v.description.trim(), basePermission: v.basePermission, require2fa: v.require2fa };
    update.mutate(next, { onSuccess: () => { draft.rebase(next); notify.ok(t("settings.saved")); }, onError: notify.fail });
  }

  return (
    <>
      <SettingsSection title={t("settings.title")}>
        <SettingsCard>
          <SettingRow label={t("settings.image")} description={t("settings.image.hint")}>
            <ImagePicker
              preview={<AvatarImg src={org.avatarUrl} className="st-ph-org">{(org.displayName || org.name).slice(0, 1).toUpperCase()}</AvatarImg>}
              hasImage={!!org.avatarUrl}
              uploading={avatar.upload.isPending}
              busy={avatar.upload.isPending || avatar.remove.isPending}
              onPick={(f) => avatar.upload.mutate(f, { onSuccess: () => notify.ok(t("settings.image.uploaded")), onError: notify.fail })}
              onRemove={() => avatar.remove.mutate(undefined, { onSuccess: () => notify.ok(t("settings.image.removed")), onError: notify.fail })}
              labels={{ upload: t("settings.image.upload"), uploading: t("settings.image.uploading"), remove: t("settings.image.remove") }}
            />
          </SettingRow>
          <SettingRow label={t("settings.displayName")} dirty={draft.isDirty("displayName")} was={draft.initial.displayName}>
            <Input label={t("settings.displayName")} hideLabel maxLength={80} value={v.displayName} onChange={(e) => draft.set("displayName", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("settings.description")} dirty={draft.isDirty("description")} was={draft.initial.description}>
            <Input label={t("settings.description")} hideLabel maxLength={200} value={v.description} onChange={(e) => draft.set("description", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("settings.basePermission")} description={t("settings.basePermissionHint")} dirty={draft.isDirty("basePermission")} was={permLabel(draft.initial.basePermission)}>
            <Select
              label={t("settings.basePermission")}
              hideLabel
              value={v.basePermission}
              onValueChange={(x) => draft.set("basePermission", x as Permission)}
              options={permissions.map((p) => ({ value: p, label: permLabel(p) }))}
            />
          </SettingRow>
          <SettingRow
            label={t("settings.require2fa")}
            description={t("settings.require2faHint")}
            dirty={draft.isDirty("require2fa")}
            was={draft.initial.require2fa ? t("settings.on") : t("settings.off")}
          >
            <Toggle label={t("settings.require2fa")} checked={v.require2fa} onChange={(x) => draft.set("require2fa", x)} />
          </SettingRow>
        </SettingsCard>
        <SaveBar
          draft={draft}
          labels={{ displayName: t("settings.displayName"), description: t("settings.description"), basePermission: t("settings.basePermission"), require2fa: t("settings.require2fa") }}
          onSave={save}
          saving={update.isPending}
        />
      </SettingsSection>
      <DangerZone title={t("danger.title")}>
        <DangerRow
          title={t("danger.delete")}
          description={t("danger.deleteHint")}
          action={
            <ConfirmDialog
              trigger={<Button variant="danger">{t("danger.deleteButton")}</Button>}
              title={t("danger.deleteTitle", { name: org.name })}
              description={t("danger.deleteDialogHint")}
              confirmLabel={t("danger.deleteConfirm")}
              typeToConfirm={org.name}
              pending={del.isPending}
              onConfirm={() => del.mutate(undefined, { onSuccess: () => { notify.ok(t("danger.deleted")); void navigate({ to: "/orgs" }); }, onError: notify.fail })}
            />
          }
        />
      </DangerZone>
    </>
  );
}

/** Settings-type tabs share one SettingsLayout; the sidebar shows them as a single entry. */
function OrgSettings({ org, tab, app, installation }: { org: Org; tab: OrgSettingsTab; app?: string; installation?: number }) {
  const t = useT("org");
  const navigate = useNavigate();
  const meta = useMeta().data;
  const instanceAdmin = useSession().data?.role === "admin";
  const appSearch = (to: AppTarget) => ({ tab: "oauth-apps" as const, app: to.kind === "list" ? undefined : to.kind === "new" ? "new" : to.clientId });
  const appsNav: AppsNav = {
    link: (to, props) => <Link to="/orgs/$org" params={{ org: org.name }} search={appSearch(to)} className={props.className}>{props.children}</Link>,
    go: (to) => void navigate({ to: "/orgs/$org", params: { org: org.name }, search: appSearch(to) }),
  };
  const ghSearch = (to: GhAppTarget) => ({ tab: "apps" as const, app: to.kind === "list" ? undefined : to.kind === "new" ? "new" : to.slug });
  const ghAppsNav: GhAppsNav = {
    link: (to, props) => <Link to="/orgs/$org" params={{ org: org.name }} search={ghSearch(to)} className={props.className}>{props.children}</Link>,
    go: (to) => void navigate({ to: "/orgs/$org", params: { org: org.name }, search: ghSearch(to) }),
  };
  const instSearch = (id: number | undefined) => ({ tab: "installations" as const, installation: id });
  const installedNav: InstalledNav = {
    link: (id, props) => <Link to="/orgs/$org" params={{ org: org.name }} search={instSearch(id)} className={props.className}>{props.children}</Link>,
    go: (id) => void navigate({ to: "/orgs/$org", params: { org: org.name }, search: instSearch(id) }),
  };
  const scope = { kind: "org" as const, org: org.name };
  const item = (id: OrgSettingsTab, icon: SettingsNavItem["icon"]): SettingsNavItem => ({ id, icon, label: t(id === "settings" ? "settings.general" : `tab.${id}`) });
  const groups: SettingsNavGroup[] = [
    { title: t("settings.group.org"), items: [item("settings", "gear"), item("hooks", "bell"), item("installations", "pkg")] },
    { title: t("settings.group.developer"), items: [item("apps", "grid"), item("oauth-apps", "cpu")] },
    { title: t("settings.group.actions"), items: [item("secrets", "lock"), item("variables", "code"), item("runners", "cpu")] },
  ];
  return (
    <SettingsLayout
      label={t("settings.sections")}
      groups={groups}
      active={tab}
      header={{
        avatar: <AvatarImg src={org.avatarUrl} className="st-ph-org">{(org.displayName || org.name).slice(0, 1).toUpperCase()}</AvatarImg>,
        name: org.displayName || org.name,
        label: t("settings.header"),
        action: <Link to="/orgs/$org" params={{ org: org.name }} className="ui-btn ui-btn-secondary">{t("settings.toOrg")}</Link>,
      }}
      renderLink={(it, props) => <Link to="/orgs/$org" params={{ org: org.name }} search={{ tab: it.id as OrgTab }} {...props} />}
    >
      {tab === "settings" && <Settings key={org.updatedAt} org={org} />}
      {tab === "hooks" && <Webhooks base={orgHooks(org.name)} scope="org" />}
      {tab === "secrets" && <Entries scope={scope} kind="secrets" />}
      {tab === "variables" && <Entries scope={scope} kind="variables" />}
      {tab === "runners" && <Runners scope={scope} />}
      {tab === "installations" && <InstalledApps owner={scope} installation={installation} nav={installedNav} />}
      {tab === "apps" && (
        <AppsSection owner={scope} app={app} nav={ghAppsNav} canRegister={canRegisterApps(meta?.appRegistration, "org", instanceAdmin)} />
      )}
      {tab === "oauth-apps" && (
        <OAuthAppsSection owner={{ kind: "org", org: org.name }} app={app} nav={appsNav} canRegister={canRegisterApps(meta?.appRegistration, "org", instanceAdmin)} />
      )}
    </SettingsLayout>
  );
}

/** Organization page: members, teams and invitations. Owners can manage; others read. */
export function OrgPage({ name, tab, app, installation, onTab }: { name: string; tab: OrgTab; app?: string; installation?: number; onTab: (tab: OrgTab) => void }) {
  const t = useT("org");
  const tc = useT("common");
  const org = useOrg(name);
  const layout = useLayout();
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
  const settingsTab = isOrgSettingsTab(tab) && isOwner ? tab : undefined;
  const current = settingsTab ? "settings" : orgTabs(canManage, isOwner).some((it) => it.tab === tab) ? tab : "repos";
  if (settingsTab) {
    return (
      <Page section="pr" title={o.displayName || o.name} quiet>
        <OrgSettings org={o} tab={settingsTab} app={app} installation={installation} />
      </Page>
    );
  }
  return (
    <Page section="pr" title={o.displayName || o.name} quiet>
      <OrgHead org={o} isOwner={isOwner} canCreate={o.role !== undefined || instanceAdmin} onTab={onTab} />
      <div className={current === "repos" ? "oh-grid" : undefined}>
        <Tabs value={current} onValueChange={(v) => onTab(v as OrgTab)}>
          {/* The console sidebar already lists these sections. */}
          {layout !== "console" && (
            <TabsList aria-label={t("tabs")}>
              {orgTabs(canManage, isOwner).map((it) => <TabsTrigger key={it.tab} value={it.tab}>{t(`tab.${it.tab}`)}</TabsTrigger>)}
            </TabsList>
          )}
          <TabsContent value="repos"><OrgRepos org={o.name} /></TabsContent>
          <TabsContent value="members"><Members org={o.name} canManage={canManage} /></TabsContent>
          <TabsContent value="teams"><Teams org={o.name} canManage={canManage} /></TabsContent>
          {canManage && <TabsContent value="invites"><Invites org={o.name} canManage={canManage} /></TabsContent>}
        </Tabs>
        {current === "repos" && <aside className="oh-aside"><OrgAside org={o.name} canManage={canManage} onTab={onTab} /></aside>}
      </div>
    </Page>
  );
}
