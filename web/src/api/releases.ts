import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PAGE_SIZE, qs, repoBase } from "./code.ts";
import { CSRF_HEADER, csrfToken } from "./csrf.ts";
import { ApiError, request } from "./http.ts";
import type { components } from "./schema";

// Releases and their assets.
type S = components["schemas"];
export type Release = S["Release"];
export type ReleaseAsset = S["ReleaseAsset"];
export type ReleaseCreate = S["ReleaseCreate"];
export type ReleaseUpdate = S["ReleaseUpdate"];

const enc = encodeURIComponent;
const relBase = (owner: string, name: string) => `${repoBase(owner, name)}/releases`;
const key = (owner: string, name: string, ...rest: unknown[]) => ["releases", owner, name, ...rest];

export const useReleases = (owner: string, name: string, page: number) =>
  useQuery({
    queryKey: key(owner, name, "list", page),
    queryFn: () => request<Release[]>("GET", `${relBase(owner, name)}${qs({ offset: page * PAGE_SIZE, limit: PAGE_SIZE })}`),
    placeholderData: keepPreviousData,
  });

export const useRelease = (owner: string, name: string, id: string) =>
  useQuery({ queryKey: key(owner, name, "one", id), queryFn: () => request<Release>("GET", `${relBase(owner, name)}/${enc(id)}`) });

function useReleaseMutation<V, R>(owner: string, name: string, fn: (v: V) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ["releases", owner, name] }) });
}

export const useCreateRelease = (owner: string, name: string) =>
  useReleaseMutation<ReleaseCreate, Release>(owner, name, (b) => request("POST", relBase(owner, name), b));
export const useUpdateRelease = (owner: string, name: string, id: string) =>
  useReleaseMutation<ReleaseUpdate, Release>(owner, name, (b) => request("PATCH", `${relBase(owner, name)}/${enc(id)}`, b));
export const useDeleteRelease = (owner: string, name: string, id: string) =>
  useReleaseMutation<void, void>(owner, name, () => request("DELETE", `${relBase(owner, name)}/${enc(id)}`));
export const useDeleteAsset = (owner: string, name: string, id: string) =>
  useReleaseMutation<string, void>(owner, name, (assetId) =>
    request("DELETE", `${relBase(owner, name)}/${enc(id)}/assets/${enc(assetId)}`),
  );

/** Download URL of an asset (streamed route, not in the OpenAPI document). */
export const assetDownloadUrl = (owner: string, name: string, id: string, assetId: string) =>
  `/api/v1${relBase(owner, name)}/${enc(id)}/assets/${enc(assetId)}/download`;

/**
 * Uploads one asset as the raw request body. XMLHttpRequest instead of fetch because
 * fetch has no upload progress events.
 */
export function uploadAsset(
  owner: string,
  name: string,
  id: string,
  file: File,
  label: string,
  onProgress: (fraction: number) => void,
  signal?: AbortSignal,
): Promise<ReleaseAsset> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1${relBase(owner, name)}/${enc(id)}/assets${qs({ name: file.name, label })}`);
    xhr.setRequestHeader("Accept", "application/json, application/problem+json");
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    const token = csrfToken(document.cookie);
    if (token) xhr.setRequestHeader(CSRF_HEADER, token);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as ReleaseAsset);
        } catch (e) {
          reject(e instanceof Error ? e : new Error(String(e)));
        }
        return;
      }
      let title = `HTTP ${xhr.status}`;
      let detail: string | undefined;
      try {
        const p = JSON.parse(xhr.responseText) as { title?: string; detail?: string };
        if (p.title) title = p.title;
        detail = p.detail;
      } catch {
        // not a JSON problem document
      }
      reject(new ApiError(xhr.status, title, detail));
    };
    xhr.onerror = () => reject(new ApiError(0, "Network error"));
    xhr.onabort = () => reject(new DOMException("Upload aborted", "AbortError"));
    signal?.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(file);
  });
}

/** Invalidates the release caches after an upload done outside react-query. */
export function useInvalidateReleases(owner: string, name: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["releases", owner, name] });
}
