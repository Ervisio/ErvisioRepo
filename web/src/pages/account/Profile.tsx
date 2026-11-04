import { useUpdateProfile, type User } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Input, SaveBar, Select, SettingRow, SettingsCard, SettingsSection, useDraft } from "../../ui/index.ts";
import { useNotify } from "./common.tsx";

/** Profile fields as settings rows with a save bar. Emails and organizations have their own pages. */
export function Profile({ user }: { user: User }) {
  const t = useT("account");
  const notify = useNotify();
  const update = useUpdateProfile();
  const draft = useDraft({ displayName: user.displayName, avatarUrl: user.avatarUrl ?? "", locale: user.locale });
  const v = draft.value;
  const options = [
    { value: "en", label: t("profile.locale.en") },
    { value: "it", label: t("profile.locale.it") },
  ];

  function save() {
    const next = { displayName: v.displayName.trim(), avatarUrl: v.avatarUrl.trim(), locale: v.locale };
    update.mutate(next, {
      onSuccess: () => {
        draft.rebase(next);
        notify.ok(t("profile.saved"));
      },
      onError: notify.fail,
    });
  }

  return (
    <SettingsSection title={t("profile.title")}>
      <SettingsCard>
        <SettingRow label={t("profile.username")} description={t("profile.username.hint")}>
          <Input label={t("profile.username")} hideLabel value={user.username} readOnly mono />
        </SettingRow>
        <SettingRow label={t("profile.displayName")} dirty={draft.isDirty("displayName")} was={draft.initial.displayName}>
          <Input label={t("profile.displayName")} hideLabel maxLength={100} value={v.displayName} onChange={(e) => draft.set("displayName", e.target.value)} />
        </SettingRow>
        <SettingRow label={t("profile.avatarUrl")} description={t("profile.avatarUrl.hint")} dirty={draft.isDirty("avatarUrl")} was={draft.initial.avatarUrl}>
          <Input
            label={t("profile.avatarUrl")}
            hideLabel
            type="url"
            inputMode="url"
            maxLength={2048}
            placeholder="https://"
            value={v.avatarUrl}
            onChange={(e) => draft.set("avatarUrl", e.target.value)}
          />
        </SettingRow>
        <SettingRow label={t("profile.locale")} dirty={draft.isDirty("locale")} was={t(`profile.locale.${draft.initial.locale}`)}>
          <Select label={t("profile.locale")} hideLabel value={v.locale} onValueChange={(x) => draft.set("locale", x as User["locale"])} options={options} />
        </SettingRow>
      </SettingsCard>
      <SaveBar
        draft={draft}
        labels={{ displayName: t("profile.displayName"), avatarUrl: t("profile.avatarUrl"), locale: t("profile.locale") }}
        onSave={save}
        saving={update.isPending}
      />
    </SettingsSection>
  );
}
