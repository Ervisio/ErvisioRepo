/**
 * When no server answers: three steps, Search / Install / Done. The page installs ErvisioRepo with Docker or as a
 * native package (install.ts) and follows the installation, also after a reload, until the unit finishes.
 */
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { getSdk, type PluginError } from '../sdk';
import * as install from '../api/install';
import { Button, Icon, Input, Segmented, toast } from '../kit';
import { errText, t } from './util';

function Steps({ at, done }: { at: number; done: number }) {
  return (
    <ol className="rp-wz-steps">
      {['search', 'install', 'done'].map((s, k) => (
        <li key={s} className={k < done ? 'is-ok' : k === at ? 'is-on' : undefined} aria-current={k === at ? 'step' : undefined}>
          <b>{k < done ? <Icon name="check" /> : k + 1}</b>
          {t(`cn.step.${s}`)}
        </li>
      ))}
    </ol>
  );
}

export function Log({ lines }: { lines: string[] }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [lines.length]);
  if (!lines.length) return null;
  return (
    <pre className="rp-snip rp-wz-log" ref={ref}>
      {lines.slice(0, -1).join('\n')}
      {lines.length > 1 && '\n'}
      <b>{lines[lines.length - 1]}</b>
    </pre>
  );
}

export const adminDeclined = (e: unknown) => (e as Partial<PluginError>)?.code === 'needs_admin';

export function Connect({ error, onRetry }: { error: unknown; onRetry(): void }) {
  const [prog, setProg] = useState<install.InstallProgress | null>(null);
  const [mode, setMode] = useState<install.InstallMode>('native');
  const [docker, setDocker] = useState<boolean | null>(null);
  const [email, setEmail] = useState('');
  const [url, setUrl] = useState('http://localhost:3400');
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    install.progress().then((p) => live && p.state !== 'idle' && setProg(p), () => {});
    install.hasDocker().then((d) => live && setDocker(d), () => live && setDocker(false));
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (prog?.state !== 'running') return;
    const id = setInterval(() => {
      install.progress().then(setProg, () => {});
    }, 2000);
    return () => clearInterval(id);
  }, [prog?.state]);

  const emailOk = mode === 'native' || install.EMAIL.test(email.trim());
  const urlOk = mode === 'native' || install.URL_RE.test(url.trim().replace(/\/$/, ''));
  const submit = async () => {
    setTouched(true);
    if (!emailOk || !urlOk) return;
    setBusy(true);
    try {
      await install.start(mode, { email: email.trim(), url: url.trim().replace(/\/$/, '') });
      setProg({ state: 'running', log: [], mode });
    } catch (e) {
      toast.err(t('cn.startFailed'), adminDeclined(e) ? t('admin.text') : errText(e));
    } finally {
      setBusy(false);
    }
  };

  const searchBtn = <Button variant="ghost" icon="refresh" onClick={onRetry}>{t('cn.search')}</Button>;
  const form = (
    <>
      <Segmented aria-label={t('cn.mode')} value={mode} onChange={(v) => setMode(v as install.InstallMode)}
        options={[{ value: 'native', label: t('cn.native') }, { value: 'docker', label: t('cn.docker') }]} />
      <p className="rp-wz-hint">{t(mode === 'docker' ? 'cn.dockerHint' : 'cn.nativeHint', { v: getSdk().plugin.version, dir: install.INSTALL_DIR })}</p>
      {mode === 'docker' && docker === false && <div className="rp-note is-warn"><Icon name="alert" /><span>{t('cn.nodocker')}</span></div>}
      {mode === 'docker' && (
        <div className="rp-wz-form">
          <Input label={t('cn.email')} type="email" value={email} autoComplete="email" spellCheck={false}
            onChange={(e) => setEmail(e.target.value)} error={touched && !emailOk ? t('cn.emailBad') : undefined} />
          <Input label={t('cn.url')} mono value={url} spellCheck={false}
            onChange={(e) => setUrl(e.target.value)} error={touched && !urlOk ? t('cn.urlBad') : undefined} />
        </div>
      )}
    </>
  );

  let at = 1;
  let done = 1;
  let body: ReactNode;
  if (prog?.state === 'running') {
    body = (
      <>
        <h2>{t('cn.running.title')}</h2>
        <p>{t('cn.running.text', { unit: install.UNIT })}</p>
        <Log lines={prog.log} />
      </>
    );
  } else if (prog?.state === 'done') {
    at = 2;
    done = 3;
    body = (
      <>
        <h2>{t('cn.done.title')}</h2>
        <div className="rp-note is-ok"><Icon name="check" /><span>{t('cn.done.text')}</span></div>
        <Log lines={prog.log} />
        <div className="rp-acts"><Button variant="primary" icon="overview" onClick={onRetry}>{t('cn.panel')}</Button></div>
      </>
    );
  } else if (prog?.state === 'failed') {
    body = (
      <>
        <h2>{t('cn.failed.title')}</h2>
        <div className="rp-note is-warn"><Icon name="alert" /><span>{t('cn.failed.text')}</span></div>
        <Log lines={prog.log} />
        {form}
        <div className="rp-acts">
          <Button variant="primary" icon="refresh" loading={busy} onClick={submit}>{t('cn.retry')}</Button>
          {searchBtn}
        </div>
      </>
    );
  } else {
    at = 0;
    done = 0;
    body = (
      <>
        <h2>{t('cn.missing.title')}</h2>
        <p>{t('cn.missing.text')}</p>
        <p className="rp-muted">{errText(error)}</p>
        {form}
        <div className="rp-acts">
          <Button variant="primary" icon="download" loading={busy} onClick={submit}>{t('cn.install')}</Button>
          {searchBtn}
        </div>
      </>
    );
  }

  return (
    <>
      <header className="rp-ph">
        <span className="rp-tile"><Icon name="server" /></span>
        <div className="rp-ph-t">
          <h1>ErvisioRepo</h1>
          <p>{t('cn.sub')}</p>
        </div>
      </header>
      <div className="rp-wz">
        <Steps at={at} done={done} />
        <div className="rp-card rp-wz-card">{body}</div>
      </div>
    </>
  );
}
