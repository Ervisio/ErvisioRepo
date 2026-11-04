import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, request } from "./http.ts";
import type { components } from "./schema";

// Identity, security, credentials, organizations and audit. Every type comes from the
// generated OpenAPI schema (api/openapi.yaml), so a contract change breaks the build here.
type S = components["schemas"];
export type User = S["User"];
export type Invite = S["Invite"];
export type SessionInfo = S["SessionInfo"];
export type EmailAddress = S["EmailAddress"];
export type Provider = S["OidcProvider"];
export type MfaChallenge = S["MfaChallenge"];
export type LoginResult = User | MfaChallenge;
export type Reauth = S["ReauthRequest"];
export type UserSummary = S["UserSummary"];

/**
 * Users matching a username or display name prefix, for the assignee and reviewer
 * pickers. The server only returns the people the caller may see; repo (owner/name)
 * adds the people of that repository.
 */
export const useUserSearch = (q: string, repo?: string) =>
  useQuery({
    queryKey: ["users", "search", q, repo],
    queryFn: () => request<UserSummary[]>("GET", `/search/users?${new URLSearchParams({ q, ...(repo ? { repo } : {}) }).toString()}`),
    enabled: q.trim() !== "",
    staleTime: 30_000,
  });
export type Passkey = S["Passkey"];
export type TotpSetup = S["TotpSetup"];
export type SecurityStatus = S["SecurityStatus"];
export type TokenInfo = S["AccessToken"];
export type NewToken = S["CreatedAccessToken"];
export type TokenScope = S["AccessTokenCreate"]["scopes"][number];
export type SSHKey = S["SshKey"];
export type GPGKey = S["GpgKey"];
export type LinkedAccount = S["OidcLink"];
export type OrgRole = S["OrgRole"];
export type Permission = S["Permission"];
export type Org = S["Org"];
export type OrgMember = S["OrgMember"];
export type Team = S["Team"];
export type OrgInvite = S["OrgInvite"];
export type AuditEntry = S["AuditEntry"];
export type AuditPage = S["AuditPage"];

export const SCOPES: readonly TokenScope[] = [
  "repo:read",
  "repo:write",
  "packages:read",
  "packages:write",
  "actions:write",
  "user:read",
  "user:write",
];

/** True when POST /auth/login answered 202: a second factor is still needed. */
export const isChallenge = (r: LoginResult): r is MfaChallenge => "methods" in r;

const enc = encodeURIComponent;

/** The signed-in user; undefined data with a 401 error means anonymous. */
export function useSession() {
  return useQuery({
    queryKey: ["user"],
    retry: false,
    staleTime: 60_000,
    queryFn: () => request<User>("GET", "/user"),
  });
}

export function isUnauthenticated(e: unknown): boolean {
  return e instanceof ApiError && e.status === 401;
}

export function useProviders() {
  return useQuery({
    queryKey: ["auth", "providers"],
    retry: false,
    queryFn: () => request<Provider[]>("GET", "/auth/oidc/providers"),
  });
}

/** Browser URL that starts an OIDC sign-in; the server redirects back to returnTo. */
export const oidcLoginURL = (name: string, returnTo: string) =>
  `/auth/oidc/${enc(name)}/login?return_to=${enc(returnTo)}`;

function useList<T>(key: string, path: string) {
  return useQuery({ queryKey: ["user", key], queryFn: () => request<T[]>("GET", path) });
}

function useInvalidating<V, R>(keys: string[][], fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => Promise.all(keys.map((queryKey) => qc.invalidateQueries({ queryKey }))),
  });
}

export const useTokens = () => useList<TokenInfo>("tokens", "/user/tokens");
export const useSSHKeys = () => useList<SSHKey>("ssh-keys", "/user/keys/ssh");
export const useGPGKeys = () => useList<GPGKey>("gpg-keys", "/user/keys/gpg");
export const useLinked = () => useList<LinkedAccount>("identities", "/user/oidc");
export const useMyOrgs = () => useQuery({ queryKey: ["orgs"], queryFn: () => request<Org[]>("GET", "/orgs") });
export const useSecurity = () =>
  useQuery({ queryKey: ["user", "security"], queryFn: () => request<SecurityStatus>("GET", "/user/security") });

export const useCreateToken = () =>
  useInvalidating<S["AccessTokenCreate"], NewToken>([["user", "tokens"]], (b) => request("POST", "/user/tokens", b));
export const useDeleteToken = () =>
  useInvalidating<string, void>([["user", "tokens"]], (id) => request("DELETE", `/user/tokens/${enc(id)}`));
export const useAddSSHKey = () =>
  useInvalidating<S["SshKeyCreate"], SSHKey>([["user", "ssh-keys"]], (b) => request("POST", "/user/keys/ssh", b));
export const useDeleteSSHKey = () =>
  useInvalidating<string, void>([["user", "ssh-keys"]], (id) => request("DELETE", `/user/keys/ssh/${enc(id)}`));
export const useAddGPGKey = () =>
  useInvalidating<S["GpgKeyCreate"], GPGKey>([["user", "gpg-keys"]], (b) => request("POST", "/user/keys/gpg", b));
export const useDeleteGPGKey = () =>
  useInvalidating<string, void>([["user", "gpg-keys"]], (id) => request("DELETE", `/user/keys/gpg/${enc(id)}`));
export const useUnlink = () =>
  useInvalidating<string, void>([["user", "identities"]], (id) => request("DELETE", `/user/oidc/${enc(id)}`));
/** Starts linking an OIDC account; the caller navigates to the returned URL. */
export const oidcLink = (provider: string, returnTo: string) =>
  request<S["OidcRedirect"]>("POST", `/user/oidc/${enc(provider)}/link`, { returnTo });
export const useCreateOrg = () => useInvalidating<S["OrgCreate"], Org>([["orgs"]], (b) => request("POST", "/orgs", b));

// Account security: every change needs a fresh password or second-factor code.
export const totpBegin = (r: Reauth) => request<TotpSetup>("POST", "/user/security/totp", r);
export const totpConfirm = (code: string) => request<S["RecoveryCodes"]>("POST", "/user/security/totp/confirm", { code });
export const totpDisable = (r: Reauth) => request("DELETE", "/user/security/totp", r);
export const regenerateRecoveryCodes = (r: Reauth) => request<S["RecoveryCodes"]>("POST", "/user/security/recovery-codes", r);
export const passkeyRegisterBegin = (r: Reauth) => request<S["PasskeyChallenge"]>("POST", "/user/security/passkeys/options", r);
export const passkeyRegisterFinish = (b: S["PasskeyRegisterRequest"]) => request<Passkey>("POST", "/user/security/passkeys", b);
export const passkeyDelete = (id: string, r: Reauth) => request("DELETE", `/user/security/passkeys/${enc(id)}`, r);

// Sign-in steps after the password (the pending-MFA cookie carries the state).
export const mfaChallenge = () => request<MfaChallenge>("GET", "/auth/mfa");
export const mfaTotp = (code: string) => request<User>("POST", "/auth/mfa/totp", { code });
export const mfaRecovery = (code: string) => request<User>("POST", "/auth/mfa/recovery", { code });
export const mfaWebauthnBegin = () => request<S["WebAuthnOptions"]>("POST", "/auth/mfa/webauthn/options");
export const mfaWebauthnFinish = (credential: Record<string, unknown>) => request<User>("POST", "/auth/mfa/webauthn", { credential });
export const mfaEnrollTotp = () => request<TotpSetup>("POST", "/auth/mfa/enroll/totp");
export const mfaEnrollTotpConfirm = (code: string) => request<S["MfaEnrollResult"]>("POST", "/auth/mfa/enroll/totp/confirm", { code });
export const passkeyLoginBegin = () => request<S["PasskeyChallenge"]>("POST", "/auth/mfa/passkey/options");
export const passkeyLoginFinish = (b: S["PasskeyLoginRequest"]) => request<User>("POST", "/auth/mfa/passkey", b);

// Organizations: members, teams, invitations.
export const acceptOrgInvite = (token: string) => request<S["OrgInviteAccepted"]>("POST", "/orgs/invites/accept", { token });
export const useOrg = (org: string) => useQuery({ queryKey: ["orgs", org], queryFn: () => request<Org>("GET", `/orgs/${enc(org)}`) });
export const useUpdateOrg = (org: string) =>
  useInvalidating<S["OrgUpdate"], Org>([["orgs", org]], (b) => request("PATCH", `/orgs/${enc(org)}`, b));
export const useOrgMembers = (org: string) =>
  useQuery({ queryKey: ["orgs", org, "members"], queryFn: () => request<OrgMember[]>("GET", `/orgs/${enc(org)}/members`) });
export const useOrgTeams = (org: string) =>
  useQuery({ queryKey: ["orgs", org, "teams"], queryFn: () => request<Team[]>("GET", `/orgs/${enc(org)}/teams`) });
export const useOrgInvites = (org: string, enabled: boolean) =>
  useQuery({
    queryKey: ["orgs", org, "invites"],
    enabled,
    queryFn: () => request<OrgInvite[]>("GET", `/orgs/${enc(org)}/invites`),
  });
export const useInviteMember = (org: string) =>
  useInvalidating<S["OrgInviteRequest"], OrgInvite>([["orgs", org, "invites"]], (b) => request("POST", `/orgs/${enc(org)}/invites`, b));
export const useCancelInvite = (org: string) =>
  useInvalidating<string, void>([["orgs", org, "invites"]], (id) => request("DELETE", `/orgs/${enc(org)}/invites/${enc(id)}`));
export const useRemoveMember = (org: string) =>
  useInvalidating<string, void>([["orgs", org, "members"]], (u) => request("DELETE", `/orgs/${enc(org)}/members/${enc(u)}`));
export const useSetMemberRole = (org: string) =>
  useInvalidating<{ username: string; role: OrgRole }, OrgMember>([["orgs", org, "members"]], ({ username, role }) =>
    request("PUT", `/orgs/${enc(org)}/members/${enc(username)}`, { role }),
  );
export const useCreateTeam = (org: string) =>
  useInvalidating<S["TeamCreate"], Team>([["orgs", org, "teams"]], (b) => request("POST", `/orgs/${enc(org)}/teams`, b));
export const useDeleteTeam = (org: string) =>
  useInvalidating<string, void>([["orgs", org, "teams"]], (n) => request("DELETE", `/orgs/${enc(org)}/teams/${enc(n)}`));
export const useUpdateTeam = (org: string) =>
  useInvalidating<{ team: string; body: S["TeamUpdate"] }, Team>([["orgs", org, "teams"]], ({ team, body }) =>
    request("PATCH", `/orgs/${enc(org)}/teams/${enc(team)}`, body),
  );
export type TeamMember = S["TeamMember"];
export type TeamRole = TeamMember["role"];
export const useTeamMembers = (org: string, team: string) =>
  useQuery({ queryKey: ["orgs", org, "teams", team, "members"], queryFn: () => request<TeamMember[]>("GET", `/orgs/${enc(org)}/teams/${enc(team)}/members`) });
export const useSetTeamMember = (org: string, team: string) =>
  useInvalidating<{ username: string; role: TeamRole }, void>([["orgs", org, "teams"]], ({ username, role }) =>
    request("PUT", `/orgs/${enc(org)}/teams/${enc(team)}/members/${enc(username)}`, { role }),
  );
export const useRemoveTeamMember = (org: string, team: string) =>
  useInvalidating<string, void>([["orgs", org, "teams"]], (u) => request("DELETE", `/orgs/${enc(org)}/teams/${enc(team)}/members/${enc(u)}`));
export const useDeleteOrg = (org: string) => useInvalidating<void, void>([["orgs"]], () => request("DELETE", `/orgs/${enc(org)}`));
/** Instance invitation (admins only). The token comes back once; there is no list or revoke endpoint. */
export const useCreateInstanceInvite = () =>
  useMutation({ mutationFn: (b: S["InviteRequest"]) => request<Invite>("POST", "/admin/invites", b) });

export function useAudit(filter: { action: string; actor: string }, before: string | undefined) {
  const q = new URLSearchParams({ limit: "50" });
  if (filter.action) q.set("action", filter.action);
  if (filter.actor) q.set("actor", filter.actor);
  if (before) q.set("before", before);
  return useQuery({ queryKey: ["admin", "audit", filter, before], queryFn: () => request<AuditPage>("GET", `/admin/audit?${q}`) });
}

// Account self-service: profile, password, sessions, email addresses.
export function useUpdateProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: S["ProfileUpdate"]) => request<User>("PATCH", "/user/profile", b),
    onSuccess: (u) => qc.setQueryData(["user"], u),
  });
}
export const changePassword = (b: S["PasswordChange"]) => request("PUT", "/user/password", b);
export const useSessions = () => useList<SessionInfo>("sessions", "/user/sessions");
export const useRevokeSession = () =>
  useInvalidating<string, void>([["user", "sessions"]], (id) => request("DELETE", `/user/sessions/${enc(id)}`));
export const useRevokeOtherSessions = () =>
  useInvalidating<void, void>([["user", "sessions"]], () => request("DELETE", "/user/sessions"));
export const useEmails = () => useList<EmailAddress>("emails", "/user/emails");
export const useAddEmail = () =>
  useInvalidating<string, EmailAddress>([["user", "emails"]], (email) => request("POST", "/user/emails", { email }));
export const useSetPrimaryEmail = () =>
  useInvalidating<string, void>([["user", "emails"], ["user"]], (id) => request("POST", `/user/emails/${enc(id)}/primary`));
export const useDeleteEmail = () =>
  useInvalidating<string, void>([["user", "emails"]], (id) => request("DELETE", `/user/emails/${enc(id)}`));
export const resendEmailVerification = (id: string) => request("POST", `/user/emails/${enc(id)}/verify`);
