import { useLocation, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, type ReactNode } from "react";
import { isUnauthenticated, useSession } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Button, EmptyState, Page } from "../ui/index.ts";

/** Renders children for a signed-in user (or admin); anonymous visitors go to /login. */
export function Protected({ admin, children }: { admin?: boolean; children: ReactNode }) {
  const t = useT("auth");
  const tc = useT("common");
  const session = useSession();
  const navigate = useNavigate();
  const here = useLocation({ select: (l) => l.pathname + (l.searchStr ?? "") });
  const anonymous = session.isError && isUnauthenticated(session.error);

  // Redirect once: the location already points at /login while this page is still unmounting.
  const redirected = useRef(false);
  useEffect(() => {
    if (!anonymous || redirected.current) return;
    redirected.current = true;
    void navigate({ to: "/login", search: { next: here }, replace: true });
  }, [anonymous, here, navigate]);

  if (session.isPending) {
    return (
      <Page title={tc("app.name")}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (session.isError) {
    if (anonymous) return null;
    return (
      <Page title={tc("app.name")}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={t("session.error")}
            description={session.error.message}
            action={<Button onClick={() => void session.refetch()}>{tc("action.retry")}</Button>}
          />
        </div>
      </Page>
    );
  }
  if (admin && session.data.role !== "admin") {
    return (
      <Page title={t("forbidden.title")}>
        <EmptyState icon="lock" title={t("forbidden.title")} description={t("forbidden.description")} />
      </Page>
    );
  }
  return <>{children}</>;
}

/** Same-origin path from ?next=, or "/" when it is missing or looks external. */
export function safeNext(next: unknown): string {
  return typeof next === "string" && next.startsWith("/") && !next.startsWith("//") && !next.includes("\\") ? next : "/";
}
