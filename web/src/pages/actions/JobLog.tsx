import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { fetchLogPage, logRawUrl, logStreamUrl, useJob, type ActionsJob, type ActionsLogLine, type ActionsLogPage, type ActionsStep } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon } from "../../ui/index.ts";
import { duration, isActive, runState, StatusIcon, type RunState } from "./common.tsx";

// Terminal colour escapes are dropped: the log shows plain text.
const ESC = String.fromCharCode(27);
const ansi = new RegExp(`${ESC}\\[[0-9;]*[A-Za-z]`, "g");
const MAX_RETRIES = 5;
// The server writes its own failures (planning, scheduling) as "##[error]" rows.
const ERROR_MARK = "##[error]";
const GROUP_MARK = "##[group]";
const END_GROUP_MARK = "##[endgroup]";
const clock = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });

type LogState = { lines: ActionsLogLine[]; complete: boolean; loading: boolean; failed: boolean };

/**
 * Pages of a job log. A finished job is read page by page on demand; a running one is read once
 * and then followed over server-sent events (events "log" and "end"). The stream resumes from the
 * next row index: the browser sends Last-Event-ID on its own reconnects, and a stream that was
 * closed is reopened by hand with from=<next row>.
 */
function useJobLog(owner: string, name: string, jobId: string, live: boolean, onEnd: () => void) {
  const [state, setState] = useState<LogState>({ lines: [], complete: false, loading: true, failed: false });
  const next = useRef(0);
  const onEndRef = useRef(onEnd);
  useEffect(() => {
    onEndRef.current = onEnd;
  }, [onEnd]);

  const append = useCallback((p: ActionsLogPage) => {
    const fresh = p.lines.filter((l) => l.index >= next.current);
    next.current = Math.max(next.current, p.next);
    setState((s) => ({ ...s, lines: fresh.length ? s.lines.concat(fresh) : s.lines, complete: s.complete || p.complete, loading: false }));
  }, []);

  const loadMore = useCallback(async () => {
    setState((s) => ({ ...s, loading: true, failed: false }));
    try {
      append(await fetchLogPage(owner, name, jobId, next.current));
    } catch {
      setState((s) => ({ ...s, loading: false, failed: true }));
    }
  }, [owner, name, jobId, append]);

  useEffect(() => {
    let closed = false;
    let source: EventSource | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retries = 0;

    function follow() {
      if (closed) return;
      const src = new EventSource(logStreamUrl(owner, name, jobId, next.current));
      source = src;
      src.addEventListener("log", (ev) => {
        retries = 0;
        append(JSON.parse((ev as MessageEvent<string>).data) as ActionsLogPage);
      });
      src.addEventListener("end", () => {
        src.close();
        setState((s) => ({ ...s, complete: true }));
        onEndRef.current();
      });
      src.onerror = () => {
        if (src.readyState !== EventSource.CLOSED) return; // the browser is reconnecting with Last-Event-ID
        if (retries++ >= MAX_RETRIES) {
          setState((s) => ({ ...s, failed: true }));
          return;
        }
        timer = setTimeout(follow, 3000);
      };
    }

    void (async () => {
      try {
        const first = await fetchLogPage(owner, name, jobId, 0);
        if (closed) return;
        append(first);
        if (live && !first.complete) follow();
      } catch {
        if (!closed) setState((s) => ({ ...s, loading: false, failed: true }));
      }
    })();

    return () => {
      closed = true;
      source?.close();
      clearTimeout(timer);
    };
  }, [owner, name, jobId, live, append]);

  return { ...state, loadMore };
}

const PHRASES: Partial<Record<RunState, string>> = {
  success: "job.after.success",
  failure: "job.after.failure",
  cancelled: "job.after.cancelled",
  timed_out: "job.after.timed_out",
  in_progress: "job.after.in_progress",
};

const isFailure = (s: RunState) => s === "failure" || s === "timed_out";
type Tail = RefObject<HTMLDivElement | null>;

export function JobLog({ owner, name, job, actions }: { owner: string; name: string; job: ActionsJob; actions?: ReactNode }) {
  const t = useT("actions");
  const qc = useQueryClient();
  const running = isActive(job.status);
  const detail = useJob(owner, name, job.id, running);
  const log = useJobLog(owner, name, job.id, running, () => void qc.invalidateQueries({ queryKey: ["actions", owner, name] }));
  const [follow, setFollow] = useState(true);
  const [stamps, setStamps] = useState(false);
  const [query, setQuery] = useState("");
  // Log body of the step being followed: only that box scrolls, never the page.
  const tail = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (running && follow && tail.current && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) tail.current.scrollTop = tail.current.scrollHeight;
  }, [log.lines.length, running, follow]);

  const steps = detail.data?.steps ?? [];
  const state = runState(job.status, job.conclusion);
  const took = duration(job.startedAt, job.completedAt);
  const phrase = took && PHRASES[state] ? t(PHRASES[state], { duration: took }) : t(`state.${state}`);
  const inStep = (i: number) => steps.some((s) => i >= s.logIndex && i < s.logIndex + s.logLength);
  const loose = steps.length > 0 ? log.lines.filter((l) => !inStep(l.index)) : log.lines;
  const flat = steps.length === 0;
  const view: LogView = { stamps, query: query.trim().toLowerCase() };

  return (
    <section className="ui-box ax-jc" aria-label={t("log.title", { job: job.name })}>
      <header className="ax-jh">
        <StatusIcon status={job.status} conclusion={job.conclusion} bare />
        <h2 className="ax-jt">{job.name}</h2>
        <span className="ui-tm">{[phrase, job.runner || job.runsOn.join(", ")].filter(Boolean).join(" · ")}</span>
        <span className="ax-jr">
          <label className="ax-search">
            <Icon name="search" />
            <input type="search" value={query} placeholder={t("log.search")} aria-label={t("log.search")} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <Button size="sm" icon="clock" aria-pressed={stamps} aria-label={t("log.timestamps")} title={t("log.timestamps")} onClick={() => setStamps(!stamps)} />
          {running && (
            <label className="acc-toggle">
              <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
              <span>{t("log.follow")}</span>
            </label>
          )}
          <a className="ui-btn ui-btn-secondary ui-btn-sm" href={logRawUrl(owner, name, job.id)} download>
            <Icon name="download" />
            {t("log.raw")}
          </a>
        </span>
      </header>
      {actions}
      <div className="ui-in">
        {!flat && (
          <ol className="ax-steps" aria-label={t("log.steps")}>
            {steps.map((s) => (
              <StepRow
                key={s.index}
                step={s}
                view={view}
                lines={log.lines.filter((l) => l.index >= s.logIndex && l.index < s.logIndex + s.logLength)}
                empty={log.loading ? t("log.loading") : s.status === "in_progress" ? t("log.waiting") : t("log.stepEmpty")}
                tail={s.status === "in_progress" ? tail : undefined}
              />
            ))}
          </ol>
        )}
        {loose.length > 0 && <LogLines lines={loose} view={view} label={t("log.lines")} tail={flat ? tail : undefined} live={flat && running} />}
        {flat && loose.length === 0 && !log.failed && <p role="status" className="ax-logmsg">{log.loading ? t("log.loading") : running ? t("log.waiting") : t("log.empty")}</p>}
        {log.failed && (
          <p role="alert" className="ax-logmsg ax-err">
            {t("log.failed")} <Button size="sm" onClick={() => void log.loadMore()}>{t("log.retry")}</Button>
          </p>
        )}
        {!running && !log.complete && !log.loading && !log.failed && (
          <div className="ax-logmore">
            <Button onClick={() => void log.loadMore()}>{t("log.more")}</Button>
          </div>
        )}
      </div>
    </section>
  );
}

/** What the job header controls: timestamps and the search filter apply to every step. */
type LogView = { stamps: boolean; query: string };

/** One expandable step: open at first when it failed or is running, or when the search matches it. */
function StepRow({ step, lines, view, empty, tail }: { step: ActionsStep; lines: ActionsLogLine[]; view: LogView; empty: string; tail?: Tail }) {
  const t = useT("actions");
  const state = runState(step.status, step.conclusion);
  const [picked, setPicked] = useState<boolean>();
  const hit = view.query !== "" && lines.some((l) => l.content.toLowerCase().includes(view.query));
  const open = picked ?? (isFailure(state) || state === "in_progress" || hit);
  const body = `ax-step-${step.index}`;
  return (
    <li className="ax-step">
      <button type="button" className="ax-sum" aria-expanded={open} aria-controls={body} onClick={() => setPicked(!open)}>
        <span className="ax-chv" aria-hidden="true"><Icon name="right" /></span>
        <StatusIcon status={step.status} conclusion={step.conclusion} bare />
        <span className="ax-sn">{step.name}</span>
        <span className="ui-tm">{duration(step.startedAt, step.completedAt)}</span>
      </button>
      {open && (
        <div className="ax-sp" id={body}>
          {lines.length > 0 ? <LogLines lines={lines} view={view} label={t("log.stepLines", { step: step.name })} tail={tail} live={state === "in_progress"} /> : <p className="ax-note">{empty}</p>}
        </div>
      )}
    </li>
  );
}

/** Log rows in a dark mono block: "##[group]" rows in the accent, "##[error]" rows highlighted. */
function LogLines({ lines, view, label, tail, live }: { lines: ActionsLogLine[]; view: LogView; label: string; tail?: Tail; live?: boolean }) {
  const t = useT("actions");
  const rows = lines
    .map((l) => {
      const raw = l.content.replace(ansi, "");
      const error = raw.startsWith(ERROR_MARK);
      const group = raw.startsWith(GROUP_MARK) || raw.startsWith(END_GROUP_MARK);
      return { l, error, group, text: error ? raw.slice(ERROR_MARK.length) : raw };
    })
    .filter((r) => !view.query || r.text.toLowerCase().includes(view.query));
  return (
    <div ref={tail} className="ax-log" role="log" aria-label={label} tabIndex={0}>
      {rows.map(({ l, error, group, text }) => {
        const at = new Date(l.time);
        return (
          <div key={l.index} className={error ? "ax-l ax-e" : group ? "ax-l ax-g" : "ax-l"}>
            <span className="ax-n" aria-hidden="true">{l.index + 1}</span>
            {view.stamps && !Number.isNaN(at.getTime()) && <span className="ax-t">{clock.format(at)}</span>}
            <span className="ax-tx">{text}</span>
          </div>
        );
      })}
      {rows.length === 0 && <p className="ax-logmsg">{t("log.noMatch")}</p>}
      {live && <span className="ax-cur" aria-hidden="true" />}
    </div>
  );
}
