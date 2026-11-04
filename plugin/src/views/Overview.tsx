/** Overview: state of the server, its address, storage and Actions. */
import * as repo from '../api/repo';
import { Badge, Button, Icon, StatCard } from '../kit';
import type { SectionId } from './RepoPage';
import { hostOf, t, useLoad } from './util';

export function Overview({ status: s, go }: { status: repo.RepoStatus; go(id: SectionId): void }) {
  const runners = useLoad(repo.runners);
  const host = hostOf(s.publicUrl || null);
  const online = runners.data?.filter((r) => r.online).length;

  return (
    <>
      <div className="rp-grid4">
        <StatCard hue="term" icon="server" label={t('ov.version')} value={s.version || '-'} sub={s.commit ? s.commit.slice(0, 8) : undefined} />
        <StatCard hue="file" icon="disk" label={t('ov.storage')}
          value={<Badge tone={s.storageConfigured ? 'ok' : 'warn'}>{s.storageConfigured ? t('ov.configured') : t('ov.notConfigured')}</Badge>} />
        <StatCard hue="ov" icon="play" label={t('ov.actions')}
          value={<Badge tone={s.actionsEnabled ? 'ok' : 'neutral'}>{s.actionsEnabled ? t('ov.on') : t('ov.off')}</Badge>}
          sub={s.actionsEnabled && online !== undefined ? t('ov.runnersOnline', { n: online }) : undefined} />
        <StatCard hue="usr" icon="lock" label="SSH"
          value={<Badge tone={s.sshEnabled ? 'ok' : 'neutral'}>{s.sshEnabled ? t('ov.on') : t('ov.off')}</Badge>} />
      </div>

      <section className="rp-card">
        <h3><span className="rp-ki hue-svc"><Icon name="globe" /></span>{t('ov.address')}</h3>
        {host ? (
          <dl className="rp-kv">
            <dt>{t('ad.host')}</dt><dd className="rp-mono">{s.publicUrl}</dd>
            <dt>{t('ov.upstream')}</dt><dd className="rp-mono">{s.upstream.host}:{s.upstream.port}</dd>
          </dl>
        ) : (
          <p className="rp-muted rp-pad">{t('ov.addrNone')}</p>
        )}
        <div><Button size="sm" icon="globe" onClick={() => go('address')}>{t(host ? 'nav.address' : 'ov.addrSet')}</Button></div>
      </section>

      {!s.configWritable && (
        <div className="rp-note is-info"><Icon name="info" /><span>{t('ov.readonly')}</span></div>
      )}
    </>
  );
}
