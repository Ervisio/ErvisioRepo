/**
 * The editable settings: which page shows each key, its control, and the checks the server makes
 * (internal/config validate.go), so a wrong value is caught on the row before it is sent. The server stays the
 * judge: its 422 errors replace these messages.
 */
import type { SettingValue } from '../api/repo';
import { t } from './util';

export type PageId = 'overview' | 'address' | 'maintenance' | 'access' | 'mail' | 'git' | 'webhooks' | 'actions' | 'storage' | 'logs';

export type DurUnit = 's' | 'm' | 'h' | 'd';
export type SizeUnit = 'B' | 'KB' | 'MB' | 'GB' | 'TB' | 'KiB' | 'MiB' | 'GiB' | 'TiB';

export type Field =
  | { kind: 'bool' }
  | { kind: 'int'; min: number; max: number }
  | { kind: 'enum'; options: string[] }
  | { kind: 'dur'; min?: number; max?: number; seconds?: boolean }
  | { kind: 'size'; min?: number }
  | { kind: 'text'; check?: 'url' | 'hostport' | 'path' | 'email'; optional?: boolean }
  | { kind: 'list'; check: 'cidr' };

const MiB = 1024 * 1024;
const H = 3600;
const D = 86400;

export const FIELDS: Record<string, Field & { page: PageId }> = {
  'server.public_url': { page: 'address', kind: 'text', check: 'url', optional: true },
  'server.trusted_proxies': { page: 'address', kind: 'list', check: 'cidr' },

  'security.registration': { page: 'access', kind: 'enum', options: ['open', 'invite', 'closed'] },
  'security.user_directory': { page: 'access', kind: 'enum', options: ['shared', 'instance'] },
  'auth.require_verified_email': { page: 'access', kind: 'bool' },
  'auth.invite_ttl': { page: 'access', kind: 'dur' },
  'auth.verify_token_ttl': { page: 'access', kind: 'dur' },
  'auth.reset_token_ttl': { page: 'access', kind: 'dur' },
  'auth.password_min_length': { page: 'access', kind: 'int', min: 8, max: 128 },
  'auth.require_2fa': { page: 'access', kind: 'enum', options: ['off', 'admins', 'all'] },
  'auth.session_idle_timeout': { page: 'access', kind: 'dur' },
  'auth.session_max_age': { page: 'access', kind: 'dur' },
  'auth.token_max_ttl': { page: 'access', kind: 'dur' },
  'auth.login_free_failures_account': { page: 'access', kind: 'int', min: 1, max: 1000 },
  'auth.login_free_failures_ip': { page: 'access', kind: 'int', min: 1, max: 100000 },
  'auth.login_backoff_base': { page: 'access', kind: 'dur', seconds: true },
  'auth.login_backoff_max': { page: 'access', kind: 'dur', seconds: true },
  'auth.actions_per_ip_per_hour': { page: 'access', kind: 'int', min: 1, max: 1000000 },

  'mail.enabled': { page: 'mail', kind: 'bool' },
  'mail.host': { page: 'mail', kind: 'text', optional: true },
  'mail.port': { page: 'mail', kind: 'int', min: 1, max: 65535 },
  'mail.tls': { page: 'mail', kind: 'enum', options: ['starttls', 'tls', 'none'] },
  'mail.username': { page: 'mail', kind: 'text', optional: true },
  'mail.password_file': { page: 'mail', kind: 'text', check: 'path', optional: true },
  'mail.from': { page: 'mail', kind: 'text', optional: true },
  'mail.helo_name': { page: 'mail', kind: 'text', optional: true },
  'mail.timeout': { page: 'mail', kind: 'dur', seconds: true },

  'git.max_push_size': { page: 'git', kind: 'size', min: MiB },
  'git.max_duration': { page: 'git', kind: 'dur' },
  'git.idle_timeout': { page: 'git', kind: 'dur', seconds: true },
  'git.requests_per_hour': { page: 'git', kind: 'int', min: 1, max: 10000000 },
  'ssh.enabled': { page: 'git', kind: 'bool' },
  'server.ssh_listen': { page: 'git', kind: 'text', check: 'hostport' },
  'ssh.max_connections': { page: 'git', kind: 'int', min: 1, max: 100000 },
  'ssh.login_grace_time': { page: 'git', kind: 'dur', seconds: true },

  'webhooks.allow_private': { page: 'webhooks', kind: 'bool' },
  'webhooks.allowed_cidrs': { page: 'webhooks', kind: 'list', check: 'cidr' },
  'webhooks.timeout': { page: 'webhooks', kind: 'dur', seconds: true, max: 300 },
  'webhooks.max_attempts': { page: 'webhooks', kind: 'int', min: 1, max: 25 },

  'actions.enabled': { page: 'actions', kind: 'bool' },
  'actions.default_actions_url': { page: 'actions', kind: 'enum', options: ['github', 'mirror-only'] },
  'actions.max_job_timeout': { page: 'actions', kind: 'dur', min: 60, max: 72 * H },
  'actions.runner_offline_after': { page: 'actions', kind: 'dur', seconds: true, min: 40, max: H },
  'actions.registration_token_ttl': { page: 'actions', kind: 'dur', min: 60, max: 720 * H },
  'actions.max_jobs_per_run': { page: 'actions', kind: 'int', min: 1, max: 10000 },
  'actions.cache_max_repo_size': { page: 'actions', kind: 'size', min: MiB },
  'actions.cache_ttl': { page: 'actions', kind: 'dur', min: H },
  'actions.artifact_retention': { page: 'actions', kind: 'dur', min: D, max: 400 * D },

  'log.level': { page: 'logs', kind: 'enum', options: ['debug', 'info', 'warn', 'error'] },
  'log.format': { page: 'logs', kind: 'enum', options: ['json', 'text'] },
};

/* ---------- durations ---------- */

const DUR_SECONDS: Record<DurUnit, number> = { s: 1, m: 60, h: H, d: D };
const GO_PART = /([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h)/g;
const GO_SECONDS: Record<string, number> = { ns: 1e-9, us: 1e-6, µs: 1e-6, ms: 1e-3, s: 1, m: 60, h: H };

/** Seconds of a duration as the server writes or accepts it ("7d", "2w", "1h30m0s", "10m"); NaN when it is not one. */
export function durSeconds(v: string): number {
  const s = v.trim();
  let m = /^([0-9]+)([dw])$/.exec(s);
  if (m) return Number(m[1]) * (m[2] === 'd' ? D : 7 * D);
  if (s === '0') return 0;
  if (!/^((?:[0-9]+(?:\.[0-9]+)?)(?:ns|us|µs|ms|s|m|h))+$/.test(s)) return NaN;
  let total = 0;
  GO_PART.lastIndex = 0;
  while ((m = GO_PART.exec(s))) total += Number(m[1]) * GO_SECONDS[m[2]];
  return total;
}

/** The number and unit to show a duration with: the largest unit that divides it. */
export function splitDur(v: string): { n: string; unit: DurUnit } {
  const sec = durSeconds(v);
  if (!Number.isFinite(sec)) return { n: '', unit: 'm' };
  for (const u of ['d', 'h', 'm'] as DurUnit[]) {
    if (sec > 0 && sec % DUR_SECONDS[u] === 0) return { n: String(sec / DUR_SECONDS[u]), unit: u };
  }
  return { n: String(Math.round(sec * 1000) / 1000), unit: 's' };
}

export const joinDur = (n: string, unit: DurUnit) => `${n.trim()}${unit}`;

/* ---------- sizes ---------- */

const SIZE_BYTES: Record<string, number> = {
  b: 1, kb: 1e3, mb: 1e6, gb: 1e9, tb: 1e12, kib: 1024, mib: MiB, gib: 1024 * MiB, tib: 1024 * 1024 * MiB,
};
const SIZE_UNITS: SizeUnit[] = ['B', 'KB', 'MB', 'GB', 'TB', 'KiB', 'MiB', 'GiB', 'TiB'];
const SIZE_RE = /^([0-9]+(?:\.[0-9]+)?)\s*([KMGT]i?B|B)$/i;

/** Bytes of a size like "2GB" or "512MiB" (a unit is required, as on the server); NaN when it is not one. */
export function sizeBytes(v: string): number {
  const m = SIZE_RE.exec(v.trim());
  if (!m) return NaN;
  return Number(m[1]) * SIZE_BYTES[m[2].toLowerCase()];
}

export function splitSize(v: string): { n: string; unit: SizeUnit } {
  const m = SIZE_RE.exec(v.trim());
  if (!m) return { n: '', unit: 'GB' };
  const unit = SIZE_UNITS.find((u) => u.toLowerCase() === m[2].toLowerCase()) ?? 'B';
  if (unit !== 'B') return { n: m[1], unit };
  const b = Number(m[1]);
  for (const u of ['TiB', 'GiB', 'MiB', 'TB', 'GB', 'MB'] as SizeUnit[]) {
    const k = SIZE_BYTES[u.toLowerCase()];
    if (b > 0 && b % k === 0) return { n: String(b / k), unit: u };
  }
  return { n: m[1], unit: 'B' };
}

export const joinSize = (n: string, unit: SizeUnit) => `${n.trim()}${unit}`;

/* ---------- checks ---------- */

const CIDR4 = /^(25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])){3}\/([0-9]|[12][0-9]|3[0-2])$/;
const CIDR6 = /^[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*\/([0-9]|[1-9][0-9]|1[01][0-9]|12[0-8])$/;
const HOSTPORT = /^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]*):[0-9]{1,5}$/;

export const isCidr = (s: string) => CIDR4.test(s) || CIDR6.test(s);

export function splitList(text: string): string[] {
  return text.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);
}

function durLimit(sec: number): string {
  const { n, unit } = splitDur(`${sec}s`);
  return `${n} ${t(`unit.${unit}`)}`;
}

/** Why a value is refused, or null when it looks fine. */
export function check(key: string, v: SettingValue): string | null {
  const f = FIELDS[key];
  if (!f) return null;
  switch (f.kind) {
    case 'int': {
      const n = typeof v === 'number' ? v : Number(v);
      if (!Number.isInteger(n) || n < f.min || n > f.max) return t('err.range', { min: f.min, max: f.max });
      return null;
    }
    case 'dur': {
      const sec = durSeconds(String(v));
      if (!Number.isFinite(sec)) return t('err.dur');
      if (sec <= 0) return t('err.positive');
      if (f.min !== undefined && f.max !== undefined && (sec < f.min || sec > f.max)) return t('err.between', { min: durLimit(f.min), max: durLimit(f.max) });
      if (f.min !== undefined && sec < f.min) return t('err.atLeast', { min: durLimit(f.min) });
      if (f.max !== undefined && sec > f.max) return t('err.atMost', { max: durLimit(f.max) });
      return null;
    }
    case 'size': {
      const b = sizeBytes(String(v));
      if (!Number.isFinite(b)) return t('err.size');
      if (f.min !== undefined && b < f.min) return t('err.atLeast', { min: '1 MiB' });
      return null;
    }
    case 'list': {
      const bad = (v as string[]).find((s) => !isCidr(s));
      return bad ? t('err.cidr', { v: bad }) : null;
    }
    case 'text': {
      const s = String(v).trim();
      if (!s) return f.optional ? null : t('err.required');
      if (f.check === 'url') {
        try {
          const u = new URL(s);
          if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.host) return t('err.url');
        } catch {
          return t('err.url');
        }
      }
      if (f.check === 'hostport' && !HOSTPORT.test(s)) return t('err.hostport');
      if (f.check === 'path' && !s.startsWith('/')) return t('err.path');
      return null;
    }
    default:
      return null;
  }
}

/** A value as people read it, for "Default ..." texts. */
export function display(key: string, v: SettingValue | undefined): string {
  if (v === undefined) return '';
  const f = FIELDS[key];
  if (typeof v === 'boolean') return v ? t('val.on') : t('val.off');
  if (Array.isArray(v)) return v.length ? v.join(', ') : t('val.none');
  if (f?.kind === 'dur') {
    const { n, unit } = splitDur(String(v));
    return n ? `${n} ${t(`unit.${unit}`)}` : String(v);
  }
  if (f?.kind === 'size') {
    const { n, unit } = splitSize(String(v));
    return n ? `${n} ${unit}` : String(v);
  }
  if (f?.kind === 'enum') return t(`opt.${key}.${v}`);
  if (v === '') return t('val.none');
  return String(v);
}

/** Two values are the same setting (lists compare by content). */
export const same = (a: SettingValue | undefined, b: SettingValue | undefined) => JSON.stringify(a) === JSON.stringify(b);
