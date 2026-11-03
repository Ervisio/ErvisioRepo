import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";

/** Message shown for a failed request; rate limits say how long to wait. */
export function useProblemText() {
  const t = useT("auth");
  return (e: unknown): string => {
    if (e instanceof ApiError) {
      if (e.status === 429) return e.retryAfter ? t("error.rateLimited", { seconds: e.retryAfter }) : t("error.rateLimitedNoWait");
      if (e.status === 401) return t("error.credentials");
      return e.detail ?? e.title;
    }
    return e instanceof Error ? e.message : String(e);
  };
}
