import { sendCodeCollaborationCommand, useSharedCodeState, type CodeShare, type CodeShareScope } from "../../api/codeSharing.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { ChatComposer, TodoPanel, Transcript } from "./CodeConversation.tsx";

export function SharedCodeConversation({ scope, share }: {
  scope: CodeShareScope;
  share: CodeShare;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const state = useSharedCodeState(scope, share.id);
  const direct = share.role === "direct";
  const available = direct && state.isSuccess && state.data.textCollaboration;
  const messages = state.isSuccess ? state.data.messages : [];
  const collaborationBusy = available && state.data.collaborationBusy;
  const busy = available && (state.data.busy || collaborationBusy);
  const canInterrupt = collaborationBusy && (scope.accountId === share.ownerId
    || state.data.collaborationContributorId === scope.accountId);
  const status = state.error instanceof ApiError ? state.error.status : undefined;
  const notice = state.isPending ? common("state.loading")
    : state.isError ? t(status === 422 ? "share.restrictedUnavailable" : status === 404 ? "share.offlineOrRevoked" : "share.stateError")
      : !messages.length ? t("share.noMessages") : undefined;

  return (
    <>
      <Transcript messages={messages} accountId={scope.accountId} ownerId={share.ownerId}
        ownerName={share.ownerDisplayName || share.ownerUsername} notice={notice}
        isError={state.isError} retry={state.isError ? () => void state.refetch() : undefined} />
      {state.isSuccess && <TodoPanel todos={state.data.todos} />}
      {direct ? <ChatComposer ready={available} busy={busy} canInterrupt={canInterrupt} expired={status === 401 || status === 404}
        status={t(!available && state.isSuccess ? "collaboration.unavailable" : "collaboration.groupChat")}
        send={(command) => sendCodeCollaborationCommand(scope, share.id, command)} refresh={state.refetch} />
        : <footer className="ec-compose-area ec-share-footer">
          <p>{t(`share.role.${share.role}`)} · {t("share.readOnly")}</p>
        </footer>}
    </>
  );
}
