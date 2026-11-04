/**
 * The ErvisioRepo service on this machine, whatever answers on the socket: find the installation (Docker Compose
 * project `ervisiorepo` or the native ervisio-repo.service), start, stop and restart it, read its log and update it.
 *
 *   const inst = await service.detect();       // null when nothing is installed
 *   await service.restart(inst);
 *   const stop = service.watchLogs(inst, (lines, replace) => ...);
 */
import { getSdk } from '../sdk';
import * as docker from './docker';
import * as install from './install';

export const PROJECT = 'ervisiorepo';
export const UNIT = 'ervisio-repo.service';
const CONFIG_DIR = '/etc/ervisio-repo';

export type Mode = 'docker' | 'native';

export interface Installation {
  mode: Mode;
  /** The compose `app` container (Docker only). */
  app?: docker.ContainerSummary;
  /** Every container of the compose project (Docker only). */
  containers: docker.ContainerSummary[];
  /** True when the container runs or the unit is active, even if the API does not answer yet. */
  running: boolean;
  /** When it last stopped (ISO or systemd text), empty when unknown. */
  stoppedAt: string;
  /** Exit code of the app container, or the unit's Result. */
  exit: string;
}

const service = (c: docker.ContainerSummary) => c.Labels?.['com.docker.compose.service'] ?? '';

async function dockerInstall(): Promise<Installation | null> {
  if (!(await install.hasDocker())) return null;
  const all = await docker.listContainers();
  const containers = all.filter((c) => c.Labels?.['com.docker.compose.project'] === PROJECT);
  const app = containers.find((c) => service(c) === 'app');
  if (!app) return null;
  let stoppedAt = '';
  let exit = '';
  if (app.State !== 'running') {
    try {
      const i = await docker.inspect(app.Id);
      stoppedAt = i.State.FinishedAt && !i.State.FinishedAt.startsWith('0001') ? i.State.FinishedAt : '';
      exit = i.State.ExitCode !== undefined ? String(i.State.ExitCode) : '';
    } catch {
      /* the summary is enough */
    }
  }
  containers.sort((a, b) => service(a).localeCompare(service(b)));
  return { mode: 'docker', app, containers, running: app.State === 'running', stoppedAt, exit };
}

async function nativeInstall(): Promise<Installation | null> {
  const p = install.props((await getSdk().api.exec('service-state')).stdout);
  if (p.LoadState !== 'loaded') return null;
  return {
    mode: 'native',
    containers: [],
    running: p.ActiveState === 'active' || p.ActiveState === 'activating',
    stoppedAt: p.InactiveEnterTimestamp ?? '',
    exit: p.Result && p.Result !== 'success' ? p.Result : '',
  };
}

/** Finds the installation: the Docker project first, then the systemd unit. Null when neither exists. */
export async function detect(): Promise<Installation | null> {
  try {
    const d = await dockerInstall();
    if (d) return d;
  } catch {
    /* Docker not readable (rights declined): try the native unit */
  }
  try {
    return await nativeInstall();
  } catch {
    return null;
  }
}

/** Where config.toml lives on the host. */
export async function hostConfigPath(inst: Installation, fallback: string): Promise<string> {
  if (inst.mode !== 'docker' || !inst.app) return fallback;
  try {
    const i = await docker.inspect(inst.app.Id);
    const m = i.Mounts.find((x) => x.Destination === CONFIG_DIR);
    if (m && fallback.startsWith(`${CONFIG_DIR}/`)) return m.Source + fallback.slice(CONFIG_DIR.length);
    if (m) return `${m.Source}/config.toml`;
  } catch {
    /* keep the path inside the container */
  }
  return fallback;
}

async function systemctl(cmd: 'service-start' | 'service-stop' | 'service-restart'): Promise<void> {
  const r = await getSdk().api.exec(cmd);
  if (r.exitCode !== 0) throw new Error((r.stderr || r.stdout).trim() || `systemctl exited with ${r.exitCode}`);
}

/** Starts what is stopped: the other containers of the project first, then the app. */
export async function start(inst: Installation): Promise<void> {
  if (inst.mode === 'native') return systemctl('service-start');
  for (const c of inst.containers) if (c !== inst.app && c.State !== 'running') await docker.start(c.Id);
  if (inst.app) await docker.start(inst.app.Id);
}

export async function stop(inst: Installation): Promise<void> {
  if (inst.mode === 'native') return systemctl('service-stop');
  if (inst.app) await docker.stop(inst.app.Id);
}

export async function restart(inst: Installation): Promise<void> {
  if (inst.mode === 'native') return systemctl('service-restart');
  if (inst.app) await docker.restart(inst.app.Id);
}

/* ---------- logs ---------- */

async function journal(): Promise<string[]> {
  const r = await getSdk().api.exec('service-log');
  if (r.exitCode !== 0) throw new Error((r.stderr || r.stdout).trim() || `journalctl exited with ${r.exitCode}`);
  return r.stdout.split('\n').filter((l) => l.trim());
}

/** The last `n` lines of the server's output. */
export async function tailLogs(inst: Installation, n = 20): Promise<string[]> {
  if (inst.mode === 'docker' && inst.app) return docker.logs(inst.app.Id, n);
  return (await journal()).slice(-n);
}

/**
 * Follows the server's output. Docker streams new lines (`replace` false); the native journal is read again every
 * 3 seconds and replaces what was shown (`replace` true). Returns the function that stops it.
 */
export function watchLogs(inst: Installation, onLines: (lines: string[], replace: boolean) => void, onError: (e: unknown) => void): () => void {
  if (inst.mode === 'docker' && inst.app) return docker.followLogs(inst.app.Id, (l) => onLines(l, false), (e) => e && onError(e));
  let live = true;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const tick = () => {
    journal().then(
      (l) => live && onLines(l, true),
      (e) => live && onError(e),
    ).finally(() => {
      if (live) timer = setTimeout(tick, 3000);
    });
  };
  tick();
  return () => {
    live = false;
    if (timer) clearTimeout(timer);
  };
}

/* ---------- update ---------- */

const VERSION = /^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$/;

/** -1, 0 or 1 like a comparator, for x.y.z versions (anything else compares equal). */
export function compareVersions(a: string, b: string): number {
  const pa = a.replace(/^v/, '').split('.').map(Number);
  const pb = b.replace(/^v/, '').split('.').map(Number);
  if (pa.length !== 3 || pb.length !== 3 || [...pa, ...pb].some((n) => Number.isNaN(n))) return 0;
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] < pb[i] ? -1 : 1;
  return 0;
}

/** Starts the update to `version` in a systemd unit (administrator rights). */
export async function startUpdate(inst: Installation, version: string): Promise<void> {
  if (!VERSION.test(version)) throw new Error(`Not a version: ${version}`);
  const cmd = inst.mode === 'docker' ? 'update-docker' : 'install-native';
  const r = await getSdk().api.exec(cmd, [version]);
  if (r.exitCode !== 0) throw new Error((r.stderr || r.stdout).trim() || `systemd-run exited with ${r.exitCode}`);
}

/** Progress of the last update unit (ervisio-repo-update for Docker, ervisio-repo-install for native). */
export function updateProgress(inst: Installation): Promise<install.UnitProgress> {
  return inst.mode === 'docker' ? install.unitProgress('update-state', 'update-log') : install.unitProgress('install-state', 'install-log');
}

/**
 * Which step the Docker update script reached, from its `==> step` markers: 0 backup, 1 pull, 2 up, 3 done.
 * -1 before the first marker.
 */
export function updateStep(log: string[]): number {
  const order = ['backup', 'pull', 'up', 'done'];
  let at = -1;
  for (const l of log) {
    const m = /^==> (backup|pull|up|done)\b/.exec(l.trim());
    if (m) at = Math.max(at, order.indexOf(m[1]));
  }
  return at;
}
