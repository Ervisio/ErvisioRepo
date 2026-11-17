// GitHub-style hovercards: hovering or focusing any link to a user (/login), an organization (/orgs/name)
// or a repository root (/owner/name) shows a small card about it. One delegated listener covers every link
// of the app, so pages only need to render plain links.
import { Link } from "@tanstack/react-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useOrg, useUserProfile } from "../api/identity.ts";
import { useRepo } from "../api/repos.ts";
import { useT } from "../i18n/useT.ts";
import { AvatarImg } from "./AvatarImg.tsx";
import { Icon } from "./Icon.tsx";

export type HoverTarget = { kind: "user"; login: string } | { kind: "org"; name: string } | { kind: "repo"; owner: string; name: string };

/** First path segments that are app pages, never a user or an owner. */
const RESERVED = new Set([
  "", "_ui", "admin", "api", "apps", "explore", "forgot-password", "login", "notifications", "org-invite", "orgs", "register",
  "repos", "reset-password", "runner", "settings", "verify-email", "assets", "raw",
]);
const SEGMENT = /^[A-Za-z0-9][A-Za-z0-9._-]*(\[bot\])?$/;
const OPEN_DELAY = 400;
const CLOSE_DELAY = 220;

/** What a link points to, or null when it is not a user, organization or repository root. */
export function hoverTargetOf(a: HTMLAnchorElement): HoverTarget | null {
  if (!a.href) return null;
  let url: URL;
  try {
    url = new URL(a.href, window.location.href);
  } catch {
    return null;
  }
  if (url.origin !== window.location.origin) return null;
  const seg = url.pathname.split("/").filter(Boolean).map((s) => {
    try {
      return decodeURIComponent(s);
    } catch {
      return s;
    }
  });
  if (seg.length === 2 && seg[0] === "orgs" && SEGMENT.test(seg[1]!)) return { kind: "org", name: seg[1]! };
  if (seg.length === 0 || RESERVED.has(seg[0]!) || !SEGMENT.test(seg[0]!)) return null;
  if (seg.length === 1) return { kind: "user", login: seg[0]! };
  if (seg.length === 2 && SEGMENT.test(seg[1]!)) return { kind: "repo", owner: seg[0]!, name: seg[1]! };
  return null;
}

const sameTarget = (a: HoverTarget, b: HoverTarget) => JSON.stringify(a) === JSON.stringify(b);

type Shown = { target: HoverTarget; anchor: HTMLElement };

/** Mount once: listens to pointer and focus on links and shows the card. */
export function HoverCardProvider({ children }: { children: ReactNode }) {
  const [shown, setShown] = useState<Shown | null>(null);
  const openTimer = useRef(0);
  const closeTimer = useRef(0);
  const current = useRef<Shown | null>(null);
  useEffect(() => {
    current.current = shown;
  }, [shown]);

  const cancel = () => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
  };
  const scheduleClose = useCallback(() => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setShown(null), CLOSE_DELAY);
  }, []);

  useEffect(() => {
    const linkOf = (t: EventTarget | null): HTMLAnchorElement | null => {
      if (!(t instanceof Element)) return null;
      const a = t.closest("a");
      if (!a || a.closest(".hc, .tb-top, [data-hovercard='off']")) return null;
      return a;
    };
    const enter = (a: HTMLAnchorElement, delay: number) => {
      const target = hoverTargetOf(a);
      if (!target) return;
      const here = hoverTargetOf(Object.assign(document.createElement("a"), { href: window.location.pathname }));
      if (here && sameTarget(here, target)) return;
      window.clearTimeout(closeTimer.current);
      const cur = current.current;
      if (cur && cur.anchor === a) return;
      window.clearTimeout(openTimer.current);
      openTimer.current = window.setTimeout(() => setShown({ target, anchor: a }), delay);
    };
    const onOver = (e: MouseEvent) => {
      const a = linkOf(e.target);
      if (a) enter(a, OPEN_DELAY);
    };
    const onOut = (e: MouseEvent) => {
      const a = linkOf(e.target);
      if (!a || (e.relatedTarget instanceof Node && a.contains(e.relatedTarget))) return;
      if (e.relatedTarget instanceof Element && e.relatedTarget.closest(".hc")) return;
      scheduleClose();
    };
    const onFocus = (e: FocusEvent) => {
      const a = linkOf(e.target);
      if (a && a.matches(":focus-visible")) enter(a, OPEN_DELAY);
    };
    const onBlur = (e: FocusEvent) => {
      if (linkOf(e.target) && !(e.relatedTarget instanceof Element && e.relatedTarget.closest(".hc"))) scheduleClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && current.current) {
        cancel();
        setShown(null);
      }
    };
    const onDown = (e: PointerEvent) => {
      if (!(e.target instanceof Element && e.target.closest(".hc"))) {
        cancel();
        setShown(null);
      }
    };
    document.addEventListener("mouseover", onOver);
    document.addEventListener("mouseout", onOut);
    document.addEventListener("focusin", onFocus);
    document.addEventListener("focusout", onBlur);
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onDown);
    window.addEventListener("scroll", onDown as unknown as EventListener, true);
    return () => {
      cancel();
      document.removeEventListener("mouseover", onOver);
      document.removeEventListener("mouseout", onOut);
      document.removeEventListener("focusin", onFocus);
      document.removeEventListener("focusout", onBlur);
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onDown);
      window.removeEventListener("scroll", onDown as unknown as EventListener, true);
    };
  }, [scheduleClose]);

  return (
    <>
      {children}
      {shown && (
        <Card
          shown={shown}
          onEnter={() => window.clearTimeout(closeTimer.current)}
          onLeave={scheduleClose}
          onClose={() => setShown(null)}
        />
      )}
    </>
  );
}

function Card({ shown, onEnter, onLeave, onClose }: { shown: Shown; onEnter: () => void; onLeave: () => void; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number; above: boolean } | null>(null);
  const [ready, setReady] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = shown.anchor.getBoundingClientRect();
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    const above = r.bottom + 8 + h > window.innerHeight && r.top - 8 - h > 0;
    const left = Math.max(8, Math.min(r.left, window.innerWidth - w - 8));
    setPos({ top: above ? r.top - h - 8 : r.bottom + 8, left, above });
  }, [shown, ready]);
  const t = shown.target;
  return createPortal(
    <div
      ref={ref}
      className={pos?.above ? "hc above" : "hc"}
      role="dialog"
      aria-modal="false"
      style={{ top: pos?.top ?? -9999, left: pos?.left ?? -9999 }}
      onMouseEnter={onEnter}
      onMouseLeave={onLeave}
      onClick={(e) => (e.target as Element).closest("a") && onClose()}
    >
      {t.kind === "user" ? (
        <UserCard login={t.login} onReady={() => setReady(true)} onFail={onClose} />
      ) : t.kind === "org" ? (
        <OrgCard name={t.name} onReady={() => setReady(true)} onFail={onClose} />
      ) : (
        <RepoCard owner={t.owner} name={t.name} onReady={() => setReady(true)} onFail={onClose} />
      )}
    </div>,
    document.body,
  );
}

type CardProps = { onReady: () => void; onFail: () => void };

function useSettled(q: { isSuccess: boolean; isError: boolean }, { onReady, onFail }: CardProps) {
  useEffect(() => {
    if (q.isSuccess) onReady();
    else if (q.isError) onFail();
  }, [q.isSuccess, q.isError, onReady, onFail]);
}

function Loading() {
  const t = useT("ui");
  return <p className="hc-load" role="status">{t("hovercard.loading")}</p>;
}

const initial = (s: string) => s.slice(0, 1).toUpperCase();

function UserCard({ login, ...p }: { login: string } & CardProps) {
  const t = useT("ui");
  const q = useUserProfile(login);
  useSettled(q, p);
  const u = q.data;
  if (!u) return <Loading />;
  return (
    <div className="hc-in">
      <div className="hc-top">
        <Link to="/$owner" params={{ owner: u.username }} className="hc-av round" aria-hidden="true" tabIndex={-1}>
          <AvatarImg src={u.avatarUrl} fill>{initial(u.username)}</AvatarImg>
        </Link>
      </div>
      <p className="hc-name">
        <Link to="/$owner" params={{ owner: u.username }}>{u.displayName || u.username}</Link>
        {u.displayName && <span className="hc-login">{u.username}</span>}
      </p>
      {(u.bio || u.description) && <p className="hc-desc">{u.bio || u.description}</p>}
      {(u.company || u.location) && (
        <ul className="hc-meta">
          {u.company && <li><Icon name="building" />{u.company}</li>}
          {u.location && <li><Icon name="place" />{u.location}</li>}
        </ul>
      )}
      {u.orgs && u.orgs.length > 0 && (
        <div className="hc-orgs">
          <span>{t("hovercard.memberOf")}</span>
          {u.orgs.slice(0, 8).map((o) => (
            <Link key={o.name} to="/orgs/$org" params={{ org: o.name }} className="hc-org" title={o.displayName || o.name} aria-label={o.displayName || o.name}>
              <AvatarImg src={o.avatarUrl} fill>{initial(o.displayName || o.name)}</AvatarImg>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

function OrgCard({ name, ...p }: { name: string } & CardProps) {
  const t = useT("ui");
  const q = useOrg(name);
  useSettled(q, p);
  const o = q.data;
  if (!o) return <Loading />;
  return (
    <div className="hc-in">
      <div className="hc-top">
        <Link to="/orgs/$org" params={{ org: o.name }} className="hc-av sq" aria-hidden="true" tabIndex={-1}>
          <AvatarImg src={o.avatarUrl} fill>{initial(o.displayName || o.name)}</AvatarImg>
        </Link>
      </div>
      <p className="hc-name">
        <Link to="/orgs/$org" params={{ org: o.name }}>{o.displayName || o.name}</Link>
        {o.displayName && o.displayName !== o.name && <span className="hc-login">{o.name}</span>}
      </p>
      {o.description && <p className="hc-desc">{o.description}</p>}
      <ul className="hc-meta">
        <li><Icon name="building" />{t("hovercard.org")}</li>
        {o.verified && <li><Icon name="shield" />{t("hovercard.verified")}</li>}
      </ul>
    </div>
  );
}

function RepoCard({ owner, name, ...p }: { owner: string; name: string } & CardProps) {
  const t = useT("ui");
  const q = useRepo(owner, name);
  useSettled(q, p);
  const r = q.data;
  if (!r) return <Loading />;
  const updated = new Date(r.updatedAt).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
  return (
    <div className="hc-in">
      <p className="hc-repo">
        <span className="hc-rimg">
          <AvatarImg src={r.avatarUrl || r.ownerAvatarUrl} fill>
            <Icon name={r.visibility === "public" ? "book" : "lock"} />
          </AvatarImg>
        </span>
        <Link to="/$owner/$name" params={{ owner: r.owner, name: r.name }}>
          {r.owner}/<b>{r.name}</b>
        </Link>
        {r.visibility !== "public" && <span className="ui-chip">{t(`hovercard.vis.${r.visibility}`)}</span>}
      </p>
      {r.description && <p className="hc-desc">{r.description}</p>}
      <ul className="hc-meta">
        <li><Icon name="star" />{r.starsCount}</li>
        <li><Icon name="clock" />{t("hovercard.updated", { date: updated })}</li>
      </ul>
    </div>
  );
}
