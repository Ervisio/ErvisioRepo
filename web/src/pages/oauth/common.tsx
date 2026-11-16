import { useState } from "react";
import { SCOPES, type TokenScope } from "../../api/identity.ts";
import type { OAuthAppOwner } from "../../api/oauth.ts";
import { useT } from "../../i18n/useT.ts";

/** App logo, or a square with its initial when there is none or it fails to load. */
export function AppLogo({ name, url, size = "md" }: { name: string; url?: string; size?: "sm" | "md" | "lg" | "xl" }) {
  const [broken, setBroken] = useState("");
  const cls = `oa-logo oa-logo-${size}`;
  if (url && broken !== url) return <img className={cls} src={url} alt="" onError={() => setBroken(url)} />;
  return <span className={cls} aria-hidden="true">{(name.trim()[0] ?? "?").toUpperCase()}</span>;
}

/** "Built-in" for the system clients, otherwise the owner's login. */
export function useOwnerLabel() {
  const t = useT("oauth");
  return (o: OAuthAppOwner) => (o.kind === "system" ? t("owner.system") : o.name);
}

const isScope = (s: string): s is TokenScope => (SCOPES as readonly string[]).includes(s);

/** Token scopes of this server as sentences; unknown ones are shown as they are. */
export function useScopeText() {
  const t = useT("oauth");
  return (s: string) => (isScope(s) ? t(`scope.${s}`) : s);
}

/** Origin of a URL for display, or the URL itself when it does not parse. */
export function originOf(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return url;
  }
}
