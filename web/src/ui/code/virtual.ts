import { useLayoutEffect, useRef, useState } from "react";

// Window-scroll virtualization with a fixed row height. Code and diff views keep the
// page as the vertical scroller (DESIGN.md: code scrolls only horizontally inside its
// card), so every list measures its own position against the viewport. One shared
// scroll/resize listener batches all lists in a single animation frame.

const listeners = new Set<() => void>();
let frame = 0;

function schedule() {
  if (frame) return;
  frame = requestAnimationFrame(() => {
    frame = 0;
    for (const fn of listeners) fn();
  });
}

function subscribe(fn: () => void): () => void {
  if (listeners.size === 0) {
    window.addEventListener("scroll", schedule, { passive: true, capture: true });
    window.addEventListener("resize", schedule);
  }
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
    if (listeners.size === 0) {
      window.removeEventListener("scroll", schedule, { capture: true });
      window.removeEventListener("resize", schedule);
    }
  };
}

/** Lists up to this many rows render in full (browser find keeps working). */
export const RENDER_ALL = 1500;
const OVERSCAN = 30;

export type Range = { start: number; end: number };

/**
 * Returns the ref to put on the list body (its top is row 0) and the range of rows to
 * render. `rowHeight` must match the CSS row height exactly.
 */
export function useVirtualRows(count: number, rowHeight: number) {
  const ref = useRef<HTMLDivElement>(null);
  const all = count <= RENDER_ALL;
  const [range, setRange] = useState<Range>(() => ({ start: 0, end: Math.min(count, all ? count : 80) }));

  useLayoutEffect(() => {
    if (all) return;
    const measure = () => {
      const el = ref.current;
      if (!el) return;
      const top = el.getBoundingClientRect().top;
      const first = Math.floor(-top / rowHeight);
      const visible = Math.ceil(window.innerHeight / rowHeight);
      const start = Math.max(0, Math.min(count, first - OVERSCAN));
      const end = Math.max(start, Math.min(count, first + visible + OVERSCAN));
      setRange((r) => (r.start === start && r.end === end ? r : { start, end }));
    };
    measure();
    return subscribe(measure);
  }, [count, rowHeight, all]);

  // Small lists render every row; the measured range only matters when virtualizing.
  const start = all ? 0 : Math.min(range.start, count);
  const end = all ? count : Math.min(range.end, count);
  return { ref, start, end, height: count * rowHeight };
}

/** Scrolls the window so that row `index` of the list under `el` is near the top. */
export function scrollToRow(el: HTMLElement | null, index: number, rowHeight: number) {
  if (!el) return;
  const top = el.getBoundingClientRect().top + window.scrollY + index * rowHeight - 120;
  window.scrollTo({ top: Math.max(0, top) });
}
