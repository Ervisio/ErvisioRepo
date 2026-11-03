import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { REACTION_EMOJI, REACTIONS, uploadAttachment, type Label, type ReactionGroup } from "../../api/issues.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Button, Icon, Menu, MenuItem, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { useInAppLinks, useMarkdownContext } from "../code/Readme.tsx";
import { listLayouts, setListLayout, useListLayout, type ListLayout } from "./layout.ts";

/* Colour contrast: label chips and avatars pick black or white text, whichever reads best. */

function parseHex(hex: string): [number, number, number] | undefined {
  let h = hex.trim().replace(/^#/, "");
  if (h.length === 3) h = h.replace(/./g, (c) => c + c);
  if (!/^[0-9a-f]{6}$/i.test(h)) return undefined;
  const n = parseInt(h, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function luminance([r, g, b]: [number, number, number]): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/**
 * Text colour for a solid background: the one of black and white with the higher WCAG contrast.
 * For any sRGB colour the better of the two is at least 4.58:1, above the 4.5:1 AA threshold.
 */
export function textOn(bg: string): "#000" | "#fff" {
  const rgb = parseHex(bg);
  if (!rgb) return "#fff";
  const l = luminance(rgb);
  return (l + 0.05) / 0.05 >= 1.05 / (l + 0.05) ? "#000" : "#fff";
}

function hslToHex(h: number, s: number, l: number): string {
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => {
    const k = (n + h / 30) % 12;
    const c = l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1));
    return Math.round(c * 255).toString(16).padStart(2, "0");
  };
  return `#${f(0)}${f(8)}${f(4)}`;
}

export function LabelChip({ label }: { label: Pick<Label, "name" | "color" | "description"> }) {
  const bg = parseHex(label.color) ? `#${label.color.replace(/^#/, "")}` : "#3a3a40";
  return (
    <span className="wk-label" style={{ background: bg, color: textOn(bg) }} title={label.description || undefined}>
      {label.name}
    </span>
  );
}

/** Initials on a colour derived from the username. */
export function Avatar({ user, size = 28 }: { user: string; size?: number }) {
  let h = 0;
  for (const c of user) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  const bg = hslToHex(h % 360, 0.55, 0.42);
  const initials = user.replace(/[^a-z0-9]/gi, "").slice(0, 2).toUpperCase() || "?";
  return (
    <span className="wk-avatar" aria-hidden="true" style={{ background: bg, color: textOn(bg), width: size, height: size, fontSize: Math.round(size * 0.4) }}>
      {initials}
    </span>
  );
}

export type WorkState = "open" | "closed" | "merged" | "draft";

/** State icon in the state colour: open green, closed red, merged violet, draft grey. */
export function StateIcon({ kind, state, label }: { kind: "issue" | "pull"; state: WorkState; label: string }) {
  const icon: IconName = kind === "pull" ? "pr" : state === "closed" ? "check" : "issue";
  return (
    <span className={`wk-state wk-state-${state}`} role="img" aria-label={label} title={label}>
      <Icon name={icon} />
    </span>
  );
}

export function StatePill({ state, label }: { state: WorkState; label: string }) {
  return <span className={`wk-pill wk-state-${state}`}>{label}</span>;
}

/* Layout switch shown in the list headers. */

export function ListLayoutSwitch() {
  const t = useT("work");
  const layout = useListLayout();
  const icons: Record<ListLayout, IconName> = { a: "menu", b: "layers", c: "grid" };
  return (
    <div className="ui-tablist wk-switch" role="group" aria-label={t("layout.label")}>
      {listLayouts.map((l) => (
        <button
          key={l}
          type="button"
          className="ui-tab"
          data-state={l === layout ? "active" : "inactive"}
          aria-pressed={l === layout}
          title={t(`layout.${l}.hint`)}
          onClick={() => setListLayout(l)}
        >
          <Icon name={icons[l]} />
          <span className="wk-switch-txt">{t(`layout.${l}`)}</span>
        </button>
      ))}
    </div>
  );
}

const typing = (el: EventTarget | null) =>
  el instanceof HTMLElement && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName));

/**
 * One list in the three layouts. A renders dense rows, B two-line rows next to a filter column,
 * C a selectable list with a preview pane (j/k move, o or Enter opens, / focuses the search).
 */
export function WorkList<T>({ layout, items, getKey, row, preview, onOpen, filters, label, searchId, empty }: {
  layout: ListLayout;
  items: T[];
  getKey: (x: T) => string;
  row: (x: T, layout: ListLayout) => ReactNode;
  preview?: (x: T) => ReactNode;
  onOpen: (x: T) => void;
  filters?: ReactNode;
  label: string;
  searchId?: string;
  empty: ReactNode;
}) {
  const t = useT("work");
  const [sel, setSel] = useState(0);
  const idx = Math.max(0, Math.min(sel, items.length - 1));
  const current = items[idx];
  const split = layout === "c" && preview !== undefined;
  const listRef = useRef<HTMLUListElement>(null);

  // Global shortcuts for the split view; ignored while typing in a field.
  const state = useRef({ items, idx, onOpen });
  useEffect(() => {
    state.current = { items, idx, onOpen };
  });
  useEffect(() => {
    if (!split) return;
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target)) return;
      const s = state.current;
      if (e.key === "j" || e.key === "k") {
        e.preventDefault();
        const next = Math.max(0, Math.min(s.idx + (e.key === "j" ? 1 : -1), s.items.length - 1));
        setSel(next);
        listRef.current?.querySelectorAll<HTMLElement>("[data-wk-item]")[next]?.focus();
      } else if (e.key === "o") {
        const x = s.items[s.idx];
        if (x !== undefined) s.onOpen(x);
      } else if (e.key === "/" && searchId) {
        e.preventDefault();
        document.getElementById(searchId)?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [split, searchId]);

  const list =
    items.length === 0 ? (
      empty
    ) : split ? (
      <ul className="wk-list wk-list-c" aria-label={label} ref={listRef}>
        {items.map((x, i) => (
          <li key={getKey(x)}>
            <button
              type="button"
              data-wk-item
              className="wk-ci"
              aria-current={i === idx ? "true" : undefined}
              onClick={() => setSel(i)}
              onDoubleClick={() => onOpen(x)}
              onKeyDown={(e: KeyboardEvent) => {
                if (e.key === "Enter") onOpen(x);
              }}
            >
              {row(x, "c")}
            </button>
          </li>
        ))}
      </ul>
    ) : (
      <ul className={`wk-list wk-list-${layout === "b" ? "b" : "a"}`} aria-label={label}>
        {items.map((x) => (
          <li key={getKey(x)}>{row(x, layout === "b" ? "b" : "a")}</li>
        ))}
      </ul>
    );

  if (split) {
    return (
      <div className="wk-split">
        <div className="wk-split-list">
          {list}
          <p className="ui-note wk-keys">{t("keys.hint")}</p>
        </div>
        <aside className="wk-pv" aria-label={t("preview.label")}>
          {current !== undefined ? preview(current) : <p className="ui-note">{t("preview.none")}</p>}
        </aside>
      </div>
    );
  }
  if (layout === "b" && filters) {
    return (
      <div className="wk-lay-b">
        <aside className="wk-filters" aria-label={t("filters.label")}>
          {filters}
        </aside>
        <div>{list}</div>
      </div>
    );
  }
  return list;
}

/** A link-like button group used in the filter column (layout B). */
export function FilterGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="wk-fgroup">
      <h4>{title}</h4>
      {children}
    </div>
  );
}

export function FilterItem({ active, onClick, children, count }: { active: boolean; onClick: () => void; children: ReactNode; count?: number }) {
  return (
    <button type="button" className="wk-fitem" aria-pressed={active} onClick={onClick}>
      <span className="acc-grow">{children}</span>
      {count !== undefined && <span className="wk-count">{count}</span>}
    </button>
  );
}

/** Milestone completion bar. */
export function Meter({ open, closed, label }: { open: number; closed: number; label: string }) {
  const total = open + closed;
  const pct = total === 0 ? 0 : Math.round((closed / total) * 100);
  return (
    <span className="wk-meter" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={label}>
      <span style={{ width: `${pct}%` }} />
    </span>
  );
}

/** Markdown body with links resolved against the default branch. */
export function Body({ owner, name, refName, source }: { owner: string; name: string; refName: string; source: string }) {
  const ctx = useMarkdownContext(owner, name, refName, "");
  const onClick = useInAppLinks();
  const t = useT("work");
  if (!source.trim()) return <p className="ui-note">{t("body.empty")}</p>;
  return (
    <div onClick={onClick}>
      <Markdown source={source} ctx={ctx} className="rp-md" />
    </div>
  );
}

/** Markdown editor with write and preview tabs and file attachments. */
export function Composer({ owner, name, refName, value, onChange, label, placeholder, rows = 6, autoFocus, children }: {
  owner: string;
  name: string;
  refName: string;
  value: string;
  onChange: (v: string) => void;
  label: string;
  placeholder?: string;
  rows?: number;
  autoFocus?: boolean;
  children?: ReactNode;
}) {
  const t = useT("work");
  const notify = useNotify();
  const id = useId();
  const [tab, setTab] = useState<"write" | "preview">("write");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  async function attach(files: FileList | null) {
    if (!files || files.length === 0) return;
    setBusy(true);
    let text = value;
    try {
      for (const f of Array.from(files)) {
        const a = await uploadAttachment(owner, name, f);
        const link = a.contentType.startsWith("image/") ? `![${a.name}](${a.url})` : `[${a.name}](${a.url})`;
        text = text ? `${text}\n${link}` : link;
        onChange(text);
      }
    } catch (e) {
      notify.fail(e);
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  return (
    <div className="wk-composer">
      <div className="wk-composer-bar">
        <div className="ui-tablist" role="tablist" aria-label={label}>
          {(["write", "preview"] as const).map((x) => (
            <button key={x} type="button" role="tab" className="ui-tab" aria-selected={tab === x} data-state={tab === x ? "active" : "inactive"} onClick={() => setTab(x)}>
              {t(`composer.${x}`)}
            </button>
          ))}
        </div>
        <span className="acc-grow" />
        <input ref={fileRef} type="file" multiple hidden onChange={(e) => void attach(e.target.files)} />
        <Button size="sm" variant="ghost" icon="upload" disabled={busy} onClick={() => fileRef.current?.click()}>
          {busy ? t("composer.uploading") : t("composer.attach")}
        </Button>
      </div>
      {tab === "write" ? (
        <>
          <label htmlFor={id} className="wk-sr">
            {label}
          </label>
          <textarea
            id={id}
            className="ui-input acc-area wk-textarea"
            rows={rows}
            placeholder={placeholder ?? t("composer.placeholder")}
            value={value}
            autoFocus={autoFocus}
            onChange={(e) => onChange(e.target.value)}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              if (e.dataTransfer.files.length === 0) return;
              e.preventDefault();
              void attach(e.dataTransfer.files);
            }}
          />
          <p className="ui-note">{t("composer.hint")}</p>
        </>
      ) : (
        <div className="wk-preview">
          <Body owner={owner} name={name} refName={refName} source={value} />
        </div>
      )}
      {children && <div className="acc-actions wk-composer-actions">{children}</div>}
    </div>
  );
}

/** Reaction groups with toggles plus a picker for the others. */
export function Reactions({ groups, disabled, onToggle }: { groups: ReactionGroup[]; disabled?: boolean; onToggle: (content: string, on: boolean) => void }) {
  const t = useT("work");
  const shown = groups.filter((g) => g.count > 0);
  return (
    <div className="wk-reactions">
      {shown.map((g) => (
        <button
          key={g.content}
          type="button"
          className="wk-react"
          aria-pressed={g.viewerReacted}
          disabled={disabled}
          aria-label={t("reactions.toggle", { reaction: t(`reaction.${g.content}`), count: g.count })}
          onClick={() => onToggle(g.content, !g.viewerReacted)}
        >
          <span aria-hidden="true">{REACTION_EMOJI[g.content] ?? g.content}</span>
          {g.count}
        </button>
      ))}
      {!disabled && (
        <Menu
          trigger={
            <button type="button" className="wk-react wk-react-add" aria-label={t("reactions.add")}>
              <Icon name="plus" />
            </button>
          }
        >
          {REACTIONS.map((c) => {
            const on = groups.some((g) => g.content === c && g.viewerReacted);
            return (
              <MenuItem key={c} icon={on ? "check" : undefined} onSelect={() => onToggle(c, !on)}>
                <span aria-hidden="true">{REACTION_EMOJI[c]}</span> {t(`reaction.${c}`)}
              </MenuItem>
            );
          })}
        </Menu>
      )}
    </div>
  );
}

/** Comment box (avatar + bubble) used by issue and pull request conversations. */
export function CommentBox({ author, head, children, footer, tone }: { author: string; head: ReactNode; children: ReactNode; footer?: ReactNode; tone?: "author" }) {
  return (
    <div className="wk-cm">
      <Avatar user={author} />
      <div className={tone === "author" ? "wk-cm-box wk-cm-own" : "wk-cm-box"}>
        <div className="wk-cm-head">{head}</div>
        <div className="wk-cm-body">{children}</div>
        {footer}
      </div>
    </div>
  );
}

/** Timeline event row: round icon and one line of text. */
export function EventRow({ icon, children, tone }: { icon: IconName; children: ReactNode; tone?: WorkState }) {
  return (
    <div className="wk-ev">
      <span className={tone ? `wk-ev-ic wk-state-${tone}` : "wk-ev-ic"} aria-hidden="true">
        <Icon name={icon} />
      </span>
      <div className="wk-ev-txt">{children}</div>
    </div>
  );
}

/** Sidebar block (layout A) or strip box (layout B). */
export function SideBlock({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <div className="wk-side-b">
      <div className="wk-side-h">
        <h4>{title}</h4>
        {action}
      </div>
      <div className="wk-side-c">{children}</div>
    </div>
  );
}
