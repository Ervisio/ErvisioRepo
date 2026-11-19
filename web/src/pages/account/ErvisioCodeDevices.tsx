import { useErvisioCodeDevices, useRevokeErvisioCodeDevice } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, ConfirmDialog, SettingsSection } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "./common.tsx";

export function ErvisioCodeDevices() {
  const t = useT("account");
  return (
    <SettingsSection title={t("tab.ervisiocode-devices")} description={t("devices.description")}>
      <ErvisioCodeDeviceList empty={t("devices.empty")} />
    </SettingsSection>
  );
}

export function ErvisioCodeDeviceList({ live = false, empty }: { live?: boolean; empty: string }) {
  const t = useT("account");
  const th = useT("codehub");
  const devices = useErvisioCodeDevices(live);
  const revoke = useRevokeErvisioCodeDevice();
  const notify = useNotify();

  return (
    <Async query={devices} empty={empty}>
      {(rows) => (
        <div className="st-list" role="list">
          {rows.map((device) => (
            <div key={device.id} className="st-item" role="listitem">
              <div className="st-item-m">
                <b>{device.name}</b>
                <div className="ui-note">{t("devices.added", { date: formatDate(device.createdAt) })}</div>
                <div className="ui-note">{device.lastUsedAt ? t("devices.lastUsed", { date: formatDate(device.lastUsedAt) }) : t("devices.neverUsed")}</div>
                <div className="ui-note">{t("devices.expires", { date: formatDate(device.expiresAt) })}</div>
              </div>
              {live && (
                <Badge tone={device.remote?.online ? "ok" : "neutral"}>
                  {th(device.remote ? (device.remote.online ? "status.online" : "status.offline") : "status.unknown")}
                </Badge>
              )}
              <ConfirmDialog
                trigger={
                  <Button size="sm" variant="danger" disabled={revoke.isPending} aria-label={t("devices.revokeFor", { name: device.name })}>
                    {t("devices.revoke")}
                  </Button>
                }
                title={t("devices.confirmTitle", { name: device.name })}
                description={t("devices.confirmDescription")}
                confirmLabel={revoke.isPending ? t("devices.revoking") : t("devices.revoke")}
                pending={revoke.isPending}
                onConfirm={() => revoke.mutate(device.id, { onSuccess: () => notify.ok(t("devices.revoked")) })}
              >
                {revoke.isError && revoke.variables === device.id && <p className="auth-error" role="alert">{t("devices.revokeError")}</p>}
              </ConfirmDialog>
            </div>
          ))}
        </div>
      )}
    </Async>
  );
}
