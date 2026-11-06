/** The settings pages: rows grouped in cards, plus the mail test and the Actions runners. */
import { useState } from 'react';
import * as repo from '../api/repo';
import { Button, ConfirmDialog, IconButton, Input, Icon, Skeleton, toast } from '../kit';
import { Term } from './Overview';
import { clock, type Server } from './RepoPage';
import { Row, SetCard, SettingsPage, useSettings } from './SettingRows';
import type { PageId } from './fields';
import { copy, errText, t, useLoad } from './util';

function Cards({ cards }: { cards: [string, string[]][] }) {
  return (
    <>
      {cards.map(([title, keys]) => (
        <SetCard key={title} title={t(title)}>
          {keys.map((k) => <Row key={k} k={k} />)}
        </SetCard>
      ))}
    </>
  );
}

function Simple({ id, cards }: { id: PageId; cards: [string, string[]][] }) {
  return <SettingsPage id={id}><Cards cards={cards} /></SettingsPage>;
}

export const AccessPage = () => (
  <Simple id="access" cards={[
    ['card.signup', ['security.registration', 'security.user_directory', 'auth.require_verified_email', 'auth.invite_ttl', 'auth.verify_token_ttl', 'auth.reset_token_ttl']],
    ['card.sessions', ['auth.password_min_length', 'auth.require_2fa', 'auth.session_idle_timeout', 'auth.session_max_age', 'auth.token_max_ttl']],
    ['card.limits', ['auth.login_free_failures_account', 'auth.login_free_failures_ip', 'auth.login_backoff_base', 'auth.login_backoff_max', 'auth.actions_per_ip_per_hour']],
    ['card.web', ['ui.default_layout']],
  ]} />
);

export const GitPage = () => (
  <Simple id="git" cards={[
    ['card.gitLimits', ['git.max_push_size', 'git.max_duration', 'git.idle_timeout', 'git.requests_per_hour']],
    ['card.ssh', ['ssh.enabled', 'server.ssh_listen', 'ssh.max_connections', 'ssh.login_grace_time']],
  ]} />
);

export const WebhooksPage = () => (
  <Simple id="webhooks" cards={[['card.delivery', ['webhooks.timeout', 'webhooks.max_attempts', 'webhooks.allow_private', 'webhooks.allowed_cidrs']]]} />
);

export const LogsPage = () => <Simple id="logs" cards={[['card.serverLog', ['log.level', 'log.format']]]} />;

/* ---------- mail ---------- */

export function MailPage() {
  return (
    <SettingsPage id="mail">
      <Cards cards={[['card.smtp', ['mail.enabled', 'mail.host', 'mail.port', 'mail.tls', 'mail.username', 'mail.password_file', 'mail.from', 'mail.helo_name', 'mail.timeout']]]} />
      <MailTest />
    </SettingsPage>
  );
}

function MailTest() {
  const s = useSettings();
  const [to, setTo] = useState('');
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<{ ok: boolean; text: string } | null>(null);
  const send = async () => {
    setBusy(true);
    setRes(null);
    try {
      await repo.testMail(to.trim());
      setRes({ ok: true, text: t('mail.sent', { to: to.trim() }) });
    } catch (e) {
      setRes({ ok: false, text: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  const pending = (s?.modified ?? []).some((k) => k.startsWith('mail.'));
  return (
    <SetCard title={t('mail.test')}>
      <div className="rp-row">
        <div className="rp-row-t">
          <b>{t('mail.to')}</b>
          <span className="rp-row-h">{t(pending ? 'mail.testPending' : 'mail.testText')}</span>
        </div>
        <div className="rp-ctl rp-ctl-wide">
          <Input compact type="email" value={to} aria-label={t('mail.to')} placeholder="name@example.com" spellCheck={false} autoCapitalize="off"
            onChange={(e) => setTo(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && to.trim() && void send()} />
          <Button size="sm" loading={busy} disabled={!to.trim()} onClick={() => void send()}>{t('mail.send')}</Button>
        </div>
        {res && (res.ok
          ? <div className="rp-ok-msg"><Icon name="check" />{res.text}</div>
          : <div className="rp-msg" role="alert"><Icon name="alert" />{res.text}</div>)}
      </div>
    </SetCard>
  );
}

/* ---------- actions ---------- */

export function ActionsPage({ srv }: { srv: Server }) {
  return (
    <SettingsPage id="actions">
      <Cards cards={[
        ['card.actions', ['actions.enabled', 'actions.default_actions_url', 'actions.max_job_timeout', 'actions.max_jobs_per_run', 'actions.runner_offline_after', 'actions.registration_token_ttl']],
        ['card.cache', ['actions.cache_max_repo_size', 'actions.cache_ttl', 'actions.artifact_retention']],
      ]} />
      <Runners on={!!srv.status?.actionsEnabled} />
    </SettingsPage>
  );
}

const notEnabled = (e: unknown) => e instanceof repo.RepoError && e.status === 501;

function Runners({ on }: { on: boolean }) {
  const rn = useLoad(() => (on ? repo.runners() : Promise.resolve([] as repo.Runner[])), [on]);
  const [token, setToken] = useState<repo.RunnerToken | null>(null);
  const [tokenBusy, setTokenBusy] = useState(false);
  const [tokenErr, setTokenErr] = useState('');
  const [del, setDel] = useState<repo.Runner | null>(null);

  const newToken = async () => {
    setTokenBusy(true);
    setTokenErr('');
    try {
      setToken(await repo.runnerToken());
    } catch (e) {
      setTokenErr(notEnabled(e) ? t('run.enableFirst') : errText(e));
    } finally {
      setTokenBusy(false);
    }
  };
  const remove = async () => {
    const r = del;
    setDel(null);
    if (!r) return;
    try {
      await repo.deleteRunner(r.id);
      toast.ok(t('run.deleted', { name: r.name }));
      rn.reload();
    } catch (e) {
      toast.err(t('run.deleteFailed'), notEnabled(e) ? t('run.enableFirst') : errText(e));
    }
  };
  const onCopy = async () => {
    if (token && (await copy(token.command))) toast.ok(t('run.copied'));
  };

  const action = on ? <Button size="sm" icon="plus" loading={tokenBusy} onClick={() => void newToken()}>{t('run.token')}</Button> : undefined;
  return (
    <SetCard title={t('run.title')} action={action}>
      {!on ? (
        <p className="rp-muted rp-pad">{t('run.off')}</p>
      ) : (
        <>
          {tokenErr && <div className="rp-pad"><div className="rp-err"><Icon name="alert" /><div><b>{t('run.tokenFailed')}</b><span>{tokenErr}</span></div></div></div>}
          {token && (
            <div className="rp-token">
              <span className="rp-row-h">{t('run.tokenText', { time: clock(token.expiresAt) })}</span>
              <Term lines={[token.command]} />
              <div className="rp-acts"><Button size="sm" icon="copy" onClick={() => void onCopy()}>{t('run.copy')}</Button></div>
            </div>
          )}
          {!rn.data ? (
            rn.loading ? <div className="rp-pad"><Skeleton height={48} /></div>
              : <p className="rp-muted rp-pad">{notEnabled(rn.error) ? t('run.enableFirst') : t('run.failed', { e: errText(rn.error) })}</p>
          ) : rn.data.length === 0 ? (
            <p className="rp-muted rp-pad">{t('run.none')}</p>
          ) : (
            <div className="rp-list">
              {rn.data.map((r) => (
                <div key={r.id} className="rp-li rp-li-act">
                  <b>{r.name}</b>
                  <span className="s">
                    {[r.labels.join(', '), r.version, !r.online && r.lastSeenAt ? t('run.lastSeen', { time: clock(r.lastSeenAt) }) : '']
                      .filter(Boolean).join('. ')}
                  </span>
                  <span className={`rp-pill${r.online ? '' : ' is-off'}`}>{r.online ? t('run.online') : t('run.offline')}</span>
                  <IconButton icon="trash" variant="ghost" size="sm" label={t('run.delete', { name: r.name })} onClick={() => setDel(r)} />
                </div>
              ))}
            </div>
          )}
        </>
      )}
      <ConfirmDialog open={!!del} onClose={() => setDel(null)} onConfirm={() => void remove()} danger icon="trash"
        title={t('run.deleteTitle', { name: del?.name ?? '' })} description={t('run.deleteText')}
        confirmLabel={t('run.deleteBtn')} cancelLabel={t('cancel')} />
    </SetCard>
  );
}
