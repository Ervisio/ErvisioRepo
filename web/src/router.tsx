import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { AdminStorage } from "./pages/AdminStorage.tsx";
import { Catalog } from "./pages/Catalog.tsx";
import { Home } from "./pages/Home.tsx";
import { AppShell } from "./ui/index.ts";

function Layout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

const rootRoute = createRootRoute({ component: Layout });
const homeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Home });
const storageRoute = createRoute({ getParentRoute: () => rootRoute, path: "/admin/storage", component: AdminStorage });
// Component catalog for the design kit (Fase UI).
const catalogRoute = createRoute({ getParentRoute: () => rootRoute, path: "/_ui", component: Catalog });

export const router = createRouter({ routeTree: rootRoute.addChildren([homeRoute, storageRoute, catalogRoute]) });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
