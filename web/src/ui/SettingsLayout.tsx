import { useBlocker } from "@tanstack/react-router";
import { useId, useRef, useState, type ReactNode } from "react";
import { useT } from "../i18n/useT.ts";
import { Button } from "./Button.tsx";
import { Dialog, DialogClose } from "./Dialog.tsx";
import { Icon, type IconName } from "./Icon.tsx";
import { Input } from "./Input.tsx";

// Settings layout (round 005, variant A): one sub-navigation of grouped sections, two-column rows
// with a dirty marker, a sticky save bar while there are pending changes, and a danger zone.
// Used by account, repository and organization settings.

export type SettingsNavItem = { id: string; label: string; icon: IconName; keywords?: string; danger?: boolean };
export type SettingsNavGroup = { title: string; items: SettingsNavItem[] };
// The router marks a link active on its own (by path prefix and search subset) and overrides aria-current, so links must match exactly.
export type SettingsLinkProps = { className: string; "aria-current"?: "page"; activeOptions: { exact: true }; children: ReactNode };

/**
 * Sub-navigation plus content. Sections are routes: `renderLink` returns the router link for an
 * item with the given props, so the kit does not need to know the URLs.
 */
export function SettingsLayout({ label, groups, active, renderLink, children }: {
  label: string;
  groups: SettingsNavGroup[];
  active: string;
  renderLink: (item: SettingsNavItem, props: SettingsLinkProps) => ReactNode;
  children: ReactNode;
}) {
  const t = useT("ui");
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const shown = groups
    .map((g) => ({ ...g, items: g.items.filter((it) => !q || `${it.label} ${it.keywords ?? ""}`.toLowerCase().includes(q)) }))
    .filter((g) => g.items.length > 0);
  return (
    <div className="st">
      <nav className="st-sub" aria-label={label}>
        <div className="st-q">
          <Icon name="search" />
          <Input label={t("settings.search")} className="st-q-f" hideLabel type="search" placeholder={t("settings.search")} value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        {shown.map((g) => (
          <div key={g.title} className="st-grp" role="group" aria-label={g.title}>
            <span className="st-gt" aria-hidden="true">{g.title}</span>
            {g.items.map((it) => (
              <span key={it.id} className="st-li-w">
                {renderLink(it, {
                  className: it.danger ? "st-li dz" : "st-li",
                  "aria-current": it.id === active ? "page" : undefined,
                  activeOptions: { exact: true },
                  children: (
                    <>
                      <Icon name={it.icon} />
                      <span>{it.label}</span>
                    </>
                  ),
                })}
              </span>
            ))}
          </div>
        ))}
        {shown.length === 0 && <p className="ui-note st-none">{t("settings.noMatch")}</p>}
      </nav>
      <div className="st-main">{children}</div>
    </div>
  );
}

/** One settings section: a heading, an optional hint and its rows or cards. */
export function SettingsSection({ title, description, actions, children }: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="st-sec">
      <header className="st-sh">
        <div>
          <h2>{title}</h2>
          {description && <p className="ui-note">{description}</p>}
        </div>
        {actions && <div className="st-sh-a">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

/** Groups setting rows on a --sunk card. */
export function SettingsCard({ children }: { children: ReactNode }) {
  return <div className="st-card">{children}</div>;
}

/**
 * Label and description on the left, control on the right. `dirty` marks a pending change and
 * `was` shows the saved value next to it.
 */
export function SettingRow({ label, description, dirty, was, htmlFor, children }: {
  label: string;
  description?: ReactNode;
  dirty?: boolean;
  was?: string;
  htmlFor?: string;
  children: ReactNode;
}) {
  const t = useT("ui");
  return (
    <div className={dirty ? "st-row dirty" : "st-row"}>
      <div className="st-l">
        {htmlFor ? <label className="st-lb" htmlFor={htmlFor}>{label}</label> : <span className="st-lb">{label}</span>}
        {dirty && <span className="st-dot" role="img" aria-label={t("settings.changed")} />}
        {description && <p className="ui-note">{description}</p>}
        {dirty && was !== undefined && <p className="st-was">{t("settings.was", { value: was || t("settings.empty") })}</p>}
      </div>
      <div className="st-c">{children}</div>
    </div>
  );
}

/** On/off switch with a 44 px target. */
export function Toggle({ label, checked, onChange, disabled, id }: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <button
      type="button"
      id={id}
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      className="st-tg"
      onClick={() => onChange(!checked)}
    >
      <span className="st-tg-k" aria-hidden="true" />
    </button>
  );
}

/* ---------- Draft state, save bar and leave guard ---------- */

export type Draft<T extends Record<string, unknown>> = {
  value: T;
  initial: T;
  set: <K extends keyof T>(key: K, value: T[K]) => void;
  isDirty: (key: keyof T) => boolean;
  dirty: (keyof T)[];
  discard: () => void;
  /** Makes `next` the saved state (after a successful save). */
  rebase: (next: T) => void;
  /** Read at navigation time, so a save that navigates right away is not blocked. */
  pendingNow: () => boolean;
};

/** Form state with a saved copy: the save bar and the dirty markers read from it. */
export function useDraft<T extends Record<string, unknown>>(start: T): Draft<T> {
  const [state, setState] = useState({ initial: start, value: start });
  // Every change goes through update(), so the ref stays in step without writing it during render.
  const ref = useRef(state);
  const update = (next: { initial: T; value: T }) => {
    ref.current = next;
    setState(next);
  };
  const changed = (s: { initial: T; value: T }) => (Object.keys(s.value) as (keyof T)[]).filter((k) => !Object.is(s.value[k], s.initial[k]));
  return {
    value: state.value,
    initial: state.initial,
    set: (key, v) => update({ ...ref.current, value: { ...ref.current.value, [key]: v } }),
    isDirty: (key) => !Object.is(state.value[key], state.initial[key]),
    dirty: changed(state),
    discard: () => update({ ...ref.current, value: ref.current.initial }),
    rebase: (next) => update({ initial: next, value: next }),
    pendingNow: () => changed(ref.current).length > 0,
  };
}

/**
 * Sticky, opaque bar shown only while the draft has changes, with Discard and Save. It also
 * warns before leaving the page (in-app navigation and browser unload) with unsaved changes.
 */
export function SaveBar<T extends Record<string, unknown>>({ draft, labels, onSave, saving }: {
  draft: Draft<T>;
  labels: Partial<Record<keyof T, string>>;
  onSave: () => void;
  saving?: boolean;
}) {
  const t = useT("ui");
  const id = useId();
  const count = draft.dirty.length;
  const blocker = useBlocker({
    shouldBlockFn: () => draft.pendingNow(),
    enableBeforeUnload: () => draft.pendingNow(),
    withResolver: true,
  });
  return (
    <>
      {count > 0 && (
        <div className="st-bar" role="region" aria-labelledby={id}>
          <div className="st-bar-t">
            <b id={id}>{t(count === 1 ? "settings.unsaved.one" : "settings.unsaved.other", { count })}</b>
            <span className="ui-note">{draft.dirty.map((k) => labels[k] ?? String(k)).join(", ")}</span>
          </div>
          <Button variant="ghost" onClick={draft.discard} disabled={saving}>{t("settings.discard")}</Button>
          <Button variant="primary" onClick={onSave} disabled={saving}>{saving ? t("settings.saving") : t("settings.save")}</Button>
        </div>
      )}
      <Dialog
        open={blocker.status === "blocked"}
        onOpenChange={(o) => { if (!o) blocker.reset?.(); }}
        title={t("settings.leave.title")}
        description={t("settings.leave.description")}
        footer={
          <>
            <Button variant="ghost" onClick={() => blocker.reset?.()}>{t("settings.leave.stay")}</Button>
            <Button variant="danger" onClick={() => blocker.proceed?.()}>{t("settings.leave.go")}</Button>
          </>
        }
      />
    </>
  );
}

/* ---------- Danger zone and confirmations ---------- */

/** Destructive actions, kept apart at the bottom of a page. */
export function DangerZone({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="st-sec">
      <header className="st-sh"><h2 className="st-dz-h">{title}</h2></header>
      <div className="st-dz">{children}</div>
    </section>
  );
}

export function DangerRow({ title, description, action }: { title: string; description: ReactNode; action: ReactNode }) {
  return (
    <div className="st-drow">
      <div className="st-l">
        <span className="st-lb">{title}</span>
        <p className="ui-note">{description}</p>
      </div>
      <div className="st-drow-a">{action}</div>
    </div>
  );
}

/**
 * Confirmation dialog for a destructive action. With `typeToConfirm`, the confirm button stays
 * disabled until that exact text is typed.
 */
export function ConfirmDialog({ trigger, open, onOpenChange, title, description, confirmLabel, onConfirm, pending, typeToConfirm, children }: {
  trigger?: ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  title: string;
  description?: string;
  confirmLabel: string;
  onConfirm: () => void;
  pending?: boolean;
  typeToConfirm?: string;
  children?: ReactNode;
}) {
  const t = useT("ui");
  const [own, setOwn] = useState(false);
  const [typed, setTyped] = useState("");
  const isOpen = open ?? own;
  const setOpen = (o: boolean) => {
    if (!o) setTyped("");
    if (onOpenChange) onOpenChange(o);
    else setOwn(o);
  };
  const ready = !typeToConfirm || typed === typeToConfirm;
  return (
    <Dialog
      open={isOpen}
      onOpenChange={setOpen}
      trigger={trigger}
      title={title}
      description={description}
      footer={
        <>
          <DialogClose asChild><Button variant="ghost">{t("dialog.cancel")}</Button></DialogClose>
          <Button variant="danger" disabled={!ready || pending} onClick={onConfirm}>{confirmLabel}</Button>
        </>
      }
    >
      {children || typeToConfirm ? (
        <>
          {children}
          {typeToConfirm && (
            <Input label={t("confirm.type", { text: typeToConfirm })} mono autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          )}
        </>
      ) : undefined}
    </Dialog>
  );
}
