// CSP nonce handed over by the server (see internal/httpapi/spa.go). Vite emits
// <meta property="csp-nonce" nonce="…"> because of html.cspNonce in vite.config.ts.
// Browsers hide the nonce attribute after parsing, so it is read via the property.

let cached: string | undefined;

/** Returns the CSP nonce of the page, or undefined (dev server, tests). */
export function cspNonce(): string | undefined {
  if (cached !== undefined || typeof document === "undefined") return cached || undefined;
  const meta = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]');
  const value = meta?.nonce || meta?.getAttribute("nonce") || "";
  // The placeholder survives only when index.html is served without the Go server.
  cached = value.startsWith("__") ? "" : value;
  return cached || undefined;
}

declare global {
  // Read by get-nonce (react-style-singleton), which Radix uses for its scroll-lock <style>.
  var __webpack_nonce__: string | undefined;
}

/** Makes the nonce available to the libraries that inject <style> tags. */
export function initCspNonce(): void {
  const n = cspNonce();
  if (n) globalThis.__webpack_nonce__ = n;
}
