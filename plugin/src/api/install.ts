/**
 * Installs ErvisioRepo on this machine when there is none, in one of two modes:
 *
 *  - `docker`: the release matching this plugin is downloaded to /opt/ervisio-repo and set up with
 *    deploy/docker/setup.sh --non-interactive --ervisio yes, which starts the Compose stack and opens the admin socket;
 *  - `native`: deploy/install.sh of that release installs the signed apt/rpm package (systemd service, admin socket at
 *    /run/ervisio-repo/api.sock).
 *
 * The work runs in a transient systemd unit (ervisio-repo-install), so it survives the page being closed and has no
 * command time limit; the page follows it through the unit's state and its journal.
 *
 *   await install.start('docker', { email: 'me@example.com', url: 'http://localhost:3400' });
 *   const p = await install.progress();        // { state: 'running' | 'done' | 'failed' | 'idle', log }
 */
import { getSdk } from '../sdk';

export const INSTALL_DIR = '/opt/ervisio-repo';
export const UNIT = 'ervisio-repo-install';

export type InstallMode = 'docker' | 'native';
export type InstallState = 'idle' | 'running' | 'done' | 'failed';

export interface InstallProgress {
  state: InstallState;
  /** The unit's output, newest last. */
  log: string[];
  /** Which mode the unit's last run was, null when it never ran. */
  mode: InstallMode | null;
}

export const EMAIL = /^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9-]{1,63}(\.[A-Za-z0-9-]{1,63}){1,8}$/;
export const URL_RE = /^https?:\/\/[A-Za-z0-9.-]{1,150}(:[0-9]{1,5})?$/;

/** Docker must be there for the `docker` mode. */
export async function hasDocker(): Promise<boolean> {
  return (await getSdk().api.exec('has-docker')).exitCode === 0;
}

/** Starts the installation (administrator rights; Ervisio asks to unlock them). */
export async function start(mode: InstallMode, opts: { email: string; url: string }): Promise<void> {
  const sdk = getSdk();
  let r;
  if (mode === 'docker') {
    if (!EMAIL.test(opts.email)) throw new Error('invalid email');
    if (!URL_RE.test(opts.url)) throw new Error('invalid url');
    r = await sdk.api.exec('install-docker', [sdk.plugin.version, opts.email.toLowerCase(), opts.url]);
  } else {
    r = await sdk.api.exec('install-native', [sdk.plugin.version]);
  }
  if (r.exitCode !== 0) throw new Error((r.stderr || r.stdout).trim() || `systemd-run exited with ${r.exitCode}`);
}

function props(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const i = line.indexOf('=');
    if (i > 0) out[line.slice(0, i)] = line.slice(i + 1).trim();
  }
  return out;
}

/** Where the installation is, with the output of its last run. */
export async function progress(): Promise<InstallProgress> {
  const sdk = getSdk();
  const p = props((await sdk.api.exec('install-state')).stdout);
  if (p.LoadState !== 'loaded' || !p.InvocationID) return { state: 'idle', log: [], mode: null };
  const state: InstallState =
    p.ActiveState === 'failed' || (p.Result && p.Result !== 'success')
      ? 'failed'
      : p.ActiveState === 'active' && p.SubState === 'exited'
        ? 'done'
        : p.ActiveState === 'inactive'
          ? 'idle'
          : 'running';
  const journal = await sdk.api.exec('install-log', [p.InvocationID]);
  const log = journal.stdout.split('\n').filter((l) => l.trim());
  return { state, log, mode: /docker/i.test(p.Description ?? '') ? 'docker' : 'native' };
}
