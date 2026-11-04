/** Maintenance: update to the plugin's version, stop and restart, check config.toml, and the server's log. */
import { useEffect, useMemo, useRef, useState } from 'react';
import { getSdk } from '../sdk';
import * as repo from '../api/repo';
import * as service from '../api/service';
import { Button, ConfirmDialog, Icon, toast } from '../kit';
import { lineClass, Term } from './Overview';
import { waitForServer, type Server } from './RepoPage';
import { copy, errText, t } from './util';

export function Maintenance({ srv }: { srv: Server }) {
  return (
    <>
      <div className="rp-pagehead">
        <h2>{t('nav.maintenance')}</h2>
        <p className="rp-lead">{t('lead.maintenance')}</p>
      </div>
      {srv.inst && <Update srv={srv} inst={srv.inst} />}
      <ServiceCard srv={srv} />
      {srv.inst && <Logs inst={srv.inst} />}
    </>
  );
}

/* ---------- update ---------- */

type Phase = 'idle' | 'running' | 'check' | 'done' | 'failed';

function Update({ srv, inst }: { srv: Server; inst: service.Installation }) {
  const target = getSdk().plugin.version;
  const current = srv.status?.version ?? '';
  const available = !!current && service.compareVersions(target, current) > 0;
  const [phase, setPhase] = useState<Phase>('idle');
  const [step, setStep] = useState(-1);
  const [log, setLog] = useState<string[]>([]);
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const { setUpdating, reload } = srv;

  const steps = inst.mode === 'docker'
    ? [t('up.backup'), t('up.pull', { v: target }), t('up.up'), t('up.check')]
    : [t('up.package', { v: target }), t('up.check')];

  const alive = useRef(true);
  const instRef = useRef(inst);
  instRef.current = inst;
  useEffect(() => () => {
    alive.current = false;
    clearTimeout(timer.current);
  }, []);

  const finish = async () => {
    setPhase('check');
    try {
      await waitForServer(target, 180_000);
      if (!alive.current) return;
      setPhase('done');
      toast.ok(t('up.done', { v: target }));
    } catch (e) {
      if (!alive.current) return;
      setError(errText(e));
      setPhase('failed');
    } finally {
      if (alive.current) {
        setUpdating(null);
        reload();
      }
    }
  };

  const poll = async (first: boolean) => {
    const i = instRef.current;
    let p;
    try {
      p = await service.updateProgress(i);
    } catch (e) {
      if (!alive.current || first) return;
      setError(errText(e));
      setPhase('failed');
      setUpdating(null);
      return;
    }
    if (!alive.current) return;
    // The native installer shares its unit with the first install: only follow it when it is running an update.
    if (first && (p.state !== 'running' || (i.mode === 'native' && !/update/i.test(p.description)))) return;
    setLog(p.log);
    setStep(i.mode === 'docker' ? service.updateStep(p.log) : p.state === 'running' ? 0 : 1);
    if (p.state === 'failed') {
      setPhase('failed');
      setUpdating(null);
      return;
    }
    if (p.state === 'running') {
      setPhase('running');
      setUpdating(target);
      timer.current = setTimeout(() => void poll(false), 2000);
      return;
    }
    await finish();
  };

  useEffect(() => {
    void poll(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inst.mode]);

  const begin = async () => {
    setConfirm(false);
    setError('');
    setLog([]);
    setStep(-1);
    try {
      await service.startUpdate(inst, target);
      setUpdating(target);
      setPhase('running');
      clearTimeout(timer.current);
      timer.current = setTimeout(() => void poll(false), 2000);
    } catch (e) {
      setError(errText(e));
      setPhase('failed');
    }
  };

  const shown = phase !== 'idle' && phase !== 'done';
  const checking = phase === 'check';
  const last = steps.length - 1;
  const stepAt = checking ? last : inst.mode === 'docker' ? Math.min(step + 1, last) : 0;

  if (!available && !shown) {
    return (
      <section className="rp-card">
        <div className="rp-upd2">
          <div><b>{t('up.latest', { v: current || '-' })}</b><p>{t('up.latestText')}</p></div>
        </div>
      </section>
    );
  }
  const notes = `https://github.com/Ervisio/ErvisioRepo/releases/tag/v${target}`;
  return (
    <section className="rp-card">
      <div className="rp-upd2">
        <div>
          <b>{shown ? t('up.to', { v: target }) : t('up.available', { v: target })}</b>
          <p>{t('up.from', { v: current })}</p>
        </div>
        {!shown && (
          <div className="rp-acts">
            <Button variant="ghost" size="sm" icon="externallink" onClick={() => getSdk().openExternal(notes)}>{t('up.notes')}</Button>
            <Button variant="primary" size="sm" onClick={() => setConfirm(true)}>{t('up.update')}</Button>
          </div>
        )}
      </div>
      {shown && (
        <ol className="rp-steps2">
          {steps.map((label, i) => {
            const cls = phase === 'failed' && i === stepAt ? 'is-err' : i < stepAt ? 'is-ok' : i === stepAt && phase !== 'failed' ? 'is-now' : '';
            return <li key={label} className={cls} aria-current={cls === 'is-now' ? 'step' : undefined}><i />{label}</li>;
          })}
        </ol>
      )}
      {phase === 'failed' && (
        <div className="rp-pad">
          <div className="rp-err"><Icon name="alert" /><div><b>{t('up.failed')}</b>{error && <span>{error}</span>}</div></div>
          {log.length > 0 && <Term lines={log.slice(-30)} />}
          <div className="rp-acts"><Button size="sm" icon="refresh" onClick={() => setConfirm(true)}>{t('retry')}</Button></div>
        </div>
      )}
      <ConfirmDialog open={confirm} onClose={() => setConfirm(false)} onConfirm={() => void begin()}
        icon="download" title={t('up.confirmTitle', { v: target })} description={t(inst.mode === 'docker' ? 'up.confirmDocker' : 'up.confirmNative')}
        confirmLabel={t('up.update')} cancelLabel={t('cancel')} />
    </section>
  );
}

/* ---------- service ---------- */

function ServiceCard({ srv }: { srv: Server }) {
  const [stopping, setStopping] = useState(false);
  const [confirmStop, setConfirmStop] = useState(false);
  const [checking, setChecking] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; errors: repo.FieldError[] } | null>(null);
  const inst = srv.inst;

  const stop = async () => {
    setConfirmStop(false);
    if (!inst) return;
    setStopping(true);
    try {
      await service.stop(inst);
      toast.ok(t('svc.stopped'));
    } catch (e) {
      toast.err(t('svc.stopFailed'), errText(e));
    } finally {
      setStopping(false);
      srv.reload();
    }
  };
  const check = async () => {
    setChecking(true);
    try {
      setResult(await repo.checkConfig());
    } catch (e) {
      setResult({ ok: false, errors: [{ path: '', msg: errText(e) }] });
    } finally {
      setChecking(false);
    }
  };

  return (
    <section className="rp-card">
      <h3>{t('svc.title')}</h3>
      <div className="rp-row">
        <div className="rp-row-t"><span className="rp-row-h">{t('svc.text')}</span></div>
        <div className="rp-ctl">
          {srv.down && !inst?.running ? (
            <Button size="sm" icon="play" loading={srv.busy === 'start'} disabled={!inst || !!srv.busy} onClick={() => void srv.start()}>{t('svc.start')}</Button>
          ) : (
            <>
              <Button size="sm" icon="stop" loading={stopping} disabled={!inst || !!srv.busy || !!srv.updating} onClick={() => setConfirmStop(true)}>{t('svc.stop')}</Button>
              <Button size="sm" icon="refresh" loading={srv.busy === 'restart'} disabled={!inst || !!srv.busy || !!srv.updating} onClick={() => void srv.restart()}>{t('svc.restart')}</Button>
            </>
          )}
        </div>
      </div>
      <div className="rp-row">
        <div className="rp-row-t"><b>{t('svc.check')}</b><span className="rp-row-h">{t('svc.checkText')}</span></div>
        <div className="rp-ctl"><Button size="sm" loading={checking} disabled={srv.down} onClick={() => void check()}>{t('svc.checkBtn')}</Button></div>
        {result && (
          result.ok && !result.errors.length ? (
            <div className="rp-ok-msg"><Icon name="check" />{t('svc.checkOk')}</div>
          ) : (
            <div className="rp-msg rp-msg-list">
              {result.errors.map((e, i) => <span key={i}><Icon name="alert" />{e.path ? <><code>{e.path}</code> {e.msg}</> : e.msg}</span>)}
            </div>
          )
        )}
      </div>
      {!inst && <p className="rp-muted rp-pad">{t('svc.noInstall')}</p>}
      <ConfirmDialog open={confirmStop} onClose={() => setConfirmStop(false)} onConfirm={() => void stop()} danger icon="stop"
        title={t('svc.stopTitle')} description={t('svc.stopText')} confirmLabel={t('svc.stop')} cancelLabel={t('cancel')} />
    </section>
  );
}

/* ---------- logs ---------- */

type Level = 'all' | 'w' | 'e';
const MAX_LINES = 2000;

function Logs({ inst }: { inst: service.Installation }) {
  const [lines, setLines] = useState<string[]>([]);
  const [level, setLevel] = useState<Level>('all');
  const [paused, setPaused] = useState(false);
  const [error, setError] = useState('');
  const [gen, setGen] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (paused) return;
    setError('');
    let retry: ReturnType<typeof setTimeout> | undefined;
    const stop = service.watchLogs(
      inst,
      (l, replace) => {
        setError('');
        setLines((x) => (replace ? l : [...x, ...l]).slice(-MAX_LINES));
      },
      (e) => {
        setError(errText(e));
        if (inst.mode === 'docker') retry = setTimeout(() => setGen((n) => n + 1), 2000);
      },
    );
    if (inst.mode === 'docker') setLines([]);
    return () => {
      stop();
      clearTimeout(retry);
    };
  }, [inst, paused, gen]);

  const shown = useMemo(
    () => (level === 'all' ? lines : lines.filter((l) => (level === 'e' ? lineClass(l) === 'e' : lineClass(l) !== 't'))),
    [lines, level],
  );

  useEffect(() => {
    const el = ref.current?.querySelector('.rp-term');
    if (el && !paused) el.scrollTop = el.scrollHeight;
  }, [shown, paused]);

  const onCopy = async () => {
    if (await copy(shown.join('\n'))) toast.ok(t('logs.copied'));
  };
  const levels: { value: Level; label: string }[] = [
    { value: 'all', label: t('logs.all') },
    { value: 'w', label: t('logs.warn') },
    { value: 'e', label: t('logs.err') },
  ];
  return (
    <section className="rp-card rp-logcard" ref={ref}>
      <div className="rp-logh">
        <h3>{t('logs.title')}</h3>
        <div className="rp-seg" role="group" aria-label={t('logs.level')}>
          {levels.map((o) => (
            <button key={o.value} type="button" aria-pressed={level === o.value} onClick={() => setLevel(o.value)}>{o.label}</button>
          ))}
        </div>
        <Button variant="ghost" size="sm" icon={paused ? 'play' : 'pause'} onClick={() => setPaused((p) => !p)}>{paused ? t('logs.resume') : t('logs.pause')}</Button>
        <Button variant="ghost" size="sm" icon="copy" disabled={!shown.length} onClick={() => void onCopy()}>{t('logs.copy')}</Button>
      </div>
      {error && <p className="rp-muted rp-pad">{t('logs.failed', { e: error })}</p>}
      <Term lines={shown} empty={t('logs.empty')} />
    </section>
  );
}
