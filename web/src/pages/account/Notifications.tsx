import { Link } from "@tanstack/react-router";
import { useNotificationSettings, useSaveNotificationSettings } from "../../api/notifications.ts";
import { useT } from "../../i18n/useT.ts";
import { Card } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

/** Per-user notification delivery settings. */
export function NotificationPrefs() {
  const t = useT("account");
  const tc = useT("common");
  const notify = useNotify();
  const settings = useNotificationSettings();
  const save = useSaveNotificationSettings();

  return (
    <Card title={t("notif.title")}>
      <p className="ui-note acc-pad">
        {t("notif.hint")} <Link to="/notifications">{t("notif.inbox")}</Link>
      </p>
      {settings.isPending ? (
        <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>
      ) : settings.isError ? (
        <p role="alert" className="ui-note acc-pad">{settings.error.message}</p>
      ) : (
        <div className="acc-form acc-pad">
          <label className="acc-check">
            <input
              type="checkbox"
              checked={settings.data.email}
              disabled={save.isPending}
              onChange={(e) => save.mutate({ email: e.target.checked }, { onSuccess: () => notify.ok(t("notif.saved")), onError: notify.fail })}
            />
            <span>{t("notif.email")}</span>
          </label>
          <p className="ui-note">{t("notif.emailHint")}</p>
        </div>
      )}
    </Card>
  );
}
