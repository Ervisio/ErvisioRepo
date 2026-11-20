import { useState, type FormEvent } from "react";
import { acceptCodeShare, acceptCodeShareById, revokeCodeShare, type CodeShare, type CodeShareScope, type useCodeShares } from "../../api/codeSharing.ts";
import { useT } from "../../i18n/useT.ts";
import { SharingDialog, useSharingAction } from "./SharingDialog.tsx";

type ShareList = ReturnType<typeof useCodeShares>;

export function CodeShareList({ scope, query, titleOf, onRead, onReview, onRestoreFocus, onClose }: {
  scope: CodeShareScope;
  query: ShareList;
  titleOf: (share: CodeShare) => string;
  onRead: (share: CodeShare) => void;
  onReview: (share: CodeShare) => void;
  onRestoreFocus: () => void;
  onClose: () => void;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const [token, setToken] = useState("");
  const [accepted, setAccepted] = useState(false);
  const [revoke, setRevoke] = useState("");
  const action = useSharingAction(query.refetch);
  const shares = query.isSuccess ? query.data.pages.flatMap((page) => page.shares) : [];
  const incoming = shares.filter((share) => share.ownerId !== scope.accountId && !share.revokedAt);
  const invitations = incoming.filter((share) => share.recipientId === scope.accountId && !share.orgId && !share.acceptedAt
    && share.inviteExpiresAt && Date.parse(share.inviteExpiresAt) > query.dataUpdatedAt);
  const received = incoming.filter((share) => share.orgId || (share.recipientId === scope.accountId && share.acceptedAt));
  const owned = shares.filter((share) => share.ownerId === scope.accountId);
  const blocked = action.busy || action.problem === "share.uncertain";

  function accept(invite: () => Promise<CodeShare>) {
    if (blocked) return;
    setAccepted(false);
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
      {query.isError ? <div className="ec-new-form">
        <p role="alert">{t("share.listError")}</p>
        <button type="button" className="ec-retry" onClick={() => void query.refetch()}>{common("action.retry")}</button>
      </div> : query.isPending ? <p role="status">{common("state.loading")}</p> : <>
        <section className="ec-new-form">
          <h3>{t("share.invitations")}</h3>
          {!invitations.length && <p>{t("share.noInvitations")}</p>}
          {invitations.map((share) => <div className="ec-share-row" key={share.id}>
            <b>{titleOf(share)}</b>
            <p>{t(`share.role.${share.role}`)} · {t("share.personal")}</p>
            <button type="button" className="ec-retry" disabled={blocked} onClick={() => accept(() => acceptCodeShareById(scope, share.id))}>
              {t("share.accept")}
            </button>
          </div>)}
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
              {(status === "active" && share.role !== "reader") && <button type="button" className="ec-retry" disabled={action.busy}
                onClick={() => onReview(share)}>{t("proposal.review")}</button>}
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
