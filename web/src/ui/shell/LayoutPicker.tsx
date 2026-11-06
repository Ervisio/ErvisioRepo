import { useSession } from "../../api/identity.ts";
import { useSavePreferences } from "../../api/preferences.ts";
import { useT } from "../../i18n/useT.ts";
import { Select } from "../Select.tsx";
import { layouts, parseLayout, useLayout } from "./layout.ts";

/** Saves the signed-in user's layout preference; the shell follows once the server answers. */
export function LayoutPicker() {
  const t = useT("shell");
  const layout = useLayout();
  const signedIn = !!useSession().data;
  const save = useSavePreferences();
  return (
    <Select
      label={t("layout.label")}
      value={layout}
      disabled={!signedIn || save.isPending}
      onValueChange={(v) => save.mutate({ layout: parseLayout(v) })}
      options={layouts.map((l) => ({ value: l, label: t(`layout.${l}`) }))}
    />
  );
}
