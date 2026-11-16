import { useState } from "react";
import { usePutStoragePolicy, type StoragePolicyConfig, type StorageTargetConfig } from "../../api/useStorage.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Dialog, DialogClose, Input, Select } from "../../ui/index.ts";

/** Radix Select rejects an empty item value, so the automatic cache has a stand-in. */
const AUTO_CACHE = "__auto__";
/** Cache value that disables the cache of a data type. */
const NO_CACHE = "none";
const DAY = 86400;

function PolicyForm({ policy, targets, onSaved, onClose }: {
  policy: StoragePolicyConfig;
  targets: StorageTargetConfig[];
  onSaved: () => void;
  onClose: () => void;
}) {
  const t = useT("admin");
  const tu = useT("ui");
  const [primary, setPrimary] = useState(policy.primary);
  const [replicas, setReplicas] = useState<string[]>(policy.replicas);
  const [cache, setCache] = useState(policy.cache || AUTO_CACHE);
  const [ttl, setTtl] = useState(policy.cacheTtlOverrideSeconds > 0 ? String(Math.round(policy.cacheTtlOverrideSeconds / DAY)) : "");
  const ttlDays = ttl.trim() === "" ? 0 : Number(ttl);
  const ttlValid = Number.isInteger(ttlDays) && ttlDays >= 0 && ttlDays <= 3650;
  const put = usePutStoragePolicy();

  const problems = put.error instanceof ApiError ? (put.error.errors ?? []) : [];
  const fieldMsg = (prefix: string) => problems.find((p) => p.path.startsWith(prefix))?.msg;
  const cacheMsg = problems.find((p) => p.path === "cache")?.msg;
  const general = [
    ...problems.filter((p) => !["primary", "replicas", "cache", "cacheTtl"].some((k) => p.path.startsWith(k))).map((p) => p.msg),
    ...(put.error && !(put.error instanceof ApiError && put.error.errors?.length) ? [put.error.message] : []),
  ];

  const label = (x: StorageTargetConfig) => `${x.name} (${x.kind})`;
  // With objects stored, only the primary the server runs with is allowed: the pending one can always be undone.
  const primaryOptions = targets
    .filter((x) => !policy.primaryLocked || x.name === policy.runningPrimary || x.name === policy.primary)
    .map((x) => ({ value: x.name, label: label(x) }));
  const cacheOptions = [
    { value: AUTO_CACHE, label: t("storage.policy.cache.auto") },
    { value: NO_CACHE, label: t("storage.policy.cache.none") },
    ...targets.filter((x) => x.kind === "local").map((x) => ({ value: x.name, label: label(x) })),
  ];
  const replicaChoices = targets.filter((x) => x.name !== primary);
  const toggle = (name: string, on: boolean) => setReplicas((r) => (on ? [...r.filter((n) => n !== name), name] : r.filter((n) => n !== name)));

  return (
    <form
      className="acc-form"
      onSubmit={(e) => {
        e.preventDefault();
        put.mutate(
          {
            policy: policy.name,
            input: {
              primary,
              replicas: replicas.filter((n) => n !== primary),
              cache: cache === AUTO_CACHE ? undefined : cache,
              cacheTtlSeconds: ttlDays * DAY,
            },
          },
          { onSuccess: onSaved },
        );
      }}
    >
      <div className="ui-field">
        <Select label={t("storage.primary")} value={primary} onValueChange={setPrimary} options={primaryOptions} disabled={policy.primaryLocked && policy.primary === policy.runningPrimary} />
        <p className={fieldMsg("primary") ? "ui-note adm-err" : "ui-note"} role={fieldMsg("primary") ? "alert" : undefined}>
          {fieldMsg("primary") ?? (policy.primaryLocked
            ? t(policy.primary === policy.runningPrimary ? "storage.policy.primary.locked" : "storage.policy.primary.undo", { objects: policy.objects, primary: policy.runningPrimary })
            : t("storage.policy.primary.free"))}
        </p>
      </div>
      <fieldset className="acc-scopes">
        <legend>{t("storage.replicas")}</legend>
        {replicaChoices.length === 0 && <p className="ui-note">{t("storage.policy.replicas.none")}</p>}
        {replicaChoices.map((x) => (
          <label key={x.name} className="acc-check">
            <input type="checkbox" checked={replicas.includes(x.name)} onChange={(e) => toggle(x.name, e.target.checked)} />
            {label(x)}
          </label>
        ))}
      </fieldset>
      <p className={fieldMsg("replicas") ? "ui-note adm-err" : "ui-note"} role={fieldMsg("replicas") ? "alert" : undefined}>
        {fieldMsg("replicas") ?? t("storage.policy.replicas.hint")}
      </p>
      <div className="ui-field">
        <Select label={t("storage.cache")} value={cache} onValueChange={setCache} options={cacheOptions} />
        <p className={cacheMsg ? "ui-note adm-err" : "ui-note"} role={cacheMsg ? "alert" : undefined}>
          {cacheMsg ?? t("storage.policy.cache.hint")}
        </p>
      </div>
      <Input
        label={t("storage.policy.ttl")}
        type="number"
        inputMode="numeric"
        min={0}
        max={3650}
        placeholder={t("storage.hot.ttl.default")}
        disabled={cache === NO_CACHE}
        value={ttl}
        error={fieldMsg("cacheTtl") ?? (ttlValid ? undefined : t("storage.hot.ttl.invalid"))}
        hint={t("storage.policy.ttl.hint", { days: Math.round(policy.cacheTtlSeconds / DAY) })}
        onChange={(e) => setTtl(e.target.value)}
      />
      {general.length > 0 && (
        <div role="alert">{general.map((m) => <p key={m} className="adm-err">{m}</p>)}</div>
      )}
      <div className="ui-dialog-foot">
        <DialogClose asChild><Button variant="ghost" onClick={onClose}>{tu("dialog.cancel")}</Button></DialogClose>
        <Button type="submit" variant="primary" disabled={put.isPending || !ttlValid}>{t("storage.target.save")}</Button>
      </div>
    </form>
  );
}

/** Edit where one data type lives: primary (only while empty), replicas and cache. */
export function StoragePolicyDialog({ policy, targets, onOpenChange, onSaved }: {
  policy: StoragePolicyConfig | undefined;
  targets: StorageTargetConfig[];
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const t = useT("admin");
  return (
    <Dialog
      open={policy !== undefined}
      onOpenChange={onOpenChange}
      title={t("storage.policy.edit", { policy: policy?.name ?? "" })}
      description={t("storage.policy.edit.hint")}
    >
      {policy && <PolicyForm key={policy.name} policy={policy} targets={targets} onSaved={onSaved} onClose={() => onOpenChange(false)} />}
    </Dialog>
  );
}
