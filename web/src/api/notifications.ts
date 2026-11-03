import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { qs, repoBase } from "./code.ts";
import { request } from "./http.ts";
import { pagedHeader } from "./issues.ts";
import type { components } from "./schema";

// Notification inbox, thread state, subscriptions and delivery settings.
type S = components["schemas"];
export type NotificationThread = S["NotificationThread"];
export type NotificationSettings = S["NotificationSettings"];
export type RepoSubscription = S["RepoSubscription"];
export type ThreadSubscription = S["ThreadSubscription"];

export type NotificationRepoCount = S["NotificationRepoCount"];
export type NotificationSubject = S["NotificationSubject"];

/** repo is owner/name, to show one repository only. */
export type InboxFilter = { filter: "unread" | "participating" | "all"; page: number; repo?: string };

const inboxParams = (f: Pick<InboxFilter, "filter">) => ({ all: f.filter === "all", participating: f.filter === "participating" });

export const useInbox = (f: InboxFilter) =>
  useQuery({
    queryKey: ["notifications", "list", f],
    queryFn: () => pagedHeader<NotificationThread>("/notifications", f.page, { ...inboxParams(f), repo: f.repo }),
    placeholderData: keepPreviousData,
  });

/** Per-repository counts of the threads the inbox filter shows. */
export const useInboxRepos = (filter: InboxFilter["filter"]) =>
  useQuery({
    queryKey: ["notifications", "repos", filter],
    queryFn: () => request<NotificationRepoCount[]>("GET", `/notifications/repos${qs(inboxParams({ filter }))}`),
    placeholderData: keepPreviousData,
  });

/** Subject of the subscription endpoints: an issue or pull request by number, or a release. */
export type SubjectRef = { owner: string; name: string; number?: number; release?: string };

const subjectPath = (r: SubjectRef) => `/notifications/subject${qs({ repo: `${r.owner}/${r.name}`, number: r.number, release: r.release })}`;
const subjectKey = (r: SubjectRef) => ["notifications", "subject", r.owner, r.name, r.number ?? null, r.release ?? null];

export const useSubjectSubscription = (r: SubjectRef, enabled = true) =>
  useQuery({ queryKey: subjectKey(r), queryFn: () => request<NotificationSubject>("GET", subjectPath(r)), retry: false, enabled });

/** subscribed null returns the subject to the default rules. */
export function useSetSubjectSubscription(r: SubjectRef) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (subscribed: boolean | null) =>
      subscribed === null ? request<NotificationSubject>("DELETE", subjectPath(r)) : request<NotificationSubject>("PUT", subjectPath(r), { ignored: !subscribed }),
    onSuccess: async (s) => {
      qc.setQueryData(subjectKey(r), s);
      await qc.invalidateQueries({ queryKey: ["notifications", "list"] });
    },
  });
}

export const useUnreadCount = () =>
  useQuery({
    queryKey: ["notifications", "count"],
    queryFn: () => request<S["NotificationCount"]>("GET", "/notifications/count"),
    refetchInterval: 60_000,
    retry: false,
  });

function useNotifMutation<V, R>(fn: (v: V) => Promise<R>, extra?: (string | undefined)[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["notifications"] });
      if (extra) await qc.invalidateQueries({ queryKey: extra });
    },
  });
}

export const useMarkAllRead = () => useNotifMutation<string | undefined, void>((lastReadAt) => request("PUT", "/notifications", { lastReadAt }));
export const useMarkRead = () => useNotifMutation<string, void>((id) => request("PATCH", `/notifications/threads/${encodeURIComponent(id)}`));
export const useMarkDone = () => useNotifMutation<string, void>((id) => request("DELETE", `/notifications/threads/${encodeURIComponent(id)}`));
export const useThreadIgnore = () =>
  useNotifMutation<{ id: string; ignored: boolean | null }, ThreadSubscription | void>(({ id, ignored }) =>
    ignored === null
      ? request("DELETE", `/notifications/threads/${encodeURIComponent(id)}/subscription`)
      : request("PUT", `/notifications/threads/${encodeURIComponent(id)}/subscription`, { ignored }),
  );

export const useNotificationSettings = () =>
  useQuery({ queryKey: ["notifications", "settings"], queryFn: () => request<NotificationSettings>("GET", "/notifications/settings") });
export const useSaveNotificationSettings = () =>
  useNotifMutation<NotificationSettings, NotificationSettings>((b) => request("PUT", "/notifications/settings", b));

export const useRepoSubscription = (owner: string, name: string) =>
  useQuery({
    queryKey: ["repos", owner, name, "subscription"],
    queryFn: () => request<RepoSubscription>("GET", `${repoBase(owner, name)}/subscription`),
    retry: false,
  });
export function useSetRepoSubscription(owner: string, name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (level: RepoSubscription["level"]) => request<RepoSubscription>("PUT", `${repoBase(owner, name)}/subscription`, { level }),
    onSuccess: (s) => qc.setQueryData(["repos", owner, name, "subscription"], s),
  });
}

/** Path of a notification subject inside the app, when it has a page. */
export function subjectHref(n: NotificationThread): string {
  const r = `/${encodeURIComponent(n.owner)}/${encodeURIComponent(n.repo)}`;
  switch (n.subjectType) {
    case "issue":
      return `${r}/issues/${n.number}`;
    case "pull_request":
      return `${r}/pulls/${n.number}`;
    case "release":
      return `${r}/releases`;
    default:
      return `${r}/commits`;
  }
}
