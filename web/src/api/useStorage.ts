import { useMutation, useQuery } from "@tanstack/react-query";
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
