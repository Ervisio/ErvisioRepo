/**
 * The ErvisioRepo page in Ervisio: a header with the server's state, a grouped menu (chips on narrow screens), one
 * section at a time and, while settings wait for a restart, a bar to apply or undo them. With the server installed
 * but stopped the page stays, with the reason and the way to start it again; with nothing installed, the install
 * steps (Connect.tsx).
 */
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { getSdk } from '../sdk';
import * as repo from '../api/repo';
import * as service from '../api/service';
import { Button, EmptyState, Icon, Skeleton, toast } from '../kit';
import { Address } from './Address';
import { Connect } from './Connect';
import { FIELDS, type PageId } from './fields';
import { Maintenance } from './Maintenance';
import { Overview } from './Overview';
import { AccessPage, ActionsPage, GitPage, LogsPage, MailPage, WebhooksPage } from './Pages';
import { SettingsProvider, useSettings } from './SettingRows';
import { Storage } from './Storage';
import { errText, hostOf, isAdminError, isMissingError, locale, t } from './util';

const NAV: { group?: string; pages: PageId[] }[] = [
  { pages: ['overview', 'address', 'maintenance'] },
  { group: 'people', pages: ['access', 'mail'] },
  { group: 'code', pages: ['git', 'webhooks'] },
  { group: 'automation', pages: ['actions'] },
  { group: 'system', pages: ['storage', 'logs'] },
];
const PAGES = NAV.flatMap((g) => g.pages);

/** What the page knows about the server. */
export interface Server {
  status?: repo.RepoStatus;
  /** Milliseconds the last status call took. */
  latency?: number;
  /** The installation, once looked for (null: nothing installed; undefined: not known yet). */
  inst?: service.Installation | null;
  /** True when an installation exists but its API does not answer. */
  down: boolean;
  reload(): void;
  /** Restarts the server and waits until it answers again; false when that failed (already told). */
  restart(): Promise<boolean>;
  start(): Promise<void>;
  busy: 'restart' | 'start' | null;
  go(p: PageId): void;
  /** Version an update is going to, while it runs. */
  updating: string | null;
  setUpdating(v: string | null): void;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Polls the status until the server answers (and, with `version`, runs that version). */
export async function waitForServer(version?: string, timeoutMs = 120_000): Promise<repo.RepoStatus> {
  const end = Date.now() + timeoutMs;
  for (;;) {
    try {
      const s = await repo.status();
      if (!version || s.version === version) return s;
    } catch (e) {
      if (!isMissingError(e)) throw e;
    }
    if (Date.now() > end) throw new Error(t('svc.timeout'));
    await sleep(2000);
  }
}

function useTheme(): boolean {
  const sdk = getSdk();
  const [light, setLight] = useState(() => sdk.theme.get().kind === 'light');
  useEffect(() => sdk.theme.onChange(() => setLight(sdk.theme.get().kind === 'light')), [sdk]);
  return light;
}

export function RepoPage() {
  const light = useTheme();
  const [status, setStatus] = useState<repo.RepoStatus>();
  const [latency, setLatency] = useState<number>();
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [inst, setInst] = useState<service.Installation | null>();
  const [tick, setTick] = useState(0);
  const [page, setPage] = useState<PageId>('overview');
  const [busy, setBusy] = useState<Server['busy']>(null);
  const [updating, setUpdating] = useState<string | null>(null);
  const mainRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    setLoading(true);
    const t0 = performance.now();
    repo.status().then(
      (s) => {
        if (!live) return;
        setLatency(Math.round(performance.now() - t0));
        setStatus(s);
        setError(null);
        setLoading(false);
      },
      (e) => {
        if (!live) return;
        setStatus(undefined);
        setError(e);
        setLoading(false);
      },
    );
    service.detect().then((i) => live && setInst(i), () => live && setInst(null));
    return () => {
      live = false;
    };
  }, [tick]);

  const reload = useCallback(() => setTick((n) => n + 1), []);
  const go = useCallback((p: PageId) => {
    setPage(p);
    mainRef.current?.scrollIntoView({ block: 'start' });
  }, []);

  const restart = useCallback(async () => {
    if (!inst) {
      toast.err(t('svc.noInstall'));
      return false;
    }
    setBusy('restart');
    try {
      await service.restart(inst);
      await waitForServer();
      toast.ok(t('svc.restarted'));
      return true;
    } catch (e) {
      toast.err(t('svc.restartFailed'), errText(e));
      return false;
    } finally {
      setBusy(null);
      reload();
    }
  }, [inst, reload]);

  const start = useCallback(async () => {
    if (!inst) return;
    setBusy('start');
    try {
      await service.start(inst);
      await waitForServer(undefined, 90_000);
      toast.ok(t('svc.started'));
    } catch (e) {
      toast.err(t('svc.startFailed'), errText(e));
    } finally {
      setBusy(null);
      reload();
    }
  }, [inst, reload]);

  const root = `rp-root hue-term${light ? ' rp-light' : ''}`;
  const missing = !status && isMissingError(error);

  if (!status && (loading || (missing && inst === undefined))) {
    return (
      <div className={root}>
        <div className="rp-pad"><Skeleton height={56} style={{ borderRadius: 18 }} /></div>
        <div className="rp-pad"><Skeleton height={320} style={{ borderRadius: 18 }} /></div>
      </div>
    );
  }
  if (!status && !(missing && inst)) {
    return (
      <div className={root}>
        {missing ? <Connect error={error} onRetry={reload} /> : <Blocked error={error} onRetry={reload} />}
      </div>
    );
  }

  const srv: Server = { status, latency, inst, down: !status, reload, restart, start, busy, go, updating, setUpdating };
  return (
    <div className={root}>
      <SettingsProvider enabled={!!status}>
        <Shell srv={srv} page={page} mainRef={mainRef}>
          {page === 'overview' && <Overview srv={srv} />}
          {page === 'address' && (status ? <Address status={status} onChanged={reload} /> : <Offline />)}
          {page === 'maintenance' && <Maintenance srv={srv} />}
          {page === 'access' && <AccessPage />}
          {page === 'mail' && <MailPage />}
          {page === 'git' && <GitPage />}
          {page === 'webhooks' && <WebhooksPage />}
          {page === 'actions' && <ActionsPage srv={srv} />}
          {page === 'storage' && (status ? <Storage /> : <Offline />)}
          {page === 'logs' && <LogsPage />}
        </Shell>
      </SettingsProvider>
    </div>
  );
}

/** "for 6 days", "for 3 hours" from an ISO start time. */
function upFor(startedAt: string): string {
  const sec = (Date.now() - Date.parse(startedAt)) / 1000;
  if (!Number.isFinite(sec) || sec < 0) return '';
  const [n, unit] = sec >= 86400 ? [sec / 86400, 'day'] : sec >= 3600 ? [sec / 3600, 'hour'] : [Math.max(1, sec / 60), 'minute'];
  return new Intl.NumberFormat(locale(), { style: 'unit', unit, unitDisplay: 'long', maximumFractionDigits: 0 }).format(Math.floor(n));
}

/** "11:42" today, or the date and time, from an ISO or systemd timestamp. */
export function clock(ts: string): string {
  const d = new Date(ts.replace(/^[A-Z][a-z]{2} /, '').replace(/ [A-Z]{3,5}$/, ''));
  if (Number.isNaN(d.getTime())) return ts;
  const today = new Date().toDateString() === d.toDateString();
  return new Intl.DateTimeFormat(locale(), today ? { hour: '2-digit', minute: '2-digit' } : { dateStyle: 'medium', timeStyle: 'short' }).format(d);
}

function Pill({ tone, children }: { tone?: 'warn' | 'err' | 'info' | 'off'; children: ReactNode }) {
  return <span className={`rp-pill${tone ? ` is-${tone}` : ''}`}>{children}</span>;
}

function Shell({ srv, page, mainRef, children }: { srv: Server; page: PageId; mainRef: RefObject<HTMLDivElement>; children: ReactNode }) {
  const sdk = getSdk();
  const set = useSettings();
  const s = srv.status;
  const host = hostOf(s?.publicUrl || null);
  const newer = s && service.compareVersions(sdk.plugin.version, s.version) > 0 ? sdk.plugin.version : null;
  const restartNeeded = !!s && (s.restartRequired || (set?.modified.length ?? 0) > 0);

  const pill = srv.updating ? (
    <Pill tone="info">{t('head.updating', { v: srv.updating })}</Pill>
  ) : srv.down ? (
    srv.inst?.running ? <Pill tone="warn">{t('head.notAnswering')}</Pill>
      : <Pill tone="err">{srv.inst?.stoppedAt ? t('head.stoppedAt', { time: clock(srv.inst.stoppedAt) }) : t('head.stopped')}</Pill>
  ) : restartNeeded ? (
    <Pill tone="warn">{t('head.restartNeeded')}</Pill>
  ) : (
    <Pill>{s?.startedAt && upFor(s.startedAt) ? t('head.runningFor', { d: upFor(s.startedAt) }) : t('head.running')}</Pill>
  );

  const version = s?.version ?? '';
  const count = (p: PageId) => set ? new Set([...set.modified, ...set.unsaved].filter((k) => FIELDS[k]?.page === p)).size : 0;
  const item = (p: PageId, cls: string) => {
    const n = count(p);
    return (
      <button key={p} type="button" className={cls} aria-current={page === p ? 'page' : undefined} onClick={() => srv.go(p)}>
        {t(`nav.${p}`)}
        {n > 0 && <span className="rp-n" aria-label={t('nav.changes', { n })}>{n}</span>}
      </button>
    );
  };

  return (
    <>
      <header className="rp-head">
        <span className="rp-htile"><Icon name="server" /></span>
        <div className="rp-ttl">
          <h1>ErvisioRepo</h1>
          <div className="rp-meta">
            {pill}
            {version && <span>{t(srv.inst?.mode === 'docker' ? 'head.docker' : srv.inst?.mode === 'native' ? 'head.native' : 'head.version', { v: version })}</span>}
            {host && <button type="button" className="rp-link" onClick={() => sdk.openExternal(s!.publicUrl)}>{host}</button>}
            {newer && !srv.updating && <button type="button" className="rp-upd" onClick={() => srv.go('maintenance')}>{t('head.available', { v: newer })}</button>}
          </div>
        </div>
        <div className="rp-hacts">
          {srv.down ? (
            srv.inst?.running
              ? <Button icon="refresh" loading={srv.busy === 'restart'} disabled={!!srv.busy} onClick={() => void srv.restart()}>{t('svc.restart')}</Button>
              : <Button icon="play" loading={srv.busy === 'start'} disabled={!!srv.busy} onClick={() => void srv.start()}>{t('svc.start')}</Button>
          ) : (
            <>
              <Button icon="refresh" loading={srv.busy === 'restart'} disabled={!!srv.busy || !!srv.updating} onClick={() => void srv.restart()}>{t('svc.restart')}</Button>
              {s?.publicUrl && <Button variant={page === 'maintenance' ? 'secondary' : 'primary'} icon="externallink" onClick={() => sdk.openExternal(s.publicUrl)}>{t('head.open')}</Button>}
            </>
          )}
        </div>
      </header>

      <div className="rp-chips" role="navigation" aria-label={t('nav.label')}>
        {PAGES.map((p) => item(p, 'rp-chip'))}
      </div>

      <div className="rp-body">
        <nav className="rp-nav" aria-label={t('nav.label')}>
          {NAV.map((g) => (
            <div key={g.group ?? 'main'} className="rp-nav-g">
              {g.group && <div className="rp-grp">{t(`nav.g.${g.group}`)}</div>}
              {g.pages.map((p) => item(p, 'rp-it'))}
            </div>
          ))}
        </nav>
        <main className="rp-main" ref={mainRef}>
          {children}
        </main>
      </div>
      {!srv.down && <Bar srv={srv} restartNeeded={restartNeeded} />}
    </>
  );
}

/** Saved-but-not-active changes (or changes that could not be saved), with undo and restart. */
function Bar({ srv, restartNeeded }: { srv: Server; restartNeeded: boolean }) {
  const set = useSettings();
  const [undoing, setUndoing] = useState(false);
  if (!set) return null;
  const unsaved = set.unsaved.length;
  const mod = set.modified.length;
  if (!unsaved && !restartNeeded) return null;

  const keys = unsaved ? set.unsaved : set.modified;
  const per = new Map<PageId, number>();
  for (const k of keys) {
    const p = FIELDS[k]?.page;
    if (p) per.set(p, (per.get(p) ?? 0) + 1);
  }
  const sections = [...per].map(([p, n]) => `${t(`nav.${p}`)} (${n})`).join(', ');

  const undo = async () => {
    setUndoing(true);
    try {
      await set.undoAll();
    } finally {
      setUndoing(false);
    }
  };
  const apply = async () => {
    if (await srv.restart()) set.applied();
  };

  const title = unsaved
    ? t(unsaved === 1 ? 'bar.unsaved.one' : 'bar.unsaved.other', { n: unsaved })
    : mod
      ? t(mod === 1 ? 'bar.saved.one' : 'bar.saved.other', { n: mod })
      : t('bar.pending');
  return (
    <div className="rp-bar" role="status">
      <span className="rp-bar-i"><Icon name={unsaved ? 'alert' : 'info'} /></span>
      <div className="rp-bar-t">
        <b>{title}</b>
        {sections && <span>{sections}</span>}
      </div>
      {(unsaved > 0 || mod > 0) && (
        <Button variant="ghost" loading={undoing} disabled={set.busy || !!srv.busy} onClick={() => void undo()}>{t('bar.undo')}</Button>
      )}
      {unsaved ? (
        <Button variant="primary" loading={set.busy} onClick={() => void set.saveAll()}>{t('bar.save')}</Button>
      ) : (
        <Button variant="primary" icon="refresh" loading={srv.busy === 'restart'} disabled={!!srv.busy || set.busy} onClick={() => void apply()}>{t('bar.apply')}</Button>
      )}
    </div>
  );
}

/** A section that needs the server answering. */
function Offline() {
  return <div className="rp-err"><Icon name="power" /><div><b>{t('set.offline')}</b><span>{t('set.offlineText')}</span></div></div>;
}

/** A call failed: rights not unlocked, or another error, with retry. */
export function Blocked({ error, onRetry }: { error: unknown; onRetry(): void }) {
  if (isAdminError(error)) {
    return (
      <EmptyState icon="lock" hue="svc" title={t('admin.title')} text={t('admin.text')}
        action={<Button variant="primary" icon="unlock" onClick={onRetry}>{t('admin.unlock')}</Button>} />
    );
  }
  return (
    <EmptyState icon="alert" hue="svc" title={isMissingError(error) ? t('error.down') : t('error.title')} text={errText(error)}
      action={<Button icon="refresh" onClick={onRetry}>{t('retry')}</Button>} />
  );
}
