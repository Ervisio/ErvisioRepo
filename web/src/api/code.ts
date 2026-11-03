import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, request } from "./http.ts";
import type { Repo } from "./repos.ts";
import type { components } from "./schema";

// Code browsing (tree, blob, blame, history, refs, compare, search, web edit).
type S = components["schemas"];
export type ResolvedRef = S["ResolvedRef"];
export type Tree = S["Tree"];
export type TreeEntry = S["TreeEntry"];
export type BlobFile = S["BlobFile"];
export type Blame = S["Blame"];
export type GitCommit = S["GitCommit"];
export type CommitPage = S["CommitPage"];
export type CommitDetail = S["CommitDetail"];
export type Diff = S["Diff"];
export type FileDiff = S["FileDiff"];
export type DiffLine = S["DiffLine"];
export type Branch = S["Branch"];
export type BranchPage = S["BranchPage"];
export type GitTag = S["GitTag"];
export type TagPage = S["TagPage"];
export type Comparison = S["Comparison"];
export type CodeSearchResult = S["CodeSearchResult"];
export type FileEdit = S["FileEdit"];
export type FileEditResult = S["FileEditResult"];

export const PAGE_SIZE = 30;

const enc = encodeURIComponent;
export const repoBase = (owner: string, name: string) => `/repos/${enc(owner)}/${enc(name)}`;

/** Builds a query string, leaving out empty values. */
export function qs(params: Record<string, string | number | boolean | undefined>): string {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "" || v === false) continue;
    u.set(k, String(v));
  }
  const s = u.toString();
  return s ? `?${s}` : "";
}

/** URL of the raw file (streamed, not in the OpenAPI document). */
export const rawUrl = (owner: string, name: string, ref: string, path: string) =>
  `/api/v1${repoBase(owner, name)}/raw${qs({ ref, path })}`;

/** URL of a source archive of a ref. */
export const archiveUrl = (owner: string, name: string, ref: string, format: "zip" | "tar.gz") =>
  `/api/v1${repoBase(owner, name)}/archive${qs({ ref, format })}`;

// A cold repository is hydrated on demand: the API answers 503 with Retry-After
// until it is ready. Retry those (and only those) a few times, honouring the header.
const hydrationRetry = {
  retry: (count: number, err: Error) => err instanceof ApiError && err.status === 503 && count < 6,
  retryDelay: (_count: number, err: Error) =>
    Math.min(err instanceof ApiError && err.retryAfter ? err.retryAfter : 5, 30) * 1000,
};

/** True while a query is waiting for the repository to become available. */
export const isHydrating = (err: Error | null | undefined) => err instanceof ApiError && err.status === 503;

const key = (owner: string, name: string, ...rest: unknown[]) => ["code", owner, name, ...rest];

function useCode<T>(owner: string, name: string, parts: unknown[], path: string, enabled = true) {
  return useQuery({
    queryKey: key(owner, name, ...parts),
    queryFn: () => request<T>("GET", `${repoBase(owner, name)}${path}`),
    enabled,
    ...hydrationRetry,
  });
}

export const useTree = (owner: string, name: string, ref: string, path: string) =>
  useCode<Tree>(owner, name, ["tree", ref, path], `/tree${qs({ ref, path, lastCommits: true })}`);

export const useBlob = (owner: string, name: string, ref: string, path: string) =>
  useCode<BlobFile>(owner, name, ["blob", ref, path], `/blob${qs({ ref, path })}`, path !== "");

export const useReadme = (owner: string, name: string, ref: string, path: string, enabled: boolean) =>
  useCode<BlobFile>(owner, name, ["readme", ref, path], `/blob${qs({ ref, path })}`, enabled);

export const useBlame = (owner: string, name: string, ref: string, path: string, enabled: boolean) =>
  useCode<Blame>(owner, name, ["blame", ref, path], `/blame${qs({ ref, path })}`, enabled);

export const useCommits = (owner: string, name: string, ref: string, path: string, page: number) =>
  useQuery({
    queryKey: key(owner, name, "commits", ref, path, page),
    queryFn: () =>
      request<CommitPage>("GET", `${repoBase(owner, name)}/commits${qs({ ref, path, offset: page * PAGE_SIZE, limit: PAGE_SIZE })}`),
    placeholderData: keepPreviousData,
    ...hydrationRetry,
  });

export const useCommit = (owner: string, name: string, sha: string) =>
  useCode<CommitDetail>(owner, name, ["commit", sha], `/commits/${enc(sha)}`);

export const useBranches = (owner: string, name: string, page: number, limit = PAGE_SIZE) =>
  useQuery({
    queryKey: key(owner, name, "branches", page, limit),
    queryFn: () => request<BranchPage>("GET", `${repoBase(owner, name)}/branches${qs({ offset: page * limit, limit })}`),
    placeholderData: keepPreviousData,
    ...hydrationRetry,
  });

export const useTags = (owner: string, name: string, page: number, limit = PAGE_SIZE) =>
  useQuery({
    queryKey: key(owner, name, "tags", page, limit),
    queryFn: () => request<TagPage>("GET", `${repoBase(owner, name)}/tags${qs({ offset: page * limit, limit })}`),
    placeholderData: keepPreviousData,
    ...hydrationRetry,
  });

export const useCompare = (owner: string, name: string, base: string, head: string) =>
  useCode<Comparison>(owner, name, ["compare", base, head], `/compare${qs({ base, head })}`, base !== "" && head !== "");

export type CodeSearch = { q: string; ref: string; path: string; regexp: boolean; ignoreCase: boolean };

export const useCodeSearch = (owner: string, name: string, s: CodeSearch) =>
  useCode<CodeSearchResult>(
    owner,
    name,
    ["search", s],
    `/search/code${qs({ q: s.q, ref: s.ref, path: s.path, regexp: s.regexp, ignoreCase: s.ignoreCase, limit: 200 })}`,
    s.q.trim() !== "",
  );

/** Creates, updates or deletes one file with a commit (POST contents). */
export function useEditFile(owner: string, name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: FileEdit) => request<FileEditResult>("POST", `${repoBase(owner, name)}/contents`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["code", owner, name] }),
  });
}

/** Global repository search by name. */
export const useRepoSearch = (q: string) =>
  useQuery({
    queryKey: ["repos", "search", q],
    queryFn: () => request<Repo[]>("GET", `/search/repos${qs({ q, limit: 20 })}`),
    enabled: q.trim() !== "",
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });

/** Repository permissions that can push (web edit, releases). */
export const canWrite = (p: Repo["permission"] | undefined) => p === "write" || p === "maintain" || p === "admin";
