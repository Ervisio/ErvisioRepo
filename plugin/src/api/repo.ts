/**
 * Typed client of ErvisioRepo's administration API, reached through the unix socket the server opens on the host
 * (/run/ervisio-repo/api.sock, capabilities.http "repo"). The server adds its per-process secret to every request that
 * comes in on that socket, so no auth header is sent here. Every call resolves with the JSON answer or throws a
 * RepoError; errors from the server arrive as application/problem+json: `detail` becomes the message and a 422 also
 * carries per-field `errors` ({path, msg}, path "" for file-level errors).
 *
 * The socket is 0660 and belongs to the server's user: the "repo" API is declared `admin`, so the first call of a
 * session from a non-root account brings up Ervisio's unlock dialog. statusQuiet() goes through "repo-user", which is
 * not admin: it never prompts and simply fails when the account may not open the socket (used by the widget).
 */
import { getSdk, type PluginError } from '../sdk';
import type { Upstream } from './caddy-core';

export type { Upstream } from './caddy-core';

const BASE = '/api/plugin';

/**
 * - `unavailable`: nothing answers on the socket (not installed, stopped, starting);
 * - `admin`: administrator rights are needed and the user did not unlock them;
 * - `forbidden`: Ervisio refused the call (plugin disabled, socket permission denied);
 * - `invalid`: the server refused the input (HTTP 400/409/413/422);
 * - `failed`: any other error (`status` tells which, e.g. 501 when Actions is off).
 */
export type RepoErrorKind = 'unavailable' | 'admin' | 'forbidden' | 'invalid' | 'failed';

export interface FieldError {
  /** Setting key such as `git.max_push_size`; empty for errors about the whole file. */
  path: string;
  msg: string;
}

export class RepoError extends Error {
  kind: RepoErrorKind;
  /** HTTP status from the server, 0 when the request never reached it. */
  status: number;
  /** Per-field errors of a 422. */
  errors: FieldError[];
  constructor(kind: RepoErrorKind, message: string, status = 0, errors: FieldError[] = []) {
    super(message);
    this.name = 'RepoError';
    this.kind = kind;
    this.status = status;
    this.errors = errors;
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

async function call<T>(method: string, path: string, body?: object, api = 'repo'): Promise<T> {
  let r;
  try {
    r = await getSdk().api.http(api, {
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
    let errors: FieldError[] = [];
    try {
      const p = r.json() as { detail?: string; title?: string; errors?: FieldError[] };
      message = p.detail || p.title || '';
      if (Array.isArray(p.errors)) errors = p.errors;
    } catch {
      message = (r.body ?? '').slice(0, 300);
    }
    message ||= `ErvisioRepo answered ${r.status}`;
    const s = r.status;
    if (s === 502 && path === '/mail/test') throw new RepoError('failed', message, s, errors);
    if (s === 502 || s === 503 || s === 404) throw new RepoError('unavailable', message, s);
    if (s === 400 || s === 409 || s === 413 || s === 422) throw new RepoError('invalid', message, s, errors);
    if (s === 401 || s === 403) throw new RepoError('forbidden', message, s);
    throw new RepoError('failed', message, s, errors);
  }
  if (r.status === 204 || !r.body) return {} as T;
  try {
    return r.json() as T;
  } catch {
    throw new RepoError('failed', 'ErvisioRepo sent an answer that is not JSON', r.status);
  }
}

/* ---------- types ---------- */

export interface RepoStatus {
  version: string;
  commit?: string;
  startedAt?: string;
  /** The address people use to reach the server, empty when unset. */
  publicUrl: string;
  upstream: Upstream;
  sshEnabled: boolean;
  sshListen?: string;
  configPath?: string;
  configWritable: boolean;
  /** Non-empty when config.toml on disk does not parse. */
  configError?: string;
  restartRequired: boolean;
  actionsEnabled: boolean;
  mailEnabled?: boolean;
  storageConfigured: boolean;
  counts?: { repos: number; users: number; orgs: number };
}

export type SettingValue = string | number | boolean | string[];
export type SettingValues = Record<string, SettingValue>;

export interface SettingsDoc {
  /** Flat `section.key` values from the file on disk (from the running config when the file does not parse). */
  values: SettingValues;
  defaults: SettingValues;
  configPath: string;
  configWritable: boolean;
  configError: string;
  restartRequired: boolean;
}

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

export interface RunnerToken {
  token: string;
  expiresAt: string;
  command: string;
}

/* ---------- calls ---------- */

export const status = () => call<RepoStatus>('GET', '/status');
/** Like status() through the non-admin API: never asks to unlock, fails when the socket is not readable. */
export const statusQuiet = () => call<RepoStatus>('GET', '/status', undefined, 'repo-user');

export const getSettings = () => call<SettingsDoc>('GET', '/settings');
/** Writes only the given keys. 422 (RepoError.errors) when a value is refused, nothing written; 409 when read-only. */
export const putSettings = (values: SettingValues) => call<SettingsDoc>('PUT', '/settings', { values });
/** Parses config.toml on disk as the server will at its next start. */
export const checkConfig = () => call<{ ok: boolean; errors: FieldError[] }>('POST', '/config/check');
/** Sends a test email with the mail settings on disk. 422 when mail is off or `to` is invalid; failed/502 with the SMTP error. */
export const testMail = (to: string) => call<{ sent: boolean }>('POST', '/mail/test', { to });

export const storage = () => call<StorageInfo>('GET', '/storage');
export const runners = async () => (await call<{ runners: Runner[] }>('GET', '/runners')).runners;
/** A new instance-wide registration token. 501 when Actions is off. */
export const runnerToken = () => call<RunnerToken>('POST', '/runners/token');

const RUNNER_ID = /^[A-Za-z0-9-]{1,64}$/;
export async function deleteRunner(id: string): Promise<void> {
  if (!RUNNER_ID.test(id)) throw new RepoError('invalid', `Not a runner id: ${id}`);
  await call<object>('DELETE', `/runners/${id}`);
}

/** Sets the server's public address (any absolute http or https URL). A restart applies it. */
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
