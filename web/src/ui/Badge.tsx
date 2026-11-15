import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon.tsx";

export type BadgeTone = "neutral" | "ok" | "warn" | "err" | "info" | "section";

/** Status pill. Status tones use status colors, never a section hue; "section" takes the surrounding --hue. */
export function Badge({ tone = "neutral", icon, children }: { tone?: BadgeTone; icon?: IconName; children: ReactNode }) {
  return <span className={`ui-badge ui-badge-${tone}`}>{icon && <Icon name={icon} />}{children}</span>;
}
