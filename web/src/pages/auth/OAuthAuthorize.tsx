import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { isUnauthenticated, useSession } from "../../api/identity.ts";
import { decideOAuthConsent, getOAuthConsent, parseAuthorizeQuery, type OAuthConsent } from "../../api/oauth.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Icon } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { AppLogo, originOf, useOwnerLabel, useScopeText } from "../oauth/common.tsx";
import { AuthFrame } from "./AuthFrame.tsx";
import { useProblemText } from "./useProblemText.ts";

export const AUTHORIZE_PATH = "/login/oauth/authorize";

// The router re-serializes search params on its first render (JSON-decoding values such as a numeric
// state), so the raw query is captured when the bundle loads, before that can happen.
const bootQuery = typeof window !== "undefined" && window.location.pathname === AUTHORIZE_PATH ? window.location.search : undefined;

type Phase = { kind: "loading" } | { kind: "error"; message: string } | { kind: "ask"; consent: OAuthConsent } | { kind: "leaving" };

/**
 * OAuth web flow consent (/login/oauth/authorize, GitHub's query parameters). Already granted scopes
 * skip the question; a request the server rejects never redirects.
 */
export function OAuthAuthorize() {
  const t = useT("oauth");
  const tc = useT("common");
  const problem = useProblemText();
  const session = useSession();
  const navigate = useNavigate();
  const owner = useOwnerLabel();
  const [raw] = useState(() => (bootQuery !== undefined && window.location.pathname === AUTHORIZE_PATH ? bootQuery : window.location.search));
  const [query] = useState(() => parseAuthorizeQuery(raw));
  const [phase, setPhase] = useState<Phase>(() => (query.clientId ? { kind: "loading" } : { kind: "error", message: t("consent.noClient") }));
  const [busy, setBusy] = useState(false);
  const started = useRef(false);
  const user = session.data;
  const anonymous = session.isError && isUnauthenticated(session.error);

  // Sign in first, coming back to this exact URL.
  useEffect(() => {
    if (anonymous) void navigate({ to: "/login", search: { next: AUTHORIZE_PATH + raw }, replace: true });
  }, [anonymous, navigate, raw]);

  useEffect(() => {
    if (!user || !query.clientId || started.current) return;
    started.current = true;
    (async () => {
      try {
        const consent = await getOAuthConsent(query);
        if (!consent.granted) {
          setPhase({ kind: "ask", consent });
          return;
        }
        setPhase({ kind: "leaving" });
        window.location.assign((await decideOAuthConsent(query, true)).url);
      } catch (e) {
        setPhase({ kind: "error", message: problem(e) });
      }
    })();
  }, [user, query, problem]);

  async function decide(approve: boolean) {
    setBusy(true);
    try {
      const { url } = await decideOAuthConsent(query, approve);
      setPhase({ kind: "leaving" });
      window.location.assign(url);
    } catch (e) {
      setBusy(false);
      setPhase({ kind: "error", message: problem(e) });
    }
  }

  const home = <Link to="/">{t("consent.home")}</Link>;
  if (session.isError && !anonymous) {
    return (
      <AuthFrame title={t("consent.errorTitle")} footer={home}>
        <div role="alert"><EmptyState icon="alert" title={t("consent.errorTitle")} description={session.error.message} /></div>
      </AuthFrame>
    );
  }
  if (!user || phase.kind === "loading" || phase.kind === "leaving") {
    return (
      <AuthFrame title={t("consent.title")}>
        <p role="status" className="auth-sub">{phase.kind === "leaving" ? t("consent.redirecting") : tc("state.loading")}</p>
      </AuthFrame>
    );
  }
  if (phase.kind === "error") {
    return (
      <AuthFrame title={t("consent.errorTitle")} footer={home}>
        <div role="alert"><EmptyState icon="alert" title={t("consent.errorTitle")} description={phase.message} /></div>
      </AuthFrame>
    );
  }

  const { app, scopes, redirectUri } = phase.consent;
  return (
    <AuthFrame
      wide
      title={t("consent.authorize", { app: app.name })}
      subtitle={t("by", { owner: owner(app.owner) })}
      footer={app.homepageUrl ? <a href={app.homepageUrl} target="_blank" rel="noreferrer noopener">{originOf(app.homepageUrl)}</a> : undefined}
    >
      <div className="auth-pair">
        <AppLogo name={app.name} url={app.logoUrl} size="xl" />
        <span className="auth-pair-d" aria-hidden="true"><i /><i /><i /></span>
        <Avatar user={user} size="xl" />
      </div>
      <p className="auth-note">{t("consent.account", { user: user.username })}</p>
      {app.description && <p className="auth-note">{app.description}</p>}
      <h2 className="sr-only">{t("consent.wants")}</h2>
      <ul className="auth-scopes">
        {scopes.length > 0 ? scopes.map((s) => <ScopeRow key={s} scope={s} />) : (
          <li className="auth-scope"><Icon name="info" /><span><b>{t("scope.none")}</b></span></li>
        )}
      </ul>
      <div className="auth-actions">
        <Button disabled={busy} onClick={() => void decide(false)}>{t("consent.cancel")}</Button>
        <Button variant="primary" disabled={busy} onClick={() => void decide(true)}>{t("consent.approve")}</Button>
      </div>
      <p className="auth-note auth-redirect">{t("consent.redirectTo")} <span className="auth-mono">{redirectUri}</span></p>
    </AuthFrame>
  );
}

const SCOPE_GROUPS = { user: "user", repo: "book", packages: "pkg", actions: "play" } as const;
type ScopeGroup = keyof typeof SCOPE_GROUPS;
const isGroup = (g: string): g is ScopeGroup => g in SCOPE_GROUPS;

/** One requested scope: an icon for its area, the area name and what the scope allows. */
function ScopeRow({ scope }: { scope: string }) {
  const t = useT("oauth");
  const scopeText = useScopeText();
  const group = scope.split(":")[0] ?? scope;
  const known = isGroup(group);
  return (
    <li className="auth-scope">
      <Icon name={known ? SCOPE_GROUPS[group] : "info"} />
      <span>
        <b>{known ? t(`scope.group.${group}`) : scope}</b>
        {known && <small>{scopeText(scope)}</small>}
      </span>
    </li>
  );
}
