import { useEffect, useId, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { REACTION_EMOJI, REACTIONS, uploadAttachment, type Label, type ReactionGroup } from "../../api/issues.ts";
import { useT } from "../../i18n/useT.ts";
import { Markdown } from "../../lib/markdown.tsx";
import { Button, Icon, Menu, MenuItem, type ButtonProps, type IconName } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { useInAppLinks, useMarkdownContext } from "../code/Readme.tsx";

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

/** Label pill: the label colour at 18% behind text mixed from it, with a 40% ring (DESIGN 2026-11-17). */
export function LabelChip({ label }: { label: Pick<Label, "name" | "color" | "description"> }) {
  const c = parseHex(label.color) ? `#${label.color.replace(/^#/, "")}` : "#8b949e";
  return (
    <span className="wk-label" style={{ "--c": c } as CSSProperties} title={label.description || undefined}>
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
const stateIconName = (kind: "issue" | "pull", state: WorkState): IconName => (kind === "pull" ? "pr" : state === "closed" ? "check" : "issue");

export function StateIcon({ kind, state, label }: { kind: "issue" | "pull"; state: WorkState; label: string }) {
  return (
    <span className={`wk-state wk-state-${state}`} role="img" aria-label={label} title={label}>
      <Icon name={stateIconName(kind, state)} />
    </span>
  );
}

/** Filled state pill (kit .ui-state); `kind` adds the state icon (detail headers). */
export function StatePill({ state, label, kind }: { state: WorkState; label: string; kind?: "issue" | "pull" }) {
  return (
    <span className={state === "open" ? "ui-state" : `ui-state ui-state-${state}`}>
      {kind && <Icon name={stateIconName(kind, state)} />}
      {label}
    </span>
  );
}

/* List pages (DESIGN 2026-11-17): saved views on the left, title row, search and filter chips, one-line rows. */

/** Frame of the issue and pull request lists. `views` holds `.wk-vw-a` links (aria-current on the active one). */
export function ListFrame({ views, viewsLabel, title, count, actions, toolbar, children }: {
  views: ReactNode;
  viewsLabel: string;
  title: string;
  count?: string;
  actions?: ReactNode;
  toolbar: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="wk-lg">
      <nav className="wk-vw" aria-label={viewsLabel}>
        {views}
      </nav>
      <div className="wk-lmain">
        <div className="wk-lh">
          <h2>{title}</h2>
          {count && <span className="ui-tm">{count}</span>}
          {actions && <span className="wk-lh-r">{actions}</span>}
        </div>
        <div className="wk-fb">{toolbar}</div>
        {children}
      </div>
    </div>
  );
}

/** Search field of a list toolbar; submits on Enter. */
export function ListSearch({ label, placeholder, initial, onSearch }: { label: string; placeholder: string; initial: string; onSearch: (q: string) => void }) {
  const [q, setQ] = useState(initial);
  return (
    <form
      className="wk-search"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        onSearch(q.trim());
      }}
    >
      <Icon name="search" />
      <input className="wk-search-in" type="search" aria-label={label} placeholder={placeholder} value={q} onChange={(e) => setQ(e.target.value)} />
    </form>
  );
}

/** Filter chip of a list toolbar: opens a menu of choices; `value` names the active one. */
export function FilterChip({ label, value, children }: { label: string; value?: string; children: ReactNode }) {
  return (
    <Menu
      label={label}
      trigger={
        <button type="button" className="wk-ch" data-on={value ? "" : undefined}>
          {label}
          {value && <b>{value}</b>}
          <Icon name="down" />
        </button>
      }
    >
      {children}
    </Menu>
  );
}

/** Rows of a list, optionally under group headings (milestones). */
export function RowGroups<T>({ groups, label, getKey, row }: {
  groups: { key: string; title?: ReactNode; items: T[] }[];
  label: string;
  getKey: (x: T) => string;
  row: (x: T) => ReactNode;
}) {
  return (
    <div className="wk-rows" aria-label={label} role="group">
      {groups.map((g) => (
        <section key={g.key} className="wk-rgroup">
          {g.title && (
            <h3 className="wk-grp">
              {g.title}
              <span className="ui-tm">{g.items.length}</span>
            </h3>
          )}
          <ul className="wk-list">
            {g.items.map((x) => (
              <li key={getKey(x)}>{row(x)}</li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

/** One list row: state icon, #number, title link (stretched over the row), tags, comments, people. */
export function WorkRow({ icon, number, link, tags, side, people }: { icon: ReactNode; number: number; link: ReactNode; tags?: ReactNode; side?: ReactNode; people?: ReactNode }) {
  return (
    <div className="wk-ir">
      {icon}
      <span className="wk-ir-n">#{number}</span>
      {link}
      <span className="wk-ir-l">{tags}</span>
      <span className="wk-ir-c">{side}</span>
      <span className="wk-ir-c">{people}</span>
    </div>
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

/** Markdown formatting buttons of the composer: one tab stop, arrow keys move between them. */
function FormatBar({ onWrap }: { onWrap: (before: string, after: string) => void }) {
  const t = useT("work");
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const items: { key: string; before: string; after: string; glyph: ReactNode }[] = [
    { key: "bold", before: "**", after: "**", glyph: <b>B</b> },
    { key: "italic", before: "_", after: "_", glyph: <i>I</i> },
    { key: "code", before: "`", after: "`", glyph: <span className="ui-mono">&lt;&gt;</span> },
    { key: "link", before: "[", after: "](url)", glyph: <Icon name="link" /> },
  ];
  function onKey(e: KeyboardEvent) {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    const to = e.key === "Home" ? 0 : e.key === "End" ? items.length - 1 : step ? (active + step + items.length) % items.length : -1;
    if (to < 0) return;
    e.preventDefault();
    setActive(to);
    ref.current?.querySelectorAll<HTMLButtonElement>("button")[to]?.focus();
  }
  return (
    <div className="wk-tbar" role="toolbar" aria-label={t("composer.format")} ref={ref} onKeyDown={onKey}>
      {items.map((x, i) => (
        <button
          key={x.key}
          type="button"
          tabIndex={i === active ? 0 : -1}
          aria-label={t(`composer.fmt.${x.key}`)}
          title={t(`composer.fmt.${x.key}`)}
          onFocus={() => setActive(i)}
          onClick={() => onWrap(x.before, x.after)}
        >
          {x.glyph}
        </button>
      ))}
    </div>
  );
}

/**
 * Markdown editor in a soft surface: text on --bg, then write/preview, formatting, attachments and the
 * caller's buttons. `before` goes inside the text area box (new issue title), `extra` under it (chips).
 */
export function Composer({ owner, name, refName, value, onChange, label, placeholder, rows = 4, autoFocus, before, extra, className, children }: {
  owner: string;
  name: string;
  refName: string;
  value: string;
  onChange: (v: string) => void;
  label: string;
  placeholder?: string;
  rows?: number;
  autoFocus?: boolean;
  before?: ReactNode;
  extra?: ReactNode;
  className?: string;
  children?: ReactNode;
}) {
  const t = useT("work");
  const notify = useNotify();
  const id = useId();
  const [tab, setTab] = useState<"write" | "preview">("write");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const areaRef = useRef<HTMLTextAreaElement>(null);

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

  function wrap(open: string, close: string) {
    const el = areaRef.current;
    if (!el) return;
    const { selectionStart: from, selectionEnd: to } = el;
    const picked = value.slice(from, to);
    onChange(value.slice(0, from) + open + picked + close + value.slice(to));
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(from + open.length, from + open.length + picked.length);
    });
  }

  return (
    <div className={className ? `wk-cps ${className}` : "wk-cps"}>
      <div className="wk-cps-in">
        {before}
        {tab === "write" ? (
          <>
            <label htmlFor={id} className="wk-sr">
              {label}
            </label>
            <textarea
              id={id}
              ref={areaRef}
              className="wk-ta"
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
          </>
        ) : (
          <div className="wk-preview">
            <Body owner={owner} name={name} refName={refName} source={value} />
          </div>
        )}
      </div>
      {extra}
      <div className="wk-cps-ft">
        <div className="ui-seg wk-cps-seg" role="group" aria-label={t("composer.mode")}>
          {(["write", "preview"] as const).map((x) => (
            <button key={x} type="button" className="ui-segi" aria-pressed={tab === x} onClick={() => setTab(x)}>
              {t(`composer.${x}`)}
            </button>
          ))}
        </div>
        {tab === "write" && <FormatBar onWrap={wrap} />}
        <input ref={fileRef} type="file" multiple hidden onChange={(e) => void attach(e.target.files)} />
        <IconTrigger icon="upload" label={busy ? t("composer.uploading") : t("composer.attach")} disabled={busy} onClick={() => fileRef.current?.click()} />
        {children && <div className="wk-cps-act">{children}</div>}
      </div>
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

/** Post of a conversation: avatar, header line (`head`), body and optional footer, no card (DESIGN 2026-11-17). */
export function CommentBox({ author, head, children, footer, compact }: { author: string; head: ReactNode; children: ReactNode; footer?: ReactNode; compact?: boolean }) {
  return (
    <article className={compact ? "wk-post wk-post-sm" : "wk-post"}>
      <Avatar user={author} size={compact ? 24 : 32} />
      <div className="wk-post-m">
        <div className="wk-post-h">{head}</div>
        <div className="wk-post-b">{children}</div>
        {footer}
      </div>
    </article>
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

/** Square icon button used as a menu trigger: the pencil of a metadata group, or the ⋯ of a card. */
export function IconTrigger({ icon, label, ...rest }: { icon: IconName; label: string } & Omit<ButtonProps, "icon" | "variant" | "size" | "aria-label">) {
  return <Button {...rest} variant="ghost" size="sm" icon={icon} className="wk-ib" aria-label={label} title={label} />;
}

/** Key/value row of the Properties panel: key, value, one edit affordance. */
export function SideBlock({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="wk-prow">
      <h3 className="wk-prow-k">{title}</h3>
      <div className="wk-prow-v">{children}</div>
      {action && <span className="wk-prow-a">{action}</span>}
    </section>
  );
}

/** Status card at the top of the Properties panel (pull request readiness and merge). */
export function StatusBox({ children }: { children: ReactNode }) {
  const t = useT("work");
  return (
    <section className="wk-stc" aria-label={t("side.status")}>
      {children}
    </section>
  );
}

const WIDE = "(min-width: 901px)";

/** The metadata groups: always open next to the timeline, one collapsible "Details" row on narrow screens. */
export function SideDetails({ summary, children }: { summary: string; children: ReactNode }) {
  const t = useT("work");
  const [open, setOpen] = useState(() => window.matchMedia(WIDE).matches);
  useEffect(() => {
    const mq = window.matchMedia(WIDE);
    const sync = () => setOpen(mq.matches);
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);
  return (
    <details className="wk-det" open={open}>
      <summary>
        <span className="wk-det-t">{t("side.details")}</span>
        <span className="wk-det-s ui-note">{summary}</span>
        <Icon name="down" />
      </summary>
      <div className="wk-gs">{children}</div>
    </details>
  );
}
