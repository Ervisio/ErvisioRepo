import { useImperativeHandle, useLayoutEffect, useRef, useState, type FormEvent, type Ref } from "react";
import { sendRemoteCommand, useRemoteTranscript, type RemoteCommand, type RemoteMessage, type RemoteSession } from "../../api/codeWorkspace.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../../ui/Icon.tsx";

type View = "expired" | "error" | "loading" | "live" | "offline" | "select" | "empty" | "opening" | "notPublished";
export type CodeComposer = { adopt: (session: Pick<RemoteSession, "deviceId" | "sessionId">, body: string) => boolean; focus: () => void };
const maxPromptLength = 16_384;
const utf8 = new TextEncoder();
const noMessages: RemoteMessage[] = [];

function commandError(error: unknown) {
  if (!(error instanceof ApiError) || error.status >= 500) return "composer.uncertain";
  if (error.status === 401 || error.status === 403) return "composer.denied";
  if (error.status === 404) return "workspace.offline";
  if (error.status === 429) return "composer.rateLimited";
  return "composer.failed";
}

export function CodeConversation({ accountId, hubAccountId, liveSession, pc, project, view, retryList, composer }: {
  accountId: string;
  hubAccountId?: string;
  liveSession?: RemoteSession;
  pc?: string;
  project?: string;
  view: View;
  retryList: () => void;
  composer?: Ref<CodeComposer>;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const transcript = useRemoteTranscript(accountId, hubAccountId, liveSession, view === "live");
  const [draft, setDraft] = useState("");
  const [adopted, setAdopted] = useState(false);
  const [pending, setPending] = useState<RemoteCommand["op"]>();
  const [failure, setFailure] = useState<ReturnType<typeof commandError> | "composer.invalid">();
  const actionInFlight = useRef(false);
  const prompt = useRef<HTMLTextAreaElement>(null);
  const isReady = view === "live" && transcript.isSuccess;
  const messages = isReady ? transcript.data.messages : noMessages;
  const busy = isReady && transcript.data.busy;
  const canSend = isReady && !busy && !pending && !!draft.trim();
  const unavailable = !isReady && view === "live" && transcript.error instanceof ApiError
    && transcript.error.status === 404;
  const expired = view === "expired" || (transcript.error instanceof ApiError && transcript.error.status === 401);
  const notice = expired ? (hubAccountId ? t("mobile.sessionUnavailable") : t("workspace.expired"))
    : unavailable ? t("workspace.offline")
      : view !== "live" ? t(`workspace.${view}`)
        : transcript.isError ? t("workspace.transcriptError")
          : transcript.isPending ? common("state.loading")
            : !messages.length ? t("workspace.noMessages") : undefined;
  const retry = expired ? undefined : view === "error" || view === "notPublished" ? retryList
    : transcript.isError && view === "live" ? () => void transcript.refetch() : undefined;

  useImperativeHandle(composer, () => ({
    adopt: ({ deviceId, sessionId }, body) => {
      if (deviceId !== liveSession?.deviceId || sessionId !== liveSession.sessionId || !isReady || expired || actionInFlight.current || draft) return false;
      setDraft(body);
      setAdopted(true);
      return true;
    },
    focus: () => prompt.current?.focus(),
  }));

  async function command(input: RemoteCommand) {
    if (!liveSession || !isReady || actionInFlight.current) return;
    actionInFlight.current = true;
    setPending(input.op);
    setFailure(undefined);
    try {
      await sendRemoteCommand(liveSession, input, hubAccountId);
      if (input.op === "prompt") {
        setDraft("");
        setAdopted(false);
      }
    } catch (error) {
      setFailure(commandError(error));
    } finally {
      await transcript.refetch();
      actionInFlight.current = false;
      setPending(undefined);
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSend) return;
    const text = draft.trim();
    if (text.includes("\0") || utf8.encode(text).length > maxPromptLength) {
      setFailure("composer.invalid");
      return;
    }
    void command({ op: "prompt", text });
  }

  return (
    <>
      <Transcript messages={messages} notice={notice} isError={expired || view === "error" || transcript.isError} retry={retry} />
      <footer className="ec-compose-area">
        <form className="ec-composer" onSubmit={submit} aria-label={t("composer.label")}>
          <textarea
            ref={prompt}
            name="prompt"
            aria-label={t("composer.label")}
            placeholder={t(isReady ? "composer.placeholder" : "composer.inactive")}
            autoComplete="off"
            rows={2}
            maxLength={maxPromptLength}
            aria-invalid={failure === "composer.invalid" || undefined}
            value={expired ? "" : draft}
            disabled={!isReady || !!pending}
            onChange={(event) => {
              setDraft(event.target.value);
              if (!event.target.value) setAdopted(false);
              if (failure === "composer.invalid") setFailure(undefined);
            }}
            onKeyDown={(event) => {
              if (adopted || event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
              event.preventDefault();
              event.currentTarget.form?.requestSubmit();
            }}
          />
          <div className="ec-composer-toolbar">
            <span role="status" className="ec-command-status">
              {pending ? t(pending === "prompt" ? "composer.sending" : "composer.stopping") : busy ? t("composer.running") : null}
            </span>
            {busy || pending === "interrupt" ? (
              <button type="button" className="ec-icon-button ec-send" aria-label={t("composer.stop")} title={t("composer.stop")}
                disabled={!isReady || !!pending} onClick={() => void command({ op: "interrupt" })}><Icon name="stop" /></button>
            ) : (
              <button type="submit" className="ec-icon-button ec-send" aria-label={t("composer.send")} title={t("composer.send")}
                disabled={!canSend}><Icon name="send" /></button>
            )}
          </div>
        </form>
        {!expired && failure && <p className="ec-command-error" role="alert">{t(failure)}</p>}
        {!expired && adopted && <p className="ec-context" role="status">{t("proposal.draftHint")}</p>}
        {liveSession && !expired && <div className="ec-context">
          <Icon name="monitor" />
          <span>{pc ? t("composer.pc", { name: pc }) : t("composer.linkedPC")}</span>
          {project && <><Icon name="folder" /><span>{project}</span></>}
        </div>}
      </footer>
    </>
  );
}

export function Transcript({ messages, notice, isError, retry }: {
  messages: readonly RemoteMessage[]; notice?: string; isError: boolean; retry?: () => void;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const scroll = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

  useLayoutEffect(() => {
    const element = scroll.current;
    if (element && atBottom.current) element.scrollTop = element.scrollHeight;
  }, [messages]);

  return (
    <div className="ec-transcript" role="region" aria-label={t("workspace.transcript")} tabIndex={0} ref={scroll}
      onScroll={(event) => {
        const { scrollHeight, scrollTop, clientHeight } = event.currentTarget;
        atBottom.current = scrollHeight - scrollTop - clientHeight < 48;
      }}>
      <div className="ec-column">
        {notice && <div className="ec-notice">
          <p role={isError ? "alert" : "status"}>{notice}</p>
          {retry && <button type="button" className="ec-retry" onClick={retry}>{common("action.retry")}</button>}
        </div>}
        {messages.map(({ role, text }, index) => (
          <article key={index} className="ec-message" data-role={role}>
            <h2>{t(role === "user" ? "message.user" : "message.assistant")}</h2>
            <p>{text}</p>
          </article>
        ))}
      </div>
    </div>
  );
}
