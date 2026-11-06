import { useState } from "react";
import { useInstanceSettings, useUpdateInstanceSettings, type InstanceSetting, type InstanceSettings } from "../../api/admin.ts";
import { ApiError } from "../../api/http.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, EmptyState, Icon, Input, SaveBar, Select, SettingRow, SettingsCard, Toggle, useDraft, type BadgeTone } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";
import { enumValues, formatOf, sectionOf, type SettingSection } from "./settingsCatalog.ts";

type DraftValue = string | boolean;
type Values = Record<string, DraftValue>;

const sourceTone: Record<InstanceSetting["source"], BadgeTone> = { default: "neutral", file: "info", db: "section", env: "warn" };

const envName = (key: string) => `EREPO_${key.toUpperCase().replaceAll(".", "_")}`;

/** Value as the control edits it: booleans stay booleans, everything else is text. */
function toDraft(v: unknown): DraftValue {
  if (typeof v === "boolean") return v;
  if (Array.isArray(v)) return v.map(String).join("\n");
  return v === null || v === undefined ? "" : String(v);
}

function toDraftValues(settings: InstanceSetting[]): Values {
  return Object.fromEntries(settings.map((s) => [s.key, toDraft(s.value)]));
}

/** Converts the edited text back to the type the server sent; undefined when it does not parse. */
function fromDraft(current: unknown, v: DraftValue): unknown {
  if (typeof current === "boolean") return v === true;
  const text = typeof v === "string" ? v : "";
  if (typeof current === "number") return /^-?\d+$/.test(text.trim()) ? Number(text.trim()) : undefined;
  if (Array.isArray(current)) return text.split("\n").map((x) => x.trim()).filter(Boolean);
  return text.trim();
}

/** The settings of one section of the instance configuration. */
export function AdminSettings({ section }: { section: SettingSection }) {
  const t = useT("admin");
  const tc = useT("common");
  const query = useInstanceSettings();
  if (query.isPending) return <AdminPane title={t(`section.${section}`)}><p role="status" className="ui-note">{t("settings.loading")}</p></AdminPane>;
  if (query.isError) {
    return (
      <AdminPane title={t(`section.${section}`)}>
        <div role="alert">
          <EmptyState icon="alert" title={t("settings.error")} description={query.error.message} action={<Button onClick={() => void query.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      </AdminPane>
    );
  }
  return <SettingsForm data={query.data} section={section} />;
}

function SettingsForm({ data, section }: { data: InstanceSettings; section: SettingSection }) {
  const t = useT("admin");
  const notify = useNotify();
  const update = useUpdateInstanceSettings();
  const draft = useDraft<Values>(toDraftValues(data.settings));
  const [problems, setProblems] = useState<Record<string, string>>({});
  const byKey = new Map(data.settings.map((s) => [s.key, s]));
  const shown = data.settings.filter((s) => sectionOf(s.key) === section);
  const label = (key: string) => t(`set.${key}`);

  function change(key: string, v: DraftValue) {
    draft.set(key, v);
    if (problems[key]) setProblems((p) => Object.fromEntries(Object.entries(p).filter(([k]) => k !== key)));
  }

  function save() {
    const values: Record<string, unknown> = {};
    const local: Record<string, string> = {};
    for (const key of draft.dirty) {
      const parsed = fromDraft(byKey.get(key)?.value, draft.value[key] ?? "");
      if (parsed === undefined) local[key] = t("settings.number");
      else values[key] = parsed;
    }
    setProblems(local);
    if (Object.keys(local).length > 0) return;
    update.mutate(values, {
      onSuccess: (next) => {
        draft.rebase(toDraftValues(next.settings));
        setProblems({});
        notify.ok(t("settings.saved"));
      },
      onError: (e) => {
        if (e instanceof ApiError && e.status === 422 && e.errors?.length) {
          const found: Record<string, string> = {};
          for (const f of e.errors) {
            const key = [...byKey.keys()].find((k) => f.path === k || f.path.endsWith(`.${k}`) || f.path.startsWith(`${k}.`)) ?? f.path;
            found[key] = f.msg;
          }
          setProblems(found);
          notify.fail(new Error(t("settings.saveFailed")));
        } else notify.fail(e);
      },
    });
  }

  return (
    <AdminPane title={t(`section.${section}`)} subtitle={t(`section.${section}.hint`)}>
      {data.restartRequired && (
        <div className="adm-notice" role="status">
          <Icon name="alert" />
          <div>
            <b>{t("settings.restart.title")}</b>
            <p className="ui-note">{t("settings.restart.body")}</p>
          </div>
        </div>
      )}
      {shown.length === 0 && <p className="ui-note">{t("settings.empty")}</p>}
      {shown.length > 0 && (
        <SettingsCard>
          {shown.map((s) => (
            <SettingField key={s.key} setting={s} value={draft.value[s.key] ?? ""} was={draft.initial[s.key]} dirty={draft.isDirty(s.key)} problem={problems[s.key]} onChange={(v) => change(s.key, v)} />
          ))}
        </SettingsCard>
      )}
      {/* Problems for keys that live in another section would otherwise stay invisible. */}
      {Object.keys(problems).some((k) => byKey.has(k) && sectionOf(k) !== section) && (
        <p className="ui-note adm-err" role="alert">{t("settings.saveFailed")}: {Object.keys(problems).filter((k) => byKey.has(k) && sectionOf(k) !== section).map(label).join(", ")}</p>
      )}
      <SaveBar draft={draft} labels={Object.fromEntries(data.settings.map((s) => [s.key, label(s.key)]))} onSave={save} saving={update.isPending} />
    </AdminPane>
  );
}

function SettingField({ setting, value, was, dirty, problem, onChange }: {
  setting: InstanceSetting;
  value: DraftValue;
  was: DraftValue | undefined;
  dirty: boolean;
  problem?: string;
  onChange: (v: DraftValue) => void;
}) {
  const t = useT("admin");
  const key = setting.key;
  const name = t(`set.${key}`);
  const format = formatOf(key);
  const options = enumValues(key);
  const isList = Array.isArray(setting.value);
  const fileValue = toDraft(setting.fileValue);

  const hint = isList ? t("settings.hint.list") : format ? t(`settings.hint.${format}`) : undefined;
  const lockedWhy = setting.source === "env" ? t("settings.locked.env", { name: envName(key) }) : t("settings.locked.file");

  let control;
  if (typeof setting.value === "boolean") {
    control = <Toggle label={name} checked={value === true} disabled={setting.locked} onChange={onChange} />;
  } else if (options) {
    control = (
      <Select
        label={name}
        hideLabel
        disabled={setting.locked}
        value={String(value)}
        onValueChange={onChange}
        options={options.map((o) => ({ value: o, label: t(`enum.${o}`) }))}
      />
    );
  } else if (isList) {
    control = (
      <div className={problem ? "ui-field ui-field-err" : "ui-field"}>
        <label className="sr-only" htmlFor={`set-${key}`}>{name}</label>
        <textarea id={`set-${key}`} className="ui-input ui-mono adm-list" rows={3} spellCheck={false} disabled={setting.locked} value={String(value)} onChange={(e) => onChange(e.target.value)} aria-invalid={problem ? true : undefined} />
        {hint && <p className="ui-note">{hint}</p>}
      </div>
    );
  } else {
    control = (
      <Input
        label={name}
        hideLabel
        mono
        autoComplete="off"
        inputMode={typeof setting.value === "number" ? "numeric" : undefined}
        disabled={setting.locked}
        value={String(value)}
        onChange={(e) => onChange(e.target.value)}
        hint={hint}
        error={problem}
      />
    );
  }

  const wasText = was === undefined ? undefined : typeof was === "boolean" ? t(was ? "settings.on" : "settings.off") : was.replaceAll("\n", ", ");
  const description = (
    <>
      {t(`set.${key}.d`)}
      {setting.source === "db" && !setting.locked && <> {t("settings.hint.fileValue", { value: fileText(fileValue, t) })}</>}
      <span className="adm-meta">
        <Badge tone={sourceTone[setting.source]}>{t(`settings.source.${setting.source}`)}</Badge>
        {setting.locked && <span className="adm-lock" title={lockedWhy}><Icon name="lock" />{t("settings.locked")}</span>}
        {setting.restartPending && <Badge tone="warn">{t("settings.restartPending")}</Badge>}
      </span>
      {setting.locked && <span className="adm-why">{lockedWhy}</span>}
    </>
  );

  return (
    <SettingRow label={name} description={description} dirty={dirty} was={wasText}>
      <div className="adm-ctl">
        {control}
        {problem && (typeof setting.value === "boolean" || options || isList) && <p className="ui-note adm-err" role="alert">{problem}</p>}
      </div>
    </SettingRow>
  );
}

function fileText(v: DraftValue, t: (k: string) => string): string {
  if (typeof v === "boolean") return t(v ? "settings.on" : "settings.off");
  return v === "" ? t("settings.none") : v.replaceAll("\n", ", ");
}
