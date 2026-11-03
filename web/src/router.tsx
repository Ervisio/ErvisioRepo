import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Protected } from "./auth/Protected.tsx";
import { AdminAudit } from "./pages/AdminAudit.tsx";
import { AdminStorage } from "./pages/AdminStorage.tsx";
import { Account } from "./pages/account/Account.tsx";
import { Login } from "./pages/auth/Login.tsx";
import { ForgotPassword, ResetPassword } from "./pages/auth/PasswordReset.tsx";
import { Register } from "./pages/auth/Register.tsx";
import { TwoFactor } from "./pages/auth/TwoFactor.tsx";
import { VerifyEmail } from "./pages/auth/VerifyEmail.tsx";
import { Catalog } from "./pages/Catalog.tsx";
import { Home } from "./pages/Home.tsx";
import { OrgPage } from "./pages/Org.tsx";
import { OrgInviteAccept } from "./pages/OrgInvite.tsx";
import { AppShell, ToastProvider } from "./ui/index.ts";

const str = (v: unknown) => (typeof v === "string" ? v : undefined);
const nextSearch = (s: Record<string, unknown>): { next?: string } => ({ next: str(s.next) });
const tokenSearch = (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) });

const rootRoute = createRootRoute({ component: Outlet });

// Signed-out pages: no navigation shell.
const bareRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "bare",
  component: () => (
    <ToastProvider>
      <Outlet />
    </ToastProvider>
  ),
});
const oidcSearch = (s: Record<string, unknown>): { oidc_error?: string } => ({ oidc_error: str(s.oidc_error) });
const loginRoute = createRoute({
  getParentRoute: () => bareRoute,
  path: "/login",
  validateSearch: (s: Record<string, unknown>) => ({ ...nextSearch(s), ...oidcSearch(s) }),
  component: function LoginPage() {
    const s = loginRoute.useSearch();
    return <Login next={s.next} oidcError={s.oidc_error} />;
  },
});
const twoFactorRoute = createRoute({ getParentRoute: () => bareRoute, path: "/login/2fa", validateSearch: nextSearch, component: () => <TwoFactor next={twoFactorRoute.useSearch().next} /> });
const registerRoute = createRoute({ getParentRoute: () => bareRoute, path: "/register", validateSearch: (s: Record<string, unknown>): { invite?: string } => ({ invite: str(s.invite) }), component: () => <Register invite={registerRoute.useSearch().invite} /> });
const verifyRoute = createRoute({ getParentRoute: () => bareRoute, path: "/verify-email", validateSearch: tokenSearch, component: () => <VerifyEmail token={verifyRoute.useSearch().token} /> });
const forgotRoute = createRoute({ getParentRoute: () => bareRoute, path: "/forgot-password", component: ForgotPassword });
const resetRoute = createRoute({ getParentRoute: () => bareRoute, path: "/reset-password", validateSearch: tokenSearch, component: () => <ResetPassword token={resetRoute.useSearch().token} /> });

// Pages inside the navigation shell.
const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "shell",
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
});
const guard = (node: ReactNode, admin = false) => <Protected admin={admin}>{node}</Protected>;
const homeRoute = createRoute({ getParentRoute: () => shellRoute, path: "/", component: Home });
const storageRoute = createRoute({ getParentRoute: () => shellRoute, path: "/admin/storage", component: () => guard(<AdminStorage />, true) });
const auditRoute = createRoute({ getParentRoute: () => shellRoute, path: "/admin/audit", component: () => guard(<AdminAudit />, true) });
const settingsRoute = createRoute({ getParentRoute: () => shellRoute, path: "/settings", validateSearch: oidcSearch, component: () => guard(<Account oidcError={settingsRoute.useSearch().oidc_error} />) });
const orgInviteRoute = createRoute({ getParentRoute: () => shellRoute, path: "/org-invite", validateSearch: tokenSearch, component: () => guard(<OrgInviteAccept token={orgInviteRoute.useSearch().token} />) });
const orgRoute = createRoute({ getParentRoute: () => shellRoute, path: "/orgs/$org", component: () => guard(<OrgPage name={orgRoute.useParams().org} />) });
// Component catalog for the design kit (Fase UI).
const catalogRoute = createRoute({ getParentRoute: () => shellRoute, path: "/_ui", component: Catalog });

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    bareRoute.addChildren([loginRoute, twoFactorRoute, registerRoute, verifyRoute, forgotRoute, resetRoute]),
    shellRoute.addChildren([homeRoute, storageRoute, auditRoute, settingsRoute, orgInviteRoute, orgRoute, catalogRoute]),
  ]),
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
