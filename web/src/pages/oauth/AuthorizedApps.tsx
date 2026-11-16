import { useOAuthAuthorizations, useRevokeOAuthAuthorization } from "../../api/oauth.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, ConfirmDialog, SettingsSection } from "../../ui/index.ts";
import { Async, formatDate, useNotify } from "../account/common.tsx";
import { AppLogo, useOwnerLabel, useScopeText } from "./common.tsx";

/** OAuth apps the account has authorized, each revocable with its tokens. */
export function AuthorizedApps() {
  const t = useT("oauth");
  const notify = useNotify();
  const list = useOAuthAuthorizations();
  const revoke = useRevokeOAuthAuthorization();
  const owner = useOwnerLabel();
  const scopeText = useScopeText();
  return (
    <SettingsSection title={t("authorized.title")} description={t("authorized.hint")}>
      <Async query={list} empty={t("authorized.empty")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((a) => (
              <div key={a.id} className="st-item">
                <AppLogo name={a.app.name} url={a.app.logoUrl} />
                <div className="st-item-m">
                  <div>
                    <b>{a.app.name}</b> <span className="ui-note">{t("by", { owner: owner(a.app.owner) })}</span>
                  </div>
                  <div className="ui-note">
                    {a.tokenScopes.length > 0 ? a.tokenScopes.map(scopeText).join("; ") : t("scope.none")}
                  </div>
                  <div className="ui-note">
                    {a.lastUsedAt ? t("lastUsed", { date: formatDate(a.lastUsedAt) }) : t("neverUsed")}
                    {" · "}
                    {t(a.tokens === 1 ? "authorized.tokens.one" : "authorized.tokens.other", { count: a.tokens })}
                  </div>
                </div>
                <ConfirmDialog
                  trigger={<Button size="sm" variant="danger">{t("authorized.revoke")}</Button>}
                  title={t("authorized.revokeTitle", { app: a.app.name })}
                  description={t("authorized.revokeHint")}
                  confirmLabel={t("authorized.revoke")}
                  pending={revoke.isPending}
                  onConfirm={() => revoke.mutate(a.id, { onSuccess: () => notify.ok(t("authorized.revoked")), onError: notify.fail })}
                />
              </div>
            ))}
          </div>
        )}
      </Async>
    </SettingsSection>
  );
}
