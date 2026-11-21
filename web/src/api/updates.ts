import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

// Server updates: the GitHub release check and the Docker updater.
type S = components["schemas"];
export type UpdateStatus = S["UpdateStatus"];
export type AvailableRelease = S["AvailableRelease"];

const key = ["admin", "updates"] as const;

/**
 * The update state. While an update runs it is polled every 3 s: the server
 * restarts in the middle, so failed polls are expected and retried.
 */
export const useUpdateStatus = (live = false) =>
  useQuery({
    queryKey: key,
    queryFn: () => request<UpdateStatus>("GET", "/admin/updates"),
    refetchInterval: (q) => {
      const u = q.state.data?.updater;
      const busy = u && (u.state === "running" || u.pending !== "");
      return live && (busy || q.state.status === "error") ? 3_000 : 5 * 60_000;
    },
    retry: live ? true : 1,
  });

function useUpdateMutation<B>(method: "POST" | "PUT", path: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: B) => request<UpdateStatus>(method, path, b),
    onSuccess: (st) => qc.setQueryData(key, st),
  });
}

export const useCheckUpdates = () => useUpdateMutation<undefined>("POST", "/admin/updates/check");
export const useSetAutoUpdate = () => useUpdateMutation<{ enabled: boolean }>("PUT", "/admin/updates/auto");
export const useApplyUpdate = () => useUpdateMutation<{ version: string }>("POST", "/admin/updates/apply");
