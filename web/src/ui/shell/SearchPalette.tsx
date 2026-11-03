import { useNavigate } from "@tanstack/react-router";
import { createContext, useContext, useEffect, useId, useState, type KeyboardEvent, type ReactNode } from "react";
import { useRepoSearch } from "../../api/code.ts";
import { useT } from "../../i18n/useT.ts";
import { Dialog } from "../Dialog.tsx";
import { Icon } from "../Icon.tsx";

const OpenContext = createContext<(() => void) | null>(null);

/** Global repository search dialog, opened from the shell or with Ctrl/Cmd+K. */
export function SearchPaletteProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return (
    <OpenContext.Provider value={() => setOpen(true)}>
      {children}
      {open && <Palette onClose={() => setOpen(false)} />}
    </OpenContext.Provider>
  );
}

/** Button that opens the palette; `iconOnly` for the rail. */
export function SearchTrigger({ className, iconOnly }: { className?: string; iconOnly?: boolean }) {
  const t = useT("shell");
  const open = useContext(OpenContext);
  if (!open) return null;
  return (
    <button type="button" className={className} onClick={open} aria-label={t("search.open")} title={t("search.shortcut")} aria-keyshortcuts="Control+K Meta+K">
      <Icon name="search" />
      {!iconOnly && <span>{t("search.button")}</span>}
    </button>
  );
}

function useDebounced(v: string, ms: number) {
  const [d, setD] = useState(v);
  useEffect(() => {
    const h = window.setTimeout(() => setD(v), ms);
    return () => window.clearTimeout(h);
  }, [v, ms]);
  return d;
}

function Palette({ onClose }: { onClose: () => void }) {
  const t = useT("shell");
  const navigate = useNavigate();
  const listId = useId();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const query = useDebounced(q.trim(), 200);
  const res = useRepoSearch(query);
  const items = query ? (res.data ?? []) : [];
  const go = (i: number) => {
    const r = items[i];
    if (!r) return;
    onClose();
    void navigate({ to: "/$owner/$name", params: { owner: r.owner, name: r.name } });
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => (items.length ? (a + 1) % items.length : 0));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => (items.length ? (a - 1 + items.length) % items.length : 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(Math.min(active, items.length - 1));
    }
  };
  const status = !query ? t("search.hint") : res.isError ? t("search.error") : res.isPending ? t("search.loading") : items.length === 0 ? t("search.none") : "";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={t("search.title")}>
      <div className="pl">
        <input
          className="ui-input"
          type="search"
          role="combobox"
          aria-label={t("search.title")}
          aria-expanded={items.length > 0}
          aria-controls={listId}
          aria-activedescendant={items.length ? `${listId}-${Math.min(active, items.length - 1)}` : undefined}
          placeholder={t("search.placeholder")}
          autoFocus
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKey}
        />
        <ul id={listId} role="listbox" className="pl-list" aria-label={t("search.results")}>
          {items.map((r, i) => (
            <li
              key={r.fullName}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              className="pl-item"
              onMouseEnter={() => setActive(i)}
              onClick={() => go(i)}
            >
              <Icon name={r.visibility === "public" ? "book" : "lock"} />
              <b>{r.fullName}</b>
              {r.description && <span className="pl-desc">{r.description}</span>}
            </li>
          ))}
        </ul>
        {status && (
          <p className="ui-note" role="status">
            {status}
          </p>
        )}
      </div>
    </Dialog>
  );
}
