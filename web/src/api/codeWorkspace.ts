import { useQuery } from "@tanstack/react-query";
import { api } from "./client.ts";
import { ApiError } from "./http.ts";
import type { components } from "./schema";

export type RemoteSession = components["schemas"]["ErvisioCodeRemoteSession"];
export type RemoteRecent = components["schemas"]["ErvisioCodeRemoteRecent"];
export type RemoteMessage = components["schemas"]["ErvisioCodeRemoteMessage"];
export type RemoteProject = components["schemas"]["ErvisioCodeRemoteProject"];
export type RemoteRepositoryCatalog = components["schemas"]["ErvisioCodeRepositoryCatalog"];
type RemoteState = components["schemas"]["ErvisioCodeRemoteState"];
type RemoteAction = components["schemas"]["ErvisioCodeRemoteAction"];

const sessionsPath = "/user/ervisiocode/sessions";
const projectsPath = "/user/ervisiocode/projects";
function sessionParams(hubAccountId?: string) {
  return { query: hubAccountId ? { accountId: hubAccountId } : {} };
}

export function useRemoteSessions(accountId: string, hubAccountId?: string) {
  return useQuery({
    queryKey: ["ervisiocode", "sessions", accountId, hubAccountId],
    queryFn: async ({ signal }) => {
      const { data, response } = await api.GET(sessionsPath, {
        params: sessionParams(hubAccountId), signal, cache: "no-store", redirect: "error",
      });
      if (!Array.isArray(data?.sessions) || !Array.isArray(data.recent)) {
        throw new ApiError(response.status, "Could not list remote sessions");
      }
      return data;
    },
    refetchInterval: 4_000,
    networkMode: "always",
    retry: false,
    gcTime: 0,
  });
}

export function useRemoteProjects(accountId: string, hubAccountId?: string) {
  return useQuery({
    queryKey: ["ervisiocode", "projects", accountId, hubAccountId],
    queryFn: async ({ signal }) => {
      const { data, response } = await api.GET(projectsPath, {
        params: sessionParams(hubAccountId), signal, cache: "no-store", redirect: "error",
      });
      if (!Array.isArray(data?.projects)) throw new ApiError(response.status, "Could not list approved projects");
      return data.projects;
    },
    refetchInterval: 4_000,
    networkMode: "always",
    retry: false,
    gcTime: 0,
  });
}

export function useRemoteRepositoryCatalog(accountId: string, hubAccountId?: string) {
  return useQuery({
    queryKey: ["ervisiocode", "catalog", accountId, hubAccountId],
    queryFn: async ({ signal }) => {
      const { data, response } = await api.GET("/user/ervisiocode/catalog", {
        params: sessionParams(hubAccountId), signal, cache: "no-store", redirect: "error",
      });
      if (!Array.isArray(data?.ervisiorepo?.repositories) || !Array.isArray(data.github?.repositories)) {
        throw new ApiError(response.status, "Could not load the repository catalog");
      }
      return data;
    },
    networkMode: "always",
    retry: false,
    gcTime: 0,
  });
}

export async function createRemoteSession({ deviceId, relayId, projectId }: RemoteProject, hubAccountId?: string) {
  const { data, error, response } = await api.POST(projectsPath, {
    params: sessionParams(hubAccountId),
    body: { deviceId, relayId, projectId }, cache: "no-store", redirect: "error",
  });
  if (!data?.sessionId) throw new ApiError(response.status, "Could not confirm session creation", error?.detail);
  return { deviceId, sessionId: data.sessionId };
}

async function act(action: RemoteAction, hubAccountId?: string, signal?: AbortSignal): Promise<RemoteState> {
  const { data, response } = await api.POST(sessionsPath, {
    params: sessionParams(hubAccountId), body: action, signal, cache: "no-store", redirect: "error",
  });
  if (!response.ok) throw new ApiError(response.status, "Remote session request failed");
  return data ?? {};
}

export function useRemoteTranscript(
  accountId: string, hubAccountId: string | undefined, session: RemoteSession | undefined, canPoll: boolean,
) {
  const { deviceId, relayId, sessionId } = session ?? {};
  return useQuery({
    queryKey: ["ervisiocode", "transcript", accountId, hubAccountId, deviceId, relayId, sessionId],
    enabled: !!session && canPoll,
    queryFn: async ({ signal }) => {
      if (!deviceId || !relayId || !sessionId) throw new Error("No active remote session");
      const { messages, busy } = await act({ deviceId, relayId, sessionId, op: "state" }, hubAccountId, signal);
      if (!Array.isArray(messages) || typeof busy !== "boolean") throw new Error("Missing remote session state");
      return { messages, busy };
    },
    refetchInterval: (query) => query.state.data?.busy ? 2_500 : 5_000,
    networkMode: "always",
    retry: false,
    gcTime: 0,
  });
}

export type RemoteCommand = { op: "prompt"; text: string } | { op: "interrupt" };

export async function sendRemoteCommand(
  { deviceId, relayId, sessionId }: RemoteSession, command: RemoteCommand, hubAccountId?: string,
) {
  await act({ deviceId, relayId, sessionId, ...command }, hubAccountId);
}
