/** Small helpers shared by the views: translation, number formats and a loader hook. */
import { useCallback, useEffect, useRef, useState } from 'react';
import { getSdk } from '../sdk';
import { RepoError } from '../api/repo';

export const t = (key: string, vars?: Record<string, string | number>) => getSdk().t(key, vars);

export const locale = () => (getSdk().lang() === 'it' ? 'it-IT' : 'en-US');

export const num = (n: number) => new Intl.NumberFormat(locale()).format(n);

const GB = 1024 ** 3;

/** Bytes with the unit people expect (1024 steps, like Ervisio). */
export function bytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = i === 0 || v >= 100 ? 0 : 1;
  return `${new Intl.NumberFormat(locale(), { maximumFractionDigits: digits }).format(v)} ${units[i]}`;
}

export const gbToBytes = (gb: number) => gb * GB;
export const gbText = (gb: number) => bytes(gb * GB);

export const errText = (e: unknown) => (e as Error)?.message ?? String(e);

/** The server's host name from its public URL, for display. */
export function hostOf(url: string | null): string | null {
  if (!url) return null;
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function initials(name: string): string {
  const parts = name.replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? '?') + (parts[1]?.[0] ?? '')).toUpperCase();
}

export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export interface Loaded<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  reload(): void;
}

/** Runs `fn` on mount and on reload(); keeps the last data while reloading. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []): Loaded<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  useEffect(() => {
    let live = true;
    setLoading(true);
    fnRef.current().then(
      (d) => {
        if (!live) return;
        setData(d);
        setError(null);
        setLoading(false);
      },
      (e) => {
        if (!live) return;
        setError(e);
        setLoading(false);
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, ...deps]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, reload };
}

export const isAdminError = (e: unknown) => e instanceof RepoError && e.kind === 'admin';
export const isMissingError = (e: unknown) => e instanceof RepoError && (e.kind === 'unavailable');
