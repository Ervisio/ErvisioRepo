import { useSyncExternalStore } from "react";

// Per-user layout of the work lists (issues, pull requests, notifications):
// "a" dense rows (default), "b" filter column, "c" split view with preview.
export const listLayouts = ["a", "b", "c"] as const;
export type ListLayout = (typeof listLayouts)[number];

const storageKey = "erepo.listLayout";
const defaultLayout: ListLayout = "a";

function read(): ListLayout {
  try {
    const v = localStorage.getItem(storageKey);
    return listLayouts.find((l) => l === v) ?? defaultLayout;
  } catch {
    return defaultLayout;
  }
}

let current: ListLayout | undefined;
const listeners = new Set<() => void>();

function snapshot(): ListLayout {
  current ??= read();
  return current;
}

export function setListLayout(layout: ListLayout) {
  try {
    localStorage.setItem(storageKey, layout);
  } catch {
    // storage unavailable: keep the choice for this page load only
  }
  current = layout;
  for (const l of listeners) l();
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  const onStorage = (e: StorageEvent) => {
    if (e.key !== storageKey) return;
    current = read();
    cb();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("storage", onStorage);
  };
}

export function useListLayout(): ListLayout {
  return useSyncExternalStore(subscribe, snapshot, () => defaultLayout);
}

/*
 * Order of the last list shown, so a detail page opened from it can step to the previous or
 * next item (split view navigation). Kept per tab in sessionStorage.
 */
const orderKey = (kind: string, owner: string, name: string) => `erepo.listOrder.${kind}.${owner}/${name}`;

export function saveListOrder(kind: string, owner: string, name: string, numbers: number[]) {
  try {
    sessionStorage.setItem(orderKey(kind, owner, name), JSON.stringify(numbers));
  } catch {
    // storage unavailable: the detail page simply hides the step navigation
  }
}

export function loadListOrder(kind: string, owner: string, name: string): number[] {
  try {
    const v: unknown = JSON.parse(sessionStorage.getItem(orderKey(kind, owner, name)) ?? "[]");
    return Array.isArray(v) ? v.filter((n): n is number => typeof n === "number") : [];
  } catch {
    return [];
  }
}
