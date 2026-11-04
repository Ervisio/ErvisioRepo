/**
 * Settings state shared by the settings pages and the bottom bar, and the setting rows.
 *
 * Every change is saved to config.toml as soon as it is made (switches and selects at once, text and numbers when the
 * field loses focus or on Enter). The server only reads the file at start, so what was saved since the page opened
 * is "saved, not active yet" until a restart. A value the server refuses stays in the field with its message.
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import * as repo from '../api/repo';
import type { SettingValue, SettingValues, SettingsDoc } from '../api/repo';
import { Button, Icon, Input, Select, Switch } from '../kit';
import {
  FIELDS, check, display, joinDur, joinSize, same, splitDur, splitList, splitSize,
  type DurUnit, type PageId, type SizeUnit,
} from './fields';
import { errText, t } from './util';

export interface SettingsApi {
  doc: SettingsDoc | null;
  loadError: unknown;
  reload(): void;
  value(k: string): SettingValue | undefined;
  /** Saves one or more keys; false when a value was refused (the message is on the row). */
  save(vals: SettingValues): Promise<boolean>;
  setDraft(k: string, v: SettingValue): void;
  errors: Record<string, string>;
  /** Errors about the whole file, or the reason nothing could be written. */
  general: string[];
  /** Keys saved since the page opened (or since the last restart from here). */
  modified: string[];
  /** Keys whose new value could not be saved. */
  unsaved: string[];
  busy: boolean;
  saveAll(): Promise<void>;
  undoAll(): Promise<void>;
  /** After a restart: what is on disk is now active. */
  applied(): void;
  readOnly: boolean;
  /** The server does not answer: nothing can be read or saved. */
  offline: boolean;
}

const Ctx = createContext<SettingsApi | null>(null);
export const useSettings = () => useContext(Ctx);

export function SettingsProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  const [doc, setDoc] = useState<SettingsDoc | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [baseline, setBaseline] = useState<SettingValues | null>(null);
  const [drafts, setDrafts] = useState<SettingValues>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [general, setGeneral] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    let live = true;
    repo.getSettings().then(
      (d) => {
        if (!live) return;
        setDoc(d);
        setLoadError(null);
        setBaseline((b) => b ?? d.values);
      },
      (e) => live && setLoadError(e),
    );
    return () => {
      live = false;
    };
  }, [enabled, tick]);

  const reload = useCallback(() => setTick((n) => n + 1), []);

  const fail = useCallback((keys: string[], e: unknown) => {
    const rowErrs: Record<string, string> = {};
    const gen: string[] = [];
    if (e instanceof repo.RepoError && e.errors.length) {
      for (const fe of e.errors) {
        if (fe.path && keys.includes(fe.path)) rowErrs[fe.path] = fe.msg;
        else gen.push(fe.path ? `${fe.path}: ${fe.msg}` : fe.msg);
      }
    } else if (e instanceof repo.RepoError && e.status === 409) {
      gen.push(t('set.readOnlyErr'));
    } else if (keys.length === 1) {
      rowErrs[keys[0]] = errText(e);
    } else {
      gen.push(errText(e));
    }
    if (!Object.keys(rowErrs).length && !gen.length) gen.push(errText(e));
    setErrors((x) => ({ ...x, ...rowErrs }));
    setGeneral(gen);
  }, []);

  const save = useCallback(async (vals: SettingValues): Promise<boolean> => {
    const keys = Object.keys(vals);
    setDrafts((d) => ({ ...d, ...vals }));
    const local: Record<string, string> = {};
    for (const k of keys) {
      const c = check(k, vals[k]);
      if (c) local[k] = c;
    }
    if (Object.keys(local).length) {
      setErrors((x) => ({ ...x, ...local }));
      return false;
    }
    const changed: SettingValues = {};
    for (const k of keys) if (!doc || !same(doc.values[k], vals[k])) changed[k] = vals[k];
    const clear = () => {
      setDrafts((d) => {
        const n = { ...d };
        for (const k of keys) delete n[k];
        return n;
      });
      setErrors((x) => {
        const n = { ...x };
        for (const k of keys) delete n[k];
        return n;
      });
    };
    if (!Object.keys(changed).length) {
      clear();
      return true;
    }
    setBusy(true);
    try {
      const d = await repo.putSettings(changed);
      setDoc(d);
      clear();
      setGeneral([]);
      return true;
    } catch (e) {
      fail(keys, e);
      return false;
    } finally {
      setBusy(false);
    }
  }, [doc, fail]);

  const setDraft = useCallback((k: string, v: SettingValue) => setDrafts((d) => ({ ...d, [k]: v })), []);

  const unsaved = useMemo(() => Object.keys(drafts).filter((k) => errors[k] || (general.length && doc && !same(doc.values[k], drafts[k]))), [drafts, errors, general, doc]);
  const modified = useMemo(
    () => (doc && baseline ? Object.keys(FIELDS).filter((k) => k in doc.values && !same(doc.values[k], baseline[k])) : []),
    [doc, baseline],
  );

  const saveAll = useCallback(async () => {
    const vals: SettingValues = {};
    for (const k of unsaved) vals[k] = drafts[k];
    if (Object.keys(vals).length) await save(vals);
  }, [unsaved, drafts, save]);

  const undoAll = useCallback(async () => {
    setDrafts({});
    setErrors({});
    setGeneral([]);
    if (!baseline || !modified.length) return;
    const vals: SettingValues = {};
    for (const k of modified) vals[k] = baseline[k];
    await save(vals);
  }, [baseline, modified, save]);

  const applied = useCallback(() => {
    setBaseline(null);
    setDoc(null);
    reload();
  }, [reload]);

  const api: SettingsApi = {
    doc,
    loadError,
    reload,
    value: (k) => (k in drafts ? drafts[k] : doc?.values[k]),
    save,
    setDraft,
    errors,
    general,
    modified,
    unsaved,
    busy,
    saveAll,
    undoAll,
    applied,
    readOnly: !!doc && !doc.configWritable,
    offline: !enabled,
  };
  return <Ctx.Provider value={api}>{children}</Ctx.Provider>;
}

/* ---------- rows ---------- */

/** One setting: title, help, key and default on the left, the control on the right. */
export function Row({ k, label, help }: { k: string; label?: string; help?: ReactNode }) {
  const s = useSettings();
  if (!s?.doc || !(k in s.doc.values)) return null;
  const err = s.errors[k];
  const mod = s.modified.includes(k) || s.unsaved.includes(k);
  const def = s.doc.defaults[k];
  return (
    <div className={`rp-row${mod ? ' is-mod' : ''}${err ? ' is-err' : ''}`}>
      <div className="rp-row-t">
        <b>{label ?? t(`f.${k}`)}</b>
        <span className="rp-row-h">{help ?? t(`f.${k}.h`)}</span>
        <span className="rp-row-k">
          <code>{k}</code>
          {def !== undefined && <span>{t('set.default', { v: display(k, def) })}</span>}
        </span>
      </div>
      <div className="rp-ctl"><Control k={k} /></div>
      {err && <div className="rp-msg" role="alert">{err}</div>}
    </div>
  );
}

function Control({ k }: { k: string }) {
  const s = useSettings()!;
  const f = FIELDS[k];
  const v = s.value(k);
  const dis = s.readOnly;
  const name = t(`f.${k}`);
  switch (f.kind) {
    case 'bool':
      return <Switch checked={!!v} disabled={dis} aria-label={name} onChange={(c) => void s.save({ [k]: c })} />;
    case 'enum':
      return (
        <Select compact disabled={dis} value={String(v ?? '')} onChange={(x) => void s.save({ [k]: x })}
          options={f.options.map((o) => ({ value: o, label: t(`opt.${k}.${o}`) }))} />
      );
    case 'int':
      return <TextCtl k={k} value={v === undefined ? '' : String(v)} type="number" name={name} disabled={dis} parse={(x) => (x.trim() === '' ? NaN : Number(x))} />;
    case 'list':
      return <TextCtl k={k} value={Array.isArray(v) ? v.join(', ') : ''} name={name} disabled={dis} parse={splitList} placeholder={t('set.listHint')} />;
    case 'text':
      return <TextCtl k={k} value={String(v ?? '')} name={name} disabled={dis} parse={(x) => x.trim()} />;
    case 'dur':
      return <DurCtl k={k} value={String(v ?? '')} name={name} disabled={dis} seconds={!!f.seconds} />;
    case 'size':
      return <SizeCtl k={k} value={String(v ?? '')} name={name} disabled={dis} />;
  }
}

const blurOnEnter = (e: { key: string; currentTarget: HTMLInputElement }) => {
  if (e.key === 'Enter') e.currentTarget.blur();
};

/** A text or number field kept locally while it has focus, saved when it loses it. */
function TextCtl({ k, value, name, disabled, type, parse, placeholder }: {
  k: string; value: string; name: string; disabled: boolean; type?: string; placeholder?: string; parse(x: string): SettingValue;
}) {
  const s = useSettings()!;
  const [text, setText] = useState(value);
  const editing = useRef(false);
  useEffect(() => {
    if (!editing.current) setText(value);
  }, [value]);
  return (
    <Input compact mono={type !== 'number'} type={type} value={text} disabled={disabled} aria-label={name} placeholder={placeholder}
      spellCheck={false} autoCapitalize="off"
      onFocus={() => { editing.current = true; }}
      onChange={(e) => setText(e.target.value)}
      onKeyDown={blurOnEnter}
      onBlur={() => {
        editing.current = false;
        void s.save({ [k]: parse(text) });
      }} />
  );
}

const unitLabel = (u: string) => t(`unitl.${u}`);

/** Number plus unit for a duration. Seconds are offered where they make sense, or when the value needs them. */
function DurCtl({ k, value, name, disabled, seconds }: { k: string; value: string; name: string; disabled: boolean; seconds: boolean }) {
  const s = useSettings()!;
  const init = splitDur(value);
  const [n, setN] = useState(init.n);
  const [unit, setUnit] = useState<DurUnit>(init.unit);
  const editing = useRef(false);
  useEffect(() => {
    if (editing.current) return;
    const x = splitDur(value);
    setN(x.n);
    setUnit(x.unit);
  }, [value]);
  const units: DurUnit[] = seconds || unit === 's' ? ['s', 'm', 'h', 'd'] : ['m', 'h', 'd'];
  return (
    <div className="rp-num">
      <Input compact type="number" min={1} value={n} disabled={disabled} aria-label={name}
        onFocus={() => { editing.current = true; }}
        onChange={(e) => setN(e.target.value)}
        onKeyDown={blurOnEnter}
        onBlur={() => {
          editing.current = false;
          void s.save({ [k]: joinDur(n, unit) });
        }} />
      <Select compact disabled={disabled} value={unit} options={units.map((u) => ({ value: u, label: unitLabel(u) }))}
        onChange={(u) => {
          setUnit(u as DurUnit);
          if (n.trim()) void s.save({ [k]: joinDur(n, u as DurUnit) });
        }} />
    </div>
  );
}

/** Number plus unit for a size: MB, GB, TB, or the binary unit the file already uses. */
function SizeCtl({ k, value, name, disabled }: { k: string; value: string; name: string; disabled: boolean }) {
  const s = useSettings()!;
  const init = splitSize(value);
  const [n, setN] = useState(init.n);
  const [unit, setUnit] = useState<SizeUnit>(init.unit);
  const editing = useRef(false);
  useEffect(() => {
    if (editing.current) return;
    const x = splitSize(value);
    setN(x.n);
    setUnit(x.unit);
  }, [value]);
  const units: SizeUnit[] = ['MB', 'GB', 'TB'];
  if (!units.includes(unit)) units.unshift(unit);
  return (
    <div className="rp-num">
      <Input compact type="number" min={0} step="any" value={n} disabled={disabled} aria-label={name}
        onFocus={() => { editing.current = true; }}
        onChange={(e) => setN(e.target.value)}
        onKeyDown={blurOnEnter}
        onBlur={() => {
          editing.current = false;
          void s.save({ [k]: joinSize(n, unit) });
        }} />
      <Select compact disabled={disabled} value={unit} options={units.map((u) => ({ value: u, label: u }))}
        onChange={(u) => {
          setUnit(u as SizeUnit);
          if (n.trim()) void s.save({ [k]: joinSize(n, u as SizeUnit) });
        }} />
    </div>
  );
}

/* ---------- page frame ---------- */

/** A settings page: title, lead, the file banners and its cards. */
export function SettingsPage({ id, children }: { id: PageId; children: ReactNode }) {
  const s = useSettings();
  return (
    <>
      <div className="rp-pagehead">
        <h2>{t(`nav.${id}`)}</h2>
        <p className="rp-lead">{t(`lead.${id}`)}</p>
      </div>
      {s && <FileBanners />}
      {!s?.doc ? (
        s?.offline ? (
          <div className="rp-err"><Icon name="power" /><div><b>{t('set.offline')}</b><span>{t('set.offlineText')}</span></div></div>
        ) : s?.loadError ? (
          <div className="rp-err"><Icon name="alert" /><div><b>{t('set.loadFailed')}</b><span>{errText(s.loadError)}</span></div>
            <Button size="sm" icon="refresh" onClick={s.reload}>{t('retry')}</Button></div>
        ) : <div className="rp-card rp-card--empty" aria-busy="true" />
      ) : children}
    </>
  );
}

/** Read-only file, a file that does not parse, or errors about the whole file. */
export function FileBanners() {
  const s = useSettings();
  if (!s?.doc) return null;
  const { doc } = s;
  return (
    <>
      {doc.configError && (
        <div className="rp-err" role="alert"><Icon name="alert" /><div><b>{t('set.fileError')}</b><span className="rp-pre">{doc.configError}</span></div></div>
      )}
      {!doc.configWritable && (
        <div className="rp-err" role="status"><Icon name="lock" /><div><b>{t('set.readOnly')}</b><span>{t('set.readOnlyText', { path: doc.configPath })}</span></div></div>
      )}
      {s.general.length > 0 && (
        <div className="rp-err" role="alert"><Icon name="alert" /><div><b>{t('set.refused')}</b>{s.general.map((g) => <span key={g} className="rp-pre">{g}</span>)}</div></div>
      )}
    </>
  );
}

export function SetCard({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="rp-card">
      <h3>{title}{action && <span className="rp-h3-end">{action}</span>}</h3>
      {children}
    </section>
  );
}
