import { useT } from "../i18n/useT.ts";
import { Badge } from "./Badge.tsx";

/** Bot logins end in "[bot]": normal usernames cannot contain brackets. */
export function isBotLogin(login: string | null | undefined): boolean {
  return !!login && login.toLowerCase().endsWith("[bot]");
}

/** The "bot" badge shown next to the name of a bot account; nothing for people. */
export function BotMark({ login }: { login: string | null | undefined }) {
  const t = useT("ui");
  return isBotLogin(login) ? <Badge>{t("bot.badge")}</Badge> : null;
}
