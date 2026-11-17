import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { qs } from "./code.ts";
import { request } from "./http.ts";
import type { ActivityItem } from "./personal.ts";
import type { Repo } from "./repos.ts";
import type { components } from "./schema";

// Public profiles, organization languages and repository activity sparklines.
type S = components["schemas"];
export type ProfileDetails = S["ProfileDetails"];
export type ContributionCalendar = S["ContributionCalendar"];
export type OrgLanguages = S["OrgLanguages"];
export type RepoWeeklyActivity = S["RepoWeeklyActivity"];
export type StarredRepo = S["StarredRepo"];

const enc = encodeURIComponent;

export const useProfileDetails = () =>
  useQuery({ queryKey: ["user", "profile-details"], queryFn: () => request<ProfileDetails>("GET", "/user/profile/details") });

export function useUpdateProfileDetails() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: ProfileDetails) => request<ProfileDetails>("PUT", "/user/profile/details", b),
    onSuccess: (d) => {
      qc.setQueryData(["user", "profile-details"], d);
      void qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export const useProfilePins = (username: string) =>
  useQuery({ queryKey: ["users", username, "pins"], queryFn: () => request<Repo[]>("GET", `/users/${enc(username)}/pins`) });

export const useProfileActivity = (username: string, limit = 10) =>
  useQuery({
    queryKey: ["users", username, "activity", limit],
    placeholderData: keepPreviousData,
    queryFn: () => request<ActivityItem[]>("GET", `/users/${enc(username)}/activity${qs({ limit })}`),
  });

export const useContributions = (username: string, year?: number) =>
  useQuery({
    queryKey: ["users", username, "contributions", year ?? 0],
    queryFn: () => request<ContributionCalendar>("GET", `/users/${enc(username)}/contributions${qs({ year })}`),
  });

export const useUserStarred = (username: string, enabled = true) =>
  useQuery({
    queryKey: ["repos", "starred", username],
    enabled: enabled && username !== "",
    queryFn: () => request<StarredRepo[]>("GET", `/users/${enc(username)}/starred?limit=100`),
  });

export const useOrgLanguages = (org: string) =>
  useQuery({ queryKey: ["orgs", org, "languages"], staleTime: 5 * 60_000, queryFn: () => request<OrgLanguages>("GET", `/orgs/${enc(org)}/languages`) });

/** Weekly event counts of the given repositories (the last 16 weeks, oldest first), keyed by repository ID. */
export function useWeeklyActivity(ids: string[]) {
  const key = [...ids].sort();
  return useQuery({
    queryKey: ["explore", "activity", key],
    enabled: key.length > 0,
    staleTime: 5 * 60_000,
    queryFn: async () => {
      const list = await request<RepoWeeklyActivity[]>("GET", `/explore/activity?${key.slice(0, 100).map((id) => `repo=${enc(id)}`).join("&")}`);
      return new Map(list.map((a) => [a.repoId, a.weeks]));
    },
  });
}
