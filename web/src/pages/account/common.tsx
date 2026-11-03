import type { UseQueryResult } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, useToast } from "../../ui/index.ts";
import { useProblemText } from "../auth/useProblemText.ts";

/** Success and failure toasts for mutations. */
export function useNotify() {
  const toast = useToast();
  const problem = useProblemText();
  const t = useT("account");
  return {
    ok: (title: string) => toast({ title, tone: "ok" }),
    fail: (e: unknown) => toast({ title: t("error.action"), description: problem(e), tone: "err" }),
  };
}

/** Loading, error and empty states around a list query. */
export function Async<T>({ query, empty, children }: {
  query: UseQueryResult<T[]>;
  empty: string;
  children: (rows: T[]) => ReactNode;
}) {
  const tc = useT("common");
  const t = useT("account");
  if (query.isPending) return <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>;
  if (query.isError) {
    return (
      <div role="alert">
        <EmptyState
          icon="alert"
          title={t("error.load")}
          description={query.error.message}
          action={<Button onClick={() => void query.refetch()}>{tc("action.retry")}</Button>}
        />
      </div>
    );
  }
  if (query.data.length === 0) return <p className="ui-note acc-pad">{empty}</p>;
  return <>{children(query.data)}</>;
}

export function formatDate(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** A secret shown once (new token, recovery codes) with a copy button. */
export function Secret({ label, value, multiline }: { label: string; value: string; multiline?: boolean }) {
  const t = useT("account");
  const notify = useNotify();
  return (
    <div className="acc-secret">
      <p className="ui-note">{label}</p>
      <pre className={multiline ? "acc-code acc-code-multi" : "acc-code"}>{value}</pre>
      <Button
        size="sm"
        icon="copy"
        onClick={() => {
          navigator.clipboard.writeText(value).then(
            () => notify.ok(t("copied")),
            (e: unknown) => notify.fail(e),
          );
        }}
      >
        {t("copy")}
      </Button>
    </div>
  );
}
