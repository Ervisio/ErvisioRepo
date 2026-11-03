import { expect, test, type Page } from "@playwright/test";

const meta = {
  name: "ErvisioRepo",
  version: "0.1.0",
  commit: "a3f9c21",
  instanceId: "00000000-0000-4000-8000-000000000001",
  publicUrl: "https://git.example.org",
  registration: "invite",
};

const caps = { range: true, presign: false, multipart: false, atomicRename: true, conditionalPut: true, maxConns: 0 };
const storage = {
  targets: [
    { name: "disk", kind: "local", encrypted: false, roles: ["cache:git"], caps },
    { name: "s3-eu", kind: "s3", encrypted: true, roles: ["primary:git", "primary:lfs"], caps: { ...caps, presign: true, multipart: true, atomicRename: false } },
    { name: "box", kind: "sftp", encrypted: true, roles: ["replica:lfs"], caps: { ...caps, maxConns: 8 } },
  ],
  policies: [
    { name: "git", primary: "s3-eu", replicas: [], cache: "disk", cacheTtlSeconds: 86400, cacheShare: 0.6, durability: "sync", objects: 1284, bytes: 734003200, pendingReplicas: 0, errors: 0 },
    { name: "lfs", primary: "s3-eu", replicas: ["box"], cacheTtlSeconds: 0, cacheShare: 0, durability: "async", objects: 312, bytes: 5368709120, pendingReplicas: 4, errors: 1 },
  ],
  caches: [{ target: "disk", budget: 10737418240, used: 2147483648, entries: 902, highWatermark: 0.9, lowWatermark: 0.8, usedBy: { git: 2147483648 } }],
  warnings: [],
};
const check = {
  target: "box",
  ok: false,
  durationMs: 412,
  steps: [
    { name: "put", ok: true, durationMs: 120 },
    { name: "stat", ok: true, durationMs: 31 },
    { name: "get", ok: false, durationMs: 250, error: "read timeout" },
    { name: "delete", ok: true, durationMs: 11 },
  ],
};

async function open(page: Page, path: string, layout: string) {
  await page.addInitScript((l) => localStorage.setItem("ervisio.layout", l), layout);
  await page.route("**/api/v1/meta", (route) => route.fulfill({ json: meta }));
  await page.route("**/api/v1/admin/storage", (route) => route.fulfill({ json: storage }));
  await page.route("**/api/v1/admin/storage/*/test", (route) => route.fulfill({ json: check }));
  await page.goto(path);
  await expect(page.locator("html")).toHaveAttribute("data-layout", layout);
  await page.evaluate(() => document.fonts.ready);
}

// Reference coverage (design/DESIGN.md): layout A at 1280x800 and 390x844, B and C at 1280 only.
const cases = [
  { layout: "console", projects: ["desktop", "mobile"] },
  { layout: "foglio", projects: ["desktop"] },
  { layout: "corsie", projects: ["desktop"] },
];
const pages = [
  { name: "home", path: "/", ready: "Online" },
  { name: "storage", path: "/admin/storage", ready: "Policies" },
  { name: "catalog", path: "/_ui", ready: "Empty state" },
];

for (const { layout, projects } of cases) {
  for (const p of pages) {
    test(`${p.name} ${layout}`, async ({ page }, info) => {
      test.skip(!projects.includes(info.project.name), "not in the reference coverage");
      await open(page, p.path, layout);
      await expect(page.getByText(p.ready, { exact: true }).first()).toBeVisible();
      await expect(page).toHaveScreenshot(`${p.name}-${layout}.png`, { fullPage: true });
    });
  }
}

test("catalog overlays work", async ({ page }, info) => {
  test.skip(info.project.name !== "desktop", "behaviour check, one viewport is enough");
  await open(page, "/_ui", "console");
  await page.getByRole("button", { name: "Open dialog" }).click();
  await expect(page.getByRole("dialog", { name: "Delete repository" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  await page.getByRole("button", { name: "Show toast" }).click();
  await expect(page.getByText("Repository created")).toBeVisible();
  await page.getByRole("button", { name: "Actions" }).click();
  await expect(page.getByRole("menuitem", { name: "Copy clone URL" })).toBeVisible();
});

test("storage connection test shows the steps", async ({ page }) => {
  await open(page, "/admin/storage", "console");
  const card = page.locator("section", { has: page.getByRole("heading", { name: "box" }) });
  await card.getByRole("button", { name: "Test connection" }).click();
  const result = page.getByRole("status", { name: "Connection test of box" });
  await expect(result.getByText("read timeout")).toBeVisible();
  await expect(page).toHaveScreenshot("storage-test.png", { fullPage: true });
});
