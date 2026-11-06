// What the UI knows about each editable instance setting: the section it lives in, the fixed set
// of values where there is one, and the format hint for text values. The server owns validation;
// the value type (switch, number, text, list) comes from the value the API returns.

export const settingSections = ["auth", "registration", "email", "git", "actions", "webhooks", "appearance", "server"] as const;
export type SettingSection = (typeof settingSections)[number];
export const isSettingSection = (s: string): s is SettingSection => (settingSections as readonly string[]).includes(s);

export type SettingFormat = "duration" | "size";

const enums: Record<string, readonly string[]> = {
  "security.registration": ["open", "invite", "closed"],
  "security.user_directory": ["shared", "instance"],
  "log.level": ["debug", "info", "warn", "error"],
  "log.format": ["json", "text"],
  "mail.tls": ["starttls", "tls", "none"],
  "ui.default_layout": ["console", "foglio", "corsie"],
};

const sizes = new Set(["git.max_push_size", "actions.cache_max_repo_size"]);
const durationKey = /(_ttl|_timeout|_max_age|_backoff_base|_backoff_max|_grace_time|_offline_after|_retention|^git\.max_duration|^mail\.timeout|^webhooks\.timeout)$/;

export function sectionOf(key: string): SettingSection {
  if (key === "server.ssh_listen") return "git";
  switch (key.split(".")[0]) {
    case "auth": return "auth";
    case "security": return "registration";
    case "mail": return "email";
    case "git":
    case "ssh": return "git";
    case "actions": return "actions";
    case "webhooks": return "webhooks";
    case "ui": return "appearance";
    default: return "server";
  }
}

export const enumValues = (key: string): readonly string[] | undefined => enums[key];

export function formatOf(key: string): SettingFormat | undefined {
  if (sizes.has(key)) return "size";
  return durationKey.test(key) ? "duration" : undefined;
}
