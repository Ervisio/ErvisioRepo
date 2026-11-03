import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// web/dist is embedded in the Go binary and must always contain .keep.
function keepFile(): Plugin {
  return {
    name: "keep-dist-placeholder",
    apply: "build",
    closeBundle() {
      writeFileSync(resolve(import.meta.dirname, "dist/.keep"), "");
    },
  };
}

const backend = "http://127.0.0.1:3400";

export default defineConfig({
  plugins: [react(), keepFile()],
  // The Go server replaces this placeholder with a fresh nonce per response and allows it in the
  // CSP style-src, so that the <style> tags injected at runtime by Radix (scroll lock, select
  // viewport) are applied without 'unsafe-inline'. See internal/httpapi/spa.go.
  html: { cspNonce: "__EREPO_CSP_NONCE__" },
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    port: 5173,
    proxy: { "/api": backend, "/auth/oidc": backend, "/healthz": backend, "/readyz": backend },
  },
});
