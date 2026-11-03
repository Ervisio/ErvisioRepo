import { useSyncExternalStore } from "react";

export const layouts = ["console", "foglio", "corsie"] as const;
export type Layout = (typeof layouts)[number];

export const defaultLayout: Layout = "console";
const storageKey = "ervisio.layout";

export function parseLayout(value: unknown): Layout {
  return layouts.find((l) => l === value) ?? defaultLayout;
}

// TODO(ui-settings): the layout comes from the instance setting "Appearance" (served in the
// initial config) with a per-user override in account settings. Until those exist the choice
// lives in localStorage, which stands in for the per-user override.
function read(): Layout {
  try {
    return parseLayout(localStorage.getItem(storageKey));
  } catch {
    return defaultLayout;
  }
}

let current: Layout = defaultLayout;
const listeners = new Set<() => void>();

function apply(layout: Layout) {
  current = layout;
  document.documentElement.dataset.layout = layout;
  for (const l of listeners) l();
}

/** Reads the stored layout and sets data-layout on <html>. Call once before rendering. */
export function initLayout() {
  apply(read());
}

export function setLayout(layout: Layout) {
  try {
    localStorage.setItem(storageKey, layout);
  } catch {
    // storage unavailable: keep the choice for this page load only
  }
  apply(layout);
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useLayout(): Layout {
  return useSyncExternalStore(subscribe, () => current, () => defaultLayout);
}
