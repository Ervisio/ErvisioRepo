import { useState, type FormEvent } from "react";
import { acceptCodeShare, acceptCodeShareById, rejectCodeShare, revokeCodeShare, type CodeShare, type CodeShareScope, type useCodeShares } from "../../api/codeSharing.ts";
import type { RemoteSession } from "../../api/codeWorkspace.ts";
import { useT } from "../../i18n/useT.ts";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { SharingDialog, useSharingAction } from "./SharingDialog.tsx";

type ShareList = ReturnType<typeof useCodeShares>;

export function CodeShareList({ scope, query, titleOf, sessions, onShare, onRead, onRestoreFocus, onClose }: {
  scope: CodeShareScope;
  query: ShareList;
  titleOf: (share: CodeShare) => string;
  sessions: readonly RemoteSession[];
  onShare: (session: RemoteSession) => void;
  onRead: (share: CodeShare) => void;
  onRestoreFocus: () => void;
  onClose: () => void;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const [token, setToken] = useState("");
  const [accepted, setAccepted] = useState(false);
  const [rejected, setRejected] = useState(false);
  const [revoke, setRevoke] = useState("");
  const [choosing, setChoosing] = useState(false);
  const action = useSharingAction(query.refetch);
  const shares = query.isSuccess ? query.data.pages.flatMap((page) => page.shares) : [];
  const incoming = shares.filter((share) => share.ownerId !== scope.accountId && !share.revokedAt && share.role !== "proposer");
  const invitations = incoming.filter((share) => share.recipientId === scope.accountId && !share.orgId && !share.acceptedAt
    && share.inviteExpiresAt && Date.parse(share.inviteExpiresAt) > query.dataUpdatedAt);
  const received = incoming.filter((share) => share.orgId || (share.recipientId === scope.accountId && share.acceptedAt));
  const owned = shares.filter((share) => share.ownerId === scope.accountId);
  const blocked = action.busy || action.problem === "share.uncertain";

  function accept(invite: () => Promise<CodeShare>) {
    if (blocked) return;
    setAccepted(false);
    setRejected(false);
    void action.run(async () => {
      await invite();
      setToken("");
      setAccepted(true);
    });
  }

  function acceptCode(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (/^[A-Za-z0-9_-]{43}$/.test(token.trim())) accept(() => acceptCodeShare(scope, token.trim()));
  }

  return (
    <SharingDialog title={t("share.manage")} description={t("share.manageHint")}
      busy={action.busy} onClose={onClose} onRestoreFocus={onRestoreFocus}>
      {action.busy && <p role="status">{t("share.saving")}</p>}
      {action.problem && <p role="alert">{t(action.problem)}</p>}
      {accepted && <p role="status">{t("share.accepted")}</p>}
      {rejected && <p role="status">{t("share.rejectionConfirmed")}</p>}
      <section className="ec-new-form">
        <button type="button" className="ec-retry ec-share-start" aria-expanded={choosing} disabled={blocked}
          onClick={() => setChoosing(!choosing)}><Icon name="plus" /> {t("share.create")}</button>
        {choosing && (sessions.length ? <ul className="ec-share-chat-list" aria-label={t("share.chooseChat")}>
          {sessions.map((session) => <li key={`${session.deviceId}:${session.sessionId}`}>
            <button type="button" className="ec-rail-row" disabled={blocked} onClick={() => onShare(session)}>
              <span>{session.title || t("session.untitled")}</span>
            </button>
          </li>)}
        </ul> : <p role="status">{t("share.noOnlineChats")}</p>)}
      </section>
      {query.isError ? <div className="ec-new-form">
        <p role="alert">{t("share.listError")}</p>
        <button type="button" className="ec-retry" onClick={() => void query.refetch()}>{common("action.retry")}</button>
      </div> : query.isPending ? <p role="status">{common("state.loading")}</p> : <>
        <section className="ec-new-form">
          <h3>{t("share.invitations")}</h3>
          {!invitations.length && <p>{t("share.noInvitations")}</p>}
          {invitations.map((share) => {
            const sender = share.ownerDisplayName || share.ownerUsername;
            return <div className="ec-share-row" key={share.id}>
              <b>{titleOf(share)}</b>
              {sender && <div className="ec-share-sender">
                <AvatarImg className="ec-avatar" src={share.ownerAvatarUrl}>{sender.slice(0, 2).toLocaleUpperCase()}</AvatarImg>
                <span>{t("share.invitedBy", { name: sender })}</span>
              </div>}
              <p>{t(`share.role.${share.role}`)}</p>
              <div className="ec-share-invite-actions">
                <button type="button" className="ec-retry" disabled={blocked} onClick={() => void action.run(async () => {
                  setAccepted(false);
                  setRejected(false);
                  await rejectCodeShare(scope, share.id);
                  setRejected(true);
                })}>{t("share.reject")}</button>
                <button type="button" className="ec-new-submit" disabled={blocked} onClick={() => accept(() => acceptCodeShareById(scope, share.id))}>{t("share.accept")}</button>
              </div>
            </div>;
          })}
        </section>
        <section className="ec-new-form">
          <h3>{t("share.received")}</h3>
          {!received.length && <p>{t("share.noReceived")}</p>}
          {received.map((share) => <div className="ec-share-row" key={share.id}>
            <b>{titleOf(share)}</b>
            <p>{t(`share.role.${share.role}`)} · {t(share.orgId ? "share.org" : "share.personal")}</p>
            <button type="button" className="ec-retry" disabled={action.busy} onClick={() => onRead(share)}>{t("share.open")}</button>
          </div>)}
        </section>
        <section className="ec-new-form">
          <h3>{t("share.owned")}</h3>
          {!owned.length && <p>{t("share.noOwned")}</p>}
          {owned.map((share) => {
            const status = share.revokedAt ? "revoked" : share.orgId || share.acceptedAt ? "active"
              : share.inviteExpiresAt && Date.parse(share.inviteExpiresAt) <= query.dataUpdatedAt ? "expired" : "pending";
            return <div className="ec-share-row" key={share.id}>
              <b>{titleOf(share)}</b>
              <p>{t(`share.role.${share.role}`)} · {t(share.orgId ? "share.org" : "share.personal")} · {t(`share.status.${status}`)}</p>
              <p className="ec-share-id">{t(share.orgId ? "share.orgId" : "share.accountId")}: {share.orgId ?? share.recipientId}</p>
              {share.role === "direct" && status === "active" && <button type="button" className="ec-retry" disabled={action.busy}
                onClick={() => onRead(share)}>{t("collaboration.open")}</button>}
              {!share.revokedAt && (revoke === share.id ? <>
                <p>{t("share.revokeHint")}</p>
                <button type="button" className="ec-retry" disabled={blocked} onClick={() => void action.run(async () => {
                  await revokeCodeShare(scope, share.id);
                  setRevoke("");
                })}>{t("share.revokeConfirm")}</button>
                <button type="button" className="ec-retry" disabled={action.busy} onClick={() => setRevoke("")}>{t("share.cancel")}</button>
              </> : <button type="button" className="ec-retry" disabled={blocked} onClick={() => setRevoke(share.id)}>{t("share.revoke")}</button>)}
            </div>;
          })}
        </section>
        {query.hasNextPage && <button type="button" className="ec-retry" disabled={query.isFetching || action.busy}
          onClick={() => void query.fetchNextPage()}>{t("share.more")}</button>}
      </>}
      <details className="ec-new-form">
        <summary>{t("share.useCode")}</summary>
        <form className="ec-new-form" onSubmit={acceptCode}>
          <label>{t("share.code")}
            <input name="invite" type="password" autoComplete="off" autoCapitalize="none" spellCheck={false}
              value={token} onChange={(event) => { setToken(event.target.value); setAccepted(false); }} disabled={blocked} />
          </label>
          <div className="ec-new-actions">
            <button className="ec-new-submit" type="submit" disabled={blocked || !/^[A-Za-z0-9_-]{43}$/.test(token.trim())}>
              {t("share.accept")}
            </button>
          </div>
        </form>
      </details>
    </SharingDialog>
  );
}
