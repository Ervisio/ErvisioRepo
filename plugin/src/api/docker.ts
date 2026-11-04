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
  /** Human text such as "Up 6 days" or "Exited (1) 2 hours ago". */
  Status?: string;
  Labels: Record<string, string> | null;
}

export interface ContainerInspect {
  Id: string;
  Name: string;
  State: { Status: string; Running: boolean; ExitCode?: number; StartedAt?: string; FinishedAt?: string };
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

/** Restarts a container (10 s to stop cleanly). */
export async function restart(id: string): Promise<void> {
  const r = await request('POST', `/containers/${checkId(id)}/restart`, { t: '10' });
  if (r.status >= 400) throw toError(r);
}

/** Stops a container; one that is already stopped is fine (Docker answers 304). */
export async function stop(id: string): Promise<void> {
  const r = await request('POST', `/containers/${checkId(id)}/stop`, { t: '10' });
  if (r.status >= 400) throw toError(r);
}

/* ---------- logs ---------- */

const splitLines = (text: string) => text.split('\n').map((l) => l.replace(/\r$/, '')).filter((l) => l !== '');

/** The last `tail` lines of a container's output (stdout and stderr, in order within each stream). */
export async function logs(id: string, tail = 300): Promise<string[]> {
  const r = await request('GET', `/containers/${checkId(id)}/logs`, { stdout: '1', stderr: '1', tail: String(tail) });
  if (r.status >= 400) throw toError(r);
  const { stdout, stderr } = demuxAll(r.bytes());
  return splitLines(stdout + (stderr ? `\n${stderr}` : ''));
}

/**
 * Follows a container's output: the last 300 lines, then new ones as they come. Frames can span chunks, so bytes are
 * buffered until a frame is whole. Call the returned function to stop.
 */
export function followLogs(id: string, onLines: (lines: string[]) => void, onEnd?: (err?: unknown) => void): () => void {
  let closed = false;
  let stream: { close(): void } | undefined;
  let buf = new Uint8Array(0);
  let framed: boolean | undefined;
  let partial = '';
  const dec = new TextDecoder();
  const emit = (text: string) => {
    const all = partial + text;
    const cut = all.lastIndexOf('\n');
    if (cut < 0) {
      partial = all;
      return;
    }
    partial = all.slice(cut + 1);
    const lines = splitLines(all.slice(0, cut));
    if (lines.length) onLines(lines);
  };
  const onData = (chunk: Uint8Array) => {
    const next = new Uint8Array(buf.length + chunk.length);
    next.set(buf);
    next.set(chunk, buf.length);
    buf = next;
    if (framed === undefined && buf.length >= 4) framed = buf[0] <= 2 && buf[1] === 0 && buf[2] === 0 && buf[3] === 0;
    if (framed === false) {
      emit(dec.decode(buf, { stream: true }));
      buf = new Uint8Array(0);
      return;
    }
    let i = 0;
    let text = '';
    while (buf.length - i >= 8) {
      const size = ((buf[i + 4] << 24) >>> 0) + (buf[i + 5] << 16) + (buf[i + 6] << 8) + buf[i + 7];
      if (buf.length - i - 8 < size) break;
      text += dec.decode(buf.subarray(i + 8, i + 8 + size));
      i += 8 + size;
    }
    buf = buf.slice(i);
    if (text) emit(text);
  };
  apiVersion().then(
    (v) => {
      if (closed) return;
      stream = getSdk().api.httpStream(
        NAME,
        { method: 'GET', path: `/v${v}/containers/${checkId(id)}/logs`, query: { follow: '1', stdout: '1', stderr: '1', tail: '300' } },
        {
          onData,
          onEnd: () => {
            if (partial) onLines([partial]);
            partial = '';
            onEnd?.();
          },
          onError: (e) => onEnd?.(e),
        },
      );
    },
    (e) => onEnd?.(e),
  );
  return () => {
    closed = true;
    stream?.close();
  };
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
