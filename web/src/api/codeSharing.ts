import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { api } from "./client.ts";
import { ApiError } from "./http.ts";
import type { components, operations } from "./schema";

type Schema = components["schemas"];
export type CodeShare = Schema["ErvisioCodeShare"];
export type CodeShareRecipientKind = Schema["ErvisioCodeRecipientKind"];
export type CodeShareCreate = Omit<Schema["ErvisioCodeShareCreate"], "role"> & { role: "reader" | "proposer" };
export type CodeProposal = Schema["ErvisioCodeProposal"];
export type CodeReview = Schema["ErvisioCodeProposalReview"]["status"];
export type CodeShareScope = { accountId: string; hubAccountId?: string };
type SharesPage = operations["listErvisioCodeShares"]["responses"][200]["content"]["application/json"];
type ProposalsPage = operations["listErvisioCodeProposals"]["responses"][200]["content"]["application/json"];

const sharePath = "/user/ervisiocode/shares";
const proposalPath = "/user/ervisiocode/shares/{shareId}/proposals";
const privateFetch = { cache: "no-store", redirect: "error" } as const;
const transient = { gcTime: 0, retry: false, networkMode: "always" } as const;

function requireData<T>(result: { data?: T; response: Response }): T {
  if (result.response.ok && result.data !== undefined) return result.data;
  throw new ApiError(result.response.status, "ErvisioCode sharing request did not complete");
}

function queryParams({ hubAccountId }: CodeShareScope, beforeId?: string) {
  return { accountId: hubAccountId, beforeId };
}

export function useCodeShares(scope: CodeShareScope) {
  return useInfiniteQuery({
    ...transient,
    queryKey: ["ervisiocode", "shares", scope],
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: SharesPage) => last.nextBeforeId,
    queryFn: async ({ signal, pageParam }) => requireData(await api.GET(sharePath, {
      ...privateFetch, signal, params: { query: queryParams(scope, pageParam) },
    })),
    refetchInterval: 10_000,
  });
}

export function useCodeShareRecipients(scope: CodeShareScope, kind: CodeShareRecipientKind, q: string, enabled: boolean) {
  return useQuery({
    ...transient,
    queryKey: ["ervisiocode", "share-recipients", scope, kind, q],
    enabled: enabled && q.length > 0,
    queryFn: async ({ signal }) => requireData(await api.GET("/user/ervisiocode/shares/recipients", {
      ...privateFetch, signal, params: { query: { accountId: scope.hubAccountId, kind, q } },
    })).recipients,
  });
}

export async function createCodeShare(scope: CodeShareScope, body: CodeShareCreate) {
  return requireData(await api.POST(sharePath, {
    ...privateFetch, params: { query: queryParams(scope) }, body,
  }));
}

export async function acceptCodeShare(scope: CodeShareScope, token: string) {
  return requireData(await api.POST("/user/ervisiocode/shares/accept", {
    ...privateFetch, params: { query: queryParams(scope) }, body: { token },
  }));
}

export async function acceptCodeShareById(scope: CodeShareScope, shareId: string) {
  return requireData(await api.POST("/user/ervisiocode/shares/{shareId}/accept", {
    ...privateFetch, params: { query: queryParams(scope), path: { shareId } }, body: {},
  }));
}

export async function revokeCodeShare(scope: CodeShareScope, shareId: string) {
  const { response } = await api.DELETE("/user/ervisiocode/shares/{shareId}", {
    ...privateFetch, params: { query: queryParams(scope), path: { shareId } },
  });
  if (!response.ok) throw new ApiError(response.status, "ErvisioCode share revocation did not complete");
}

export function useSharedCodeState(scope: CodeShareScope, shareId: string) {
  return useQuery({
    ...transient,
    queryKey: ["ervisiocode", "shared-transcript", scope, shareId],
    queryFn: async ({ signal }) => {
      const state = requireData(await api.GET("/user/ervisiocode/shares/{shareId}/state", {
        ...privateFetch, signal, params: { query: queryParams(scope), path: { shareId } },
      }));
      if (!Array.isArray(state.messages) || typeof state.busy !== "boolean") {
        throw new Error("Missing shared ErvisioCode session state");
      }
      return { messages: state.messages, busy: state.busy };
    },
    refetchInterval: 5_000,
  });
}

export function useCodeProposals(scope: CodeShareScope, shareId: string) {
  return useInfiniteQuery({
    ...transient,
    queryKey: ["ervisiocode", "proposals", scope, shareId],
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last: ProposalsPage) => last.nextBeforeId,
    queryFn: async ({ signal, pageParam }) => requireData(await api.GET(proposalPath, {
      ...privateFetch, signal, params: { query: queryParams(scope, pageParam), path: { shareId } },
    })),
    refetchInterval: 10_000,
  });
}

export async function submitCodeProposal(scope: CodeShareScope, shareId: string, body: string) {
  return requireData(await api.POST(proposalPath, {
    ...privateFetch, params: { query: queryParams(scope), path: { shareId } }, body: { body },
  }));
}

export async function reviewCodeProposal(scope: CodeShareScope, shareId: string, proposalId: string, status: CodeReview) {
  return requireData(await api.POST("/user/ervisiocode/shares/{shareId}/proposals/{proposalId}/review", {
    ...privateFetch, params: { query: queryParams(scope), path: { shareId, proposalId } }, body: { status },
  }));
}
