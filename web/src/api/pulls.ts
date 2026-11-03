import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PAGE_SIZE, qs, repoBase, type Diff } from "./code.ts";
import { request } from "./http.ts";
import { paged } from "./issues.ts";
import type { components } from "./schema";

// Pull requests: list, detail, diff, merge, reviews and review comments.
type S = components["schemas"];
export type PullRequest = S["PullRequest"];
export type PullRequestCreate = S["PullRequestCreate"];
export type PullRequestUpdate = S["PullRequestUpdate"];
export type PullFile = S["PullFile"];
export type PullMergeability = S["PullMergeability"];
export type PullMergeRequest = S["PullMergeRequest"];
export type PullAutoMergeRequest = S["PullAutoMergeRequest"];
export type PullReview = S["PullReview"];
export type PullReviewCreate = S["PullReviewCreate"];
export type PullReviewComment = S["PullReviewComment"];
export type PullReviewCommentCreate = S["PullReviewCommentCreate"];
export type PullCommitPage = S["PullCommitPage"];
export type MergeMethod = NonNullable<PullMergeRequest["method"]>;
export type PullUpdateBranchRequest = S["PullUpdateBranchRequest"];
export type LinkedPull = S["LinkedPull"];
export type CombinedStatus = S["CombinedStatus"];
export type CheckRun = S["CheckRun"];
export type CommitStatus = S["CommitStatus"];

const enc = encodeURIComponent;
const base = (owner: string, name: string) => `${repoBase(owner, name)}/pulls`;
const key = (owner: string, name: string, ...rest: unknown[]) => ["pulls", owner, name, ...rest];

export type PullFilter = {
  state: "open" | "closed" | "merged" | "all";
  q?: string;
  author?: string;
  reviewRequested?: string;
  base?: string;
  sort?: "created" | "updated";
  page: number;
};

export const usePulls = (owner: string, name: string, f: PullFilter) =>
  useQuery({
    queryKey: key(owner, name, "list", f),
    queryFn: () => paged<PullRequest>(base(owner, name), f.page, {
        state: f.state,
        q: f.q,
        author: f.author,
        reviewRequested: f.reviewRequested,
        base: f.base,
        sort: f.sort,
      }),
    placeholderData: keepPreviousData,
  });

export const usePull = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: key(owner, name, "one", number), queryFn: () => request<PullRequest>("GET", `${base(owner, name)}/${number}`) });

export const usePullCommits = (owner: string, name: string, number: number, page: number) =>
  useQuery({
    queryKey: key(owner, name, "commits", number, page),
    queryFn: () => request<PullCommitPage>("GET", `${base(owner, name)}/${number}/commits${qs({ offset: (page - 1) * PAGE_SIZE, limit: PAGE_SIZE })}`),
    placeholderData: keepPreviousData,
  });

export const usePullFiles = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: key(owner, name, "files", number), queryFn: () => request<PullFile[]>("GET", `${base(owner, name)}/${number}/files`) });

export const usePullDiff = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: key(owner, name, "diff", number), queryFn: () => request<Diff>("GET", `${base(owner, name)}/${number}/diff`) });

export const useMergeability = (owner: string, name: string, number: number, enabled = true) =>
  useQuery({
    queryKey: key(owner, name, "mergeability", number),
    queryFn: () => request<PullMergeability>("GET", `${base(owner, name)}/${number}/mergeability`),
    enabled,
  });

/** Combined commit status and latest check runs of the head commit. */
export const useHeadStatus = (owner: string, name: string, sha: string, enabled = true) =>
  useQuery({
    queryKey: key(owner, name, "status", sha),
    queryFn: () => request<CombinedStatus>("GET", `${repoBase(owner, name)}/commits/${enc(sha)}/status`),
    enabled: enabled && sha !== "",
  });
export const useHeadChecks = (owner: string, name: string, sha: string, enabled = true) =>
  useQuery({
    queryKey: key(owner, name, "checks", sha),
    queryFn: () => request<S["CheckRunList"]>("GET", `${repoBase(owner, name)}/commits/${enc(sha)}/check-runs${qs({ filter: "latest", limit: 100 })}`),
    enabled: enabled && sha !== "",
  });

/** Pull requests whose body or comments reference an issue. */
export const useLinkedPulls = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: ["issues", owner, name, "linked-pulls", number], queryFn: () => request<LinkedPull[]>("GET", `${repoBase(owner, name)}/issues/${number}/linked-pulls`) });

export const useReviews = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: key(owner, name, "reviews", number), queryFn: () => request<PullReview[]>("GET", `${base(owner, name)}/${number}/reviews`) });

export const useReviewComments = (owner: string, name: string, number: number) =>
  useQuery({
    queryKey: key(owner, name, "comments", number),
    queryFn: () => request<PullReviewComment[]>("GET", `${base(owner, name)}/${number}/comments${qs({ limit: 250 })}`),
  });

function usePullMutation<V, R>(owner: string, name: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["pulls", owner, name] });
      await qc.invalidateQueries({ queryKey: ["issues", owner, name] });
    },
  });
}

export const useCreatePull = (owner: string, name: string) =>
  usePullMutation<PullRequestCreate, PullRequest>(owner, name, (b) => request("POST", base(owner, name), b));
export const useUpdatePull = (owner: string, name: string, number: number) =>
  usePullMutation<PullRequestUpdate, PullRequest>(owner, name, (b) => request("PATCH", `${base(owner, name)}/${number}`, b));
export const useMerge = (owner: string, name: string, number: number) =>
  usePullMutation<PullMergeRequest, S["PullMergeResult"]>(owner, name, (b) => request("PUT", `${base(owner, name)}/${number}/merge`, b));
export const useUpdateBranch = (owner: string, name: string, number: number) =>
  usePullMutation<PullUpdateBranchRequest, S["PullMergeResult"]>(owner, name, (b) => request("PUT", `${base(owner, name)}/${number}/update-branch`, b));
export const useAutoMerge = (owner: string, name: string, number: number) =>
  usePullMutation<PullAutoMergeRequest | null, PullRequest | void>(owner, name, (b) =>
    b ? request("PUT", `${base(owner, name)}/${number}/auto-merge`, b) : request("DELETE", `${base(owner, name)}/${number}/auto-merge`),
  );
export const useRequestReviewers = (owner: string, name: string, number: number) =>
  usePullMutation<{ add: boolean; reviewers: string[] }, unknown>(owner, name, ({ add, reviewers }) =>
    add
      ? request("POST", `${base(owner, name)}/${number}/requested-reviewers`, { reviewers })
      : request("DELETE", `${base(owner, name)}/${number}/requested-reviewers${qs({ reviewers: reviewers.join(",") })}`),
  );

export const useCreateReview = (owner: string, name: string, number: number) =>
  usePullMutation<PullReviewCreate, PullReview>(owner, name, (b) => request("POST", `${base(owner, name)}/${number}/reviews`, b));
export const useSubmitReview = (owner: string, name: string, number: number) =>
  usePullMutation<{ id: string } & S["PullReviewSubmit"], PullReview>(owner, name, ({ id, ...b }) =>
    request("POST", `${base(owner, name)}/${number}/reviews/${enc(id)}/submit`, b),
  );
export const useDeleteReview = (owner: string, name: string, number: number) =>
  usePullMutation<string, void>(owner, name, (id) => request("DELETE", `${base(owner, name)}/${number}/reviews/${enc(id)}`));
export const useDismissReview = (owner: string, name: string, number: number) =>
  usePullMutation<{ id: string; message: string }, PullReview>(owner, name, ({ id, message }) =>
    request("PUT", `${base(owner, name)}/${number}/reviews/${enc(id)}/dismiss`, { message }),
  );

export const useAddReviewComment = (owner: string, name: string, number: number) =>
  usePullMutation<PullReviewCommentCreate, PullReviewComment>(owner, name, (b) => request("POST", `${base(owner, name)}/${number}/comments`, b));
export const useEditReviewComment = (owner: string, name: string) =>
  usePullMutation<{ id: string; body: string }, PullReviewComment>(owner, name, ({ id, body }) =>
    request("PATCH", `${base(owner, name)}/comments/${enc(id)}`, { body }),
  );
export const useDeleteReviewComment = (owner: string, name: string) =>
  usePullMutation<string, void>(owner, name, (id) => request("DELETE", `${base(owner, name)}/comments/${enc(id)}`));
export const useResolveThread = (owner: string, name: string) =>
  usePullMutation<{ id: string; resolved: boolean }, unknown>(owner, name, ({ id, resolved }) =>
    request(resolved ? "PUT" : "DELETE", `${base(owner, name)}/comments/${enc(id)}/resolve`),
  );
export const useApplySuggestion = (owner: string, name: string) =>
  usePullMutation<{ id: string; message?: string }, { sha: string }>(owner, name, ({ id, message }) =>
    request("POST", `${base(owner, name)}/comments/${enc(id)}/apply-suggestion`, { message }),
  );

/** Extracts the first ```suggestion block of a review comment body, if any. */
export function suggestionOf(body: string): string | undefined {
  const m = /```suggestion[^\n]*\n([\s\S]*?)```/.exec(body);
  return m ? (m[1] ?? "").replace(/\n$/, "") : undefined;
}
