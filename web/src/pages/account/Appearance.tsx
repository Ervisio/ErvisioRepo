import { useSavePreferences, useUserPreferences } from "../../api/preferences.ts";
import { useT } from "../../i18n/useT.ts";
import { Select, SettingRow, SettingsCard, SettingsSection } from "../../ui/index.ts";
import { layouts, themes, type Layout, type Theme } from "../../ui/shell/layout.ts";
import { useNotify } from "./common.tsx";

/** Per-user theme and layout. An empty layout means the instance default. */
export function Appearance() {
  const t = useT("account");
  const tc = useT("common");
  const notify = useNotify();
  const prefs = useUserPreferences();
  const save = useSavePreferences();
  const change = (b: { theme?: Theme; layout?: Layout | "" }) => save.mutate(b, { onSuccess: () => notify.ok(t("appearance.saved")), onError: notify.fail });

  return (
    <SettingsSection title={t("appearance.title")} description={t("appearance.hint")}>
      <SettingsCard>
        {prefs.isPending ? (
          <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>
        ) : prefs.isError ? (
          <p role="alert" className="ui-note acc-pad">{prefs.error.message}</p>
        ) : (
          <>
            <SettingRow label={t("appearance.theme")} description={t("appearance.themeHint")}>
              <Select
                label={t("appearance.theme")}
                hideLabel
                value={prefs.data.theme}
                disabled={save.isPending}
                onValueChange={(v) => change({ theme: themes.find((x) => x === v) })}
                options={themes.map((x) => ({ value: x, label: t(`theme.${x}`) }))}
              />
            </SettingRow>
            <SettingRow label={t("appearance.layout")} description={t("appearance.layoutHint")}>
              <Select
                label={t("appearance.layout")}
                hideLabel
                value={prefs.data.layout || "default"}
                disabled={save.isPending}
                onValueChange={(v) => change({ layout: v === "default" ? "" : layouts.find((x) => x === v) })}
                options={[{ value: "default", label: t("layout.default") }, ...layouts.map((x) => ({ value: x, label: t(`layout.${x}`) }))]}
              />
            </SettingRow>
          </>
        )}
      </SettingsCard>
    </SettingsSection>
  );
}
