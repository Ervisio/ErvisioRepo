import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { qs, repoBase } from "./code.ts";
import { request } from "./http.ts";
import { pagedHeader } from "./issues.ts";
import type { components } from "./schema";

// Repository and organization settings of phase 5: branch protection, tag rules, webhooks, GitHub.
type S = components["schemas"];
export type BranchProtection = S["BranchProtection"];
export type BranchProtectionInput = S["BranchProtectionInput"];
export type TagRule = S["TagRule"];
export type TagRuleInput = S["TagRuleInput"];
export type Webhook = S["Webhook"];
export type WebhookCreate = S["WebhookCreate"];
export type WebhookUpdate = S["WebhookUpdate"];
export type WebhookDelivery = S["WebhookDelivery"];
export type WebhookDeliveryDetail = S["WebhookDeliveryDetail"];
export type GitHubConnection = S["GitHubConnection"];
export type GitHubConnectionCreate = S["GitHubConnectionCreate"];
export type GitHubConnectionUpdate = S["GitHubConnectionUpdate"];
export type GitHubImport = S["GitHubImport"];
export type GitHubImportCreate = S["GitHubImportCreate"];
export type GitHubSyncLink = S["GitHubSyncLink"];
export type GitHubSyncLinkPut = S["GitHubSyncLinkPut"];
export type GitHubSyncRef = S["GitHubSyncRef"];

const enc = encodeURIComponent;

function useInvalidating<V, R>(prefix: unknown[], fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: prefix }) });
}

/* Rules: branch protection and tag rules share the same CRUD shape. */

type RuleKind = "branch-protections" | "tag-rules";

export function useRules<T>(owner: string, name: string, kind: RuleKind) {
  return useQuery({ queryKey: ["rules", owner, name, kind], queryFn: () => request<T[]>("GET", `${repoBase(owner, name)}/${kind}`) });
}

export function useSaveRule<I>(owner: string, name: string, kind: RuleKind) {
  return useInvalidating<{ id?: string; body: I }, unknown>(["rules", owner, name, kind], ({ id, body }) =>
    id ? request("PUT", `${repoBase(owner, name)}/${kind}/${enc(id)}`, body) : request("POST", `${repoBase(owner, name)}/${kind}`, body),
  );
}

export function useDeleteRule(owner: string, name: string, kind: RuleKind) {
  return useInvalidating<string, void>(["rules", owner, name, kind], (id) => request("DELETE", `${repoBase(owner, name)}/${kind}/${enc(id)}`));
}

/* Webhooks: base is "/repos/{o}/{n}/hooks" or "/orgs/{org}/hooks". */

export const repoHooks = (owner: string, name: string) => `${repoBase(owner, name)}/hooks`;
export const orgHooks = (org: string) => `/orgs/${enc(org)}/hooks`;

/** Events a webhook can subscribe to, from the server ("*" means every event). */
export const useWebhookEvents = () =>
  useQuery({
    queryKey: ["hooks", "events"],
    queryFn: async () => (await request<S["WebhookEventCatalog"]>("GET", "/webhooks/events")).events,
    staleTime: Infinity,
  });

export const useHooks = (base: string) => useQuery({ queryKey: ["hooks", base], queryFn: () => request<Webhook[]>("GET", base) });

export const useSaveHook = (base: string) =>
  useInvalidating<{ id?: string; body: WebhookCreate | WebhookUpdate }, Webhook>(["hooks", base], ({ id, body }) =>
    id ? request("PATCH", `${base}/${enc(id)}`, body) : request("POST", base, body),
  );
export const useDeleteHook = (base: string) => useInvalidating<string, void>(["hooks", base], (id) => request("DELETE", `${base}/${enc(id)}`));
export const usePingHook = (base: string) => useInvalidating<string, unknown>(["hooks", base], (id) => request("POST", `${base}/${enc(id)}/pings`));

export const useDeliveries = (base: string, hookId: string, page: number) =>
  useQuery({
    queryKey: ["hooks", base, hookId, "deliveries", page],
    queryFn: () => pagedHeader<WebhookDelivery>(`${base}/${enc(hookId)}/deliveries`, page),
    placeholderData: keepPreviousData,
  });
export const useDelivery = (base: string, hookId: string, deliveryId: string | undefined) =>
  useQuery({
    queryKey: ["hooks", base, hookId, "delivery", deliveryId],
    queryFn: () => request<WebhookDeliveryDetail>("GET", `${base}/${enc(hookId)}/deliveries/${enc(deliveryId ?? "")}`),
    enabled: deliveryId !== undefined,
  });
export const useRedeliver = (base: string, hookId: string) =>
  useInvalidating<string, unknown>(["hooks", base], (deliveryId) => request("POST", `${base}/${enc(hookId)}/deliveries/${enc(deliveryId)}/attempts`));

/* GitHub connections, imports and sync. */

export const useConnections = (owner?: string) =>
  useQuery({ queryKey: ["github", "connections", owner], queryFn: () => request<GitHubConnection[]>("GET", `/github/connections${qs({ owner })}`) });
export const useSaveConnection = () =>
  useInvalidating<{ id?: string; body: GitHubConnectionCreate | GitHubConnectionUpdate }, GitHubConnection>(["github", "connections"], ({ id, body }) =>
    id ? request("PATCH", `/github/connections/${enc(id)}`, body) : request("POST", "/github/connections", body),
  );
export const useDeleteConnection = () => useInvalidating<string, void>(["github", "connections"], (id) => request("DELETE", `/github/connections/${enc(id)}`));
export const useCheckConnection = () =>
  useMutation({ mutationFn: (id: string) => request<S["GitHubConnectionCheck"]>("POST", `/github/connections/${enc(id)}/check`) });

export const useStartImport = () => useInvalidating<GitHubImportCreate, GitHubImport>(["github"], (b) => request("POST", "/github/imports", b));
export const useMyImports = (page: number) =>
  useQuery({
    queryKey: ["github", "imports", page],
    queryFn: () => pagedHeader<GitHubImport>("/github/imports", page),
    placeholderData: keepPreviousData,
    refetchInterval: (q) => (q.state.data?.items.some((i) => i.status === "pending" || i.status === "running") ? 5000 : false),
  });
export const useRepoImport = (owner: string, name: string) =>
  useQuery({
    queryKey: ["github", "import", owner, name],
    queryFn: () => request<GitHubImport>("GET", `${repoBase(owner, name)}/github/import`),
    retry: false,
    refetchInterval: (q) => (q.state.data && (q.state.data.status === "pending" || q.state.data.status === "running") ? 5000 : false),
  });

const syncBase = (owner: string, name: string) => `${repoBase(owner, name)}/github/sync`;
export const useSyncLink = (owner: string, name: string) =>
  useQuery({ queryKey: ["github", "sync", owner, name], queryFn: () => request<GitHubSyncLink>("GET", syncBase(owner, name)), retry: false });
export const useSyncRefs = (owner: string, name: string, enabled: boolean) =>
  useQuery({ queryKey: ["github", "sync", owner, name, "refs"], queryFn: () => request<GitHubSyncRef[]>("GET", `${syncBase(owner, name)}/refs`), enabled });
export const usePutSync = (owner: string, name: string) =>
  useInvalidating<GitHubSyncLinkPut | null, unknown>(["github", "sync", owner, name], (b) =>
    b ? request("PUT", syncBase(owner, name), b) : request("DELETE", syncBase(owner, name)),
  );
export const useRunSync = (owner: string, name: string) =>
  useInvalidating<void, unknown>(["github", "sync", owner, name], () => request("POST", `${syncBase(owner, name)}/run`));
export const useResolveSync = (owner: string, name: string) =>
  useInvalidating<S["GitHubSyncResolve"], unknown>(["github", "sync", owner, name], (b) => request("POST", `${syncBase(owner, name)}/resolve`, b));
