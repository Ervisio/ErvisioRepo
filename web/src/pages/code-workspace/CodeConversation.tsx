import { useId, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { sendRemoteCommand, useRemoteTranscript, type RemoteCommand, type RemoteMessage, type RemoteSession, type RemoteTodo } from "../../api/codeWorkspace.ts";
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
  const ready = view === "live" && transcript.isSuccess;
  const messages = ready ? transcript.data.messages : noMessages;
  const busy = ready && (transcript.data.busy || transcript.data.collaborationBusy);
  const unavailable = !ready && view === "live" && transcript.error instanceof ApiError && transcript.error.status === 404;
  const expired = view === "expired" || (transcript.error instanceof ApiError && transcript.error.status === 401);
  const notice = expired ? (hubAccountId ? t("mobile.sessionUnavailable") : t("workspace.expired"))
    : unavailable ? t("workspace.offline")
      : view !== "live" ? t(`workspace.${view}`)
        : transcript.isError ? t("workspace.transcriptError")
          : transcript.isPending ? common("state.loading")
            : !messages.length ? t("workspace.noMessages") : undefined;
  const retry = expired ? undefined : view === "error" || view === "notPublished" ? retryList
    : transcript.isError && view === "live" ? () => void transcript.refetch() : undefined;

  return <>
    <Transcript messages={messages} accountId={accountId} ownerId={accountId} notice={notice}
      isError={expired || view === "error" || transcript.isError} retry={retry} />
    {ready && <TodoPanel todos={transcript.data.todos} />}
    <ChatComposer ready={ready} busy={busy} expired={expired}
      onNewChat={onNewChat} onShareChat={onShareChat}
      send={async (input) => {
        if (liveSession) await sendRemoteCommand(liveSession, input, hubAccountId);
      }} refresh={transcript.refetch}>
      {liveSession && !expired && <div className="ec-context">
        <Icon name="monitor" />
        <span>{pc ? t("composer.pc", { name: pc }) : t("composer.linkedPC")}</span>
        {project && <><Icon name="folder" /><span>{project}</span></>}
      </div>}
    </ChatComposer>
  </>;
}

export function ChatComposer({ ready, busy, expired, canInterrupt = busy, onNewChat, onShareChat, send, refresh, status, children }: {
  ready: boolean;
  busy: boolean;
  expired?: boolean;
  canInterrupt?: boolean;
  onNewChat?: (trigger: HTMLElement | null) => void;
  onShareChat?: (trigger: HTMLElement | null) => void;
  send: (input: RemoteCommand) => Promise<void>;
  refresh: () => Promise<unknown>;
  status?: string;
  children?: ReactNode;
}) {
  const t = useT("codehub");
  const [draft, setDraft] = useState("");
  const [showCommands, setShowCommands] = useState(false);
  const [commandIndex, setCommandIndex] = useState(-1);
  const commandListId = useId();
  const prompt = useRef<HTMLTextAreaElement>(null);
  const [pending, setPending] = useState<RemoteCommand["op"]>();
  const [failure, setFailure] = useState<ReturnType<typeof commandError> | "composer.invalid">();
  const actionInFlight = useRef(false);
  const applicableCommands = slashCommands.filter((name) => name !== "/new" || onNewChat)
    .filter((name) => name !== "/share" || onShareChat);
  const shellMode = draft.trimStart().startsWith("!");
  const slash = /^\/[a-z-]*(?=\s|$)/i.exec(draft)?.[0].toLowerCase();
  const commands = slash && ready && !pending && showCommands && slash === draft.toLowerCase()
    ? applicableCommands.filter((name) => name.startsWith(slash)) : [];
  const activeCommand = commands[commandIndex];
  const commandText = draft.trim().toLowerCase();
  const runnableSlash = applicableCommands.some((name) => name === commandText) && (commandText !== "/stop" || canInterrupt);
  const canSend = ready && !pending && !shellMode && !!draft.trim() && (slash ? runnableSlash : !busy);

  async function command(input: RemoteCommand) {
    if (!ready || actionInFlight.current) return;
    actionInFlight.current = true;
    setPending(input.op);
    setFailure(undefined);
    try {
      await send(input);
      if (input.op === "prompt") setDraft("");
    } catch (error) {
      setFailure(commandError(error));
    } finally {
      try {
        await refresh();
      } finally {
        actionInFlight.current = false;
        setPending(undefined);
      }
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
    if (name === "/stop" && !canInterrupt) return;
    setDraft("");
    setShowCommands(false);
    if (name === "/new") onNewChat?.(prompt.current);
    if (name === "/share") onShareChat?.(prompt.current);
    if (name === "/stop") void command({ op: "interrupt" });
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canSend) return;
    const text = draft.trim();
    if (slash) {
      const name = applicableCommands.find((candidate) => candidate === text.toLowerCase());
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
      <footer className="ec-compose-area">
        <form className="ec-composer" onSubmit={submit} aria-label={t("composer.label")}>
          <textarea
            ref={prompt}
            name="prompt"
            aria-label={t("composer.label")}
            placeholder={t(ready ? "composer.placeholder" : "composer.inactive")}
            autoComplete="off"
            rows={2}
            aria-controls={commands.length ? commandListId : undefined}
            aria-autocomplete="list"
            maxLength={maxPromptLength}
            aria-invalid={failure === "composer.invalid" || undefined}
            value={expired ? "" : draft}
            disabled={!ready || !!pending}
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
            {!applicableCommands.some((name) => name === slash) && slash !== "/" && ` ${t("composer.unknownCommand")}`}
          </p>}
          <div className="ec-composer-toolbar">
            <span role="status" className="ec-command-status">
              {pending ? t(pending === "prompt" ? "composer.sending" : "composer.stopping") : busy ? t("composer.running") : status}
            </span>
            {busy || pending === "interrupt" ? (
              <button type="button" className="ec-icon-button ec-send" aria-label={t("composer.stop")} title={t("composer.stop")}
                disabled={!ready || !canInterrupt || !!pending} onClick={() => void command({ op: "interrupt" })}><Icon name="stop" /></button>
            ) : (
              <button type="submit" className="ec-icon-button ec-send" aria-label={t("composer.send")} title={t("composer.send")}
                disabled={!canSend}><Icon name="send" /></button>
            )}
          </div>
        </form>
        {!expired && failure && <p className="ec-command-error" role="alert">{t(failure)}</p>}
        {children}
      </footer>
  );
}

const todoMarkers = { pending: "○", in_progress: "●", completed: "✓", cancelled: "–" } as const;

/** The agent's current todo list, kept above the composer like the PC terminal shows it. */
export function TodoPanel({ todos }: { todos?: readonly RemoteTodo[] }) {
  const t = useT("codehub");
  const [open, setOpen] = useState(true);
  const listId = useId();
  if (!todos?.length) return null;
  const done = todos.filter(({ status }) => status === "completed").length;
  return (
    <section className="ec-todos" aria-label={t("todo.title")}>
      <button type="button" className="ec-todos-header" aria-expanded={open} aria-controls={listId}
        onClick={() => setOpen((value) => !value)}>
        <Icon name={open ? "down" : "right"} />
        <b>{t("todo.title")}</b>
        <span aria-live="polite">{t("todo.progress", { done, total: todos.length })}</span>
      </button>
      {open && <ol id={listId}>
        {todos.map(({ content, status }, index) => <li key={index} data-status={status}>
          <span aria-hidden="true">{todoMarkers[status]}</span>
          <span>{content}<span className="sr-only"> · {t(`todo.status.${status}`)}</span></span>
        </li>)}
      </ol>}
    </section>
  );
}

export function Transcript({ messages, notice, isError, retry, accountId, ownerId, ownerName }: {
  messages: readonly RemoteMessage[];
  notice?: string; isError: boolean; retry?: () => void;
  accountId?: string; ownerId?: string; ownerName?: string;
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
        {messages.map(({ messageId, role, text, contributorId }, index) => {
          const author = !contributorId
            ? ownerId && ownerId !== accountId ? ownerName || t("collaboration.owner") : t("message.user")
            : contributorId === accountId ? t("message.user")
              : t("collaboration.contributor", { id: contributorId.slice(0, 8) });
          const assistant = contributorId
            ? contributorId === accountId ? t("collaboration.replyToYou") : t("collaboration.replyTo", { author })
            : t("message.assistant");
          return <article key={messageId ?? index} className="ec-message" data-role={role}>
            <h2>{role === "user" ? author : assistant}</h2>
            <p>{text}</p>
          </article>;
        })}
      </div>
    </div>
  );
}
