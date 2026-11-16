import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

// OAuth apps (registration, client secrets), the apps a user has authorized and the web flow consent.
type S = components["schemas"];
export type OAuthApp = S["OAuthApp"];
export type OAuthAppCreate = S["OAuthAppCreate"];
export type OAuthAppUpdate = S["OAuthAppUpdate"];
export type OAuthAppSecret = S["OAuthAppSecret"];
export type CreatedOAuthAppSecret = S["CreatedOAuthAppSecret"];
export type OAuthAppOwner = S["OAuthAppOwner"];
export type OAuthAppSummary = S["OAuthAppSummary"];
export type OAuthAuthorization = S["OAuthAuthorization"];
export type OAuthConsent = S["OAuthConsent"];
export type OAuthConsentDecision = S["OAuthConsentDecision"];
export type AppRegistration = NonNullable<S["Meta"]["appRegistration"]>;

/** Who owns the apps of a list: the signed-in account or an organization. */
export type OAuthAppsOwner = { kind: "user" } | { kind: "org"; org: string };

/** The server allows at most this many client secrets per app. */
export const MAX_CLIENT_SECRETS = 5;

const enc = encodeURIComponent;
const listPath = (o: OAuthAppsOwner) => (o.kind === "user" ? "/user/oauth-apps" : `/orgs/${enc(o.org)}/oauth-apps`);
const listKey = (o: OAuthAppsOwner) => ["oauth-apps", o.kind === "user" ? "user" : `org:${o.org}`];
const appPath = (clientId: string) => `/oauth-apps/${enc(clientId)}`;

export const useOAuthApps = (owner: OAuthAppsOwner) =>
  useQuery({ queryKey: listKey(owner), queryFn: () => request<OAuthApp[]>("GET", listPath(owner)) });

export function useCreateOAuthApp(owner: OAuthAppsOwner) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: OAuthAppCreate) => request<OAuthApp>("POST", listPath(owner), body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["oauth-apps"] }),
  });
}

export const useOAuthApp = (clientId: string) =>
  useQuery({ queryKey: ["oauth-app", clientId], queryFn: () => request<OAuthApp>("GET", appPath(clientId)) });

/** Invalidates the app and every list after a change. */
function useAppMutation<V, R>(clientId: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => Promise.all([
      qc.invalidateQueries({ queryKey: ["oauth-app", clientId] }),
      qc.invalidateQueries({ queryKey: ["oauth-apps"] }),
    ]),
  });
}

export const useUpdateOAuthApp = (clientId: string) =>
  useAppMutation<OAuthAppUpdate, OAuthApp>(clientId, (b) => request("PATCH", appPath(clientId), b));

export function useDeleteOAuthApp(clientId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => request<void>("DELETE", appPath(clientId)),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["oauth-app", clientId] });
      return Promise.all([
        qc.invalidateQueries({ queryKey: ["oauth-apps"] }),
        qc.invalidateQueries({ queryKey: ["user", "oauth-authorizations"] }),
      ]);
    },
  });
}

export const useCreateOAuthSecret = (clientId: string) =>
  useAppMutation<void, CreatedOAuthAppSecret>(clientId, () => request("POST", `${appPath(clientId)}/secrets`));

export const useDeleteOAuthSecret = (clientId: string) =>
  useAppMutation<string, void>(clientId, (id) => request("DELETE", `${appPath(clientId)}/secrets/${enc(id)}`));

export const useOAuthAuthorizations = () =>
  useQuery({ queryKey: ["user", "oauth-authorizations"], queryFn: () => request<OAuthAuthorization[]>("GET", "/user/oauth-authorizations") });

export function useRevokeOAuthAuthorization() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => request<void>("DELETE", `/user/oauth-authorizations/${enc(id)}`),
    onSuccess: () => Promise.all([
      qc.invalidateQueries({ queryKey: ["user", "oauth-authorizations"] }),
      qc.invalidateQueries({ queryKey: ["user", "tokens"] }),
    ]),
  });
}

/** GitHub's authorize query parameters the consent page forwards. */
export type AuthorizeQuery = {
  clientId: string;
  redirectUri?: string;
  scope?: string;
  state?: string;
  codeChallenge?: string;
  codeChallengeMethod?: string;
};

/** Reads GitHub's authorize parameters from a raw query string (allow_signup and login are ignored). */
export function parseAuthorizeQuery(search: string): AuthorizeQuery {
  const q = new URLSearchParams(search);
  const opt = (k: string) => q.get(k) ?? undefined;
  return {
    clientId: q.get("client_id") ?? "",
    redirectUri: opt("redirect_uri"),
    scope: opt("scope"),
    state: opt("state"),
    codeChallenge: opt("code_challenge"),
    codeChallengeMethod: opt("code_challenge_method"),
  };
}

export function getOAuthConsent(q: AuthorizeQuery): Promise<OAuthConsent> {
  const p = new URLSearchParams({ client_id: q.clientId });
  if (q.redirectUri !== undefined) p.set("redirect_uri", q.redirectUri);
  if (q.scope !== undefined) p.set("scope", q.scope);
  if (q.codeChallenge !== undefined) p.set("code_challenge", q.codeChallenge);
  if (q.codeChallengeMethod !== undefined) p.set("code_challenge_method", q.codeChallengeMethod);
  return request<OAuthConsent>("GET", `/user/oauth/authorize?${p.toString()}`);
}

/** Approves or denies the request; the answer is where to send the browser. */
export const decideOAuthConsent = (q: AuthorizeQuery, approve: boolean) =>
  request<S["OAuthRedirect"]>("POST", "/user/oauth/authorize", { ...q, approve } satisfies OAuthConsentDecision);

/** Whether the current user may register an app under the given owner, per the instance policy. */
export function canRegisterApps(policy: AppRegistration | undefined, owner: "user" | "org", instanceAdmin: boolean): boolean {
  if (instanceAdmin) return true;
  switch (policy ?? "everyone") {
    case "everyone": return true;
    case "org_owners": return owner === "org";
    default: return false;
  }
}
