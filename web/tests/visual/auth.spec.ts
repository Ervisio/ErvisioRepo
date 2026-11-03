import { expect, test, type Page, type Route } from "@playwright/test";

// Identity pages (login, account, organization, audit) against a mocked API, layout A (console).
const user = {
  id: "00000000-0000-4000-8000-0000000000a1",
  username: "marta",
  displayName: "Marta Rossi",
  role: "admin",
  locale: "en",
  email: "marta@example.org",
  emailVerified: true,
  createdAt: "2026-01-10T09:00:00Z",
};
const t0 = "2026-03-02T10:30:00Z";

const org = {
  id: "00000000-0000-4000-8000-0000000000b1",
  name: "ervisio",
  displayName: "Ervisio",
  description: "Open infrastructure tools",
  basePermission: "read",
  require2fa: false,
  verified: true,
  role: "owner",
  createdAt: t0,
  updatedAt: t0,
};

const data: Record<string, unknown> = {
  "/meta": { name: "ErvisioRepo", version: "0.1.0", commit: "a3f9c21", instanceId: "00000000-0000-4000-8000-000000000001", publicUrl: "https://git.example.org", registration: "open" },
  "/auth/oidc/providers": [{ name: "github", label: "GitHub" }, { name: "keycloak", label: "Keycloak" }],
  "/auth/mfa": { methods: ["totp", "recovery"], enrollmentRequired: false, expiresAt: "2026-03-02T10:35:00Z" },
  "/orgs": [org],
  "/user/security": {
    totp: false,
    totpPending: false,
    passkeys: [{ id: "p1", name: "MacBook", createdAt: t0, lastUsedAt: t0 }],
    recoveryCodesLeft: 10,
    recoveryCodesTotal: 10,
    required: false,
    webauthnAvailable: true,
  },
  "/user/tokens": [{ id: "k1", name: "ci", hint: "erp_…3f9c", scopes: ["repo:read"], createdAt: t0, expiresAt: "2026-06-01T00:00:00Z" }],
  "/user/keys/ssh": [{ id: "h1", title: "laptop", type: "ssh-ed25519", fingerprint: "SHA256:abc", publicKey: "ssh-ed25519 AAAA", createdAt: t0 }],
  "/user/keys/gpg": [],
  "/user/oidc": [{ id: "i1", provider: "github", label: "GitHub", email: "marta@example.org", createdAt: t0 }],
  "/orgs/ervisio": org,
  "/orgs/ervisio/members": [
    { username: "marta", displayName: "Marta Rossi", role: "owner", since: t0 },
    { username: "luca", displayName: "Luca Bianchi", role: "member", since: t0 },
    { username: "sara", displayName: "Sara Verdi", role: "billing", since: t0 },
  ],
  "/orgs/ervisio/teams": [
    { id: "t1", name: "core", description: "Maintainers", members: 2, createdAt: t0, updatedAt: t0 },
    { id: "t2", name: "docs", description: "", members: 1, createdAt: t0, updatedAt: t0 },
  ],
  "/orgs/ervisio/invites": [{ id: "v1", email: "new@example.org", role: "member", teams: [], invitedBy: "marta", expiresAt: "2026-03-20T00:00:00Z", createdAt: t0 }],
  "/admin/audit": {
    entries: [
      { id: "1", at: t0, action: "auth.login", actor: "marta", targetType: "user", targetId: "marta", ip: "203.0.113.7", data: {} },
      { id: "2", at: t0, action: "auth.login_failed", actor: "luca", targetType: "user", targetId: "luca", ip: "198.51.100.4", data: {} },
      { id: "3", at: t0, action: "org.member_added", actor: "marta", targetType: "org", targetId: "ervisio", ip: "203.0.113.7", data: {} },
    ],
  },
};

async function mock(page: Page, signedIn: boolean) {
  await page.addInitScript(() => localStorage.setItem("ervisio.layout", "console"));
  await page.route("**/api/v1/**", (route: Route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    if (path === "/user") {
      return signedIn ? route.fulfill({ json: user }) : route.fulfill({ status: 401, contentType: "application/problem+json", json: { title: "Unauthorized", status: 401 } });
    }
    if (path === "/user/security/totp") {
      return route.fulfill({ json: { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/ErvisioRepo:marta?secret=JBSWY3DPEHPK3PXP&issuer=ErvisioRepo" } });
    }
    const body = data[path];
    return body === undefined ? route.fulfill({ status: 404, json: { title: "Not found", status: 404 } }) : route.fulfill({ json: body });
  });
}

async function shot(page: Page, name: string) {
  await page.evaluate(() => document.fonts.ready);
  await expect(page).toHaveScreenshot(`${name}.png`, { fullPage: true });
}

const bare = [
  { name: "login", path: "/login", ready: "Welcome back to ErvisioRepo" },
  { name: "login-2fa", path: "/login/2fa?next=/", ready: "Two-factor authentication" },
  { name: "register", path: "/register", ready: "Create your account" },
  { name: "forgot", path: "/forgot-password", ready: "Reset your password" },
  { name: "reset", path: "/reset-password?token=abc", ready: "Choose a new password" },
];
for (const p of bare) {
  test(`auth ${p.name}`, async ({ page }) => {
    await mock(page, false);
    await page.goto(p.path);
    await expect(page.getByText(p.ready, { exact: true })).toBeVisible();
    await shot(page, `auth-${p.name}`);
  });
}

test("auth verify email", async ({ page }) => {
  await mock(page, false);
  await page.route("**/api/v1/auth/email/verify", (route) => route.fulfill({ status: 204 }));
  await page.goto("/verify-email?token=abc");
  await expect(page.getByText("Email verified", { exact: true })).toBeVisible();
  await shot(page, "auth-verify");
});

test("protected pages send visitors to the login page", async ({ page }) => {
  await mock(page, false);
  await page.goto("/settings");
  await expect(page).toHaveURL(/\/login\?next=%2Fsettings/);
});

const account = [
  { tab: "Profile", name: "profile", ready: "Organizations" },
  { tab: "Security", name: "security", ready: "Authenticator app" },
  { tab: "Tokens", name: "tokens", ready: "Personal access tokens" },
  { tab: "SSH and GPG keys", name: "keys", ready: "SSH keys" },
  { tab: "Linked accounts", name: "linked", ready: "Linked accounts" },
];
for (const a of account) {
  test(`account ${a.name}`, async ({ page }) => {
    await mock(page, true);
    await page.goto("/settings");
    await page.getByRole("tab", { name: a.tab }).click();
    await expect(page.getByText(a.ready, { exact: true }).first()).toBeVisible();
    await shot(page, `account-${a.name}`);
  });
}

test("account totp setup shows the QR code", async ({ page }) => {
  await mock(page, true);
  await page.goto("/settings");
  await page.getByRole("tab", { name: "Security" }).click();
  await page.getByRole("button", { name: "Set up authenticator" }).click();
  await page.getByLabel("Current password").fill("secret");
  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("img", { name: "QR code for the authenticator app" })).toBeVisible();
  await shot(page, "account-totp");
});

test("organization", async ({ page }) => {
  await mock(page, true);
  await page.goto("/orgs/ervisio");
  await expect(page.getByText("Open infrastructure tools")).toBeVisible();
  await expect(page.getByText("Luca Bianchi")).toBeVisible();
  await shot(page, "org-members");
  await page.getByRole("tab", { name: "Teams" }).click();
  await expect(page.getByText("Maintainers")).toBeVisible();
  await shot(page, "org-teams");
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(page.getByText("Require two-factor authentication")).toBeVisible();
  await shot(page, "org-settings");
});

test("organization invitation", async ({ page }) => {
  await mock(page, true);
  await page.goto("/org-invite?token=abc");
  await expect(page.getByRole("button", { name: "Accept the invitation" })).toBeVisible();
  await shot(page, "org-invite");
});

test("admin audit", async ({ page }) => {
  await mock(page, true);
  await page.goto("/admin/audit");
  await expect(page.getByText("org.member_added")).toBeVisible();
  await shot(page, "admin-audit");
});
