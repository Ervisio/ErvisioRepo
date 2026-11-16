import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

// Repositories. Types come from the generated schema (api/openapi.yaml).
type S = components["schemas"];
export type Repo = S["Repo"];
export type RepoCreate = S["RepoCreate"];
export type RepoUpdate = S["RepoUpdate"];
export type RepoTransfer = S["RepoTransfer"];
export type Visibility = S["Visibility"];

const enc = encodeURIComponent;
const repoPath = (owner: string, name: string) => `/repos/${enc(owner)}/${enc(name)}`;

/** Repositories of the signed-in user. */
export const useMyRepos = () => useQuery({ queryKey: ["repos", "mine"], queryFn: () => request<Repo[]>("GET", "/user/repos?limit=100") });

/** Repositories owned by a user (kind "user") or an organization (kind "org"). */
export const useOwnerRepos = (owner: string, kind: "user" | "org") =>
  useQuery({
    queryKey: ["repos", kind, owner],
    queryFn: () => request<Repo[]>("GET", `/${kind === "org" ? "orgs" : "users"}/${enc(owner)}/repos?limit=100`),
  });

export const useRepo = (owner: string, name: string) =>
  useQuery({ queryKey: ["repos", "one", owner, name], retry: false, queryFn: () => request<Repo>("GET", repoPath(owner, name)) });

function useRepoMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["repos"] }) });
}

/** Creates a repository under the user (no org) or under an organization. */
export const useCreateRepo = (org?: string) =>
  useRepoMutation<RepoCreate, Repo>((b) => request("POST", org ? `/orgs/${enc(org)}/repos` : "/user/repos", b));
export const useUpdateRepo = (owner: string, name: string) =>
  useRepoMutation<RepoUpdate, Repo>((b) => request("PATCH", repoPath(owner, name), b));
export const useTransferRepo = (owner: string, name: string) =>
  useRepoMutation<RepoTransfer, Repo>((b) => request("POST", `${repoPath(owner, name)}/transfer`, b));
export const useForkRepo = (owner: string, name: string) =>
  useRepoMutation<S["ForkCreate"], Repo>((b) => request("POST", `${repoPath(owner, name)}/forks`, b));
export const useDeleteRepo = (owner: string, name: string) =>
  useRepoMutation<void, void>(() => request("DELETE", repoPath(owner, name)));

/** Stars or unstars a repository; the cached repository shows the change before the server answers. */
export function useStar(owner: string, name: string) {
  const qc = useQueryClient();
  const key = ["repos", "one", owner, name];
  return useMutation({
    mutationFn: (star: boolean) => request<void>(star ? "PUT" : "DELETE", `${repoPath(owner, name)}/star`),
    onMutate: async (star: boolean) => {
      await qc.cancelQueries({ queryKey: key });
      const prev = qc.getQueryData<Repo>(key);
      if (prev && prev.starred !== star) qc.setQueryData<Repo>(key, { ...prev, starred: star, starsCount: Math.max(0, prev.starsCount + (star ? 1 : -1)) });
      return { prev };
    },
    onError: (_e, _star, ctx) => {
      if (ctx?.prev) qc.setQueryData(key, ctx.prev);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["repos"] }),
  });
}
