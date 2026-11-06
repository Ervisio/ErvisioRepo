import { useEffect } from "react";
import { useSession } from "../../api/identity.ts";
import { useUserPreferences } from "../../api/preferences.ts";
import { useMeta } from "../../api/useMeta.ts";
import { applyAppearance } from "./layout.ts";

/** Keeps <html> in step with the server: user preference, then instance default, then console. */
export function useAppearance() {
  const session = useSession();
  const signedIn = !!session.data;
  const meta = useMeta();
  const prefs = useUserPreferences(signedIn);
  const waiting = session.isPending || (signedIn && prefs.isPending) || meta.isPending;
  const layout = prefs.data?.layout;
  const theme = prefs.data?.theme;
  const instanceLayout = meta.data?.defaultLayout;
  useEffect(() => {
    if (waiting) return;
    applyAppearance({ layout, theme, instanceLayout });
  }, [waiting, layout, theme, instanceLayout]);
}
