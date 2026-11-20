import { useSharedCodeState, type CodeShare, type CodeShareScope } from "../../api/codeSharing.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Transcript } from "./CodeConversation.tsx";

export function SharedCodeConversation({ scope, share }: {
  scope: CodeShareScope;
  share: CodeShare;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const state = useSharedCodeState(scope, share.id);
  const messages = state.isSuccess ? state.data.messages : [];
  const status = state.error instanceof ApiError ? state.error.status : undefined;
  const notice = state.isPending ? common("state.loading")
    : state.isError ? t(status === 422 ? "share.restrictedUnavailable" : status === 404 ? "share.offlineOrRevoked" : "share.stateError")
      : !messages.length ? t("share.noMessages") : undefined;
  return (
    <>
      <Transcript messages={messages} notice={notice} isError={state.isError} retry={state.isError ? () => void state.refetch() : undefined} />
      <footer className="ec-compose-area ec-share-footer">
        <p>{t(`share.role.${share.role}`)} · {t("share.readOnly")}</p>
      </footer>
    </>
  );
}
