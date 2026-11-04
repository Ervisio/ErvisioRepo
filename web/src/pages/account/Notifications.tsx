import { Link } from "@tanstack/react-router";
import { useNotificationSettings, useSaveNotificationSettings } from "../../api/notifications.ts";
import { useT } from "../../i18n/useT.ts";
import { SettingRow, SettingsCard, SettingsSection, Toggle } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

/** Per-user notification delivery settings. The API has one switch: email delivery. */
export function NotificationPrefs() {
  const t = useT("account");
  const tc = useT("common");
  const notify = useNotify();
  const settings = useNotificationSettings();
  const save = useSaveNotificationSettings();

  return (
    <SettingsSection
      title={t("notif.title")}
      description={<>{t("notif.hint")} <Link to="/notifications">{t("notif.inbox")}</Link></>}
    >
      <SettingsCard>
        {settings.isPending ? (
          <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>
        ) : settings.isError ? (
          <p role="alert" className="ui-note acc-pad">{settings.error.message}</p>
        ) : (
          <SettingRow label={t("notif.email")} description={t("notif.emailHint")}>
            <Toggle
              label={t("notif.email")}
              checked={settings.data.email}
              disabled={save.isPending}
              onChange={(email) => save.mutate({ email }, { onSuccess: () => notify.ok(t("notif.saved")), onError: notify.fail })}
            />
          </SettingRow>
        )}
      </SettingsCard>
    </SettingsSection>
  );
}
