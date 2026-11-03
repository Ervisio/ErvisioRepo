import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { useT } from "./i18n/useT.ts";
import { Home } from "./pages/Home.tsx";

function Layout() {
  const t = useT("common");
  return (
    <main className="shell">
      <h1>{t("app.name")}</h1>
      <Outlet />
    </main>
  );
}

const rootRoute = createRootRoute({ component: Layout });
const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Home });

export const router = createRouter({ routeTree: rootRoute.addChildren([homeRoute]) });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
