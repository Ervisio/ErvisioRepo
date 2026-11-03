import { CSRF_HEADER, csrfToken, needsCSRF } from "./csrf.ts";

/** Problem document (RFC 9457) returned by the API, as a thrown error. */
export class ApiError extends Error {
  readonly status: number;
  readonly title: string;
  readonly detail?: string;
  readonly retryAfter?: number;

  constructor(status: number, title: string, detail?: string, retryAfter?: number) {
    super(detail ?? title);
    this.name = "ApiError";
    this.status = status;
    this.title = title;
    this.detail = detail;
    this.retryAfter = retryAfter;
  }
}

/**
 * JSON request against /api/v1 for endpoints that are not (yet) in the generated client.
 * Sends the CSRF header on unsafe methods; throws ApiError on any non-2xx answer.
 */
export async function request<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json, application/problem+json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (needsCSRF(method) && typeof document !== "undefined") {
    const token = csrfToken(document.cookie);
    if (token) headers[CSRF_HEADER] = token;
  }
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw await toError(res);
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

async function toError(res: Response): Promise<ApiError> {
  const retry = Number(res.headers.get("Retry-After"));
  let title = `HTTP ${res.status}`;
  let detail: string | undefined;
  try {
    const p = (await res.json()) as { title?: string; detail?: string };
    if (p.title) title = p.title;
    detail = p.detail;
  } catch {
    // not a JSON problem document
  }
  return new ApiError(res.status, title, detail, Number.isFinite(retry) && retry > 0 ? retry : undefined);
}

export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
