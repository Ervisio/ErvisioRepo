/**
 * Typed client of ErvisioRepo's administration API, reached through the unix socket the server opens on the host
 * (/run/ervisio-repo/api.sock, capabilities.http "repo"). The server adds its per-process secret to every request that
 * comes in on that socket, so no auth header is sent here. Every call resolves with the JSON answer or throws a
 * RepoError; errors from the server arrive as application/problem+json and the `detail` becomes the message.
 *
 *   if (await repo.available()) { const s = await repo.status(); ... }
 *
 * The socket is 0660 and belongs to the server's user: the API is declared `admin`, so the first call of a session from
 * a non-root account brings up Ervisio's unlock dialog.
 */
import { getSdk, type PluginError } from '../sdk';
import type { Upstream } from './caddy-core';

export type { Upstream } from './caddy-core';

const NAME = 'repo';
const BASE = '/api/plugin';

/**
 * - `unavailable`: nothing answers on the socket (not installed, stopped, starting);
 * - `admin`: administrator rights are needed and the user did not unlock them;
 * - `forbidden`: Ervisio refused the call (plugin disabled, socket permission denied);
 * - `invalid`: the server refused the input (HTTP 400/409/422);
 * - `failed`: any other error.
 */
export type RepoErrorKind = 'unavailable' | 'admin' | 'forbidden' | 'invalid' | 'failed';

export class RepoError extends Error {
  kind: RepoErrorKind;
  /** HTTP status from the server, 0 when the request never reached it. */
  status: number;
  constructor(kind: RepoErrorKind, message: string, status = 0) {
    super(message);
    this.name = 'RepoError';
    this.kind = kind;
    this.status = status;
  }
}

function fromPluginError(e: unknown): RepoError {
  if (e instanceof RepoError) return e;
  const err = e as Partial<PluginError> | undefined;
  const message = err?.message ?? String(e);
  switch (err?.code) {
    case 'needs_admin':
      return new RepoError('admin', message);
    case 'forbidden':
      return new RepoError('forbidden', message);
    case 'invalid':
      return new RepoError('invalid', message);
    case 'not_found':
    case 'unavailable':
      return new RepoError('unavailable', message);
    default:
      return new RepoError('failed', message);
  }
}

async function call<T>(method: string, path: string, body?: object): Promise<T> {
  let r;
  try {
    r = await getSdk().api.http(NAME, {
      method,
      path: `${BASE}${path}`,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body,
    });
  } catch (e) {
    throw fromPluginError(e);
  }
  if (r.status >= 400) {
    let message = '';
    try {
      message = (r.json() as { detail?: string }).detail ?? '';
    } catch {
      message = (r.body ?? '').slice(0, 300);
    }
    message ||= `ErvisioRepo answered ${r.status}`;
    if (r.status === 502 || r.status === 503 || r.status === 404) throw new RepoError('unavailable', message, r.status);
    if (r.status === 400 || r.status === 409 || r.status === 413 || r.status === 422) throw new RepoError('invalid', message, r.status);
    if (r.status === 401 || r.status === 403) throw new RepoError('forbidden', message, r.status);
    throw new RepoError('failed', message, r.status);
  }
  try {
    return (r.body ? r.json() : {}) as T;
  } catch {
    throw new RepoError('failed', 'ErvisioRepo sent an answer that is not JSON', r.status);
  }
}

/* ---------- types ---------- */

export interface RepoStatus {
  version: string;
  commit?: string;
  /** The address people use to reach the server, empty when unset. */
  publicUrl: string;
  upstream: Upstream;
  sshEnabled: boolean;
  configWritable: boolean;
  restartRequired: boolean;
  actionsEnabled: boolean;
  storageConfigured: boolean;
}

export interface Limits {
  maxPushSize: string;
  gitIdleTimeout: string;
  gitMaxDuration: string;
  gitRequestsPerHour: number;
}

export interface Settings {
  publicUrl: string;
  registration: string;
  userDirectory: string;
  require2fa: string;
  actionsEnabled: boolean;
  defaultActionsUrl: string;
  limits: Limits;
  restartRequired: boolean;
}

/** Fields left out are kept as they are. */
export type SettingsPatch = Partial<Omit<Settings, 'limits' | 'restartRequired'>> & { limits?: Partial<Limits> };

export interface StorageTarget {
  name: string;
  kind: string;
  encrypted: boolean;
  roles: string[];
  budget?: number;
}

export interface StoragePolicy {
  name: string;
  primary: string;
  replicas: string[];
  cache?: string;
  durability: string;
  objects: number;
  bytes: number;
  pendingReplicas: number;
  errors: number;
}

export interface StorageCache {
  target: string;
  budget: number;
  used: number;
  entries: number;
}

export interface StorageInfo {
  targets: StorageTarget[];
  policies: StoragePolicy[];
  caches: StorageCache[];
  warnings: { code: string; policy?: string }[];
}

export interface Runner {
  id: string;
  name: string;
  labels: string[];
  version: string;
  ephemeral: boolean;
  online: boolean;
  lastSeenAt: string | null;
}

/* ---------- calls ---------- */

export const status = () => call<RepoStatus>('GET', '/status');
export const getSettings = () => call<Settings>('GET', '/settings');
export const updateSettings = (patch: SettingsPatch) => call<Settings>('PUT', '/settings', patch);
export const storage = () => call<StorageInfo>('GET', '/storage');
export const runners = async () => (await call<{ runners: Runner[] }>('GET', '/runners')).runners;

/** Sets the server's public address (https, or http on a loopback host). A restart applies it. */
export const setPublicUrl = (url: string) => call<{ publicUrl: string; restartRequired: boolean }>('PUT', '/public-url', { url });

/**
 * True when a server answers on the socket. False when none does (not installed, stopped); throws a RepoError for
 * `admin` (rights not unlocked) and `forbidden`, which the page shows as such.
 */
export async function available(): Promise<boolean> {
  try {
    await status();
    return true;
  } catch (e) {
    if (e instanceof RepoError && e.kind === 'unavailable') return false;
    throw e;
  }
}
