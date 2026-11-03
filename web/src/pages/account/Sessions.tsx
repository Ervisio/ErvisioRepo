import { useRevokeOtherSessions, useRevokeSession, useSessions } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "./common.tsx";

/** Active sessions of the account, with revoke. */
export function Sessions() {
  const t = useT("account");
  const notify = useNotify();
  const sessions = useSessions();
  const revoke = useRevokeSession();
  const revokeOthers = useRevokeOtherSessions();
  const others = (sessions.data ?? []).some((s) => !s.current);

  return (
    <Card
      title={t("sessions.title")}
      actions={
        others ? (
          <Button size="sm" variant="danger" disabled={revokeOthers.isPending} onClick={() => revokeOthers.mutate(undefined, { onSuccess: () => notify.ok(t("sessions.revokedOthers")), onError: notify.fail })}>
            {t("sessions.revokeOthers")}
          </Button>
        ) : undefined
      }
    >
      <Async query={sessions} empty={t("sessions.empty")}>
        {(rows) =>
          rows.map((s) => (
            <div key={s.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title">
                  <span>{s.userAgent || t("sessions.unknownAgent")}</span>
                  {s.current && <Badge tone="ok">{t("sessions.current")}</Badge>}
                </div>
                <div className="ui-note">
                  {[s.ip, t("sessions.lastSeen", { date: formatDate(s.lastSeenAt) }), t("sessions.started", { date: formatDate(s.createdAt) })].filter(Boolean).join(" · ")}
                </div>
              </div>
              {!s.current && (
                <Button size="sm" variant="ghost" aria-label={t("sessions.revokeFor", { ip: s.ip })} onClick={() => revoke.mutate(s.id, { onSuccess: () => notify.ok(t("sessions.revoked")), onError: notify.fail })}>
                  {t("sessions.revoke")}
                </Button>
              )}
            </div>
          ))
        }
      </Async>
    </Card>
  );
}
