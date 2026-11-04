/** Storage targets, policies and caches, and the Actions runners. Read-only: storage is configured in config.toml. */
import * as repo from '../api/repo';
import { Badge, Icon, Skeleton } from '../kit';
import { Blocked } from './RepoPage';
import { bytes, num, t, useLoad } from './util';

export function Storage() {
  const st = useLoad(repo.storage);
  const rn = useLoad(repo.runners);

  if (!st.data) {
    if (st.loading) return <Skeleton height={320} style={{ borderRadius: 18 }} />;
    return <Blocked error={st.error} onRetry={st.reload} />;
  }
  const s = st.data;
  return (
    <>
      {s.warnings.length > 0 && s.warnings.map((w) => (
        <div key={`${w.code}${w.policy ?? ''}`} className="rp-note is-warn"><Icon name="alert" /><span>{t('st.warn', { code: w.code, policy: w.policy ?? '-' })}</span></div>
      ))}

      <section className="rp-card">
        <h3><span className="rp-ki hue-file"><Icon name="disk" /></span>{t('st.policies')}</h3>
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
        <h3><span className="rp-ki hue-sw"><Icon name="server" /></span>{t('st.targets')}</h3>
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

      <section className="rp-card">
        <h3><span className="rp-ki hue-ov"><Icon name="play" /></span>{t('st.runners')}</h3>
        {!rn.data ? (
          rn.loading ? <Skeleton height={64} /> : <p className="rp-muted rp-pad">{t('st.runnersFailed')}</p>
        ) : rn.data.length === 0 ? (
          <p className="rp-muted rp-pad">{t('st.noRunners')}</p>
        ) : (
          <table className="rp-table">
            <thead><tr><th>{t('st.name')}</th><th>{t('st.labels')}</th><th>{t('st.version')}</th><th /></tr></thead>
            <tbody>
              {rn.data.map((r) => (
                <tr key={r.id}>
                  <td>{r.name}</td>
                  <td>{r.labels.join(', ') || '-'}</td>
                  <td>{r.version || '-'}</td>
                  <td><Badge tone={r.online ? 'ok' : 'neutral'} dot>{r.online ? t('st.online') : t('st.offline')}</Badge></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
