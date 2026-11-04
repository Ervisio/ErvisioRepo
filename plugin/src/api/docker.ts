/**
 * The part of the Docker Engine API this plugin needs, over sdk.api.http('docker', ...): list, inspect and start
 * containers, run a command in one (exec) and attach a container to the server's network. The API version is negotiated once
 * with GET /version; paths are passed without the version prefix.
 *
 * Adapted from Ervisio/plugin-caddy (src/api/docker.ts), MIT License, Copyright (c) 2026 Fonlogen.
 */
import { getSdk, type HttpResponse, type Query } from '../sdk';

const NAME = 'docker';

export class DockerError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'DockerError';
    this.status = status;
  }
}

let version: Promise<string> | undefined;

function apiVersion(): Promise<string> {
  if (!version) {
    version = getSdk()
      .api.http(NAME, { method: 'GET', path: '/version' })
      .then((r) => {
        if (r.status >= 400) throw toError(r);
        return (r.json() as { ApiVersion: string }).ApiVersion;
      })
      .catch((e) => {
        version = undefined;
        throw e;
      });
  }
  return version;
}

function toError(r: HttpResponse): DockerError {
  let msg = '';
  try {
    msg = (r.json() as { message?: string }).message ?? '';
  } catch {
    msg = r.body?.slice(0, 300) ?? '';
  }
  return new DockerError(r.status, msg || `Docker answered ${r.status}`);
}

async function request(method: string, path: string, query?: Query, body?: object): Promise<HttpResponse> {
  const v = await apiVersion();
  return getSdk().api.http(NAME, {
    method,
    path: `/v${v}${path}`,
    query,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body,
  });
}

async function json<T>(method: string, path: string, query?: Query, body?: object): Promise<T> {
  const r = await request(method, path, query, body);
  if (r.status >= 400) throw toError(r);
  return (r.body ? r.json() : undefined) as T;
}

/* ---------- types (only the fields used) ---------- */

export interface ContainerSummary {
  Id: string;
  Names: string[];
  Image: string;
  Command: string;
  State: string;
  Labels: Record<string, string> | null;
}

export interface ContainerInspect {
  Id: string;
  Name: string;
  State: { Status: string; Running: boolean };
  Config: { Image: string; Cmd: string[] | null; Entrypoint: string[] | null; Env: string[] | null; Labels: Record<string, string> | null };
  HostConfig?: { NetworkMode?: string };
  Mounts: { Type: string; Source: string; Destination: string; RW: boolean; Name?: string }[];
  NetworkSettings: { Networks: Record<string, unknown> | null };
}

const HEX_ID = /^[a-f0-9]{12,64}$/;
const checkId = (id: string) => {
  if (!HEX_ID.test(id)) throw new DockerError(400, `Not a container id: ${id}`);
  return id;
};

export const listContainers = () => json<ContainerSummary[]>('GET', '/containers/json', { all: '1' });
export const inspect = (id: string) => json<ContainerInspect>('GET', `/containers/${checkId(id)}/json`);

/**
 * Attaches a container to a network (only ervisiorepo_edge is allowed by the manifest). Resolves `false` when it was
 * already attached.
 */
export async function connectNetwork(network: string, container: string): Promise<boolean> {
  const r = await request('POST', `/networks/${encodeURIComponent(network)}/connect`, undefined, { Container: checkId(container) });
  if (r.status < 400) return true;
  const err = toError(r);
  if (/already exists in network|already attached|already connected|endpoint with name .* already exists/i.test(err.message)) return false;
  throw err;
}

/** Starts a container; a container that already runs is fine (Docker answers 304). */
export async function start(id: string): Promise<void> {
  const r = await request('POST', `/containers/${checkId(id)}/start`);
  if (r.status >= 400) throw toError(r);
}

/* ---------- exec ---------- */

/** Splits Docker's multiplexed frames: an 8-byte header [stream, 0, 0, 0, size uint32 BE] then `size` bytes. */
function demuxAll(bytes: Uint8Array): { stdout: string; stderr: string } {
  const framed = bytes.length >= 8 && bytes[0] <= 2 && bytes[1] === 0 && bytes[2] === 0 && bytes[3] === 0;
  const dec = new TextDecoder();
  if (!framed) return { stdout: dec.decode(bytes), stderr: '' };
  const out: Uint8Array[] = [];
  const err: Uint8Array[] = [];
  let i = 0;
  while (bytes.length - i >= 8) {
    const size = ((bytes[i + 4] << 24) >>> 0) + (bytes[i + 5] << 16) + (bytes[i + 6] << 8) + bytes[i + 7];
    if (bytes.length - i - 8 < size) break;
    (bytes[i] === 2 ? err : out).push(bytes.subarray(i + 8, i + 8 + size));
    i += 8 + size;
  }
  const join = (parts: Uint8Array[]) => {
    const all = new Uint8Array(parts.reduce((a, p) => a + p.length, 0));
    let k = 0;
    for (const p of parts) {
      all.set(p, k);
      k += p.length;
    }
    return dec.decode(all);
  };
  return { stdout: join(out), stderr: join(err) };
}

export interface ExecOutput {
  exitCode: number;
  stdout: string;
  stderr: string;
}

/** Runs a command inside a container and waits for it. */
export async function execIn(container: string, cmd: string[], env?: string[]): Promise<ExecOutput> {
  const created = await json<{ Id: string }>('POST', `/containers/${checkId(container)}/exec`, undefined, {
    AttachStdout: true,
    AttachStderr: true,
    Tty: false,
    Cmd: cmd,
    Env: env,
  });
  const r = await request('POST', `/exec/${created.Id}/start`, undefined, { Detach: false, Tty: false });
  if (r.status >= 400) throw toError(r);
  const { stdout, stderr } = demuxAll(r.bytes());
  const info = await json<{ ExitCode: number | null }>('GET', `/exec/${created.Id}/json`);
  return { exitCode: info.ExitCode ?? -1, stdout, stderr };
}

export const resetDocker = () => {
  version = undefined;
};
