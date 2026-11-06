import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "./http.ts";
import type { components } from "./schema";

// Instance administration: users, invitations and editable instance settings.
type S = components["schemas"];
export type AdminUser = S["AdminUser"];
export type AdminUserUpdate = S["AdminUserUpdate"];
export type AdminInvite = S["AdminInvite"];
export type InstanceSetting = S["InstanceSetting"];
export type InstanceSettings = S["InstanceSettings"];
export type UserRole = AdminUser["role"];
export const USER_ROLES: readonly UserRole[] = ["admin", "user", "restricted"];
export const ADMIN_PAGE_SIZE = 30;

const enc = encodeURIComponent;

export type UserFilter = { q?: string; role?: UserRole; suspended?: boolean; page: number };

export function useAdminUsers(f: UserFilter) {
  const q = new URLSearchParams({ limit: String(ADMIN_PAGE_SIZE), offset: String((f.page - 1) * ADMIN_PAGE_SIZE) });
  if (f.q) q.set("q", f.q);
  if (f.role) q.set("role", f.role);
  if (f.suspended !== undefined) q.set("suspended", String(f.suspended));
  return useQuery({
    queryKey: ["admin", "users", "list", f],
    placeholderData: keepPreviousData,
    queryFn: () => request<S["AdminUserPage"]>("GET", `/admin/users?${q}`),
  });
}

export const useAdminUser = (login: string) =>
  useQuery({ queryKey: ["admin", "users", "one", login], queryFn: () => request<AdminUser>("GET", `/admin/users/${enc(login)}`) });

export function useUpdateAdminUser(login: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: AdminUserUpdate) => request<AdminUser>("PATCH", `/admin/users/${enc(login)}`, b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "users"] }),
  });
}

export function useResetUser2fa(login: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => request("DELETE", `/admin/users/${enc(login)}/2fa`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "users"] }),
  });
}

export function useAdminInvites(page: number) {
  const q = new URLSearchParams({ limit: String(ADMIN_PAGE_SIZE), offset: String((page - 1) * ADMIN_PAGE_SIZE) });
  return useQuery({
    queryKey: ["admin", "invites", page],
    placeholderData: keepPreviousData,
    queryFn: () => request<S["AdminInvitePage"]>("GET", `/admin/invites?${q}`),
  });
}

export function useCreateAdminInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: S["InviteRequest"]) => request<S["Invite"]>("POST", "/admin/invites", b),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "invites"] }),
  });
}

export function useRevokeAdminInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => request("DELETE", `/admin/invites/${enc(id)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "invites"] }),
  });
}

export const useInstanceSettings = () =>
  useQuery({ queryKey: ["admin", "settings"], queryFn: () => request<InstanceSettings>("GET", "/admin/settings") });

export function useUpdateInstanceSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (values: Record<string, unknown>) => request<InstanceSettings>("PUT", "/admin/settings", { values }),
    onSuccess: (data) => qc.setQueryData(["admin", "settings"], data),
  });
}
