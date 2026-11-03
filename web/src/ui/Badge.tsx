import type { ReactNode } from "react";

export type BadgeTone = "neutral" | "ok" | "warn" | "err" | "info" | "section";

/** Status pill. Status tones use status colors, never a section hue; "section" takes the surrounding --hue. */
export function Badge({ tone = "neutral", children }: { tone?: BadgeTone; children: ReactNode }) {
  return <span className={`ui-badge ui-badge-${tone}`}>{children}</span>;
}
