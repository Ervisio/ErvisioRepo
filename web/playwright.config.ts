import { defineConfig } from "@playwright/test";

// Visual reference tests. Screenshots depend on the OS font stack, so baselines are per platform
// (<name>-<project>-<platform>.png); regenerate them on the CI platform with `npm run test:visual -- --update-snapshots`.
export default defineConfig({
  testDir: "tests/visual",
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}-{projectName}{-snapshotSuffix}{ext}",
  fullyParallel: true,
  reporter: [["list"]],
  expect: { toHaveScreenshot: { animations: "disabled", maxDiffPixelRatio: 0.01 } },
  use: { baseURL: "http://127.0.0.1:4173", colorScheme: "dark", locale: "en-US", reducedMotion: "reduce" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 800 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 }, deviceScaleFactor: 1 } },
  ],
  webServer: {
    command: "npx vite --host 127.0.0.1 --port 4173 --strictPort",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
