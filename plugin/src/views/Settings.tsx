/**
 * Server settings: sign-up, 2FA, Actions and Git limits. They are saved to the server's config.toml (PUT
 * /api/plugin/settings); the server validates the whole file and a restart applies the change.
 */
import { useEffect, useState } from 'react';
import * as repo from '../api/repo';
import { Button, Icon, Input, Select, Skeleton, Switch, toast } from '../kit';
import { Blocked } from './RepoPage';
import { errText, t, useLoad } from './util';

type Draft = Omit<repo.Settings, 'restartRequired' | 'limits'> & { limits: Omit<repo.Limits, 'gitRequestsPerHour'> & { gitRequestsPerHour: string } };

const toDraft = (s: repo.Settings): Draft => ({
  publicUrl: s.publicUrl,
  registration: s.registration,
  userDirectory: s.userDirectory,
  require2fa: s.require2fa,
  actionsEnabled: s.actionsEnabled,
  defaultActionsUrl: s.defaultActionsUrl,
  limits: { ...s.limits, gitRequestsPerHour: String(s.limits.gitRequestsPerHour) },
});

const opts = (prefix: string, values: string[]) => values.map((v) => ({ value: v, label: t(`${prefix}.${v}`) }));

/** Sizes like 2GB / 500MB and durations like 5m / 1h30m, as the server's config accepts them. */
const SIZE = /^\d+(\.\d+)?\s?(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)?$/i;
const DURATION = /^(\d+(\.\d+)?(ns|us|ms|s|m|h))+$/;

export function Settings({ onChanged }: { onChanged(): void }) {
  const st = useLoad(repo.getSettings);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (st.data) setDraft(toDraft(st.data));
  }, [st.data]);

  if (!st.data || !draft) {
    if (st.loading) return <Skeleton height={420} style={{ borderRadius: 18 }} />;
    return <Blocked error={st.error} onRetry={st.reload} />;
  }
  const base = toDraft(st.data);
  const set = (p: Partial<Draft>) => setDraft({ ...draft, ...p });
  const setLimit = (p: Partial<Draft['limits']>) => setDraft({ ...draft, limits: { ...draft.limits, ...p } });
  const perHour = Number(draft.limits.gitRequestsPerHour);
  const bad = {
    size: !SIZE.test(draft.limits.maxPushSize.trim()),
    idle: !DURATION.test(draft.limits.gitIdleTimeout.trim()),
    max: !DURATION.test(draft.limits.gitMaxDuration.trim()),
    rate: !Number.isInteger(perHour) || perHour < 0,
  };
  const invalid = Object.values(bad).some(Boolean);
  const changed = JSON.stringify(draft) !== JSON.stringify(base);

  const save = async () => {
    const patch: repo.SettingsPatch = {};
    if (draft.registration !== base.registration) patch.registration = draft.registration;
    if (draft.userDirectory !== base.userDirectory) patch.userDirectory = draft.userDirectory;
    if (draft.require2fa !== base.require2fa) patch.require2fa = draft.require2fa;
    if (draft.actionsEnabled !== base.actionsEnabled) patch.actionsEnabled = draft.actionsEnabled;
    if (draft.defaultActionsUrl !== base.defaultActionsUrl) patch.defaultActionsUrl = draft.defaultActionsUrl.trim();
    const limits: Partial<repo.Limits> = {};
    if (draft.limits.maxPushSize !== base.limits.maxPushSize) limits.maxPushSize = draft.limits.maxPushSize.trim();
    if (draft.limits.gitIdleTimeout !== base.limits.gitIdleTimeout) limits.gitIdleTimeout = draft.limits.gitIdleTimeout.trim();
    if (draft.limits.gitMaxDuration !== base.limits.gitMaxDuration) limits.gitMaxDuration = draft.limits.gitMaxDuration.trim();
    if (draft.limits.gitRequestsPerHour !== base.limits.gitRequestsPerHour) limits.gitRequestsPerHour = perHour;
    if (Object.keys(limits).length) patch.limits = limits;
    setSaving(true);
    try {
      await repo.updateSettings(patch);
      toast.ok(t('se.saved'), t('se.savedText'));
      st.reload();
      onChanged();
    } catch (e) {
      toast.err(t('error.title'), errText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <section className="rp-card">
        <h3><span className="rp-ki hue-usr"><Icon name="user" /></span>{t('se.access')}<small>{t('se.sub')}</small></h3>
        <div className="rp-rows">
          <div className="rp-set">
            <div className="rp-set-t"><b>{t('se.registration')}</b><span>{t('se.registrationHint')}</span></div>
            <div className="rp-set-c"><Select compact value={draft.registration} onChange={(v) => set({ registration: v })} options={opts('se.reg', ['open', 'invite', 'closed'])} /></div>
          </div>
          <div className="rp-set">
            <div className="rp-set-t"><b>{t('se.directory')}</b><span>{t('se.directoryHint')}</span></div>
            <div className="rp-set-c"><Select compact value={draft.userDirectory} onChange={(v) => set({ userDirectory: v })} options={opts('se.dir', ['shared', 'instance'])} /></div>
          </div>
          <div className="rp-set">
            <div className="rp-set-t"><b>{t('se.twofa')}</b><span>{t('se.twofaHint')}</span></div>
            <div className="rp-set-c"><Select compact value={draft.require2fa} onChange={(v) => set({ require2fa: v })} options={opts('se.fa', ['off', 'admins', 'all'])} /></div>
          </div>
        </div>
      </section>

      <section className="rp-card">
        <h3><span className="rp-ki hue-ov"><Icon name="play" /></span>{t('se.actions')}</h3>
        <div className="rp-rows">
          <div className="rp-set">
            <div className="rp-set-t"><b>{t('se.actionsEnabled')}</b><span>{t('se.actionsEnabledHint')}</span></div>
            <Switch checked={draft.actionsEnabled} onChange={(v) => set({ actionsEnabled: v })} aria-label={t('se.actionsEnabled')} />
          </div>
          <Input label={t('se.actionsUrl')} hint={t('se.actionsUrlHint')} mono value={draft.defaultActionsUrl} spellCheck={false}
            onChange={(e) => set({ defaultActionsUrl: e.target.value })} />
        </div>
      </section>

      <section className="rp-card">
        <h3><span className="rp-ki hue-log"><Icon name="cog" /></span>{t('se.limits')}</h3>
        <div className="rp-rows">
          <Input label={t('se.maxPush')} hint={t('se.maxPushHint')} mono value={draft.limits.maxPushSize} spellCheck={false}
            error={bad.size ? t('se.bad') : undefined} onChange={(e) => setLimit({ maxPushSize: e.target.value })} />
          <Input label={t('se.idle')} hint={t('se.durationHint')} mono value={draft.limits.gitIdleTimeout} spellCheck={false}
            error={bad.idle ? t('se.bad') : undefined} onChange={(e) => setLimit({ gitIdleTimeout: e.target.value })} />
          <Input label={t('se.maxDuration')} hint={t('se.durationHint')} mono value={draft.limits.gitMaxDuration} spellCheck={false}
            error={bad.max ? t('se.bad') : undefined} onChange={(e) => setLimit({ gitMaxDuration: e.target.value })} />
          <Input label={t('se.rate')} hint={t('se.rateHint')} inputMode="numeric" value={draft.limits.gitRequestsPerHour}
            error={bad.rate ? t('se.bad') : undefined} onChange={(e) => setLimit({ gitRequestsPerHour: e.target.value.replace(/\D/g, '') })} />
        </div>
        <div className="rp-acts rp-acts--end">
          <Button variant="primary" icon="check" loading={saving} disabled={!changed || invalid} onClick={save}>{t('se.save')}</Button>
        </div>
        <p className="rp-muted"><Icon name="info" size={13} /> {t('se.restartNote')}</p>
      </section>
    </>
  );
}
