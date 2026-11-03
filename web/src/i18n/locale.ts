export const locales = ["en", "it"] as const;
export type Locale = (typeof locales)[number];

export const fallbackLocale: Locale = "en";

/** Picks the first supported language from a navigator.languages-style list. */
export function resolveLocale(languages: readonly string[]): Locale {
  for (const tag of languages) {
    const primary = tag.toLowerCase().split("-")[0];
    const match = locales.find((l) => l === primary);
    if (match) return match;
  }
  return fallbackLocale;
}

/** Replaces {name} placeholders with the given values. */
export function format(message: string, params?: Record<string, string | number>): string {
  if (!params) return message;
  return message.replace(/\{(\w+)\}/g, (whole, name: string) => String(params[name] ?? whole));
}
