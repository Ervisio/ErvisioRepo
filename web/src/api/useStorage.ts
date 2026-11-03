import { useMutation, useQuery } from "@tanstack/react-query";
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
