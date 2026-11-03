import { useQuery } from "@tanstack/react-query";
import { api } from "./client.ts";
import type { components } from "./schema";

export type Meta = components["schemas"]["Meta"];

export function useMeta() {
  return useQuery({
    queryKey: ["meta"],
    queryFn: async (): Promise<Meta> => {
      const { data, error, response } = await api.GET("/meta");
      if (data) return data;
      throw new Error(error?.title ?? `HTTP ${response.status}`);
    },
  });
}
