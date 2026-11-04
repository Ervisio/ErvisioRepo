import { Icon, type IconName } from "../../ui/index.ts";
import { useT } from "../../i18n/useT.ts";

/** Visual state of a run, job or step: the conclusion once completed, else the status. */
export type RunState = "success" | "failure" | "cancelled" | "skipped" | "timed_out" | "in_progress" | "queued" | "waiting" | "pending" | "blocked";
type Tone = "ok" | "err" | "run" | "wait" | "idle";

export function runState(status: string, conclusion: string): RunState {
  if (status === "completed") {
    return conclusion === "success" || conclusion === "failure" || conclusion === "cancelled" || conclusion === "skipped" || conclusion === "timed_out" ? conclusion : "failure";
  }
  return status === "in_progress" || status === "queued" || status === "waiting" || status === "pending" || status === "blocked" ? status : "queued";
}

const tones: Record<RunState, Tone> = {
  success: "ok",
  failure: "err",
  timed_out: "err",
  in_progress: "run",
  waiting: "wait",
  blocked: "wait",
  cancelled: "idle",
  skipped: "idle",
  queued: "idle",
  pending: "idle",
};
const icons: Record<RunState, IconName> = {
  success: "check",
  failure: "x",
  timed_out: "clock",
  in_progress: "play",
  waiting: "lock",
  blocked: "lock",
  cancelled: "x",
  skipped: "right",
  queued: "clock",
  pending: "clock",
};

export const isActive = (status: string) => status !== "completed";

/** Icon only, with an accessible name. */
export function StatusIcon({ status, conclusion }: { status: string; conclusion: string }) {
  const t = useT("actions");
  const s = runState(status, conclusion);
  return (
    <span className={`ax-si ax-${tones[s]}`} role="img" aria-label={t(`state.${s}`)} title={t(`state.${s}`)}>
      <Icon name={icons[s]} />
    </span>
  );
}

/** Pill with icon and text; the colour never carries the meaning alone. */
export function StatusPill({ status, conclusion }: { status: string; conclusion: string }) {
  const t = useT("actions");
  const s = runState(status, conclusion);
  return (
    <span className={`ax-pill ax-${tones[s]}`}>
      <Icon name={icons[s]} />
      {t(`state.${s}`)}
    </span>
  );
}

/** "1m 05s" style duration between two instants (now when still running). */
export function duration(start?: string | null, end?: string | null, now = Date.now()): string {
  if (!start) return "";
  const a = Date.parse(start);
  const b = end ? Date.parse(end) : now;
  if (Number.isNaN(a) || Number.isNaN(b)) return "";
  const s = Math.max(0, Math.round((b - a) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

export const branchOf = (ref: string) => ref.replace(/^refs\/(heads|tags)\//, "");
