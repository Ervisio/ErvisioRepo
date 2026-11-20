import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { createCodeShare, useCodeShareRecipients, type CodeShareScope } from "../../api/codeSharing.ts";
import { useRemoteTranscript, type RemoteSession } from "../../api/codeWorkspace.ts";
import { useT } from "../../i18n/useT.ts";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Icon } from "../../ui/Icon.tsx";
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
  const [search, setSearch] = useState("");
  const [lookup, setLookup] = useState("");
  const [recipient, setRecipient] = useState<NonNullable<ReturnType<typeof useCodeShareRecipients>["data"]>[number]>();
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const listId = useId();
  const searchInput = useRef<HTMLInputElement>(null);
  const [history, setHistory] = useState<History>("new");
  const [boundary, setBoundary] = useState("");
  const [consent, setConsent] = useState(false);
  const [created, setCreated] = useState<{ token?: string }>();
  const [copied, setCopied] = useState(false);
  const invitation = useRef<HTMLInputElement>(null);
  const action = useSharingAction(refresh);
  const prefix = search.trim();
  const lookupCurrent = prefix.length > 0 && prefix === lookup;
  const searching = lookupCurrent && !recipient && !created;
  const people = useCodeShareRecipients(scope, "user", lookup, searching);
  const orgs = useCodeShareRecipients(scope, "org", lookup, searching);
  const loading = !recipient && !!prefix && (!lookupCurrent || people.isPending || orgs.isPending);
  const lookupError = searching && (people.isError || orgs.isError);
  const matches = searching && !loading ? [
    ...(people.isSuccess ? people.data.filter((entry) => entry.kind === "user" && entry.id !== scope.accountId) : []),
    ...(orgs.isSuccess ? orgs.data.filter((entry) => entry.kind === "org") : []),
  ] : [];
  const target = recipient?.kind;
  const transcript = useRemoteTranscript(scope.accountId, scope.hubAccountId, session, isLive && history !== "new" && !created);
  const messages = isLive && transcript.isSuccess ? transcript.data.messages : [];
  const needsBoundary = history === "from" || history === "after";
  const boundaryIndex = messages.findIndex((message) => boundary && message.messageId === boundary);
  const expanded = !recipient && !dismissed && matches.length > 0;
  const activeIndex = Math.min(active, matches.length - 1);
  const canCreate = isLive && recipient && consent && !action.busy && action.problem !== "share.uncertain"
    && (!needsBoundary || boundaryIndex >= 0);
  const preview = history === "full" ? messages : boundaryIndex < 0 ? [] : messages.slice(boundaryIndex + (history === "after" ? 1 : 0));

  useEffect(() => {
    const timer = window.setTimeout(() => setLookup(prefix), 250);
    return () => window.clearTimeout(timer);
  }, [prefix]);

  function retryLookup() {
    if (people.isError) void people.refetch();
    if (orgs.isError) void orgs.refetch();
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canCreate || created) return;
    const destination = recipient.kind === "user" ? { recipientId: recipient.id } : { orgId: recipient.id };
    const range = history === "full" ? { fullHistory: true as const }
      : history === "from" ? { fromMessageId: boundary }
        : history === "after" ? { afterMessageId: boundary } : { newOnly: true as const };
    void action.run(async () => {
      const result = await createCodeShare(scope, { deviceId: session.deviceId, sessionId: session.sessionId, role: "reader", ...destination, ...range });
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

  function pickTarget(entry?: typeof recipient) {
    setRecipient(entry);
    setConsent(false);
    setActive(0);
    setDismissed(false);
  }

  function searchKey(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Escape" && expanded) {
      event.preventDefault();
      event.stopPropagation();
      setDismissed(true);
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (expanded) pickTarget(matches[activeIndex]);
    } else if (matches.length && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      event.preventDefault();
      setDismissed(false);
      const next = expanded ? (activeIndex + (event.key === "ArrowDown" ? 1 : -1) + matches.length) % matches.length : 0;
      setActive(next);
      requestAnimationFrame(() => document.getElementById(`${listId}-${next}`)?.scrollIntoView({ block: "nearest" }));
    }
  }

  return (
    <SharingDialog title={t("share.create")} description={t("share.createHint", { title: session.title || t("session.untitled") })}
      busy={action.busy} onClose={onClose} onRestoreFocus={onRestoreFocus} initialFocus={searchInput}>
      {created ? <div className="ec-new-form">
        <p role="status">{t(target === "user" ? "share.inviteCreated" : "share.orgCreated")}</p>
        {created.token && <>
          <p>{t("share.codeOptional")}</p>
          <label>{t("share.code")}<input ref={invitation} name="invitation-code" value={created.token} readOnly autoComplete="off" /></label>
          <button type="button" className="ec-retry" onClick={() => { if (created.token) void copy(created.token); }}>{t("share.copy")}</button>
          {copied && <p role="status">{t("share.copied")}</p>}
        </>}
      </div> : <form className="ec-new-form" onSubmit={submit}>
        <div className="ec-share-recipient">
          <label htmlFor={`${listId}-search`}>{t("share.searchRecipients")}</label>
          <div className="ec-share-recipient-box" onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setDismissed(true);
          }}>
            <div className="ec-share-search">
              <Icon name="search" />
              <input id={`${listId}-search`} ref={searchInput} name="recipient-search" type="search"
                autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={100}
                role="combobox" aria-autocomplete="list" aria-expanded={expanded}
                aria-controls={listId} aria-activedescendant={expanded ? `${listId}-${activeIndex}` : undefined}
                placeholder={t("share.searchHint")} value={recipient ? recipient.displayName || recipient.name : search} disabled={action.busy}
                onFocus={() => setDismissed(false)}
                onChange={(event) => { setSearch(event.target.value); pickTarget(); }}
                onKeyDown={searchKey} />
            </div>
            <ul className="ec-share-results" id={listId} role="listbox" aria-label={t("share.matches")} hidden={!expanded}>
              {expanded && matches.map((entry, index) =>
                <li key={`${entry.kind}:${entry.id}`} id={`${listId}-${index}`} role="option" aria-selected={index === activeIndex}
                  className="ec-share-person" data-kind={entry.kind} onPointerMove={() => setActive(index)}
                  onMouseDown={(event) => event.preventDefault()} onClick={() => {
                    pickTarget(entry);
                    searchInput.current?.focus();
                  }}>
                  <AvatarImg className="ec-avatar" src={entry.avatarUrl}>{entry.name.slice(0, 2).toLocaleUpperCase()}</AvatarImg>
                  <span><b>{entry.displayName || entry.name}</b><small>{entry.kind === "org" ? t("share.org") : `@${entry.name}`}</small></span>
                </li>)}
            </ul>
            {recipient && <div className="ec-share-person" data-kind={recipient.kind}>
              <AvatarImg className="ec-avatar" src={recipient.avatarUrl}>{recipient.name.slice(0, 2).toLocaleUpperCase()}</AvatarImg>
              <span><b>{recipient.displayName || recipient.name}</b><small>{target === "org" ? t("share.org") : `@${recipient.name}`}</small></span>
            </div>}
            {loading && <p role="status">{common("state.loading")}</p>}
            {searching && !loading && !lookupError && !matches.length && <p role="status">{t("share.noRecipients")}</p>}
            {lookupError && <div className="ec-share-lookup-error">
              <p role="alert">{t("share.directoryError")}</p>
              <button type="button" className="ec-retry" onClick={retryLookup}>{common("action.retry")}</button>
            </div>}
          </div>
        </div>
        {recipient && <p>{t(target === "org" ? "share.orgHint" : "share.personalHint")}</p>}
        <div className="ec-share-options">
          <div className="ec-share-access"><span>{t("share.role")}</span><p>{t("share.role.reader")}</p></div>
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
        </div>
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
        <div className="ec-new-actions ec-share-create-actions">
          <button type="button" className="ec-retry" disabled={action.busy} onClick={onClose}>{t("share.cancel")}</button>
          <button type="submit" className="ec-new-submit" disabled={!canCreate}>{t(action.busy ? "share.saving" : target === "org" ? "share.grant" : "share.invite")}</button>
        </div>
      </form>}
    </SharingDialog>
  );
}
