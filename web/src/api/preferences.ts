import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

export type UserPreferences = components["schemas"]["UserPreferences"];
export type UserPreferencesUpdate = components["schemas"]["UserPreferencesUpdate"];

/** UI preferences of the signed-in user; pass enabled=false for anonymous visitors. */
export const useUserPreferences = (enabled = true) =>
  useQuery({ queryKey: ["user", "preferences"], queryFn: () => request<UserPreferences>("GET", "/user/preferences"), enabled, retry: false });

export function useSavePreferences() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: UserPreferencesUpdate) => request<UserPreferences>("PATCH", "/user/preferences", b),
    onSuccess: (p) => qc.setQueryData(["user", "preferences"], p),
  });
}
