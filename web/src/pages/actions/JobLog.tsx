import { useQueryClient } from "@tanstack/react-query";
import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { fetchLogPage, logRawUrl, logStreamUrl, useJob, type ActionsJob, type ActionsLogLine, type ActionsLogPage } from "../../api/actions.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Icon } from "../../ui/index.ts";
import { duration, isActive, StatusIcon } from "./common.tsx";

// Terminal colour escapes are dropped: the log shows plain text.
const ESC = String.fromCharCode(27);
const ansi = new RegExp(`${ESC}\\[[0-9;]*[A-Za-z]`, "g");
const MAX_RETRIES = 5;

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

export function JobLog({ owner, name, job }: { owner: string; name: string; job: ActionsJob }) {
  const t = useT("actions");
  const qc = useQueryClient();
  const running = isActive(job.status);
  const detail = useJob(owner, name, job.id, running);
  const log = useJobLog(owner, name, job.id, running, () => void qc.invalidateQueries({ queryKey: ["actions", owner, name] }));
  const [follow, setFollow] = useState(true);
  const end = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (running && follow && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) end.current?.scrollIntoView({ block: "end" });
  }, [log.lines.length, running, follow]);

  const steps = detail.data?.steps ?? [];
  const stepAt = new Map(steps.filter((s) => s.logLength > 0).map((s) => [s.logIndex, s]));

  return (
    <section className="ax-logbox" aria-label={t("log.title", { job: job.name })}>
      <header className="ax-logbar">
        <h3>{job.name}</h3>
        <span className="acc-grow" />
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
      </header>
      {steps.length > 0 && (
        <ol className="ax-steps" aria-label={t("log.steps")}>
          {steps.map((s) => (
            <li key={s.index}>
              <a href={`#ax-step-${s.logIndex}`} className="ax-step-chip" onClick={(e) => (e.preventDefault(), document.getElementById(`ax-step-${s.logIndex}`)?.scrollIntoView({ block: "start" }))}>
                <StatusIcon status={s.status} conclusion={s.conclusion} />
                <span>{s.name}</span>
                <span className="ax-step-dur">{duration(s.startedAt, s.completedAt)}</span>
              </a>
            </li>
          ))}
        </ol>
      )}
      <div className="ax-log" role="log" aria-label={t("log.lines")} tabIndex={0}>
        {log.lines.map((l) => {
          const step = stepAt.get(l.index);
          return (
            <Fragment key={l.index}>
              {step && (
                <div className="ax-step-h" id={`ax-step-${l.index}`}>
                  <StatusIcon status={step.status} conclusion={step.conclusion} />
                  {step.name}
                </div>
              )}
              <div className="ax-line">
                <span className="ax-ln" aria-hidden="true">{l.index + 1}</span>
                <span className="ax-lt">{l.content.replace(ansi, "")}</span>
              </div>
            </Fragment>
          );
        })}
        {log.loading && <p role="status" className="ax-logmsg">{t("log.loading")}</p>}
        {!log.loading && !log.failed && log.lines.length === 0 && <p className="ax-logmsg">{running ? t("log.waiting") : t("log.empty")}</p>}
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
        <div ref={end} />
      </div>
    </section>
  );
}
