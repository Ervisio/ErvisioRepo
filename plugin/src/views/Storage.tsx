/** Storage targets, policies and caches. Read-only: storage is configured in config.toml. */
import * as repo from '../api/repo';
import { Badge, Icon, Skeleton } from '../kit';
import { Blocked } from './RepoPage';
import { bytes, num, t, useLoad } from './util';

export function Storage() {
  const st = useLoad(repo.storage);

  const head = (
    <div className="rp-pagehead">
      <h2>{t('nav.storage')}</h2>
      <p className="rp-lead">{t('lead.storage')}</p>
    </div>
  );
  if (!st.data) {
    return <>{head}{st.loading ? <Skeleton height={320} style={{ borderRadius: 18 }} /> : <Blocked error={st.error} onRetry={st.reload} />}</>;
  }
  const s = st.data;
  return (
    <>
      {head}
      {s.warnings.length > 0 && s.warnings.map((w) => (
        <div key={`${w.code}${w.policy ?? ''}`} className="rp-note is-warn"><Icon name="alert" /><span>{t('st.warn', { code: w.code, policy: w.policy ?? '-' })}</span></div>
      ))}

      <section className="rp-card">
        <h3>{t('st.policies')}</h3>
        {s.policies.length === 0 ? (
          <p className="rp-muted rp-pad">{t('st.noPolicies')}</p>
        ) : (
          <table className="rp-table">
            <thead><tr><th>{t('st.name')}</th><th>{t('st.primary')}</th><th>{t('st.replicas')}</th><th>{t('st.objects')}</th><th>{t('st.size')}</th><th /></tr></thead>
            <tbody>
              {s.policies.map((p) => (
                <tr key={p.name}>
                  <td className="rp-mono">{p.name}</td>
                  <td>{p.primary}</td>
                  <td>{p.replicas.join(', ') || '-'}</td>
                  <td>{num(p.objects)}</td>
                  <td>{bytes(p.bytes)}</td>
                  <td>
                    {p.errors > 0 ? <Badge tone="err">{t('st.errors', { n: p.errors })}</Badge>
                      : p.pendingReplicas > 0 ? <Badge tone="warn">{t('st.pending', { n: p.pendingReplicas })}</Badge>
                        : <Badge tone="ok">{p.durability}</Badge>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="rp-card">
        <h3>{t('st.targets')}</h3>
        <table className="rp-table">
          <thead><tr><th>{t('st.name')}</th><th>{t('st.kind')}</th><th>{t('st.roles')}</th><th>{t('st.budget')}</th><th /></tr></thead>
          <tbody>
            {s.targets.map((x) => {
              const cache = s.caches.find((c) => c.target === x.name);
              return (
                <tr key={x.name}>
                  <td className="rp-mono">{x.name}</td>
                  <td>{x.kind}</td>
                  <td>{x.roles.join(', ')}</td>
                  <td>{x.budget ? bytes(x.budget) : '-'}{cache && <span className="rp-muted"> {t('st.cache', { used: bytes(cache.used), n: num(cache.entries) })}</span>}</td>
                  <td>{x.encrypted && <Badge tone="info">{t('st.encrypted')}</Badge>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>
    </>
  );
}
