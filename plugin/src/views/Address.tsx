/**
 * Address and Caddy: proposes git.<domain> from the sites Caddy already serves, lets the user change it, then writes
 * the site, checks it with caddy validate, reloads Caddy (rolling back on failure) and sets the server's address.
 */
import { useEffect, useState } from 'react';
import * as caddy from '../api/caddy';
import { dockerProxyLabels, generateBlock, normalizeHost, proxyTarget } from '../api/caddy-core';
import * as repo from '../api/repo';
import { loadPrefs, savePrefs } from '../api/prefs';
import { Badge, Button, ConfirmDialog, Icon, Input, Skeleton, Switch, toast } from '../kit';
import { Blocked } from './RepoPage';
import { copy, t, useLoad } from './util';

async function load(): Promise<caddy.Proposal> {
  const prefs = await loadPrefs();
  return caddy.propose({ askDocker: prefs.askDocker ?? false });
}

export function Address({ status, onChanged }: { status: repo.RepoStatus; onChanged(): void }) {
  const pr = useLoad(load);
  const [host, setHost] = useState('');
  const [touched, setTouched] = useState(false);
  const [updateUrl, setUpdateUrl] = useState(true);
  const [busy, setBusy] = useState<'apply' | 'remove' | null>(null);
  const [result, setResult] = useState<caddy.CaddyResult | null>(null);
  const [confirm, setConfirm] = useState(false);

  const p = pr.data;
  const initial = p?.current?.host ?? p?.publishedHost ?? p?.suggestion?.host ?? '';
  useEffect(() => {
    if (!touched) setHost(initial);
  }, [initial, touched]);

  if (!p) {
    if (pr.loading) return <Skeleton height={320} style={{ borderRadius: 18 }} />;
    return <Blocked error={pr.error} onRetry={pr.reload} />;
  }

  const inst = p.instance;
  const norm = normalizeHost(host);
  const upstream = status.upstream;
  const target = inst ? caddy.targetFor(inst, upstream) : proxyTarget('system', upstream);
  const snippet = norm
    ? inst?.unsupported === 'labels' ? dockerProxyLabels(norm, upstream) : generateBlock(norm, target)
    : '';
  const isCurrent = !!p.current && norm === p.current.host;
  const isSuggested = !p.current && !!p.suggestion && norm === p.suggestion.host;
  const canApply = !!inst && !inst.unsupported && !!norm;

  const onApply = async () => {
    if (!inst || !norm) return;
    setBusy('apply');
    setResult(null);
    try {
      const r = await caddy.apply(inst, norm, upstream, { publicUrl: updateUrl });
      setResult(r);
      if (r.ok) toast.ok(t('ad.done', { host: norm }));
      if (r.ok || r.caddyApplied) {
        setTouched(false);
        pr.reload();
        onChanged();
      }
    } finally {
      setBusy(null);
    }
  };

  const onRemove = async () => {
    if (!inst) return;
    setBusy('remove');
    setResult(null);
    try {
      const r = await caddy.remove(inst);
      if (r.ok) {
        toast.ok(t('ad.removed'));
        setTouched(false);
        pr.reload();
      } else setResult(r);
    } finally {
      setBusy(null);
      setConfirm(false);
    }
  };

  const onCopy = async () => {
    if (await copy(result?.snippet ?? snippet)) toast.ok(t('ad.copied'));
  };

  const searchDocker = async () => {
    await savePrefs({ askDocker: true });
    pr.reload();
  };

  const done = result?.ok && result.step === 'done';
  return (
    <section className="rp-card">
      <h3>
        <span className="rp-ki hue-svc"><Icon name="globe" /></span>{t('nav.address')}
        <small>{p.current ? t('ad.configured') : inst ? t('ad.found') : ''}</small>
      </h3>

      {inst ? (
        <div className={`rp-note ${inst.unsupported ? 'is-warn' : 'is-ok'}`}>
          <Icon name={inst.unsupported ? 'alert' : 'check'} />
          <span>
            {t(inst.kind === 'system' ? 'ad.foundSystem' : 'ad.foundDocker', { name: inst.kind === 'system' ? 'caddy.service' : inst.name, path: inst.configPath })}
            {inst.unsupported && <span className="rp-block">{t(`ad.unsupported.${inst.unsupported}`)}</span>}
          </span>
        </div>
      ) : (
        <div className="rp-note is-info"><Icon name="info" /><span>{t('ad.none')}</span></div>
      )}
      {p.detection?.dockerSkipped && (
        <div className="rp-note is-info">
          <Icon name="lock" />
          <span className="rp-sp">{t('ad.dockerSkipped')}</span>
          <Button size="sm" icon="search" onClick={searchDocker}>{t('ad.searchDocker')}</Button>
        </div>
      )}

      <Input
        label={t('ad.host')}
        mono
        value={host}
        spellCheck={false}
        autoCapitalize="off"
        onChange={(e) => {
          setHost(e.target.value);
          setTouched(true);
        }}
        error={host && !norm ? t('ad.invalid') : undefined}
        hint={p.suggestion && !p.current ? t('ad.hostHint', { sites: p.suggestion.base }) : t('ad.hostHintPlain')}
        end={isCurrent ? <Badge tone="ok">{t('ad.current')}</Badge> : isSuggested ? <Badge tone="info">{t('ad.suggested')}</Badge> : undefined}
      />
      {p.suggestion?.renamed && !p.current && <p className="rp-muted rp-pad">{t('ad.renamed', { base: p.suggestion.base })}</p>}

      {canApply && (
        <div className="rp-set">
          <div className="rp-set-t"><b>{t('ad.updateUrl')}</b><span>{t('ad.updateUrlHint')}</span></div>
          <Switch checked={updateUrl} onChange={setUpdateUrl} aria-label={t('ad.updateUrl')} />
        </div>
      )}

      {done ? (
        <Steps inst={inst!} result={result!} publicUrl={updateUrl} />
      ) : result ? (
        <div className="rp-note is-err">
          <Icon name="alert" />
          <span>
            <b className="rp-block">{result.caddyApplied ? t('ad.done', { host: result.host ?? '' }) : t('ad.failed')}</b>
            <span className="rp-pre">{result.message}</span>
            {result.rolledBack && <span className="rp-block">{t('ad.rolledBack')}</span>}
          </span>
        </div>
      ) : null}
      {result?.warnings.map((w) => <div key={w} className="rp-note is-warn"><Icon name="alert" /><span>{w}</span></div>)}

      {!done && (result?.snippet ?? snippet) && <pre className="rp-snip">{result?.snippet ?? snippet}</pre>}

      <div className="rp-acts">
        {canApply && (
          <Button variant="primary" icon="wand" loading={busy === 'apply'} disabled={!!busy} onClick={onApply}>
            {p.current ? t('ad.update') : t('ad.apply')}
          </Button>
        )}
        <Button variant="ghost" icon="copy" disabled={!snippet} onClick={onCopy}>{t('ad.copy')}</Button>
        {p.current && inst && !inst.unsupported && (
          <Button variant="ghost" icon="trash" disabled={!!busy} onClick={() => setConfirm(true)}>{t('ad.remove')}</Button>
        )}
      </div>
      <p className="rp-muted"><Icon name="info" size={13} /> {t('ad.note')}</p>

      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={onRemove}
        danger
        icon="trash"
        title={t('ad.removeTitle')}
        description={t('ad.removeText', { host: p.current?.host ?? '' })}
        confirmLabel={t('ad.remove')}
        cancelLabel={t('cancel')}
      />
    </section>
  );
}

/** What the plugin did, in order, after a successful apply. */
function Steps({ inst, result, publicUrl }: { inst: caddy.CaddyInstance; result: caddy.CaddyResult; publicUrl: boolean }) {
  const steps =
    inst.layout === 'file'
      ? [t('ad.stepWrote', { file: caddy.repoFilePath(inst) }), t('ad.stepImport')]
      : [t('ad.stepInline')];
  steps.push(t('ad.stepValidate'), result.notRunning ? t('ad.stepStopped') : t('ad.stepReload'));
  if (publicUrl) steps.push(t('ad.stepUrl'));
  return (
    <ol className="rp-steps">
      {steps.map((s) => <li key={s}><Icon name="check" />{s}</li>)}
    </ol>
  );
}
