import { oidcLink, useLinked, useProviders, useUnlink } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card } from "../../ui/index.ts";
import { oidcErrorText } from "../auth/Login.tsx";
import { Async, formatDate, useNotify } from "./common.tsx";

/** OIDC identities linked to this account (bound to issuer + subject, never to the email alone). */
export function Linked({ oidcError }: { oidcError?: string }) {
  const t = useT("account");
  const ta = useT("auth");
  const notify = useNotify();
  const linked = useLinked();
  const providers = useProviders();
  const unlink = useUnlink();

  const link = (provider: string) =>
    oidcLink(provider, "/settings").then(
      (r) => window.location.assign(r.url),
      (e: unknown) => notify.fail(e),
    );

  return (
    <Card title={t("linked.title")}>
      {oidcError && <p className="auth-error acc-pad" role="alert">{oidcErrorText(ta, oidcError)}</p>}
      <Async query={linked} empty={t("linked.empty")}>
        {(rows) =>
          rows.map((l) => (
            <div key={l.id} className="ui-row acc-wrap">
              <div className="acc-grow">
                <div className="acc-title">{l.label}</div>
                <div className="ui-note">{[l.email, formatDate(l.createdAt)].filter(Boolean).join(" · ")}</div>
              </div>
              <Button size="sm" variant="ghost" aria-label={t("linked.unlink", { name: l.label })} onClick={() => unlink.mutate(l.id, { onSuccess: () => notify.ok(t("linked.unlinked")), onError: notify.fail })}>
                {t("linked.unlinkButton")}
              </Button>
            </div>
          ))
        }
      </Async>
      {(providers.data?.length ?? 0) > 0 && (
        <div className="acc-inline">
          {providers.data?.map((p) => (
            <Button key={p.name} onClick={() => void link(p.name)}>
              {t("linked.link", { name: p.label })}
            </Button>
          ))}
        </div>
      )}
    </Card>
  );
}
