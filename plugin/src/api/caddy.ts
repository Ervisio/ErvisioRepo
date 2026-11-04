/**
 * Publishing ErvisioRepo on its own web address through Caddy, installed on the system (caddy.service) or in a
 * Docker container.
 *
 *   const p = await propose();                  // server reachable + Caddy found → { instance, suggestion, current }
 *   const r = await apply(p.instance!, 'git.example.com', p.server!.upstream);
 *   if (!r.ok) show(r.step, r.message, r.snippet); // snippet = the block to paste by hand
 *
 * Our site lives in its own file, `ervisio-repo.caddy` next to the Caddyfile, loaded by one `import` line appended to
 * the Caddyfile. In a container whose config folder is not mounted (only the Caddyfile is, or nothing), a new file
 * would vanish when the container is recreated and the import would then stop Caddy, so the block goes inside the
 * Caddyfile between marker comments instead (layout "inline").
 *
 * Every change: check the host name, read the files, write, `caddy validate`, reload; if the check or the reload
 * fails the files are written back exactly as they were (a file that did not exist is removed) and Caddy reloads.
 * Only then is the server told its new public address.
 */
import { getSdk, type ExecResult, type PluginError } from '../sdk';
import {
  REPO_FILE,
  adapterFromArgv,
  cleanOutput,
  configFromArgv,
  dockerProxyLabels,
  editBlocker,
  ensureImport,
  generateBlock,
  hostTaken,
  importsCovering,
  normalizeHost,
  parseManaged,
  proxyTarget,
  readInline,
  removeImport,
  removeInline,
  scan,
  siteHosts,
  suggestHost as suggestFrom,
  upsertInline,
  MARKER,
  type Suggestion,
  type Upstream,
} from './caddy-core';
import { connectNetwork, execIn, inspect, listContainers, type ContainerInspect, type ContainerSummary } from './docker';
import * as repo from './repo';

export type { Suggestion, Upstream } from './caddy-core';

/* ---------- instances ---------- */

export type InstanceKind = 'system' | 'docker';

/**
 * Why the plugin will not edit this Caddy (it still offers the snippet to paste):
 * - `json`: Caddy runs from a JSON config (or --resume), not a Caddyfile;
 * - `path`: the system Caddyfile is outside /etc/caddy, where the plugin may not write;
 * - `labels`: caddy-docker-proxy, configured with container labels;
 * - `stopped`: the container is not running, so nothing can run inside it;
 * - `readonly`: the container's Caddyfile is on a read-only mount.
 */
export type Unsupported = 'json' | 'path' | 'labels' | 'stopped' | 'readonly';

export interface CaddyInstance {
  /** "system" or "docker-<container name>". */
  key: string;
  kind: InstanceKind;
  name: string;
  running: boolean;
  /** The Caddyfile as Caddy sees it (inside the container for Docker). */
  configPath: string;
  /** "file": ervisio-repo.caddy next to the Caddyfile plus an import line; "inline": inside the Caddyfile. */
  layout: 'file' | 'inline';
  /** Docker with network_mode host: reaches the server on the host port, like a system Caddy. */
  hostNetwork?: boolean;
  /** Docker without any mount for the Caddyfile: changes last until the container is recreated. */
  ephemeral?: boolean;
  container?: { id: string; name: string; image: string; networks: string[] };
  unsupported?: Unsupported;
}

export interface Detection {
  instances: CaddyInstance[];
  docker: { present: boolean; direct: boolean };
  /** Docker exists but needs an unlock prompt the caller did not allow (`askDocker: false`). */
  dockerSkipped: boolean;
  dockerError?: string;
}

const dirOf = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/';
const run = (command: string, args: string[] = []): Promise<ExecResult> => getSdk().api.exec(command, args);
const errText = (e: unknown) => (e as Error)?.message ?? String(e);
const isMissing = (e: unknown) => {
  const err = e as Partial<PluginError>;
  return err?.code === 'not_found' || /no such file|not found|does not exist/i.test(err?.message ?? '');
};

function prop(out: string, name: string): string {
  return out.match(new RegExp(`^${name}=(.*)$`, 'm'))?.[1].trim() ?? '';
}

async function detectSystem(): Promise<CaddyInstance | null> {
  let out: string;
  try {
    out = (await run('service-show')).stdout;
  } catch {
    return null;
  }
  if (prop(out, 'LoadState') !== 'loaded') return null;
  const argv = (prop(out, 'ExecStart').match(/argv\[\]=([^;]*)/)?.[1] ?? '').trim().split(/\s+/);
  const configPath = configFromArgv(argv);
  let unsupported: Unsupported | undefined;
  if (argv.includes('--resume') || adapterFromArgv(argv, configPath) !== 'caddyfile') unsupported = 'json';
  else if (!/^\/etc\/caddy\/[A-Za-z0-9._-]{1,100}(\/[A-Za-z0-9._-]{1,100})?$/.test(configPath)) unsupported = 'path';
  return {
    key: 'system',
    kind: 'system',
    name: 'Caddy',
    running: prop(out, 'ActiveState') === 'active',
    configPath,
    layout: 'file',
    unsupported,
  };
}

async function dockerAccess(): Promise<{ present: boolean; direct: boolean }> {
  try {
    if ((await run('has-docker')).exitCode !== 0) return { present: false, direct: false };
  } catch {
    return { present: false, direct: false };
  }
  try {
    const [u, g] = await Promise.all([run('user'), run('groups')]);
    return { present: true, direct: u.stdout.trim() === 'root' || g.stdout.trim().split(/\s+/).includes('docker') };
  } catch {
    return { present: true, direct: false };
  }
}

const PROXY_IMAGE = /caddy-docker-proxy/i;

/** A container that runs Caddy: its image is caddy (any registry or tag), or its command is caddy. */
export function looksLikeCaddy(c: ContainerSummary): boolean {
  const img = c.Image.toLowerCase();
  const base = img.split('@')[0].split('/').pop()!.split(':')[0];
  if (base === 'caddy' || base.startsWith('caddy-') || base.endsWith('-caddy') || PROXY_IMAGE.test(img)) return true;
  return /^(\/usr\/bin\/)?caddy\s+(run|docker-proxy)/.test(c.Command.replace(/^"|"$/g, ''));
}

export function instanceFromInspect(c: ContainerInspect): CaddyInstance {
  const name = c.Name.replace(/^\//, '');
  const argv = [...(c.Config.Entrypoint ?? []), ...(c.Config.Cmd ?? [])];
  const configPath = configFromArgv(argv);
  const dir = dirOf(configPath);
  const covers = (dest: string, p: string) => p === dest || p.startsWith(dest.replace(/\/$/, '') + '/');
  const dirMounted = c.Mounts.some((m) => covers(m.Destination, dir));
  const fileMounted = c.Mounts.some((m) => m.Destination === configPath);
  const closest = c.Mounts.filter((m) => covers(m.Destination, configPath)).sort((a, b) => b.Destination.length - a.Destination.length)[0];
  let unsupported: Unsupported | undefined;
  if (PROXY_IMAGE.test(c.Config.Image) || argv.includes('docker-proxy')) unsupported = 'labels';
  else if (argv.includes('--resume') || adapterFromArgv(argv, configPath) !== 'caddyfile') unsupported = 'json';
  else if (!c.State.Running) unsupported = 'stopped';
  else if (closest && !closest.RW) unsupported = 'readonly';
  return {
    key: `docker-${name}`,
    kind: 'docker',
    name,
    running: c.State.Running,
    configPath,
    layout: dirMounted ? 'file' : 'inline',
    hostNetwork: c.HostConfig?.NetworkMode === 'host',
    ephemeral: !dirMounted && !fileMounted,
    container: { id: c.Id, name, image: c.Config.Image, networks: Object.keys(c.NetworkSettings.Networks ?? {}) },
    unsupported,
  };
}

/**
 * Finds every Caddy on this machine. Docker is asked only when its socket exists, and without an unlock prompt unless
 * `askDocker` (members of the docker group and root use the socket as themselves).
 */
export async function detect(opts: { askDocker?: boolean } = {}): Promise<Detection> {
  const [sys, docker] = await Promise.all([detectSystem(), dockerAccess()]);
  const instances: CaddyInstance[] = sys ? [sys] : [];
  let dockerSkipped = false;
  let dockerError: string | undefined;
  if (docker.present && (docker.direct || opts.askDocker)) {
    try {
      const found = (await listContainers()).filter(looksLikeCaddy);
      const list = await Promise.all(found.map((c) => inspect(c.Id).then(instanceFromInspect)));
      instances.push(...list.sort((a, b) => Number(b.running) - Number(a.running) || a.name.localeCompare(b.name)));
    } catch (e) {
      dockerError = errText(e);
    }
  } else if (docker.present) dockerSkipped = true;
  return { instances, docker, dockerSkipped, dockerError };
}

/* ---------- reading and writing, for both kinds ---------- */

interface Outcome {
  ok: boolean;
  output: string;
}

interface Backend {
  /** null when the file does not exist. */
  read(path: string): Promise<string | null>;
  write(path: string, text: string): Promise<void>;
  remove(path: string): Promise<void>;
  /** Files in the Caddyfile's folder and one level below (for imports). */
  files(): Promise<string[]>;
  validate(): Promise<Outcome>;
  reload(): Promise<Outcome>;
}

function systemBackend(inst: CaddyInstance): Backend {
  const fs = () => getSdk().files;
  return {
    async read(path) {
      try {
        return await fs().read(path);
      } catch (e) {
        if (isMissing(e)) return null;
        throw e;
      }
    },
    write: (path, text) => fs().write(path, text),
    async remove(path) {
      try {
        await fs().remove(path);
      } catch (e) {
        if (!isMissing(e)) throw e;
      }
    },
    async files() {
      const out: string[] = [];
      const walk = async (dir: string, depth: number) => {
        for (const e of await fs().list(dir)) {
          if (e.name.startsWith('.')) continue;
          const p = `${dir}/${e.name}`;
          if (e.type === 'file') out.push(p);
          else if (e.type === 'dir' && depth < 1) await walk(p, depth + 1);
        }
      };
      await walk(dirOf(inst.configPath), 0);
      return out.sort();
    },
    async validate() {
      const r = await run('validate', [inst.configPath]);
      return { ok: r.exitCode === 0, output: (r.stderr + r.stdout).trim() };
    },
    async reload() {
      const r = await run('reload');
      return { ok: r.exitCode === 0, output: (r.stderr || r.stdout).trim() };
    },
  };
}

function toBase64(bytes: Uint8Array): string {
  let s = '';
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

/** Inside the container with exec (adapted from Ervisio/plugin-caddy's DockerBackend, MIT). */
function dockerBackend(inst: CaddyInstance): Backend {
  const id = inst.container!.id;
  const sh = (script: string, args: string[] = []) => execIn(id, ['sh', '-c', script, 'sh', ...args]);
  return {
    async read(path) {
      const r = await execIn(id, ['cat', path]);
      if (r.exitCode === 0) return r.stdout;
      if (/No such file/i.test(r.stderr)) return null;
      throw new Error(r.stderr.trim() || `Cannot read ${path}`);
    },
    /** Written in pieces (an exec argument is at most 128 KiB), then copied over the file with `cat >` so a Caddyfile
     *  mounted as a single file keeps its inode (a rename fails on a bind mount). */
    async write(path, text) {
      const b64 = toBase64(new TextEncoder().encode(text));
      const tmp = `${path}.ervisio-tmp`;
      const step = 96 * 1024;
      for (let i = 0; i === 0 || i < b64.length; i += step) {
        const r = await sh(`printf %s "$1" | base64 -d ${i === 0 ? '>' : '>>'} "$2"`, [b64.slice(i, i + step), tmp]);
        if (r.exitCode !== 0) throw new Error(r.stderr.trim() || `Cannot write ${path}`);
      }
      const r = await sh('cat "$1" > "$2" && rm -f "$1"', [tmp, path]);
      if (r.exitCode !== 0) throw new Error(r.stderr.trim() || `Cannot write ${path}`);
    },
    async remove(path) {
      const r = await execIn(id, ['rm', '-f', path]);
      if (r.exitCode !== 0) throw new Error(r.stderr.trim() || `Cannot remove ${path}`);
    },
    async files() {
      const r = await execIn(id, ['find', dirOf(inst.configPath), '-maxdepth', '2', '-type', 'f', '!', '-name', '.*', '!', '-name', '*.ervisio-tmp']);
      return r.stdout.split('\n').map((l) => l.trim()).filter(Boolean).sort();
    },
    async validate() {
      const r = await execIn(id, ['caddy', 'validate', '--config', inst.configPath, '--adapter', 'caddyfile'], [
        'XDG_DATA_HOME=/tmp/ervisio-repo-caddy/data',
        'XDG_CONFIG_HOME=/tmp/ervisio-repo-caddy/config',
      ]);
      return { ok: r.exitCode === 0, output: (r.stderr + r.stdout).trim() };
    },
    async reload() {
      const r = await execIn(id, ['caddy', 'reload', '--config', inst.configPath, '--adapter', 'caddyfile', '--force']);
      return { ok: r.exitCode === 0, output: (r.stderr + r.stdout).trim() };
    },
  };
}

const backendFor = (inst: CaddyInstance): Backend => (inst.kind === 'docker' ? dockerBackend(inst) : systemBackend(inst));

/** Path of our own file for this instance. */
export const repoFilePath = (inst: CaddyInstance) => `${dirOf(inst.configPath)}/${REPO_FILE}`;

/** Where this Caddy sends requests for the server. */
export function targetFor(inst: CaddyInstance, upstream: Upstream): string {
  return proxyTarget(inst.kind === 'docker' && !inst.hostNetwork ? 'docker' : 'system', upstream);
}

/* ---------- reading what is there ---------- */

export interface Current {
  host: string;
  target: string;
  layout: 'file' | 'inline';
  /** The file or Caddyfile that holds the block. */
  path: string;
  /** layout "file": the main Caddyfile loads the file (else the block is written but not served). */
  imported: boolean;
}

/** Host names of every site the Caddyfile and the files it imports serve, and our own block if any. */
async function inventory(inst: CaddyInstance, b: Backend, main: string): Promise<{ hosts: string[]; own: Current | null }> {
  const r = scan(main);
  if (!r.ok) throw new Error(r.error);
  const ownFile = repoFilePath(inst);
  let hosts = siteHosts(r.outline);
  let own: Current | null = null;

  const inline = (() => {
    try {
      return readInline(main);
    } catch {
      return null;
    }
  })();
  if (inline) {
    const m = parseManaged(inline);
    if (m) own = { ...m, layout: 'inline', path: inst.configPath, imported: true };
  }

  let all: string[] = [];
  if (r.outline.imports.length) all = await b.files().catch(() => [] as string[]);
  const imported = new Set<string>();
  for (const f of all) if (f !== inst.configPath && importsCovering(r.outline, inst.configPath, f).length) imported.add(f);

  const ownText = await b.read(ownFile).catch(() => null);
  if (ownText !== null) {
    const m = parseManaged(ownText);
    if (m && !own) own = { ...m, layout: 'file', path: ownFile, imported: imported.has(ownFile) };
  }
  for (const f of imported) {
    if (f === ownFile) continue;
    const text = await b.read(f).catch(() => null);
    if (text === null) continue;
    const s = scan(text);
    if (s.ok) hosts = hosts.concat(siteHosts(s.outline));
  }
  // Our own host is not "taken": hosts of an inline block are in the main file's list, drop them once.
  if (own) {
    const i = hosts.indexOf(own.host);
    if (i >= 0 && own.layout === 'inline') hosts.splice(i, 1);
  }
  return { hosts, own };
}

/** The plugin can read this Caddy's Caddyfile (even when it may not change it). */
const readable = (inst: CaddyInstance) => inst.unsupported !== 'json' && inst.unsupported !== 'stopped' && inst.unsupported !== 'labels';

/** Our managed block on this Caddy, or null when there is none. */
export async function current(inst: CaddyInstance): Promise<Current | null> {
  if (!readable(inst)) return null;
  const b = backendFor(inst);
  const main = await b.read(inst.configPath);
  if (main === null) return null;
  return (await inventory(inst, b, main)).own;
}

/**
 * git.<base domain> from the sites this Caddy already serves (e.g. ervisio.example.com and shop.example.com →
 * git.example.com), another label when that name is taken; null when no public domain is found.
 */
export async function suggestHost(inst: CaddyInstance): Promise<Suggestion | null> {
  if (!readable(inst)) return null;
  const b = backendFor(inst);
  const main = await b.read(inst.configPath).catch(() => null);
  if (main === null) return null;
  try {
    const { hosts, own } = await inventory(inst, b, main);
    return suggestFrom(own ? [...hosts, own.host] : hosts, own?.host);
  } catch {
    return null;
  }
}

/* ---------- changing Caddy ---------- */

export type Step = 'host' | 'unsupported' | 'read' | 'conflict' | 'network' | 'write' | 'validate' | 'reload' | 'public-url' | 'done';

export interface CaddyResult {
  ok: boolean;
  /** Where it stopped ("done" on success). */
  step: Step;
  message: string;
  /** What to paste by hand when the plugin cannot or may not edit the config (Caddyfile block, or labels). */
  snippet: string | null;
  /** The normalized host name. */
  host?: string;
  /** The reverse_proxy upstream used. */
  target?: string;
  /** For `unsupported`. */
  reason?: Unsupported | 'unsafe' | 'not-ours';
  /** The files were written back as they were after a failed check or reload. */
  rolledBack?: boolean;
  /** Caddy was stopped: the change is checked and waits for its next start. */
  notRunning?: boolean;
  /** Caddy serves the server but the server's public address could not be set (step `public-url`). */
  caddyApplied?: boolean;
  warnings: string[];
}

const UNSUPPORTED_TEXT: Record<Unsupported, string> = {
  json: 'This Caddy runs from a JSON config, not a Caddyfile: add the site yourself.',
  path: 'The Caddyfile is outside /etc/caddy, where the plugin may not write: add the site yourself.',
  labels: 'This Caddy is caddy-docker-proxy and reads sites from container labels: add these labels to the server.',
  stopped: 'The Caddy container is not running.',
  readonly: "The container's Caddyfile is mounted read-only: add the site yourself.",
};

type Files = Record<string, string | null>;

/** Writes `next` in order; on failure or on demand writes `before` back (in reverse order, missing files removed). */
async function writeAll(b: Backend, order: string[], next: Files) {
  for (const p of order) {
    const v = next[p];
    if (v === null) await b.remove(p);
    else await b.write(p, v);
  }
}

async function restoreAll(b: Backend, order: string[], before: Files): Promise<string[]> {
  const failed: string[] = [];
  for (const p of [...order].reverse()) {
    try {
      const v = before[p];
      if (v === null) await b.remove(p);
      else await b.write(p, v);
    } catch {
      failed.push(p);
    }
  }
  return failed;
}

/** write → validate → reload, with rollback. `order` = the order to write in (the reverse is used to restore). */
async function commit(
  inst: CaddyInstance,
  b: Backend,
  order: string[],
  before: Files,
  next: Files,
  base: Pick<CaddyResult, 'host' | 'target' | 'snippet' | 'warnings'>,
): Promise<CaddyResult> {
  const changed = order.filter((p) => before[p] !== next[p]);
  if (!changed.length) return { ok: true, step: 'done', message: 'Nothing to change', ...base };
  const rollback = async () => {
    const failed = await restoreAll(b, changed, before);
    if (failed.length) base.warnings.push(`Could not write back: ${failed.join(', ')}`);
    return !failed.length;
  };
  try {
    await writeAll(b, changed, next);
  } catch (e) {
    const rolledBack = await rollback();
    return { ok: false, step: 'write', message: errText(e), rolledBack, ...base };
  }
  const v = await b.validate().catch((e): Outcome => ({ ok: false, output: errText(e) }));
  if (!v.ok) {
    const rolledBack = await rollback();
    return { ok: false, step: 'validate', message: cleanOutput(v.output) || 'caddy validate failed', rolledBack, ...base };
  }
  if (!inst.running) return { ok: true, step: 'done', message: 'Checked; Caddy is stopped and loads it when it starts', notRunning: true, ...base };
  const r = await b.reload().catch((e): Outcome => ({ ok: false, output: errText(e) }));
  if (!r.ok) {
    const rolledBack = await rollback();
    const again = await b.reload().catch((e): Outcome => ({ ok: false, output: errText(e) }));
    if (!again.ok) base.warnings.push(`Caddy did not reload the previous config either: ${cleanOutput(again.output)}`);
    return { ok: false, step: 'reload', message: cleanOutput(r.output) || 'Caddy did not reload', rolledBack, ...base };
  }
  return { ok: true, step: 'done', message: '', ...base };
}

/**
 * Serves the server at `hostInput` through this Caddy and sets the server's public address to https://<host> (unless
 * `opts.publicUrl` is false). `upstream` defaults to the server's own answer (GET /api/plugin/status).
 */
export async function apply(inst: CaddyInstance, hostInput: string, upstream?: Upstream, opts: { publicUrl?: boolean } = {}): Promise<CaddyResult> {
  const warnings: string[] = [];
  const host = normalizeHost(hostInput);
  if (!host) return { ok: false, step: 'host', message: `"${hostInput}" is not a valid host name (like git.example.com)`, snippet: null, warnings };

  if (!upstream) {
    try {
      upstream = (await repo.status()).upstream;
    } catch (e) {
      return { ok: false, step: 'read', message: `ErvisioRepo did not answer: ${errText(e)}`, snippet: null, host, warnings };
    }
  }
  const target = targetFor(inst, upstream);
  const snippet = generateBlock(host, target);
  const base = { host, target, snippet, warnings };

  if (inst.unsupported) {
    return {
      ok: false,
      step: 'unsupported',
      reason: inst.unsupported,
      message: UNSUPPORTED_TEXT[inst.unsupported],
      ...base,
      snippet: inst.unsupported === 'labels' ? dockerProxyLabels(host, upstream) : snippet,
    };
  }

  const b = backendFor(inst);
  let main: string | null;
  try {
    main = await b.read(inst.configPath);
  } catch (e) {
    return { ok: false, step: 'read', message: errText(e), ...base };
  }
  if (main === null) return { ok: false, step: 'read', message: `${inst.configPath} does not exist`, ...base };
  const blocker = editBlocker(main);
  if (blocker) return { ok: false, step: 'unsupported', reason: 'unsafe', message: `${blocker}. Add the site yourself.`, ...base };

  let inv: Awaited<ReturnType<typeof inventory>>;
  try {
    inv = await inventory(inst, b, main);
  } catch (e) {
    return { ok: false, step: 'read', message: errText(e), ...base };
  }
  // inventory() leaves our own block out, so reusing its host is fine.
  if (hostTaken(inv.hosts, host)) return { ok: false, step: 'conflict', message: `Caddy already serves ${host} in another site`, ...base };

  if (inst.kind === 'docker' && !inst.hostNetwork && !inst.container!.networks.includes(upstream.network)) {
    try {
      await connectNetwork(upstream.network, inst.container!.id);
    } catch (e) {
      return { ok: false, step: 'network', message: `Could not attach ${inst.name} to ${upstream.network}: ${errText(e)}`, ...base };
    }
  }
  if (inst.ephemeral) warnings.push('The Caddyfile of this container is not on a volume: the change lasts until the container is recreated.');

  let before: Files;
  let next: Files;
  let order: string[];
  try {
    if (inst.layout === 'file') {
      const file = repoFilePath(inst);
      const old = await b.read(file);
      if (old !== null && old.split('\n')[0]?.trim() !== MARKER) {
        return { ok: false, step: 'conflict', reason: 'not-ours', message: `${file} exists and was not written by this plugin`, ...base };
      }
      // An inline block left from another layout would define the site twice.
      const cleaned = removeInline(main).text;
      before = { [file]: old, [inst.configPath]: main };
      next = { [file]: snippet, [inst.configPath]: ensureImport(cleaned, inst.configPath, file).text };
      order = [file, inst.configPath]; // the file first, so the import never points at nothing
    } else {
      before = { [inst.configPath]: main };
      next = { [inst.configPath]: upsertInline(main, generateBlock(host, target, true)).text };
      order = [inst.configPath];
    }
  } catch (e) {
    return { ok: false, step: 'unsupported', reason: 'unsafe', message: `${errText(e)}. Add the site yourself.`, ...base };
  }

  const res = await commit(inst, b, order, before, next, base);
  if (!res.ok || opts.publicUrl === false) return res;
  try {
    await repo.setPublicUrl(`https://${host}`);
  } catch (e) {
    return { ...res, ok: false, step: 'public-url', caddyApplied: true, message: `Caddy serves ${host}, but the server's address could not be set: ${errText(e)}` };
  }
  return { ...res, step: 'done' };
}

/** Removes our site (the import line and our file, or the inline block), checks and reloads, with rollback. */
export async function remove(inst: CaddyInstance): Promise<CaddyResult> {
  const warnings: string[] = [];
  const base = { snippet: null, warnings };
  if (inst.unsupported) return { ok: false, step: 'unsupported', reason: inst.unsupported, message: UNSUPPORTED_TEXT[inst.unsupported], ...base };
  const b = backendFor(inst);
  let main: string | null;
  try {
    main = await b.read(inst.configPath);
  } catch (e) {
    return { ok: false, step: 'read', message: errText(e), ...base };
  }
  if (main === null) return { ok: false, step: 'read', message: `${inst.configPath} does not exist`, ...base };
  const parsed = scan(main);
  if (!parsed.ok) return { ok: false, step: 'unsupported', reason: 'unsafe', message: parsed.error, ...base };

  const file = repoFilePath(inst);
  let before: Files;
  let next: Files;
  let order: string[];
  try {
    const old = await b.read(file);
    const ours = old !== null && old.split('\n')[0]?.trim() === MARKER;
    let text = removeInline(main).text;
    if (ours) text = removeImport(text, inst.configPath, file).text;
    before = { [inst.configPath]: main, ...(ours ? { [file]: old } : {}) };
    next = { [inst.configPath]: text, ...(ours ? { [file]: null } : {}) };
    order = ours ? [inst.configPath, file] : [inst.configPath]; // the import goes first, then the file
  } catch (e) {
    return { ok: false, step: 'unsupported', reason: 'unsafe', message: errText(e), ...base };
  }
  const host = parseManaged(String(before[file] ?? readInlineSafe(main) ?? ''))?.host;
  return commit(inst, b, order, before, next, { ...base, host });
}

function readInlineSafe(main: string): string | null {
  try {
    return readInline(main);
  } catch {
    return null;
  }
}

/* ---------- the automatic proposal ---------- */

export interface Proposal {
  /** null when no server answers on the socket. */
  server: repo.RepoStatus | null;
  detection: Detection | null;
  /** The Caddy to use: the first running, supported one (system first). */
  instance: CaddyInstance | null;
  /** Our block on that Caddy, when the server is already published. */
  current: Current | null;
  /** git.<base domain> to offer (the user may accept or edit it). When `current` is set, offer its host instead. */
  suggestion: Suggestion | null;
  /** The server's host when its public address is already https (nothing to propose then, unless the user wants to). */
  publishedHost: string | null;
}

/** Picks the Caddy to propose: running and supported first, the system service before containers. */
export function pickInstance(list: CaddyInstance[]): CaddyInstance | null {
  const score = (i: CaddyInstance) => (i.unsupported ? 0 : 2) + (i.running ? 1 : 0);
  return [...list].sort((a, b) => score(b) - score(a))[0] ?? null;
}

/**
 * Everything the page needs to offer "Publish at git.example.com": the server's status, the Caddy found, our current
 * block and the suggested host. Throws only for RepoError `admin` / `forbidden` (see repo.available()).
 */
export async function propose(opts: { askDocker?: boolean } = {}): Promise<Proposal> {
  const empty: Proposal = { server: null, detection: null, instance: null, current: null, suggestion: null, publishedHost: null };
  if (!(await repo.available())) return empty;
  const status = await repo.status();
  let publishedHost: string | null = null;
  try {
    const u = status.publicUrl ? new URL(status.publicUrl) : null;
    if (u && u.protocol === 'https:') publishedHost = u.hostname;
  } catch {
    /* not a URL */
  }
  const detection = await detect(opts);
  const instance = pickInstance(detection.instances);
  if (!instance) return { ...empty, server: status, detection, publishedHost };
  const [cur, suggestion] = await Promise.all([current(instance).catch(() => null), suggestHost(instance)]);
  return { server: status, detection, instance, current: cur, suggestion, publishedHost };
}
