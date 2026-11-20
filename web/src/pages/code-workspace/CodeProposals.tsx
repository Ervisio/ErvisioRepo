import { useState, type FormEvent } from "react";
import { reviewCodeProposal, submitCodeProposal, useCodeProposals, type CodeShare, type CodeShareScope } from "../../api/codeSharing.ts";
import { useT } from "../../i18n/useT.ts";
import { When } from "../code/common.tsx";
import { SharingDialog, useSharingAction } from "./SharingDialog.tsx";

const proposalLimit = 16_384;
const utf8 = new TextEncoder();

export function CodeProposals({ scope, share, onClose, onRestoreFocus, onUseDraft }: {
  scope: CodeShareScope;
  share: CodeShare;
  onClose: () => void;
  onRestoreFocus: () => void;
  onUseDraft?: (body: string) => boolean;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const owner = share.ownerId === scope.accountId;
  const query = useCodeProposals(scope, share.id);
  const action = useSharingAction(query.refetch);
  const [draft, setDraft] = useState("");
  const [saved, setSaved] = useState(false);
  const [draftConflict, setDraftConflict] = useState(false);
  const body = draft.trim();
  const invalid = body.includes("\0") || utf8.encode(body).length > proposalLimit;
  const blocked = !query.isSuccess || action.busy || action.problem === "share.uncertain";
  const proposals = query.isSuccess ? query.data.pages.flatMap((page) => page.proposals) : [];

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!body || invalid || owner || share.role !== "proposer" || blocked) return;
    void action.run(async () => {
      await submitCodeProposal(scope, share.id, body);
      setDraft("");
      setSaved(true);
    });
  }

  return (
    <SharingDialog title={t(owner ? "proposal.review" : "proposal.title")} description={t(owner ? "proposal.reviewHint" : "proposal.submitHint")}
      busy={action.busy} onClose={onClose} onRestoreFocus={onRestoreFocus}>
      {!owner && share.role === "proposer" && query.isSuccess && <form className="ec-new-form" onSubmit={submit}>
        <label>{t("proposal.label")}
          <textarea name="proposal" rows={4} autoComplete="off" maxLength={proposalLimit} value={draft} disabled={blocked}
            aria-invalid={invalid || undefined} onChange={(event) => { setDraft(event.target.value); setSaved(false); }} />
        </label>
        {invalid && <p role="alert">{t("proposal.invalid")}</p>}
        <div className="ec-new-actions">
          <button type="submit" className="ec-new-submit" disabled={!body || invalid || blocked}>{t("proposal.submit")}</button>
        </div>
        {saved && <p role="status">{t("proposal.saved")}</p>}
      </form>}
      {action.busy && <p role="status">{t("share.saving")}</p>}
      {action.problem && <p role="alert">{t(action.problem)}</p>}
      {draftConflict && <p role="alert">{t("proposal.draftConflict")}</p>}
      <section className="ec-new-form" aria-label={t("proposal.title")}>
        {query.isPending && <p role="status">{common("state.loading")}</p>}
        {query.isError && <>
          <p role="alert">{t("proposal.listError")}</p>
          <button type="button" className="ec-retry" onClick={() => void query.refetch()}>{common("action.retry")}</button>
        </>}
        {query.isSuccess && !proposals.length && <p>{t("proposal.empty")}</p>}
        {proposals.map((proposal) => <article className="ec-share-row ec-message" key={proposal.id}>
          <h2>{t(`proposal.status.${proposal.status}`)} · <When iso={proposal.createdAt} /></h2>
          {owner && <div className="ec-share-id">{t("proposal.author", { id: proposal.authorId })}</div>}
          <p>{proposal.body}</p>
          {owner && proposal.status === "pending" && <>
            <button type="button" className="ec-retry" disabled={blocked} onClick={() => void action.run(async () => {
              await reviewCodeProposal(scope, share.id, proposal.id, "approved");
            })}>{t("proposal.approve")}</button>
            <button type="button" className="ec-retry" disabled={blocked} onClick={() => void action.run(async () => {
              await reviewCodeProposal(scope, share.id, proposal.id, "rejected");
            })}>{t("proposal.reject")}</button>
          </>}
          {owner && proposal.status === "approved" && onUseDraft && <button type="button" className="ec-retry"
            disabled={blocked} onClick={() => setDraftConflict(!onUseDraft(proposal.body))}>{t("proposal.draft")}</button>}
        </article>)}
        {query.hasNextPage && <button type="button" className="ec-retry" disabled={query.isFetching || action.busy}
          onClick={() => void query.fetchNextPage()}>{t("share.more")}</button>}
      </section>
    </SharingDialog>
  );
}
