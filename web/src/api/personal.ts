import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { qs } from "./code.ts";
import { request } from "./http.ts";
import type { Repo } from "./repos.ts";
import type { components } from "./schema";

// Personal dashboard endpoints: my issues, failed runs, activity feed, pins and public exploration.
type S = components["schemas"];
export type UserIssue = S["UserIssue"];
export type UserIssueList = S["UserIssueList"];
export type UserActionsRun = S["UserActionsRun"];
export type UserActionsRunList = S["UserActionsRunList"];
export type ActivityItem = S["ActivityItem"];
export type ExploreRepoList = S["ExploreRepoList"];

export type UserIssueFilter = "assigned" | "created" | "mentioned" | "review_requested";

const enc = encodeURIComponent;

export const useUserIssues = (p: { filter: UserIssueFilter; type?: "issue" | "pr" | "all"; state?: "open" | "closed" | "all"; page?: number; perPage?: number }) =>
  useQuery({
    queryKey: ["user", "issues", p],
    queryFn: () => request<UserIssueList>("GET", `/user/issues${qs(p)}`),
    placeholderData: keepPreviousData,
  });

export const useUserRuns = (p: { status?: string; page?: number; perPage?: number }) =>
  useQuery({
    queryKey: ["user", "runs", p],
    queryFn: () => request<UserActionsRunList>("GET", `/user/actions/runs${qs(p)}`),
  });

export const useUserActivity = (p: { offset?: number; limit?: number }) =>
  useQuery({
    queryKey: ["user", "activity", p],
    queryFn: () => request<ActivityItem[]>("GET", `/user/activity${qs(p)}`),
  });

export const PIN_LIMIT = 12;

export const useUserPins = (enabled = true) =>
  useQuery({ queryKey: ["user", "pins"], queryFn: () => request<Repo[]>("GET", "/user/pins"), enabled, retry: false });

/** Pins or unpins a repository; callers handle the 409 raised beyond PIN_LIMIT pins. */
export function useSetPin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ owner, name, pinned }: { owner: string; name: string; pinned: boolean }) =>
      request<void>(pinned ? "PUT" : "DELETE", `/user/pins/${enc(owner)}/${enc(name)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["user", "pins"] }),
  });
}

export type ExploreParams = { sort?: "updated" | "name"; q?: string; page?: number; perPage?: number };

export const useExploreRepos = (p: ExploreParams) =>
  useQuery({
    queryKey: ["explore", p],
    queryFn: () => request<ExploreRepoList>("GET", `/explore/repos${qs(p)}`),
    placeholderData: keepPreviousData,
  });
