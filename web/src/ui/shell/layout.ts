import { useSyncExternalStore } from "react";

export const layouts = ["console", "foglio", "corsie"] as const;
export type Layout = (typeof layouts)[number];
export const themes = ["system", "light", "dark"] as const;
export type Theme = (typeof themes)[number];

export const defaultLayout: Layout = "console";
// localStorage only caches the last resolved values so the first paint has no flash.
// The server values (user preference, then instance default) always win once loaded.
const layoutKey = "ervisio.layout";
const themeKey = "ervisio.theme";

export function parseLayout(value: unknown): Layout {
  return layouts.find((l) => l === value) ?? defaultLayout;
}

export function parseTheme(value: unknown): Theme {
  return themes.find((x) => x === value) ?? "system";
}

function readCache(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeCache(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // storage unavailable: the cache only avoids a flash
  }
}

let current: Layout = defaultLayout;
let currentTheme: Theme = "system";
const listeners = new Set<() => void>();
const dark = typeof matchMedia === "function" ? matchMedia("(prefers-color-scheme: dark)") : undefined;

function paint() {
  const root = document.documentElement;
  root.dataset.layout = current;
  root.dataset.theme = currentTheme === "system" ? (dark?.matches === false ? "light" : "dark") : currentTheme;
  for (const l of listeners) l();
}

/** Applies the cached layout and theme to <html>. Call once before rendering. */
export function initLayout() {
  current = parseLayout(readCache(layoutKey));
  currentTheme = parseTheme(readCache(themeKey));
  dark?.addEventListener("change", paint);
  paint();
}

/** What the server says: the user's choice ("" = none), the instance default and the user's theme. */
export type Appearance = { layout: Layout | "" | undefined; instanceLayout: Layout | undefined; theme: Theme | undefined };

/** Effective layout = user preference || instance default || console; theme follows the user preference. */
export function applyAppearance(a: Appearance) {
  current = a.layout ? parseLayout(a.layout) : a.instanceLayout ? parseLayout(a.instanceLayout) : defaultLayout;
  currentTheme = parseTheme(a.theme);
  writeCache(layoutKey, current);
  writeCache(themeKey, currentTheme);
  paint();
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function useLayout(): Layout {
  return useSyncExternalStore(subscribe, () => current, () => defaultLayout);
}
