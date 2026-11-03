import { useT } from "../../i18n/useT.ts";
import { Select } from "../Select.tsx";
import { layouts, parseLayout, setLayout, useLayout } from "./layout.ts";

/** Local stand-in for the instance default and per-user override (see TODO in layout.ts). */
export function LayoutPicker() {
  const t = useT("shell");
  const layout = useLayout();
  return (
    <Select
      label={t("layout.label")}
      value={layout}
      onValueChange={(v) => setLayout(parseLayout(v))}
      options={layouts.map((l) => ({ value: l, label: t(`layout.${l}`) }))}
    />
  );
}
