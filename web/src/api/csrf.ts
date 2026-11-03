// CSRF protection: unsafe requests echo the CSRF cookie in the
// X-CSRF-Token header (see internal/auth). The cookie carries the __Host-
// prefix on secure origins.
export const CSRF_HEADER = "X-CSRF-Token";
const COOKIE_NAMES = ["__Host-erepo_csrf", "erepo_csrf"];
const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

export function csrfToken(cookieHeader: string): string | undefined {
  for (const part of cookieHeader.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    const name = part.slice(0, eq).trim();
    if (COOKIE_NAMES.includes(name)) {
      return decodeURIComponent(part.slice(eq + 1).trim());
    }
  }
  return undefined;
}

export function needsCSRF(method: string): boolean {
  return !SAFE_METHODS.has(method.toUpperCase());
}
