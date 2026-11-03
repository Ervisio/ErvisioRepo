import { format, resolveLocale, type Locale } from "./locale.ts";

type Messages = Record<string, string>;

const modules = import.meta.glob<Messages>("./*/*.json", { eager: true, import: "default" });

function load(locale: Locale, namespace: string): Messages {
  return modules[`./${locale}/${namespace}.json`] ?? {};
}

const locale = resolveLocale(navigator.languages?.length ? navigator.languages : [navigator.language]);

/** Returns a translate function bound to one namespace (src/i18n/<lang>/<namespace>.json). */
export function useT(namespace: string) {
  const messages = load(locale, namespace);
  const fallback = load("en", namespace);
  return (key: string, params?: Record<string, string | number>): string =>
    format(messages[key] ?? fallback[key] ?? key, params);
}
