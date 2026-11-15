import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import { api } from "./client.ts";
import type { components } from "./schema";

export type StorageStatus = components["schemas"]["StorageStatus"];
export type StorageCheckResult = components["schemas"]["StorageCheckResult"];
export type RepoTiering = components["schemas"]["RepoTiering"];

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
export function useStorageDraftTest() {
  return useMutation({
    mutationFn: (input: StorageTargetInput) => request<StorageCheckResult>("POST", "/admin/storage/test", input),
  });
}

/** Pins, unpins or evicts one repository's hot copy (PUT/DELETE /admin/repos/{owner}/{name}/pin, POST .../evict). */
export function useTiering() {
  return useMutation({
    mutationFn: ({ owner, name, op }: { owner: string; name: string; op: "pin" | "unpin" | "evict" }): Promise<RepoTiering> => {
      const path = `/admin/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`;
      if (op === "evict") return request<RepoTiering>("POST", `${path}/evict`);
      return request<RepoTiering>(op === "pin" ? "PUT" : "DELETE", `${path}/pin`);
    },
  });
}
