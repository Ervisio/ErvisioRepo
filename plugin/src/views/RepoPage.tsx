/**
 * The ErvisioRepo page in Ervisio: a header with the server's state, an inner menu, and one section at a time
 * (overview, settings, address and Caddy, storage and runners). With no server answering, the install steps
 * (Connect.tsx).
 */
import { useState } from 'react';
import { getSdk } from '../sdk';
import * as repo from '../api/repo';
import { Badge, Button, EmptyState, Icon, Skeleton, type HueId } from '../kit';
import { Address } from './Address';
import { Connect } from './Connect';
import { Overview } from './Overview';
import { Settings } from './Settings';
import { Storage } from './Storage';
import { errText, hostOf, isAdminError, isMissingError, t, useLoad } from './util';

export type SectionId = 'overview' | 'settings' | 'address' | 'storage';

const SECTIONS: { id: SectionId; hue: HueId; icon: string }[] = [
  { id: 'overview', hue: 'ov', icon: 'overview' },
  { id: 'settings', hue: 'log', icon: 'cog' },
  { id: 'address', hue: 'svc', icon: 'globe' },
  { id: 'storage', hue: 'file', icon: 'disk' },
];

export function RepoPage() {
  const st = useLoad(repo.status);
  const [section, setSection] = useState<SectionId>('overview');

  if (!st.data && st.loading) {
    return (
      <div className="rp-root hue-term">
        <Skeleton height={56} style={{ borderRadius: 18 }} />
        <div className="rp-inner"><Skeleton height={200} style={{ borderRadius: 18 }} /><Skeleton height={320} style={{ borderRadius: 18 }} /></div>
      </div>
    );
  }
  if (!st.data) {
    return (
      <div className="rp-root hue-term">
        <Blocked error={st.error} onRetry={st.reload} />
      </div>
    );
  }

  const s = st.data;
  const host = hostOf(s.publicUrl || null);
  return (
    <div className="rp-root hue-term">
      <header className="rp-ph">
        <span className="rp-tile"><Icon name="server" /></span>
        <div className="rp-ph-t">
          <h1>ErvisioRepo</h1>
          <p>
            <Badge tone="ok" dot>{t('repo.running')}</Badge>
            {s.version && <span>{t('repo.version', { v: s.version })}</span>}
            {host ? (
              <button type="button" className="rp-linkbtn" onClick={() => getSdk().openExternal(s.publicUrl)}>{host}</button>
            ) : (
              <span>{t('repo.noUrl')}</span>
            )}
          </p>
        </div>
        {s.publicUrl && (
          <Button variant="primary" icon="externallink" onClick={() => getSdk().openExternal(s.publicUrl)}>{t('repo.open')}</Button>
        )}
      </header>
      {s.restartRequired && (
        <div className="rp-note is-warn" role="status"><Icon name="alert" /><span>{t('repo.restart')}</span></div>
      )}

      <div className="rp-inner">
        <nav className="rp-nav" aria-label={t('nav.label')}>
          <div className="rp-nav-lab">{t('nav.label')}</div>
          {SECTIONS.map((it) => (
            <button key={it.id} type="button" className="rp-nav-it" aria-current={section === it.id ? 'page' : undefined} onClick={() => setSection(it.id)}>
              <span className={`rp-ki hue-${it.hue}`}><Icon name={it.icon} /></span>
              {t(`nav.${it.id}`)}
            </button>
          ))}
        </nav>
        <div className="rp-main">
          {section === 'overview' && <Overview status={s} go={setSection} />}
          {section === 'settings' && <Settings onChanged={st.reload} />}
          {section === 'address' && <Address status={s} onChanged={st.reload} />}
          {section === 'storage' && <Storage />}
        </div>
      </div>
    </div>
  );
}

/** Why the page cannot show the server: not installed or stopped (install steps), rights not unlocked, or another error. */
export function Blocked({ error, onRetry }: { error: unknown; onRetry(): void }) {
  if (isMissingError(error)) return <Connect error={error} onRetry={onRetry} />;
  if (isAdminError(error)) {
    return (
      <EmptyState icon="lock" hue="svc" title={t('admin.title')} text={t('admin.text')}
        action={<Button variant="primary" icon="unlock" onClick={onRetry}>{t('admin.unlock')}</Button>} />
    );
  }
  return (
    <EmptyState icon="alert" hue="svc" title={t('error.title')} text={errText(error)}
      action={<Button icon="refresh" onClick={onRetry}>{t('retry')}</Button>} />
  );
}
