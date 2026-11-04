import { useNavigate, useParams } from "@tanstack/react-router";
import { createContext, useContext, useEffect, useId, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { useRepoSearch } from "../../api/code.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Dialog } from "../Dialog.tsx";
import { Icon } from "../Icon.tsx";

export type SearchScope = { owner: string; name: string };
const OpenContext = createContext<((scope?: SearchScope) => void) | null>(null);

const typing = (el: EventTarget | null) =>
  el instanceof HTMLElement && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName));

/** Global search dialog, opened from the shell, with Ctrl/Cmd+K or with "/" outside text fields. */
export function SearchPaletteProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState<{ scope?: SearchScope } | null>(null);
  const scope = useScopeFromRoute();
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      const mod = (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k";
      const slash = e.key === "/" && !e.ctrlKey && !e.metaKey && !e.altKey && !typing(e.target);
      if (mod || slash) {
        e.preventDefault();
        setOpen({ scope });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [scope]);
  return (
    <OpenContext.Provider value={(s) => setOpen({ scope: s })}>
      {children}
      {open && <Palette scope={open.scope} onClose={() => setOpen(null)} />}
    </OpenContext.Provider>
  );
}

function useScopeFromRoute(): SearchScope | undefined {
  const p = useParams({ strict: false }) as { owner?: string; name?: string };
  const owner = p.owner;
  const name = p.name;
  return useMemo(() => (owner && name ? { owner, name } : undefined), [owner, name]);
}

/** Button that opens the palette; `iconOnly` for compact bars. */
export function SearchTrigger({ className, iconOnly }: { className?: string; iconOnly?: boolean }) {
  const t = useT("shell");
  const open = useContext(OpenContext);
  const scope = useScopeFromRoute();
  if (!open) return null;
  return (
    <button type="button" className={className} onClick={() => open(scope)} aria-label={t("search.open")} title={t("search.shortcut")} aria-keyshortcuts="/ Control+K Meta+K">
      <Icon name="search" />
      {!iconOnly && <span>{t("search.button")}</span>}
    </button>
  );
}

/** Field-shaped trigger for the Console top bar: placeholder, repository scope chip and the "/" hint. */
export function SearchField({ className }: { className?: string }) {
  const t = useT("shell");
  const open = useContext(OpenContext);
  const scope = useScopeFromRoute();
  if (!open) return null;
  return (
    <button type="button" className={className} onClick={() => open(scope)} aria-label={scope ? t("search.openIn", { repo: `${scope.owner}/${scope.name}` }) : t("search.open")} aria-keyshortcuts="/ Control+K Meta+K">
      <Icon name="search" />
      {scope && <span className="sb-scope sec-code">{scope.name}</span>}
      <span className="sb-q">{scope ? t("search.fieldRepo") : t("search.field")}</span>
      <kbd className="sb-kbd">/</kbd>
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

type Option = { kind: "code" } | { kind: "repo"; repo: Repo };

function Palette({ scope, onClose }: { scope?: SearchScope; onClose: () => void }) {
  const t = useT("shell");
  const navigate = useNavigate();
  const listId = useId();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const query = useDebounced(q.trim(), 200);
  const res = useRepoSearch(query);
  const repos = query ? (res.data ?? []) : [];
  const items: Option[] = [...(scope && query ? [{ kind: "code" } as const] : []), ...repos.map((repo) => ({ kind: "repo", repo }) as const)];
  const go = (i: number) => {
    const o = items[i];
    if (!o) return;
    onClose();
    if (o.kind === "code" && scope) void navigate({ to: "/$owner/$name/search", params: scope, search: { q: query } });
    else if (o.kind === "repo") void navigate({ to: "/$owner/$name", params: { owner: o.repo.owner, name: o.repo.name } });
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
  const status = !query ? t("search.hint") : res.isError ? t("search.error") : res.isPending ? t("search.loading") : repos.length === 0 ? t("search.none") : "";
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
          {items.map((o, i) => (
            <li
              key={o.kind === "code" ? "code" : o.repo.fullName}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              className="pl-item"
              onMouseEnter={() => setActive(i)}
              onClick={() => go(i)}
            >
              {o.kind === "code" && scope ? (
                <>
                  <Icon name="code" />
                  <b>{t("search.inRepo", { query, repo: `${scope.owner}/${scope.name}` })}</b>
                </>
              ) : o.kind === "repo" ? (
                <>
                  <Icon name={o.repo.visibility === "public" ? "book" : "lock"} />
                  <b>{o.repo.fullName}</b>
                  {o.repo.description && <span className="pl-desc">{o.repo.description}</span>}
                </>
              ) : null}
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
