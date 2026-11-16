import { useRef } from "react";
import { useDeleteAvatar, useUpdateProfile, useUploadAvatar, type User } from "../../api/identity.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Input, SaveBar, Select, SettingRow, SettingsCard, SettingsSection, useDraft } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { useNotify } from "./common.tsx";

/** Profile fields as settings rows with a save bar. Emails and organizations have their own pages. */
export function Profile({ user }: { user: User }) {
  const t = useT("account");
  const notify = useNotify();
  const update = useUpdateProfile();
  const upload = useUploadAvatar();
  const remove = useDeleteAvatar();
  const picker = useRef<HTMLInputElement>(null);
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

  // The avatar changes at once: the saved URL follows it, pending edits of the other fields stay.
  function avatarChanged(u: User) {
    const pending = draft.value;
    draft.rebase({ ...draft.initial, avatarUrl: u.avatarUrl ?? "" });
    if (pending.displayName !== draft.initial.displayName) draft.set("displayName", pending.displayName);
    if (pending.locale !== draft.initial.locale) draft.set("locale", pending.locale);
  }

  function pick(file: File | undefined) {
    if (!file) return;
    upload.mutate(file, {
      onSuccess: (u) => {
        avatarChanged(u);
        notify.ok(t("profile.avatar.uploaded"));
      },
      onError: notify.fail,
    });
  }

  function clear() {
    remove.mutate(undefined, {
      onSuccess: (u) => {
        avatarChanged(u);
        notify.ok(t("profile.avatar.removed"));
      },
      onError: notify.fail,
    });
  }

  const busy = upload.isPending || remove.isPending;

  return (
    <SettingsSection title={t("profile.title")}>
      <SettingsCard>
        <SettingRow label={t("profile.username")} description={t("profile.username.hint")}>
          <Input label={t("profile.username")} hideLabel value={user.username} readOnly mono />
        </SettingRow>
        <SettingRow label={t("profile.displayName")} dirty={draft.isDirty("displayName")} was={draft.initial.displayName}>
          <Input label={t("profile.displayName")} hideLabel maxLength={100} value={v.displayName} onChange={(e) => draft.set("displayName", e.target.value)} />
        </SettingRow>
        <SettingRow label={t("profile.avatar")} description={t("profile.avatar.hint")}>
          <div className="ui-inline">
            <Avatar user={user} size="xl" />
            <input
              ref={picker}
              type="file"
              accept="image/png,image/jpeg,image/gif"
              hidden
              aria-label={t("profile.avatar.upload")}
              onChange={(e) => {
                pick(e.target.files?.[0]);
                e.target.value = "";
              }}
            />
            <Button size="sm" icon="upload" disabled={busy} onClick={() => picker.current?.click()}>
              {upload.isPending ? t("profile.avatar.uploading") : t("profile.avatar.upload")}
            </Button>
            {user.avatarUrl && (
              <Button size="sm" icon="trash" disabled={busy} onClick={clear}>
                {t("profile.avatar.remove")}
              </Button>
            )}
          </div>
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
