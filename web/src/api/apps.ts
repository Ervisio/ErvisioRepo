import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

// Apps (GitHub App style): registration, keys, installations and the permission catalog.
type S = components["schemas"];
export type App = S["App"];
export type AppCreate = S["AppCreate"];
export type AppUpdate = S["AppUpdate"];
export type AppPermissions = S["AppPermissions"];
export type AppCatalog = S["AppCatalog"];
export type AppCatalogEvent = S["AppCatalogEvent"];
export type AppKey = S["AppKey"];
export type CreatedAppKey = S["CreatedAppKey"];
export type AppInstallation = S["AppInstallation"];
export type AppInstall = S["AppInstall"];
export type AppInstallResult = S["AppInstallResult"];
export type AppInstallTarget = S["AppInstallTarget"];
export type AppInstallationUpdate = S["AppInstallationUpdate"];
export type RepositorySelection = AppInstall["repositorySelection"];

/** Permission level in the matrix; "none" is the absence of the key. */
export type PermissionLevel = "none" | "read" | "write";

/** Who owns a list of Apps or installations: the signed-in account or an organization. */
export type AppsOwner = { kind: "user" } | { kind: "org"; org: string };

/** The server allows at most this many private keys per App. */
export const MAX_APP_KEYS = 10;

const enc = encodeURIComponent;
const ownerKey = (o: AppsOwner) => (o.kind === "user" ? "user" : `org:${o.org}`);
const appsPath = (o: AppsOwner) => (o.kind === "user" ? "/user/apps" : `/orgs/${enc(o.org)}/apps`);
const installsPath = (o: AppsOwner) => (o.kind === "user" ? "/user/app-installations" : `/orgs/${enc(o.org)}/app-installations`);
/** Path of one App; its deliveries hang from it. */
export const appPath = (slug: string) => `/apps/${enc(slug)}`;
const installationPath = (id: number) => `/app-installations/${id}`;

export const useAppCatalog = () =>
  useQuery({ queryKey: ["app-catalog"], queryFn: () => request<AppCatalog>("GET", "/app-catalog"), staleTime: Infinity });

export const useApps = (owner: AppsOwner) =>
  useQuery({ queryKey: ["apps", ownerKey(owner)], queryFn: () => request<App[]>("GET", appsPath(owner)) });

export function useCreateApp(owner: AppsOwner) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AppCreate) => request<App>("POST", appsPath(owner), body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["apps"] }),
  });
}

export const useApp = (slug: string) =>
  useQuery({ queryKey: ["app", slug], queryFn: () => request<App>("GET", appPath(slug)), retry: false });

/** Invalidates the App and every list after a change. */
function useAppMutation<V, R>(slug: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => Promise.all([qc.invalidateQueries({ queryKey: ["app", slug] }), qc.invalidateQueries({ queryKey: ["apps"] })]),
  });
}

export const useUpdateApp = (slug: string) => useAppMutation<AppUpdate, App>(slug, (b) => request("PATCH", appPath(slug), b));

export function useDeleteApp(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => request<void>("DELETE", appPath(slug)),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["app", slug] });
      return Promise.all([qc.invalidateQueries({ queryKey: ["apps"] }), qc.invalidateQueries({ queryKey: ["app-installations"] })]);
    },
  });
}

export const useCreateAppKey = (slug: string) => useAppMutation<void, CreatedAppKey>(slug, () => request("POST", `${appPath(slug)}/keys`));
export const useDeleteAppKey = (slug: string) => useAppMutation<string, void>(slug, (id) => request("DELETE", `${appPath(slug)}/keys/${enc(id)}`));

/** Client secrets of the App's OAuth client go through the OAuth app endpoints. */
export const useCreateAppSecret = (slug: string, clientId: string) =>
  useAppMutation<void, S["CreatedOAuthAppSecret"]>(slug, () => request("POST", `/oauth-apps/${enc(clientId)}/secrets`));
export const useDeleteAppSecret = (slug: string, clientId: string) =>
  useAppMutation<string, void>(slug, (id) => request("DELETE", `/oauth-apps/${enc(clientId)}/secrets/${enc(id)}`));

/** Where an App is installed (its managers only). */
export const useAppInstallationsOf = (slug: string) =>
  useQuery({ queryKey: ["app", slug, "installations"], queryFn: () => request<AppInstallation[]>("GET", `${appPath(slug)}/installations`) });

export const useInstallTargets = (slug: string) =>
  useQuery({ queryKey: ["app", slug, "install-targets"], queryFn: () => request<AppInstallTarget[]>("GET", `${appPath(slug)}/install-targets`) });

export function useInstallApp(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AppInstall) => request<AppInstallResult>("POST", `${appPath(slug)}/installations`, body),
    onSuccess: () => Promise.all([qc.invalidateQueries({ queryKey: ["app", slug] }), qc.invalidateQueries({ queryKey: ["app-installations"] })]),
  });
}

/** Apps installed on the account or organization. */
export const useInstallations = (owner: AppsOwner) =>
  useQuery({ queryKey: ["app-installations", ownerKey(owner)], queryFn: () => request<AppInstallation[]>("GET", installsPath(owner)) });

export const useInstallation = (id: number) =>
  useQuery({ queryKey: ["app-installations", "one", id], queryFn: () => request<AppInstallation>("GET", installationPath(id)), retry: false });

function useInstallationMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["app-installations"] }) });
}

export const useUpdateInstallation = (id: number) =>
  useInstallationMutation<AppInstallationUpdate, AppInstallation>((b) => request("PATCH", installationPath(id), b));
export const useSuspendInstallation = (id: number) =>
  useInstallationMutation<boolean, void>((suspend) => request(suspend ? "PUT" : "DELETE", `${installationPath(id)}/suspended`));
export const useAcceptPermissions = (id: number) =>
  useInstallationMutation<void, unknown>(() => request("PUT", `${installationPath(id)}/permissions`));

export function useUninstall(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => request<void>("DELETE", installationPath(id)),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["app-installations", "one", id] });
      return qc.invalidateQueries({ queryKey: ["app-installations"] });
    },
  });
}

/** Level of a permission in a set; metadata is always readable. */
export function levelOf(perms: AppPermissions | undefined, name: string): PermissionLevel {
  const v = perms?.[name];
  if (v === "read" || v === "write") return v;
  return name === "metadata" ? "read" : "none";
}

/** Whether the permissions allow an event: its permission (or the alternative) at least readable. */
export function eventAllowed(perms: AppPermissions, ev: AppCatalogEvent): boolean {
  if (!ev.permission) return true;
  return levelOf(perms, ev.permission) !== "none" || (!!ev.alternative && levelOf(perms, ev.alternative) !== "none");
}

/** Slug the server derives from a name when none is given (lowercase, dashes, at most 34 characters). */
export const slugify = (name: string) =>
  name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 34).replace(/-+$/, "");
