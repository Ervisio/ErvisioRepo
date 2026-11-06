import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { USER_ROLES, useAdminUser, useResetUser2fa, useUpdateAdminUser, type UserRole } from "../../api/admin.ts";
import { ApiError } from "../../api/http.ts";
import { useSession } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, ConfirmDialog, DangerRow, DangerZone, EmptyState, Icon, Select, SettingRow, SettingsCard, SettingsSection } from "../../ui/index.ts";
import { formatDate, useNotify } from "../account/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";
import { roleTone, UserStatus } from "./AdminUsers.tsx";

/** Admin: one user, with role, suspension and second-factor reset. */
export function AdminUserDetail({ login }: { login: string }) {
  const t = useT("admin");
  const tc = useT("common");
  const notify = useNotify();
  const me = useSession().data;
  const query = useAdminUser(login);
  const update = useUpdateAdminUser(login);
  const reset = useResetUser2fa(login);
  const [role, setRole] = useState<UserRole | undefined>();
  const [roleOpen, setRoleOpen] = useState(false);
  const [suspendOpen, setSuspendOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);

  const back = <Link to="/admin/users" className="adm-back"><Icon name="back" />{t("user.back")}</Link>;
  if (query.isPending) return <AdminPane title={login} actions={back}><p role="status" className="ui-note">{tc("state.loading")}</p></AdminPane>;
  if (query.isError) {
    return (
      <AdminPane title={login} actions={back}>
        <div role="alert">
          <EmptyState icon="alert" title={t("user.error")} description={query.error.message} action={<Button onClick={() => void query.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      </AdminPane>
    );
  }

  const u = query.data;
  const self = me?.username === u.username;
  const chosen = role ?? u.role;

  // 409 covers acting on yourself and the last active admin: say so plainly, keep the server text as detail.
  const fail = (e: unknown) => {
    if (e instanceof ApiError && e.status === 409) {
      notify.fail(new Error(`${t("user.conflict")} ${e.detail ? t("user.conflictDetail", { message: e.detail }) : ""}`.trim()));
    } else {
      notify.fail(e);
    }
  };

  function applyRole() {
    update.mutate({ role: chosen }, {
      onSuccess: () => { setRoleOpen(false); setRole(undefined); notify.ok(t("user.role.done")); },
      onError: (e) => { setRoleOpen(false); fail(e); },
    });
  }

  function setSuspended(suspended: boolean) {
    update.mutate({ suspended }, {
      onSuccess: () => { setSuspendOpen(false); notify.ok(t(suspended ? "user.suspend.done" : "user.unsuspend.done")); },
      onError: (e) => { setSuspendOpen(false); fail(e); },
    });
  }

  function reset2fa() {
    reset.mutate(undefined, {
      onSuccess: () => { setResetOpen(false); notify.ok(t("user.reset2fa.done")); },
      onError: (e) => { setResetOpen(false); fail(e); },
    });
  }

  const fields: [string, string][] = [
    [t("user.field.displayName"), u.displayName || u.username],
    [t("user.field.email"), u.email ?? t("user.noEmail")],
    [t("user.field.created"), formatDate(u.createdAt)],
    [t("user.field.lastActive"), u.lastActivity ? formatDate(u.lastActivity) : t("users.never")],
    [t("user.field.twoFactor"), u.has2fa ? t("users.twoFactor.on") : t("users.twoFactor.off")],
  ];

  return (
    <AdminPane title={u.username} subtitle={t("user.subtitle")} actions={back}>
      <p className="adm-badges">
        <Badge tone={roleTone(u.role)}>{t(`users.role.${u.role}`)}</Badge>
        <UserStatus user={u} />
      </p>
      {self && <p className="ui-note" role="status">{t("user.self")}</p>}
      <SettingsSection title={t("user.account")}>
        <SettingsCard>
          <dl className="adm-kv">
            {fields.map(([k, v]) => (
              <div key={k}><dt>{k}</dt><dd>{v}</dd></div>
            ))}
          </dl>
        </SettingsCard>
      </SettingsSection>
      <SettingsSection title={t("user.access")}>
        <SettingsCard>
          <SettingRow label={t("user.role.title")} description={t("user.role.hint")} dirty={chosen !== u.role} was={t(`users.role.${u.role}`)}>
            <div className="adm-ctl adm-row">
              <Select
                label={t("user.role.title")}
                hideLabel
                disabled={self}
                value={chosen}
                onValueChange={(v) => setRole(USER_ROLES.find((r) => r === v))}
                options={USER_ROLES.map((r) => ({ value: r, label: t(`users.role.${r}`) }))}
              />
              <ConfirmDialog
                open={roleOpen}
                onOpenChange={setRoleOpen}
                trigger={<Button variant="primary" disabled={self || chosen === u.role}>{t("user.role.save")}</Button>}
                title={t("user.role.dialogTitle", { login: u.username })}
                description={t("user.role.dialogHint", { login: u.username, role: t(`users.role.${chosen}`) })}
                confirmLabel={t("user.role.save")}
                pending={update.isPending}
                onConfirm={applyRole}
              />
            </div>
          </SettingRow>
        </SettingsCard>
      </SettingsSection>
      <DangerZone title={t("user.suspend.title")}>
        {u.suspendedAt ? (
          <DangerRow
            title={t("user.unsuspend.title")}
            description={t("user.unsuspend.hint", { date: formatDate(u.suspendedAt) })}
            action={<Button disabled={update.isPending} onClick={() => setSuspended(false)}>{t("user.unsuspend.button")}</Button>}
          />
        ) : (
          <DangerRow
            title={t("user.suspend.title")}
            description={t("user.suspend.hint")}
            action={
              <ConfirmDialog
                open={suspendOpen}
                onOpenChange={setSuspendOpen}
                trigger={<Button variant="danger" disabled={self}>{t("user.suspend.button")}</Button>}
                title={t("user.suspend.dialogTitle", { login: u.username })}
                description={t("user.suspend.dialogHint", { login: u.username })}
                confirmLabel={t("user.suspend.button")}
                pending={update.isPending}
                onConfirm={() => setSuspended(true)}
              />
            }
          />
        )}
        <DangerRow
          title={t("user.reset2fa.title")}
          description={u.has2fa ? t("user.reset2fa.hint") : t("user.reset2fa.none")}
          action={
            <ConfirmDialog
              open={resetOpen}
              onOpenChange={setResetOpen}
              trigger={<Button variant="danger" disabled={!u.has2fa}>{t("user.reset2fa.button")}</Button>}
              title={t("user.reset2fa.dialogTitle", { login: u.username })}
              description={t("user.reset2fa.dialogHint")}
              confirmLabel={t("user.reset2fa.button")}
              pending={reset.isPending}
              onConfirm={reset2fa}
            />
          }
        />
      </DangerZone>
    </AdminPane>
  );
}
