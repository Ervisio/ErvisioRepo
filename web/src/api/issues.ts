import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PAGE_SIZE, qs, repoBase } from "./code.ts";
import { CSRF_HEADER, csrfToken } from "./csrf.ts";
import { ApiError, request, requestWithHeaders } from "./http.ts";
import type { components } from "./schema";

// Issues, comments, reactions, labels, milestones, templates and attachments.
type S = components["schemas"];
export type Issue = S["Issue"];
export type IssueCreate = S["IssueCreate"];
export type IssueUpdate = S["IssueUpdate"];
export type IssueComment = S["IssueComment"];
export type Label = S["Label"];
export type LabelCreate = S["LabelCreate"];
export type Milestone = S["Milestone"];
export type ReactionGroup = S["ReactionGroup"];
export type TimelineEvent = S["TimelineEvent"];
export type IssueTemplates = S["IssueTemplates"];
export type IssueTemplate = S["IssueTemplate"];
export type IssueAttachment = S["IssueAttachment"];
export type Collaborator = S["RepoCollaborator"];

const enc = encodeURIComponent;
const base = (owner: string, name: string) => `${repoBase(owner, name)}/issues`;
const key = (owner: string, name: string, ...rest: unknown[]) => ["issues", owner, name, ...rest];

/** Reactions accepted by the API, in display order. */
export const REACTIONS = ["+1", "-1", "laugh", "hooray", "confused", "heart", "rocket", "eyes"] as const;
export const REACTION_EMOJI: Record<string, string> = {
  "+1": "👍",
  "-1": "👎",
  laugh: "😄",
  hooray: "🎉",
  confused: "😕",
  heart: "❤️",
  rocket: "🚀",
  eyes: "👀",
};

export type IssueFilter = {
  state: "open" | "closed" | "all";
  labels?: string[];
  milestone?: string;
  assignee?: string;
  author?: string;
  mentioned?: string;
  q?: string;
  sort?: "created" | "updated" | "comments";
  page: number;
};

/** A page of a list endpoint that answers a bare array: one extra row tells whether more exist. */
export type Paged<T> = { items: T[]; hasMore: boolean };

export async function paged<T>(path: string, page: number, params: Record<string, string | number | boolean | undefined> = {}): Promise<Paged<T>> {
  const rows = await request<T[]>("GET", `${path}${qs({ ...params, offset: (page - 1) * PAGE_SIZE, limit: PAGE_SIZE + 1 })}`);
  return { items: rows.slice(0, PAGE_SIZE), hasMore: rows.length > PAGE_SIZE };
}

/** A page of a list endpoint that answers the X-Has-More header. */
export async function pagedHeader<T>(path: string, page: number, params: Record<string, string | number | boolean | undefined> = {}): Promise<Paged<T>> {
  const { data, headers } = await requestWithHeaders<T[]>("GET", `${path}${qs({ ...params, offset: (page - 1) * PAGE_SIZE, limit: PAGE_SIZE })}`);
  return { items: data, hasMore: headers.get("X-Has-More") === "true" };
}

export const useIssues = (owner: string, name: string, f: IssueFilter) =>
  useQuery({
    queryKey: key(owner, name, "list", f),
    queryFn: () =>
      pagedHeader<Issue>(base(owner, name), f.page, {
        state: f.state,
        labels: f.labels?.join(","),
        milestone: f.milestone,
        assignee: f.assignee,
        author: f.author,
        mentioned: f.mentioned,
        q: f.q,
        sort: f.sort,
      }),
    placeholderData: keepPreviousData,
  });

export const useIssue = (owner: string, name: string, number: number) =>
  useQuery({ queryKey: key(owner, name, "one", number), queryFn: () => request<Issue>("GET", `${base(owner, name)}/${number}`) });

/** Comments and events in one stream (the timeline carries the comment bodies). */
export const useTimeline = (owner: string, name: string, number: number) =>
  useQuery({
    queryKey: key(owner, name, "timeline", number),
    queryFn: () => request<TimelineEvent[]>("GET", `${base(owner, name)}/${number}/timeline${qs({ limit: 250 })}`),
  });

function useIssueMutation<V, R>(owner: string, name: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["issues", owner, name] });
      await qc.invalidateQueries({ queryKey: ["pulls", owner, name] });
    },
  });
}

export const useCreateIssue = (owner: string, name: string) =>
  useIssueMutation<IssueCreate, Issue>(owner, name, (b) => request("POST", base(owner, name), b));
export const useUpdateIssue = (owner: string, name: string, number: number) =>
  useIssueMutation<IssueUpdate, Issue>(owner, name, (b) => request("PATCH", `${base(owner, name)}/${number}`, b));
export const useLockIssue = (owner: string, name: string, number: number) =>
  useIssueMutation<{ lock: boolean; reason?: string }, Issue>(owner, name, ({ lock, reason }) =>
    lock ? request("PUT", `${base(owner, name)}/${number}/lock`, { lockReason: reason }) : request("DELETE", `${base(owner, name)}/${number}/lock`),
  );
export const useAddComment = (owner: string, name: string, number: number) =>
  useIssueMutation<string, IssueComment>(owner, name, (body) => request("POST", `${base(owner, name)}/${number}/comments`, { body }));
export const useEditComment = (owner: string, name: string) =>
  useIssueMutation<{ id: string; body: string }, IssueComment>(owner, name, ({ id, body }) => request("PATCH", `${base(owner, name)}/comments/${enc(id)}`, { body }));
export const useDeleteComment = (owner: string, name: string) =>
  useIssueMutation<string, void>(owner, name, (id) => request("DELETE", `${base(owner, name)}/comments/${enc(id)}`));

/** Adds or removes the viewer's reaction on an issue (commentId undefined) or a comment. */
export const useReact = (owner: string, name: string, number: number) =>
  useIssueMutation<{ commentId?: string; content: string; on: boolean }, ReactionGroup[]>(owner, name, ({ commentId, content, on }) => {
    const target = commentId ? `${base(owner, name)}/comments/${enc(commentId)}` : `${base(owner, name)}/${number}`;
    return on ? request("POST", `${target}/reactions`, { content }) : request("DELETE", `${target}/reactions/${enc(content)}`);
  });

export const useLabels = (owner: string, name: string) =>
  useQuery({ queryKey: ["issues", owner, name, "labels"], queryFn: () => request<Label[]>("GET", `${repoBase(owner, name)}/labels${qs({ limit: 100 })}`) });
export const useCreateLabel = (owner: string, name: string) =>
  useIssueMutation<LabelCreate, Label>(owner, name, (b) => request("POST", `${repoBase(owner, name)}/labels`, b));
export const useUpdateLabel = (owner: string, name: string) =>
  useIssueMutation<{ label: string; body: S["LabelUpdate"] }, Label>(owner, name, ({ label, body }) =>
    request("PATCH", `${repoBase(owner, name)}/labels/${enc(label)}`, body),
  );
export const useDeleteLabel = (owner: string, name: string) =>
  useIssueMutation<string, void>(owner, name, (label) => request("DELETE", `${repoBase(owner, name)}/labels/${enc(label)}`));

export const useMilestones = (owner: string, name: string, state: "open" | "closed" | "all" = "open") =>
  useQuery({
    queryKey: ["issues", owner, name, "milestones", state],
    queryFn: () => request<Milestone[]>("GET", `${repoBase(owner, name)}/milestones${qs({ state, limit: 100 })}`),
  });
export const useCreateMilestone = (owner: string, name: string) =>
  useIssueMutation<S["MilestoneCreate"], Milestone>(owner, name, (b) => request("POST", `${repoBase(owner, name)}/milestones`, b));
export const useUpdateMilestone = (owner: string, name: string) =>
  useIssueMutation<{ id: string; body: S["MilestoneUpdate"] }, Milestone>(owner, name, ({ id, body }) =>
    request("PATCH", `${repoBase(owner, name)}/milestones/${enc(id)}`, body),
  );
export const useDeleteMilestone = (owner: string, name: string) =>
  useIssueMutation<string, void>(owner, name, (id) => request("DELETE", `${repoBase(owner, name)}/milestones/${enc(id)}`));

export const useIssueTemplates = (owner: string, name: string) =>
  useQuery({ queryKey: ["issues", owner, name, "templates"], queryFn: () => request<IssueTemplates>("GET", `${repoBase(owner, name)}/issue-templates`), retry: false });

/** People who can be assigned or asked for review. */
export const useCollaborators = (owner: string, name: string, enabled = true) =>
  useQuery({
    queryKey: ["repos", owner, name, "collaborators"],
    queryFn: () => request<Collaborator[]>("GET", `${repoBase(owner, name)}/collaborators`),
    enabled,
    retry: false,
  });

export type CollaboratorPermission = S["RepoPermissionRequest"]["permission"];
function useCollaboratorMutation<V>(owner: string, name: string, fn: (v: V) => Promise<void>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["repos", owner, name, "collaborators"] }) });
}
export const useSetCollaborator = (owner: string, name: string) =>
  useCollaboratorMutation<{ username: string; permission: CollaboratorPermission }>(owner, name, ({ username, permission }) =>
    request("PUT", `${repoBase(owner, name)}/collaborators/${enc(username)}`, { permission }),
  );
export const useRemoveCollaborator = (owner: string, name: string) =>
  useCollaboratorMutation<string>(owner, name, (username) => request("DELETE", `${repoBase(owner, name)}/collaborators/${enc(username)}`));

/** Uploads an attachment as the raw request body (route outside the OpenAPI document). */
export async function uploadAttachment(owner: string, name: string, file: File): Promise<IssueAttachment> {
  const headers: Record<string, string> = {
    Accept: "application/json, application/problem+json",
    "Content-Type": file.type || "application/octet-stream",
  };
  const token = csrfToken(document.cookie);
  if (token) headers[CSRF_HEADER] = token;
  const res = await fetch(`/api/v1${base(owner, name)}/attachments${qs({ name: file.name })}`, { method: "POST", headers, credentials: "same-origin", body: file });
  if (!res.ok) {
    let title = `HTTP ${res.status}`;
    let detail: string | undefined;
    try {
      const p = (await res.json()) as { title?: string; detail?: string };
      if (p.title) title = p.title;
      detail = p.detail;
    } catch {
      // not a JSON problem document
    }
    throw new ApiError(res.status, title, detail);
  }
  return (await res.json()) as IssueAttachment;
}
