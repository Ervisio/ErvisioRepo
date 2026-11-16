import { useState } from "react";
import { ApiError } from "../../api/http.ts";
import { useAvatarStorage, usePutAvatarStorage, type AvatarStorage } from "../../api/useStorage.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Input, Select, SettingRow } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";

// Radix Select has no empty value, so the local storage gets a sentinel.
const LOCAL = "__local";

/** Where uploaded avatars are stored and the optional CDN base URL; saved changes apply at once. */
export function AvatarStorageCard() {
  const q = useAvatarStorage();
  if (!q.data) return null;
  // Keyed by the saved settings, so a save resets the form to what the server stored.
  return <AvatarStorageForm key={`${q.data.target}|${q.data.publicUrl}`} data={q.data} />;
}

function AvatarStorageForm({ data }: { data: AvatarStorage }) {
  const t = useT("admin");
  const notify = useNotify();
  const put = usePutAvatarStorage();
  const [target, setTarget] = useState(data.target || LOCAL);
  const [publicUrl, setPublicUrl] = useState(data.publicUrl);

  const local = data.localTarget ? t("storage.avatars.local", { target: data.localTarget }) : t("storage.avatars.localNone");
  const options = [
    { value: LOCAL, label: local },
    ...data.targets.map((x) => ({ value: x.name, label: `${x.name} (${x.kind}${x.encrypted ? `, ${t("storage.avatars.encrypted")}` : ""})` })),
  ];
  const problems = put.error instanceof ApiError ? (put.error.errors ?? []) : [];
  const fieldError = (path: string) => problems.find((p) => p.path === path)?.msg;
  const dirty = target !== (data.target || LOCAL) || publicUrl.trim() !== data.publicUrl;

  function save() {
    put.mutate(
      { target: target === LOCAL ? "" : target, publicUrl: publicUrl.trim() },
      { onSuccess: () => notify.ok(t("storage.avatars.saved")), onError: (e) => { if (!(e instanceof ApiError && e.errors?.length)) notify.fail(e); } },
    );
  }

  return (
    <Card
      title={t("storage.avatars")}
      actions={
        <Button size="sm" variant="primary" disabled={!dirty || put.isPending} onClick={save}>
          {t("storage.avatars.save")}
        </Button>
      }
    >
      <SettingRow label={t("storage.avatars.target")} description={t("storage.avatars.target.hint")}>
        <Select label={t("storage.avatars.target")} hideLabel value={target} onValueChange={setTarget} options={options} />
        {fieldError("target") && <p className="ui-note" role="alert">{fieldError("target")}</p>}
      </SettingRow>
      <SettingRow label={t("storage.avatars.publicUrl")} description={t("storage.avatars.publicUrl.hint")}>
        <Input
          label={t("storage.avatars.publicUrl")}
          hideLabel
          type="url"
          inputMode="url"
          maxLength={512}
          placeholder="https://cdn.example.com"
          value={publicUrl}
          error={fieldError("publicUrl")}
          onChange={(e) => setPublicUrl(e.target.value)}
        />
      </SettingRow>
    </Card>
  );
}
