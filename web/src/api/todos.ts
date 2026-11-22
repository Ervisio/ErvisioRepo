import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, request } from "./http.ts";
import type { components } from "./schema";

type Schemas = components["schemas"];
export type TodoBoard = Schemas["TodoBoard"];
export type TodoItem = Schemas["TodoItem"];
export type TodoCategory = Schemas["TodoCategory"];
export type TodoView = Schemas["TodoView"];
export type TodoScope = { owner: string; name: string } | { org: string };

function writeBoard({ revision, categories, items }: TodoBoard) {
  return {
    revision,
    categories,
    items: items.map(({ id, categoryId, title, completed }) => ({ id, categoryId, title, completed })),
  };
}

export function todoAccessDenied(error: unknown) {
  return error instanceof ApiError && [401, 403, 404].includes(error.status);
}

export function useTodo(scope: TodoScope, viewer: string | undefined) {
  const path = "org" in scope
    ? `/orgs/${encodeURIComponent(scope.org)}/todo`
    : `/repos/${encodeURIComponent(scope.owner)}/${encodeURIComponent(scope.name)}/todo`;
  const key = ["todo", viewer, path];
  const client = useQueryClient();
  const query = useQuery({
    queryKey: key,
    enabled: !!viewer,
    queryFn: () => request<TodoView>("GET", path),
    retry: false,
    gcTime: 0,
  });
  const save = useMutation({
    mutationFn: (board: TodoBoard) => request<TodoBoard>("PUT", path, writeBoard(board)),
    onMutate: () => client.cancelQueries({ queryKey: key }),
    onSuccess: (board) => client.setQueryData<TodoView>(key, (view) => view && { ...view, board }),
    onError: (error) => {
      if (todoAccessDenied(error) || (error instanceof ApiError && error.status === 409)) {
        return client.invalidateQueries({ queryKey: key });
      }
    },
  });
  const preference = useMutation({
    mutationFn: (collapsed: boolean) => request("PATCH", path, { collapsed }),
    onMutate: () => client.cancelQueries({ queryKey: key }),
    onSuccess: (_, collapsed) => client.setQueryData<TodoView>(key, (view) => view && { ...view, collapsed }),
    onError: (error) => {
      if (todoAccessDenied(error)) return client.invalidateQueries({ queryKey: key });
    },
  });
  return { query, save, preference };
}
