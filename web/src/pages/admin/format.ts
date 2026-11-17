// Time formatting shared by the administration pages.

/** Hour and minute of a time, with the day when it is not today. */
export function formatClock(d: Date, now = new Date()): string {
  const sameDay = d.toDateString() === now.toDateString();
  return d.toLocaleString(undefined, sameDay ? { hour: "2-digit", minute: "2-digit" } : { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
}
