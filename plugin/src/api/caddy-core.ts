/**
 * Pure Caddyfile helpers for publishing ErvisioRepo behind Caddy: no SDK, no I/O, so they run under `node --test`.
 *
 *   const outline = scan(text);                       // top-level sites, imports, snippets; null when unsafe
 *   const s = suggestHost(siteHosts(outline));         // "git.example.com" from the sites already served
 *   const block = generateBlock('git.example.com', '127.0.0.1:3000');
 *   const main = ensureImport(text, '/etc/caddy/Caddyfile', '/etc/caddy/ervisio-repo.caddy');
 *
 * The tokenizer follows Caddy's own rules (and is adapted from Ervisio/plugin-caddy, src/api/caddyfile.ts, MIT
 * License, Copyright (c) 2026 Fonlogen): words, "quoted" and `backtick` strings, <<HEREDOCs, # comments.
 */

/** The first line of every block this plugin writes. */
export const MARKER = '# Managed by the ErvisioRepo plugin';
/** Second line: a hint for people who open the file. */
export const MARKER_HINT = '# Change it from Ervisio > Repo: edits made here are replaced.';
/** Last line of a block kept inside the main Caddyfile (containers whose config folder is not mounted). */
export const END_MARKER = '# End of the ErvisioRepo block';
/** The comment written above the import line. */
export const IMPORT_COMMENT = '# Added by the ErvisioRepo plugin';
/** Name of the file the site block lives in, next to the main Caddyfile. */
export const REPO_FILE = 'ervisio-repo.caddy';

/** Where a reverse proxy reaches the server (GET /api/plugin/status → data.upstream). */
export interface Upstream {
  host: string;
  port: number;
  container: string;
  containerPort: number;
  network: string;
}

/* ---------- hostnames ---------- */

const LABEL = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

/**
 * A hostname Caddy can serve with a public certificate: lower case, at least two DNS labels of 1 to 63 letters, digits
 * or inner hyphens, at most 253 characters, a top-level label that is not numeric, no wildcard, no IP address.
 */
export function validHost(host: string): boolean {
  if (typeof host !== 'string' || host.length > 253 || !host.includes('.')) return false;
  const labels = host.split('.');
  if (labels.length < 2 || !labels.every((l) => LABEL.test(l))) return false;
  const tld = labels[labels.length - 1];
  return /[a-z]/.test(tld) && !/^[0-9-]+$/.test(tld);
}

/** Trims, lower-cases and drops a trailing dot or an https:// prefix someone pasted; null when not a valid host. */
export function normalizeHost(input: string): string | null {
  if (typeof input !== 'string') return null;
  let h = input.trim().toLowerCase();
  h = h.replace(/^https?:\/\//, '').replace(/\/$/, '').replace(/\.$/, '');
  return validHost(h) ? h : null;
}

/* ---------- tokens ---------- */

export interface Token {
  text: string;
  /** 0-based lines where the token starts and ends. */
  line: number;
  endLine: number;
  quoted: boolean;
}

/** Splits a Caddyfile into tokens. Throws on an unterminated string or heredoc. */
export function tokenize(text: string): Token[] {
  const out: Token[] = [];
  const src = text.replace(/\r\n?/g, '\n');
  const n = src.length;
  let i = 0;
  let line = 0;
  while (i < n) {
    const c = src[i];
    if (c === '\n') {
      line++;
      i++;
      continue;
    }
    if (c === ' ' || c === '\t') {
      i++;
      continue;
    }
    if (c === '#') {
      while (i < n && src[i] !== '\n') i++;
      continue;
    }
    const start = line;
    if (c === '"' || c === '`') {
      let j = i + 1;
      let s = '';
      while (j < n && src[j] !== c) {
        if (c === '"' && src[j] === '\\' && j + 1 < n) {
          if (src[j + 1] === '\n') line++;
          s += src[j + 1];
          j += 2;
          continue;
        }
        if (src[j] === '\n') line++;
        s += src[j++];
      }
      if (j >= n) throw new Error(`Unterminated string on line ${start + 1}`);
      out.push({ text: s, line: start, endLine: line, quoted: true });
      i = j + 1;
      continue;
    }
    if (c === '<' && src[i + 1] === '<' && /[A-Za-z]/.test(src[i + 2] ?? '')) {
      let j = i + 2;
      while (j < n && /[A-Za-z0-9_]/.test(src[j])) j++;
      const marker = src.slice(i + 2, j);
      // The closing marker starts its own line; Caddy reads more tokens after it ("HTML 200").
      const endRe = new RegExp(`^[ \\t]*${marker}(?=[ \\t]|$)`);
      let k = src.indexOf('\n', j);
      let closed = false;
      let body = '';
      let resume = n;
      while (k >= 0 && k < n) {
        line++;
        const e = src.indexOf('\n', k + 1);
        const l = src.slice(k + 1, e < 0 ? n : e);
        const m = endRe.exec(l);
        if (m) {
          closed = true;
          resume = k + 1 + m[0].length;
          break;
        }
        body += (body ? '\n' : '') + l;
        k = e < 0 ? n : e;
      }
      if (!closed) throw new Error(`Unterminated heredoc on line ${start + 1}`);
      out.push({ text: body, line: start, endLine: line, quoted: true });
      i = resume;
      continue;
    }
    let j = i;
    while (j < n && src[j] !== ' ' && src[j] !== '\t' && src[j] !== '\n') j++;
    out.push({ text: src.slice(i, j), line, endLine: line, quoted: false });
    i = j;
  }
  return out;
}

/* ---------- top-level outline ---------- */

export type EntryKind = 'global' | 'snippet' | 'site' | 'import' | 'other';

export interface Entry {
  kind: EntryKind;
  /** The tokens before the opening brace (or the whole directive line). */
  head: string[];
  /** 0-based first and last line (the closing brace for a block). */
  start: number;
  end: number;
}

export interface Outline {
  entries: Entry[];
  sites: Entry[];
  /** Arguments of top-level `import` lines that are files or globs (snippet imports left out). */
  imports: string[];
  snippets: string[];
  /**
   * The file has top-level directives that are not imports: a single site written without braces
   * ("example.com" on the first line, directives below). Adding a site would change its meaning, so it is never edited.
   */
  braceless: boolean;
}

/**
 * The top-level structure of a Caddyfile, or an error message when it cannot be read safely (unbalanced braces,
 * unterminated strings, a brace that is not alone as a token). Nothing is ever written to a file that fails here.
 */
export function scan(text: string): { ok: true; outline: Outline } | { ok: false; error: string } {
  let tokens: Token[];
  try {
    tokens = tokenize(text);
  } catch (e) {
    return { ok: false, error: (e as Error).message };
  }
  const entries: Entry[] = [];
  let depth = 0;
  let head: Token[] = [];
  let blockStart = -1;
  let blockHead: string[] = [];
  const flushLine = () => {
    if (!head.length) return;
    const name = head[0].text;
    entries.push({
      kind: name === 'import' && !head[0].quoted ? 'import' : 'other',
      head: head.map((t) => t.text),
      start: head[0].line,
      end: head[head.length - 1].endLine,
    });
    head = [];
  };
  for (const t of tokens) {
    const open = !t.quoted && t.text === '{';
    const close = !t.quoted && t.text === '}';
    if (!t.quoted && !open && !close && /[{}]$/.test(t.text) && !/\{[^}]*\}$/.test(t.text) && depth === 0) {
      // "example.com{" glued to its brace: Caddy needs a space, so do not guess.
      return { ok: false, error: `Line ${t.line + 1}: "${t.text}" needs a space before the brace` };
    }
    if (depth === 0) {
      if (open) {
        blockStart = head.length ? head[0].line : t.line;
        blockHead = head.map((x) => x.text);
        head = [];
        depth = 1;
        continue;
      }
      if (close) return { ok: false, error: `Line ${t.line + 1}: "}" without a matching "{"` };
      if (head.length && t.line !== head[head.length - 1].endLine) flushLine();
      head.push(t);
      continue;
    }
    if (open) depth++;
    else if (close) {
      depth--;
      if (depth === 0) {
        const name = blockHead[0] ?? '';
        const kind: EntryKind = !blockHead.length ? 'global' : /^\(.+\)$/.test(name) ? 'snippet' : 'site';
        entries.push({ kind, head: blockHead, start: blockStart, end: t.line });
        blockHead = [];
      }
    }
  }
  if (depth !== 0) return { ok: false, error: `A block opened on line ${blockStart + 1} is never closed` };
  flushLine();
  const snippets = entries.filter((e) => e.kind === 'snippet').map((e) => e.head[0].slice(1, -1));
  const imports = entries
    .filter((e) => e.kind === 'import' && e.head[1])
    .map((e) => e.head[1])
    .filter((a) => !snippets.includes(a));
  const braceless = entries.some((e) => e.kind === 'other');
  return { ok: true, outline: { entries, sites: entries.filter((e) => e.kind === 'site'), imports, snippets, braceless } };
}

/** The host names of a site header: "a.com, b.com" → ["a.com", "b.com"], schemes, ports and paths removed. */
export function hostsOf(head: string[]): string[] {
  return head
    .join(' ')
    .split(/[\s,]+/)
    .map((a) => a.trim().toLowerCase())
    .filter(Boolean)
    .map((a) => a.replace(/^[a-z][a-z0-9+.-]*:\/\//, '').replace(/\/.*$/, '').replace(/:\d+$/, ''))
    .filter(Boolean);
}

/** Every host name the sites of an outline answer to (wildcards and IPs included as written). */
export function siteHosts(outline: Outline): string[] {
  const hosts = outline.sites.flatMap((s) => hostsOf(s.head));
  // A braceless Caddyfile: its first line is the site address.
  const first = outline.entries.find((e) => e.kind !== 'global');
  if (outline.braceless && first?.kind === 'other') hosts.unshift(...hostsOf(first.head));
  return hosts;
}

/** Why a Caddyfile must not be edited by this plugin, or null when it can be. */
export function editBlocker(text: string): string | null {
  const r = scan(text);
  if (!r.ok) return r.error;
  if (r.outline.braceless) return 'The Caddyfile defines its site without braces, so another site cannot be added safely';
  return null;
}

/* ---------- base domain and suggestion ---------- */

/** Second-level labels under which registries sell names (co.uk, com.au...). Not the whole public suffix list. */
const SECOND_LEVEL = new Set([
  'ac', 'co', 'com', 'edu', 'gov', 'net', 'org', 'ne', 'or', 'go', 'gob', 'gv', 'ltd', 'plc', 'nom', 'mil', 'sch', 'me',
]);
const PRIVATE_SUFFIX = /\.(local|lan|internal|home|test|localhost|invalid|example|arpa|home\.arpa)$/;
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;

/**
 * The registrable domain of a public host name ("shop.example.co.uk" → "example.co.uk"), or null for wildcards'
 * own labels, IPs, single labels and private names (.local, .lan, .internal, localhost...).
 */
export function registrableDomain(host: string): string | null {
  let h = host.toLowerCase().replace(/\.$/, '');
  if (h.startsWith('*.')) h = h.slice(2);
  if (!h || IPV4.test(h) || h.includes(':') || h.includes('{') || h === 'localhost' || PRIVATE_SUFFIX.test(h)) return null;
  if (!validHost(h)) return null;
  const labels = h.split('.');
  const n = labels.length;
  const twoLevel = n >= 3 && labels[n - 1].length === 2 && SECOND_LEVEL.has(labels[n - 2]);
  const take = twoLevel ? 3 : 2;
  if (n < take) return null;
  return labels.slice(n - take).join('.');
}

/** The registrable domain most sites use (first seen wins a tie), or null when there is none. */
export function baseDomain(hosts: string[]): string | null {
  const count = new Map<string, number>();
  for (const h of hosts) {
    const d = registrableDomain(h);
    if (d) count.set(d, (count.get(d) ?? 0) + 1);
  }
  let best: string | null = null;
  let max = 0;
  for (const [d, c] of count) {
    if (c > max) {
      best = d;
      max = c;
    }
  }
  return best;
}

export interface Suggestion {
  host: string;
  base: string;
  /** git.<base> was already served by another site, so another label was picked. */
  renamed: boolean;
}

const LABELS = ['git', 'code', 'repo', 'src'];

/**
 * git.<base domain> for the hosts Caddy already serves; another label when that name is already taken by a site
 * that is not ours (`own` = the host of our managed block, which may be reused). Null when no base domain is found.
 */
export function suggestHost(hosts: string[], own?: string | null): Suggestion | null {
  const base = baseDomain(hosts);
  if (!base) return null;
  const taken = new Set(hosts.map((h) => h.toLowerCase()).filter((h) => h !== own));
  const wildcard = (h: string) => taken.has(`*.${h.slice(h.indexOf('.') + 1)}`);
  for (const label of LABELS) {
    const host = `${label}.${base}`;
    if (!taken.has(host) && !wildcard(host)) return { host, base, renamed: label !== 'git' };
  }
  for (let i = 2; i < 100; i++) {
    const host = `git${i}.${base}`;
    if (!taken.has(host) && !wildcard(host)) return { host, base, renamed: true };
  }
  return null;
}

/** True when `host` is already an address of one of `hosts` (exactly, or through a wildcard of the same level). */
export function hostTaken(hosts: string[], host: string): boolean {
  const h = host.toLowerCase();
  const parent = h.slice(h.indexOf('.') + 1);
  return hosts.some((x) => x.toLowerCase() === h || x.toLowerCase() === `*.${parent}`);
}

/* ---------- the site block ---------- */

/** Where Caddy sends requests: the published host port for Caddy on the host, the container for Caddy in Docker. */
export function proxyTarget(kind: 'system' | 'docker' | 'docker-host', u: Upstream): string {
  if (kind === 'docker') return `${u.container}:${u.containerPort}`;
  let host = u.host;
  if (host === '0.0.0.0' || host === '' || host === '::' || host === '[::]') host = '127.0.0.1';
  else if (host.includes(':') && !host.startsWith('[')) host = `[${host}]`;
  return `${host}:${u.port}`;
}

/** The text of our site block (ends with a newline). `inline` adds the end marker for blocks inside the Caddyfile. */
export function generateBlock(host: string, target: string, inline = false): string {
  if (!validHost(host)) throw new Error(`Not a valid host name: ${host}`);
  if (!/^[A-Za-z0-9._\[\]:-]+$/.test(target)) throw new Error(`Not a valid upstream: ${target}`);
  // Git needs large pushes, clones that run for a long time, streamed logs and no compression of packs (PLAN 9.4).
  const lines = [
    MARKER,
    MARKER_HINT,
    `${host} {`,
    '\trequest_body {',
    '\t\tmax_size 10GB',
    '\t}',
    `\treverse_proxy ${target} {`,
    '\t\tflush_interval -1',
    '\t\ttransport http {',
    '\t\t\tread_timeout 0',
    '\t\t\twrite_timeout 0',
    '\t\t}',
    '\t}',
    '\tencode {',
    '\t\tzstd',
    '\t\tgzip',
    '\t\tmatch {',
    '\t\t\tnot header Content-Type application/x-git-*',
    '\t\t}',
    '\t}',
    '}',
  ];
  if (inline) lines.push(END_MARKER);
  return lines.join('\n') + '\n';
}

export interface Managed {
  host: string;
  target: string;
}

/** Reads one of our blocks (a whole ervisio-repo.caddy file, or the inline part). Null when it is not ours. */
export function parseManaged(text: string): Managed | null {
  const src = text.replace(/\r\n?/g, '\n');
  if (src.split('\n')[0]?.trim() !== MARKER) return null;
  const r = scan(src);
  if (!r.ok || r.outline.sites.length !== 1) return null;
  const site = r.outline.sites[0];
  const hosts = hostsOf(site.head);
  if (hosts.length !== 1) return null;
  const body = src.split('\n').slice(site.start + 1, site.end).join('\n');
  const target = body.match(/^\s*reverse_proxy\s+(\S+)/m)?.[1] ?? '';
  return { host: hosts[0], target };
}

/* ---------- import line ---------- */

const dirOf = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/';

/** A Caddy import glob as a regular expression over absolute paths (`*` and `?` stay within a folder). */
export function globToRe(glob: string): RegExp {
  const esc = glob.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*').replace(/\?/g, '[^/]');
  return new RegExp(`^${esc}$`);
}

/** The import arguments of `outline` (relative to `mainPath`'s folder) that already load `file`. */
export function importsCovering(outline: Outline, mainPath: string, file: string): string[] {
  const dir = dirOf(mainPath);
  return outline.imports.filter((imp) => {
    const abs = (imp.startsWith('/') ? imp : `${dir}/${imp}`).replace(/\/\.\//g, '/');
    return globToRe(abs).test(file);
  });
}

/** The argument we write: relative when the file sits next to the Caddyfile. */
export function importArg(mainPath: string, file: string): string {
  return dirOf(file) === dirOf(mainPath) ? file.slice(file.lastIndexOf('/') + 1) : file;
}

export interface Edit {
  text: string;
  changed: boolean;
}

/**
 * Adds `import <file>` at the end of the main Caddyfile, once. Unchanged when an import (a glob included) already
 * loads the file. Throws when the Caddyfile cannot be read safely.
 */
export function ensureImport(main: string, mainPath: string, file: string): Edit {
  const blocker = editBlocker(main);
  if (blocker) throw new Error(blocker);
  const r = scan(main);
  if (!r.ok) throw new Error(r.error);
  if (importsCovering(r.outline, mainPath, file).length) return { text: main, changed: false };
  const base = main.replace(/\s+$/, '');
  const add = `${IMPORT_COMMENT}\nimport ${importArg(mainPath, file)}\n`;
  return { text: base ? `${base}\n\n${add}` : add, changed: true };
}

/**
 * Removes the import lines that load exactly `file` (not globs, which load other files too), the comment we wrote
 * above them and the blank line before it.
 */
export function removeImport(main: string, mainPath: string, file: string): Edit {
  const r = scan(main);
  if (!r.ok) throw new Error(r.error);
  const want = new Set([file, importArg(mainPath, file), `./${importArg(mainPath, file)}`]);
  const lines = main.split('\n');
  const drop = new Set<number>();
  for (const e of r.outline.entries) {
    if (e.kind !== 'import' || e.head.length !== 2 || !want.has(e.head[1]) || e.start !== e.end) continue;
    drop.add(e.start);
    const k = e.start - 1;
    if (k >= 0 && lines[k].trim() === IMPORT_COMMENT) {
      drop.add(k);
      // ensureImport put one blank line before the comment.
      if (k > 0 && lines[k - 1].trim() === '') drop.add(k - 1);
    }
  }
  if (!drop.size) return { text: main, changed: false };
  let text = lines.filter((_, i) => !drop.has(i)).join('\n');
  if (main.endsWith('\n') && !text.endsWith('\n')) text += '\n';
  return { text, changed: true };
}

/* ---------- block kept inside the main Caddyfile ---------- */

/** Line range of our inline block (MARKER ... END_MARKER), or null. Throws when the markers do not pair up. */
export function findInline(main: string): { start: number; end: number } | null {
  const lines = main.replace(/\r\n?/g, '\n').split('\n');
  const starts = lines.flatMap((l, i) => (l.trim() === MARKER ? [i] : []));
  const ends = lines.flatMap((l, i) => (l.trim() === END_MARKER ? [i] : []));
  if (!starts.length && !ends.length) return null;
  if (starts.length !== 1 || ends.length !== 1 || ends[0] < starts[0]) throw new Error('The ErvisioRepo markers in the Caddyfile do not pair up');
  return { start: starts[0], end: ends[0] };
}

/** The inline block's text, or null. */
export function readInline(main: string): string | null {
  const at = findInline(main);
  if (!at) return null;
  return main.replace(/\r\n?/g, '\n').split('\n').slice(at.start, at.end + 1).join('\n') + '\n';
}

/** Puts `block` (from generateBlock(..., true)) in place of our inline block, or at the end of the Caddyfile. */
export function upsertInline(main: string, block: string): Edit {
  const blocker = editBlocker(main);
  if (blocker) throw new Error(blocker);
  const at = findInline(main);
  const lines = main.split('\n');
  let text: string;
  if (at) {
    const before = lines.slice(0, at.start).join('\n');
    const after = lines.slice(at.end + 1).join('\n');
    text = (before ? before + '\n' : '') + block + after;
  } else {
    const base = main.replace(/\s+$/, '');
    text = base ? `${base}\n\n${block}` : block;
  }
  return { text, changed: text !== main };
}

/** Removes our inline block and the blank line before it. */
export function removeInline(main: string): Edit {
  const at = findInline(main);
  if (!at) return { text: main, changed: false };
  const lines = main.split('\n');
  let start = at.start;
  if (start > 0 && lines[start - 1].trim() === '') start--;
  const kept = [...lines.slice(0, start), ...lines.slice(at.end + 1)];
  let text = kept.join('\n');
  if (main.endsWith('\n') && !text.endsWith('\n')) text += '\n';
  return { text, changed: true };
}

/* ---------- fallbacks ---------- */

/** Labels for caddy-docker-proxy, which reads sites from container labels instead of a Caddyfile. */
export function dockerProxyLabels(host: string, u: Upstream): string {
  return [
    `caddy: ${host}`,
    `caddy.reverse_proxy: "{{upstreams ${u.containerPort}}}"`,
    'caddy.reverse_proxy.flush_interval: -1',
    'caddy.request_body.max_size: 10GB',
  ].join('\n');
}

/** Caddy's output reduced to its error lines, without JSON log noise. */
export function cleanOutput(out: string): string {
  const lines = out.split('\n').map((l) => l.trim()).filter(Boolean);
  const err = lines.filter((l) => /^Error:|error/i.test(l) && !l.startsWith('{'));
  const pick = (err.length ? err : lines).slice(-3);
  return pick
    .map((l) => {
      if (!l.startsWith('{')) return l;
      try {
        const o = JSON.parse(l) as { msg?: string; error?: string };
        return [o.msg, o.error].filter(Boolean).join(': ');
      } catch {
        return l;
      }
    })
    .join('\n')
    .slice(0, 800);
}

/** The --config value of a Caddy command line, or /etc/caddy/Caddyfile. */
export function configFromArgv(argv: string[]): string {
  const i = argv.findIndex((a) => a === '--config' || a === '-c');
  if (i >= 0 && argv[i + 1]) return argv[i + 1];
  const eq = argv.find((a) => a.startsWith('--config='));
  return eq ? eq.slice(9) : '/etc/caddy/Caddyfile';
}

/** The adapter of a Caddy command line ("caddyfile" unless --adapter says otherwise or the config is .json). */
export function adapterFromArgv(argv: string[], configPath: string): string {
  const i = argv.indexOf('--adapter');
  if (i >= 0 && argv[i + 1]) return argv[i + 1];
  const eq = argv.find((a) => a.startsWith('--adapter='));
  if (eq) return eq.slice(10);
  if (/\.json$/i.test(configPath)) return 'json';
  return 'caddyfile';
}
