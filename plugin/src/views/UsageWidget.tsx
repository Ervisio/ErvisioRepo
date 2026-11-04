/** Overview widget: the server's state and its address. It never asks for administrator rights. */
import { getSdk } from '../sdk';
import * as repo from '../api/repo';
import { Button } from '../kit';
import { hostOf, t, useLoad } from './util';

export function UsageWidget() {
  const st = useLoad(repo.statusQuiet);
  const open = () => getSdk().open('repo');

  if (!st.data) {
    if (st.loading) return <p className="rp-w-muted">{t('w.loading')}</p>;
    return <p className="rp-w-muted">{t('w.unavailable')}</p>;
  }
  const host = hostOf(st.data.publicUrl || null);
  return (
    <div className="rp-w">
      <div className="rp-w-st"><i /><b>{t('repo.running')}</b></div>
      <div className="rp-w-big">
        <b>{t('repo.version', { v: st.data.version })}</b>
        <span>{host ?? t('repo.noUrl')}</span>
      </div>
      <Button size="sm" onClick={open}>{t('w.open')}</Button>
    </div>
  );
}
