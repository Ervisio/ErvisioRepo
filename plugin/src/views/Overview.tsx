/** Overview: service, repositories, space and runners; containers and how to reach the server; why it stopped. */
import { useEffect, useState } from 'react';
import * as repo from '../api/repo';
import * as service from '../api/service';
import type { ContainerSummary } from '../api/docker';
import { Button, Icon, Skeleton } from '../kit';
import { clock, type Server } from './RepoPage';
import { bytes, hostOf, num, t, useLoad } from './util';

export function Overview({ srv }: { srv: Server }) {
  return (
    <>
      <div className="rp-pagehead"><h2>{t('nav.overview')}</h2></div>
      {srv.down && srv.inst && <DownBanner srv={srv} inst={srv.inst} />}
      {srv.status && <Tiles srv={srv} s={srv.status} />}
      <div className="rp-two">
        {srv.inst && <Containers inst={srv.inst} down={srv.down} />}
        {srv.status && <AccessCard s={srv.status} inst={srv.inst ?? null} />}
      </div>
    </>
  );
}

/** Classes for a log line: plain, warning or error. */
export function lineClass(l: string): string {
  if (/\b(ERROR|ERR|FATAL|PANIC)\b|level=(error|fatal)|"level":"(error|fatal)"/i.test(l)) return 'e';
  if (/\b(WARN|WARNING|WRN)\b|level=warn|"level":"warn"/i.test(l)) return 'w';
  return 't';
}

export function Term({ lines, className, empty }: { lines: string[]; className?: string; empty?: string }) {
  return (
    <div className={`rp-term${className ? ` ${className}` : ''}`} role="log">
      {lines.length ? lines.map((l, i) => <div key={i} className={lineClass(l)}>{l}</div>) : <div className="t">{empty ?? ''}</div>}
    </div>
  );
}

function DownBanner({ srv, inst }: { srv: Server; inst: service.Installation }) {
  const logs = useLoad(() => service.tailLogs(inst, 20), [inst]);
  const name = inst.mode === 'docker' ? (inst.app?.Names?.[0]?.replace(/^\//, '') ?? 'app') : service.UNIT;
  const title = inst.running
    ? t('down.notAnswering')
    : inst.stoppedAt ? t('down.stoppedAt', { time: clock(inst.stoppedAt) }) : t('down.stopped');
  const text = inst.running
    ? t('down.notAnsweringText', { name })
    : inst.exit
      ? t(inst.mode === 'docker' ? 'down.exitCode' : 'down.result', { name, code: inst.exit })
      : t('down.text', { name });
  return (
    <div className="rp-down" role="alert">
      <Icon name="alert" />
      <div className="rp-down-b">
        <b>{title}</b>
        <p>{text}</p>
        {logs.data ? <Term lines={logs.data} empty={t('logs.empty')} /> : logs.loading ? <Skeleton height={80} /> : null}
        <div className="rp-acts">
          {inst.running ? (
            <Button variant="primary" size="sm" icon="refresh" loading={srv.busy === 'restart'} disabled={!!srv.busy} onClick={() => void srv.restart()}>{t('svc.restart')}</Button>
          ) : (
            <Button variant="primary" size="sm" icon="play" loading={srv.busy === 'start'} disabled={!!srv.busy} onClick={() => void srv.start()}>{t('svc.start')}</Button>
          )}
          <Button variant="ghost" size="sm" onClick={() => srv.go('maintenance')}>{t('down.fullLog')}</Button>
        </div>
      </div>
    </div>
  );
}

function Tile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rp-tile2">
      <span className="lb">{label}</span>
      <span className="v">{value}</span>
      {sub && <span className="s">{sub}</span>}
    </div>
  );
}

function Tiles({ srv, s }: { srv: Server; s: repo.RepoStatus }) {
  const st = useLoad(repo.storage);
  const rn = useLoad(() => (s.actionsEnabled ? repo.runners() : Promise.resolve(null)), [s.actionsEnabled]);
  const used = st.data?.policies.reduce((a, p) => a + p.bytes, 0);
  const runners = rn.data;
  const online = runners?.filter((r) => r.online).length ?? 0;
  return (
    <div className="rp-grid">
      <Tile label={t('ov.service')} value={t('ov.active')} sub={srv.latency !== undefined ? t('ov.latency', { ms: srv.latency }) : undefined} />
      <Tile label={t('ov.repos')} value={s.counts ? num(s.counts.repos) : '-'}
        sub={s.counts ? t('ov.people', { users: num(s.counts.users), orgs: num(s.counts.orgs) }) : undefined} />
      <Tile label={t('ov.space')} value={used !== undefined ? bytes(used) : st.loading ? '…' : '-'}
        sub={st.data ? t(st.data.policies.length === 1 ? 'ov.policies.one' : 'ov.policies.other', { n: st.data.policies.length }) : undefined} />
      {!s.actionsEnabled ? (
        <Tile label={t('ov.runners')} value={t('ov.off')} sub={t('ov.actionsOff')} />
      ) : runners ? (
        <Tile label={t('ov.runners')} value={t('ov.of', { n: online, total: runners.length })}
          sub={runners.length - online > 0 ? t('ov.offline', { n: runners.length - online }) : runners.length ? t('ov.allOnline') : t('ov.noRunners')} />
      ) : (
        <Tile label={t('ov.runners')} value={rn.loading ? '…' : '-'} />
      )}
    </div>
  );
}

/** A container's state as a pill: healthy, running, unhealthy, exited with its code. */
function containerPill(c: ContainerSummary, down: boolean) {
  const status = c.Status ?? '';
  if (c.State === 'running') {
    if (/\(unhealthy\)/.test(status)) return <span className="rp-pill is-warn">{t('ct.unhealthy')}</span>;
    if (/\(health: starting\)/.test(status)) return <span className="rp-pill is-info">{t('ct.starting')}</span>;
    return <span className="rp-pill">{/\(healthy\)/.test(status) ? t('ct.healthy') : t('ct.running')}</span>;
  }
  if (c.State === 'restarting') return <span className="rp-pill is-warn">{t('ct.restarting')}</span>;
  const code = /Exited \((\d+)\)/.exec(status)?.[1];
  const tone = code && code !== '0' ? 'is-err' : down ? 'is-err' : 'is-off';
  return <span className={`rp-pill ${tone}`}>{code ? t('ct.exited', { code }) : t('ct.stopped')}</span>;
}

function Containers({ inst, down }: { inst: service.Installation; down: boolean }) {
  if (inst.mode === 'native') {
    return (
      <section className="rp-card">
        <h3>{t('ov.service')}</h3>
        <div className="rp-list">
          <div className="rp-li">
            <b className="rp-mono">{service.UNIT}</b>
            <span className="s">{t('ov.native')}</span>
            <span className={`rp-pill${inst.running ? '' : ' is-err'}`}>{inst.running ? t('ct.running') : t('ct.stopped')}</span>
          </div>
        </div>
      </section>
    );
  }
  return (
    <section className="rp-card">
      <h3>{t('ov.containers')}</h3>
      <div className="rp-list">
        {inst.containers.map((c) => (
          <div key={c.Id} className="rp-li">
            <b className="rp-mono">{c.Labels?.['com.docker.compose.service'] ?? c.Names?.[0]}</b>
            <span className="s rp-mono">{c.Image}</span>
            {containerPill(c, down)}
          </div>
        ))}
      </div>
    </section>
  );
}

function sshClone(s: repo.RepoStatus): string | null {
  if (!s.sshEnabled || !s.sshListen) return null;
  const host = hostOf(s.publicUrl || null)?.replace(/:\d+$/, '');
  const port = /:(\d+)$/.exec(s.sshListen)?.[1];
  if (!host || !port) return null;
  return port === '22' ? `ssh://git@${host}` : `ssh://git@${host}:${port}`;
}

function AccessCard({ s, inst }: { s: repo.RepoStatus; inst: service.Installation | null }) {
  const [cfg, setCfg] = useState(s.configPath ?? '');
  useEffect(() => {
    let live = true;
    if (inst && s.configPath) service.hostConfigPath(inst, s.configPath).then((p) => live && setCfg(p));
    return () => {
      live = false;
    };
  }, [inst, s.configPath]);
  const ssh = sshClone(s);
  return (
    <section className="rp-card">
      <h3>{t('ov.access')}</h3>
      <div className="rp-list">
        <div className="rp-li"><b>{t('ov.web')}</b><span className="s rp-mono">{s.publicUrl || t('ov.noUrl')}</span></div>
        <div className="rp-li"><b>{t('ov.ssh')}</b><span className="s rp-mono">{ssh ?? t('ov.sshOff')}</span></div>
        {cfg && <div className="rp-li"><b>{t('ov.config')}</b><span className="s rp-mono">{cfg}</span></div>}
      </div>
    </section>
  );
}
