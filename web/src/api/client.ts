import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema";
import { CSRF_HEADER, csrfToken, needsCSRF } from "./csrf";

const csrf: Middleware = {
  onRequest({ request }) {
    if (needsCSRF(request.method) && typeof document !== "undefined") {
      const token = csrfToken(document.cookie);
      if (token) request.headers.set(CSRF_HEADER, token);
    }
    return request;
  },
};

export const api = createClient<paths>({ baseUrl: "/api/v1", credentials: "same-origin" });
api.use(csrf);
