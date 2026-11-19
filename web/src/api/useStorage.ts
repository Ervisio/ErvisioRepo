import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import { api } from "./client.ts";
import type { components } from "./schema";

export type StorageStatus = components["schemas"]["StorageStatus"];
export type StorageCheckResult = components["schemas"]["StorageCheckResult"];

/** Storage overview for the admin page (GET /admin/storage, loopback only). */
export function useStorageStatus() {
  return useQuery({
    queryKey: ["admin", "storage"],
    queryFn: async (): Promise<StorageStatus> => {
      const { data, error, response } = await api.GET("/admin/storage");
      if (data) return data;
      throw new Error(error?.title ?? `HTTP ${response.status}`);
    },
  });
}

/** Runs the connection test on one target (POST /admin/storage/{target}/test). */
export function useStorageTest() {
  return useMutation({
    mutationFn: async (target: string): Promise<StorageCheckResult> => {
      const { data, error, response } = await api.POST("/admin/storage/{target}/test", {
        params: { path: { target } },
      });
      if (data) return data;
      throw new Error(error?.detail ?? error?.title ?? `HTTP ${response.status}`);
    },
  });
}

export type StorageConfig = components["schemas"]["StorageConfig"];
export type StorageTargetConfig = components["schemas"]["StorageTargetConfig"];
export type StoragePolicyConfig = components["schemas"]["StoragePolicyConfig"];
export type StorageTargetInput = components["schemas"]["StorageTargetInput"];
export type StoragePolicyInput = components["schemas"]["StoragePolicyInput"];

const STORAGE_KEY = ["admin", "storage"] as const;
const HOT_REPOS_KEY = [...STORAGE_KEY, "hot-repos"] as const;

/** Targets and policies as the next start will run with them (GET /admin/storage/config). */
export function useStorageConfig() {
  return useQuery({
    queryKey: [...STORAGE_KEY, "config"],
    queryFn: () => request<StorageConfig>("GET", "/admin/storage/config"),
  });
}

/** Creates or updates an S3 or SFTP target (PUT /admin/storage/targets/{name}). Throws ApiError with field errors on 422. */
export function usePutStorageTarget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, input }: { name: string; input: StorageTargetInput }) =>
      request<StorageConfig>("PUT", `/admin/storage/targets/${encodeURIComponent(name)}`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: STORAGE_KEY }),
  });
}

/** Deletes a target created in the web admin (DELETE /admin/storage/targets/{name}). */
export function useDeleteStorageTarget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => request<StorageConfig>("DELETE", `/admin/storage/targets/${encodeURIComponent(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: STORAGE_KEY }),
  });
}

/** Changes primary, replicas and cache of a data type (PUT /admin/storage/policies/{policy}). */
export function usePutStoragePolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ policy, input }: { policy: string; input: StoragePolicyInput }) =>
      request<StorageConfig>("PUT", `/admin/storage/policies/${encodeURIComponent(policy)}`, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: STORAGE_KEY }),
  });
}

/** Connection test of a target definition that is not saved (POST /admin/storage/test). */
export type StorageHostKeys = components["schemas"]["StorageHostKeys"];

/** Reads the SSH host keys an SFTP server offers, as known_hosts lines (POST /admin/storage/host-keys). */
export function useStorageHostKeys() {
  return useMutation({
    mutationFn: (to: { host: string; port?: number }) => request<StorageHostKeys>("POST", "/admin/storage/host-keys", to),
  });
}

export function useStorageDraftTest() {
  return useMutation({
    mutationFn: (input: StorageTargetInput) => request<StorageCheckResult>("POST", "/admin/storage/test", input),
  });
}

export type AvatarStorage = components["schemas"]["AvatarStorage"];
export type AvatarStorageInput = components["schemas"]["AvatarStorageInput"];

/** Where uploaded avatars go and the optional CDN base URL (GET /admin/storage/avatars). */
export function useAvatarStorage() {
  return useQuery({
    queryKey: [...STORAGE_KEY, "avatars"],
    queryFn: () => request<AvatarStorage>("GET", "/admin/storage/avatars"),
  });
}

/** Saves the avatar storage settings (PUT /admin/storage/avatars); applies at once. */
export function usePutAvatarStorage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AvatarStorageInput) => request<AvatarStorage>("PUT", "/admin/storage/avatars", input),
    onSuccess: (v) => qc.setQueryData([...STORAGE_KEY, "avatars"], v),
  });
}

export type StorageHotCache = components["schemas"]["StorageHotCache"];
export type StorageHotCacheInput = components["schemas"]["StorageHotCacheInput"];
export type HotRepo = components["schemas"]["HotRepo"];
export type HotRepoPage = components["schemas"]["HotRepoPage"];

/** Saves the automatic hot cache setting (PUT /admin/storage/hot-cache); applies after a restart. */
export function usePutHotCache() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: StorageHotCacheInput) => request<StorageConfig>("PUT", "/admin/storage/hot-cache", input),
    onSuccess: () => qc.invalidateQueries({ queryKey: STORAGE_KEY }),
  });
}

/** A page of the repositories on the local disk, most recently used first, with the hot tier usage (GET /admin/storage/hot-repos). all also lists remote-only ones. */
export function useHotRepos(q: string, all: boolean, offset: number, limit: number) {
  return useQuery({
    queryKey: [...HOT_REPOS_KEY, q, all, offset, limit],
    placeholderData: (prev) => prev,
    queryFn: () => {
      const params = new URLSearchParams({ offset: String(offset), limit: String(limit) });
      if (q) params.set("q", q);
      if (all) params.set("all", "true");
      return request<HotRepoPage>("GET", `/admin/storage/hot-repos?${params.toString()}`);
    },
  });
}
