import { useId, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { sendRemoteCommand, useRemoteTranscript, type RemoteCommand, type RemoteMessage, type RemoteSession } from "../../api/codeWorkspace.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../../ui/Icon.tsx";

type View = "expired" | "error" | "loading" | "live" | "offline" | "select" | "empty" | "opening" | "notPublished";
const maxPromptLength = 16_384;
const utf8 = new TextEncoder();
const noMessages: RemoteMessage[] = [];
const slashCommands = ["/help", "/new", "/share", "/stop"] as const;

function commandError(error: unknown) {
  if (!(error instanceof ApiError) || error.status >= 500) return "composer.uncertain";
  if (error.status === 401 || error.status === 403) return "composer.denied";
  if (error.status === 404) return "workspace.offline";
  if (error.status === 429) return "composer.rateLimited";
  return "composer.failed";
}

export function CodeConversation({ accountId, hubAccountId, liveSession, pc, project, view, retryList, onNewChat, onShareChat }: {
  accountId: string;
  hubAccountId?: string;
  liveSession?: RemoteSession;
  pc?: string;
  project?: string;
  view: View;
  retryList: () => void;
  onNewChat: (trigger: HTMLElement | null) => void;
  onShareChat: (trigger: HTMLElement | null) => void;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const transcript = useRemoteTranscript(accountId, hubAccountId, liveSession, view === "live");
  const [draft, setDraft] = useState("");
  const [showCommands, setShowCommands] = useState(false);
  const [commandIndex, setCommandIndex] = useState(-1);
  const commandListId = useId();
  const prompt = useRef<HTMLTextAreaElement>(null);
  const [pending, setPending] = useState<RemoteCommand["op"]>();
  const [failure, setFailure] = useState<ReturnType<typeof commandError> | "composer.invalid">();
  const actionInFlight = useRef(false);
  const isReady = view === "live" && transcript.isSuccess;
  const messages = isReady ? transcript.data.messages : noMessages;
  const busy = isReady && transcript.data.busy;
  const shellMode = draft.trimStart().startsWith("!");
  const slash = /^\/[a-z-]*(?=\s|$)/i.exec(draft)?.[0].toLowerCase();
  const commands = slash && isReady && !pending && showCommands && slash === draft.toLowerCase()
    ? slashCommands.filter((name) => name.startsWith(slash)) : [];
  const activeCommand = commands[commandIndex];
  const commandText = draft.trim().toLowerCase();
  const runnableSlash = slashCommands.some((name) => name === commandText) && (commandText !== "/stop" || busy);
  const canSend = isReady && !pending && !shellMode && !!draft.trim() && (slash ? runnableSlash : !busy);
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

  async function command(input: RemoteCommand) {
    if (!liveSession || !isReady || actionInFlight.current) return;
    actionInFlight.current = true;
    setPending(input.op);
    setFailure(undefined);
    try {
      await sendRemoteCommand(liveSession, input, hubAccountId);
      if (input.op === "prompt") setDraft("");
    } catch (error) {
      setFailure(commandError(error));
    } finally {
      await transcript.refetch();
      actionInFlight.current = false;
      setPending(undefined);
    }
  }

  function runSlash(name: typeof slashCommands[number]) {
    if (name === "/help") {
      setDraft("/");
      setShowCommands(true);
      setCommandIndex(-1);
      prompt.current?.focus();
      return;
    }
    if (name === "/stop" && !busy) return;
    setDraft("");
    setShowCommands(false);
    if (name === "/new") onNewChat(prompt.current);
    if (name === "/share") onShareChat(prompt.current);
    if (name === "/stop") void command({ op: "interrupt" });
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSend) return;
    const text = draft.trim();
    if (slash) {
      const name = slashCommands.find((candidate) => candidate === text.toLowerCase());
      if (name) runSlash(name);
      return;
    }
    if (text.includes("\0") || utf8.encode(text).length > maxPromptLength) {
      setFailure("composer.invalid");
      return;
    }
    void command({ op: "prompt", text });
  }

  function handleKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Escape" && commands.length) {
      event.preventDefault();
      event.stopPropagation();
      setShowCommands(false);
      return;
    }
    if (commands.length && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      event.preventDefault();
      const last = commands.length - 1;
      setCommandIndex(event.key === "ArrowDown" ? (commandIndex >= last ? 0 : commandIndex + 1)
        : (commandIndex <= 0 ? last : commandIndex - 1));
      return;
    }
    if (event.key !== "Enter" || event.shiftKey) return;
    event.preventDefault();
    if (activeCommand) {
      setDraft(activeCommand);
      setShowCommands(false);
      setCommandIndex(-1);
      return;
    }
    event.currentTarget.form?.requestSubmit();
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
            aria-controls={commands.length ? commandListId : undefined}
            aria-autocomplete="list"
            maxLength={maxPromptLength}
            aria-invalid={failure === "composer.invalid" || undefined}
            value={expired ? "" : draft}
            disabled={!isReady || !!pending}
            onChange={(event) => {
              setDraft(event.target.value);
              setShowCommands(true);
              setCommandIndex(-1);
              if (failure === "composer.invalid") setFailure(undefined);
            }}
            onKeyDown={handleKey}
          />
          {commands.length > 0 && <ul id={commandListId} className="ec-slash-list" aria-label={t("composer.commands")}>
            {commands.map((name, index) => <li key={name}>
              <button type="button" className="ec-rail-row" data-active={commandIndex === index}
                onClick={() => {
                  setDraft(name);
                  setShowCommands(false);
                  setCommandIndex(-1);
                  prompt.current?.focus();
                }}>
                <code>{name}</code><span>{t(`composer.command.${name.slice(1)}`)}</span>
              </button>
            </li>)}
          </ul>}
          {shellMode && <p className="ec-command-status ec-composer-mode"><code>!</code> {t("composer.shellLocalOnly")}</p>}
          {slash && !commands.length && <p className="ec-command-status ec-composer-mode"><code>{slash}</code>
            {!slashCommands.some((name) => name === slash) && slash !== "/" && ` ${t("composer.unknownCommand")}`}
          </p>}
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
