import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  createCodeShare, useCodeShareRecipients, type CodeShareCreate as ShareInput,
  type CodeShareRecipientKind, type CodeShareScope,
} from "../../api/codeSharing.ts";
import { useRemoteTranscript, type RemoteSession } from "../../api/codeWorkspace.ts";
import { useT } from "../../i18n/useT.ts";
import { SharingDialog, useSharingAction } from "./SharingDialog.tsx";

type History = "new" | "full" | "from" | "after";

export function CodeShareCreate({ scope, session, isLive, onClose, onRestoreFocus, refresh }: {
  scope: CodeShareScope;
  session: RemoteSession;
  isLive: boolean;
  onClose: () => void;
  onRestoreFocus: () => void;
  refresh: () => Promise<unknown>;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const [target, setTarget] = useState<CodeShareRecipientKind>("user");
  const [search, setSearch] = useState("");
  const [lookup, setLookup] = useState("");
  const [targetId, setTargetId] = useState("");
  const [role, setRole] = useState<ShareInput["role"]>("reader");
  const [history, setHistory] = useState<History>("new");
  const [boundary, setBoundary] = useState("");
  const [consent, setConsent] = useState(false);
  const [created, setCreated] = useState<{ token?: string }>();
  const [copied, setCopied] = useState(false);
  const invitation = useRef<HTMLInputElement>(null);
  const action = useSharingAction(refresh);
  const prefix = search.trim();
  const lookupCurrent = prefix.length > 0 && prefix === lookup;
  const directory = useCodeShareRecipients(scope, target, lookup, lookupCurrent && !created);
  const matches = lookupCurrent && directory.isSuccess
    ? directory.data.filter((entry) => entry.kind === target && (target !== "user" || entry.id !== scope.accountId))
    : [];
  const transcript = useRemoteTranscript(scope.accountId, scope.hubAccountId, session, isLive && history !== "new" && !created);
  const messages = isLive && transcript.isSuccess ? transcript.data.messages : [];
  const needsBoundary = history === "from" || history === "after";
  const boundaryIndex = messages.findIndex((message) => boundary && message.messageId === boundary);
  const recipient = matches.find(({ id }) => id === targetId);
  const canCreate = isLive && recipient && consent && !action.busy && action.problem !== "share.uncertain"
    && (!needsBoundary || boundaryIndex >= 0);
  const preview = history === "full" ? messages : boundaryIndex < 0 ? [] : messages.slice(boundaryIndex + (history === "after" ? 1 : 0));

  useEffect(() => {
    const timer = window.setTimeout(() => setLookup(prefix), 250);
    return () => window.clearTimeout(timer);
  }, [prefix]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canCreate || created) return;
    const destination = recipient.kind === "user" ? { recipientId: recipient.id } : { orgId: recipient.id };
    const range = history === "full" ? { fullHistory: true as const }
      : history === "from" ? { fromMessageId: boundary }
        : history === "after" ? { afterMessageId: boundary } : { newOnly: true as const };
    void action.run(async () => {
      const result = await createCodeShare(scope, { deviceId: session.deviceId, sessionId: session.sessionId, role, ...destination, ...range });
      setCreated({ token: result.token });
    });
  }

  async function copy(token: string) {
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
    } catch {
      invitation.current?.focus();
      invitation.current?.select();
    }
  }

  function pickTarget(id: string) {
    setTargetId(id);
    setConsent(false);
  }

  return (
    <SharingDialog title={t("share.create")} description={t("share.createHint", { title: session.title || t("session.untitled") })}
      busy={action.busy} onClose={onClose} onRestoreFocus={onRestoreFocus}>
      {created ? <div className="ec-new-form">
        <p role="status">{t(target === "user" ? "share.inviteCreated" : "share.orgCreated")}</p>
        {created.token && <>
          <p>{t("share.codeOptional")}</p>
          <label>{t("share.code")}<input ref={invitation} name="invitation-code" value={created.token} readOnly autoComplete="off" /></label>
          <button type="button" className="ec-retry" onClick={() => { if (created.token) void copy(created.token); }}>{t("share.copy")}</button>
          {copied && <p role="status">{t("share.copied")}</p>}
        </>}
      </div> : <form className="ec-new-form" onSubmit={submit}>
        <label>{t("share.target")}
          <select name="target" value={target} disabled={action.busy} onChange={(event) => {
            setTarget(event.target.value === "org" ? "org" : "user");
            setSearch("");
            setLookup("");
            pickTarget("");
          }}>
            <option value="user">{t("share.personal")}</option>
            <option value="org">{t("share.org")}</option>
          </select>
        </label>
        <label>{t(target === "org" ? "share.searchOrg" : "share.searchPerson")}
          <input name="recipient-search" type="search" autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={100}
            placeholder={t("share.searchHint")} value={search} disabled={action.busy}
            onChange={(event) => { setSearch(event.target.value); pickTarget(""); }}
            onKeyDown={(event) => { if (event.key === "Enter" && !event.nativeEvent.isComposing) event.preventDefault(); }} />
        </label>
        {prefix && (!lookupCurrent || directory.isPending) && <p role="status">{common("state.loading")}</p>}
        {lookupCurrent && directory.isSuccess && !matches.length && <p role="status">{t("share.noRecipients")}</p>}
        {lookupCurrent && directory.isError && <>
          <p role="alert">{t("share.directoryError")}</p>
          <button type="button" className="ec-retry" onClick={() => void directory.refetch()}>{common("action.retry")}</button>
        </>}
        {matches.length > 0 && <label>{t("share.matches")}
          <select name="recipient" value={recipient?.id ?? ""} disabled={action.busy} onChange={(event) => pickTarget(event.target.value)}>
            <option value="">{t("share.selectRecipient")}</option>
            {matches.map(({ id, name, displayName }) =>
              <option key={id} value={id}>{displayName ? `${displayName} (@${name})` : `@${name}`}</option>)}
          </select>
        </label>}
        <p>{t(target === "org" ? "share.orgHint" : "share.personalHint")}</p>
        <label>{t("share.role")}
          <select name="role" value={role} disabled={action.busy} onChange={(event) => {
            setRole(event.target.value === "proposer" ? "proposer" : "reader");
            setConsent(false);
          }}>
            <option value="reader">{t("share.role.reader")}</option>
            <option value="proposer">{t("share.role.proposer")}</option>
          </select>
        </label>
        <label>{t("share.history")}
          <select name="history" value={history} disabled={action.busy} onChange={(event) => {
            const mode = event.target.value;
            setHistory(mode === "full" || mode === "from" || mode === "after" ? mode : "new");
            setBoundary("");
            setConsent(false);
          }}>
            <option value="new">{t("share.history.new")}</option>
            <option value="full">{t("share.history.full")}</option>
            <option value="from">{t("share.history.from")}</option>
            <option value="after">{t("share.history.after")}</option>
          </select>
        </label>
        {needsBoundary && <label>{t("share.boundary")}
          <select name="boundary" value={boundaryIndex >= 0 ? boundary : ""} disabled={action.busy || !messages.length} onChange={(event) => {
            setBoundary(event.target.value);
            setConsent(false);
          }}>
            <option value="">{t("share.selectMessage")}</option>
            {messages.map(({ messageId, role, text }, index) => messageId && <option key={messageId} value={messageId}>
              {t("share.messageOption", { number: index + 1, role: t(`message.${role}`), text: text.replace(/\s+/g, " ").slice(0, 86) })}
            </option>)}
          </select>
        </label>}
        {!isLive && <p role="status">{t("workspace.offline")}</p>}
        {isLive && history !== "new" && transcript.isPending && <p role="status">{common("state.loading")}</p>}
        {isLive && history !== "new" && transcript.isError && <p role="alert">{t("share.previewUnavailable")}</p>}
        {isLive && needsBoundary && transcript.isSuccess && !messages.some((message) => message.messageId) && <p role="status">{t("share.noBoundaries")}</p>}
        {preview.length > 0 && <details className="ec-share-preview">
          <summary>{t("share.preview")}</summary>
          <p>{t("share.previewHint")}</p>
          {preview.map(({ role, text, messageId }, index) => <article key={messageId ?? index} className="ec-message">
            <h2>{t(`message.${role}`)}</h2>
            <p>{text}</p>
          </article>)}
        </details>}
        <label className="ec-share-check">
          <input type="checkbox" checked={consent} disabled={action.busy} onChange={(event) => setConsent(event.target.checked)} />
          {t(`share.consent.${history}`)}
        </label>
        {action.problem && <p role="alert">{t(action.problem)}</p>}
        <div className="ec-new-actions">
          <button type="submit" className="ec-new-submit" disabled={!canCreate}>{t(action.busy ? "share.saving" : target === "org" ? "share.grant" : "share.invite")}</button>
        </div>
      </form>}
    </SharingDialog>
  );
}
