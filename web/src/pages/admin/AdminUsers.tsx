import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { ADMIN_PAGE_SIZE, useAdminUsers, useBulkUpdateAdminUsers, type AdminUser, type UserRole } from "../../api/admin.ts";
import { useT } from "../../i18n/useT.ts";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Badge, BotMark, Button, EmptyState, Icon, Input } from "../../ui/index.ts";
import { useNotify } from "../account/common.tsx";
import { When } from "../code/common.tsx";
import { Avatar } from "../work/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";

export type UsersSearch = { q?: string; role?: UserRole; suspended?: boolean; twoFactor?: boolean; bot?: boolean; page?: number };

export const roleTone = (role: UserRole) => (role === "admin" ? ("section" as const) : role === "restricted" ? ("warn" as const) : ("neutral" as const));

export function UserStatus({ user }: { user: AdminUser }) {
  const t = useT("admin");
  return user.suspendedAt ? <Badge tone="err" icon="alert">{t("users.status.suspended")}</Badge> : <Badge tone="ok" icon="check">{t("users.status.active")}</Badge>;
}

type View = "all" | "admins" | "suspended" | "without2fa" | "bots";
const views: View[] = ["all", "admins", "suspended", "without2fa", "bots"];

/** Filter shortcut of a search; "all" when it is not one of them. */
function viewOf(s: UsersSearch): View {
  if (s.role === "admin") return "admins";
  if (s.suspended) return "suspended";
  if (s.twoFactor === false) return "without2fa";
  if (s.bot) return "bots";
  return "all";
}

function searchOf(v: View, q: string | undefined): UsersSearch {
  const base = { q };
  switch (v) {
    case "admins": return { ...base, role: "admin" };
    case "suspended": return { ...base, suspended: true };
    case "without2fa": return { ...base, twoFactor: false, bot: false };
    case "bots": return { ...base, bot: true };
    default: return base;
  }
}

const roleChip: Record<UserRole, string> = { admin: "ui-chip ui-chip-acc", user: "ui-chip", restricted: "ui-chip ui-chip-warn" };

function UserIdentity({ user: u }: { user: AdminUser }) {
  const t = useT("admin");
  const manage = t("users.manage", { login: u.username });
  const identity = (
    <>
      <AvatarImg src={u.avatarUrl} size={32} radius="50%">
        <Avatar user={u.username} size={32} />
      </AvatarImg>
      <span className="au-n">
        <b>{u.displayName || u.username} <BotMark login={u.username} /></b>
        <small>{[u.username, u.email].filter(Boolean).join(" · ")}</small>
      </span>
    </>
  );
  return (
    <span role="cell" className="au-u">
      {u.suspendedAt ? <span className="au-id">{identity}</span> : (
        <Link to="/$owner" params={{ owner: u.username }} className="au-id">{identity}</Link>
      )}
      <Link to="/admin/users/$login" params={{ login: u.username }} className="ui-iconbtn" aria-label={manage} title={manage}>
        <Icon name="gear" />
      </Link>
    </span>
  );
}

/** Admin: users with filter shortcuts and counts, search, and selection for bulk suspend or reactivate. Filters live in the URL. */
export function AdminUsers({ search, onSearch }: { search: UsersSearch; onSearch: (s: UsersSearch) => void }) {
  const t = useT("admin");
  const tc = useT("common");
  const notify = useNotify();
  const page = search.page ?? 1;
  const users = useAdminUsers({ ...search, page });
  const bulk = useBulkUpdateAdminUsers();
  const [q, setQ] = useState(search.q ?? "");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const view = viewOf(search);
  const total = users.data?.total ?? 0;
  const counts = users.data?.counts;
  const from = total === 0 ? 0 : (page - 1) * ADMIN_PAGE_SIZE + 1;
  const to = Math.min(page * ADMIN_PAGE_SIZE, total);
  const items = users.data?.items ?? [];
  const pickable = items.filter((u) => !u.isBot);
  const allPicked = pickable.length > 0 && pickable.every((u) => picked.has(u.username));

  const toggle = (login: string) =>
    setPicked((p) => {
      const next = new Set(p);
      if (next.has(login)) next.delete(login);
      else next.add(login);
      return next;
    });

  function apply(suspended: boolean) {
    bulk.mutate(
      { logins: [...picked], change: { suspended } },
      {
        onSuccess: (results) => {
          const failed = results.filter((r) => r.status === "rejected").length;
          if (failed > 0) notify.fail(new Error(t("users.bulk.failed", { n: failed })));
          else notify.ok(t(suspended ? "users.bulk.suspended" : "users.bulk.reactivated", { n: results.length }));
          setPicked(new Set());
        },
      },
    );
  }

  return (
    <AdminPane title={t("users.title")} actions={<Link to="/admin/invites" className="ui-btn ui-btn-primary">{t("users.invite")}</Link>}>
      <div className="au-tool">
        <div className="au-pills" role="group" aria-label={t("users.views")}>
          {views.map((v) => (
            <button
              key={v}
              type="button"
              className="au-pill"
              aria-pressed={v === view}
              onClick={() => { setPicked(new Set()); onSearch(searchOf(v, search.q)); }}
            >
              {t(`users.view.${v}`)}
              {counts && <span className="ui-tm">{counts[v]}</span>}
            </button>
          ))}
        </div>
        <form
          className="au-q"
          role="search"
          onSubmit={(e) => {
            e.preventDefault();
            onSearch({ ...search, q: q.trim() || undefined, page: undefined });
          }}
        >
          <Input label={t("users.search")} hideLabel type="search" placeholder={t("users.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} />
        </form>
      </div>
      {picked.size > 0 && (
        <div className="au-bulk" role="region" aria-label={t("users.bulk.label")}>
          <b>{t("users.bulk.selected", { n: picked.size })}</b>
          <Button size="sm" onClick={() => apply(true)} disabled={bulk.isPending}>{t("users.bulk.suspend")}</Button>
          <Button size="sm" onClick={() => apply(false)} disabled={bulk.isPending}>{t("users.bulk.reactivate")}</Button>
          <Button size="sm" variant="ghost" onClick={() => setPicked(new Set())}>{t("users.bulk.clear")}</Button>
        </div>
      )}
      {users.isPending && <p role="status" className="ui-note">{tc("state.loading")}</p>}
      {users.isError && (
        <div role="alert">
          <EmptyState icon="alert" title={t("users.error")} description={users.error.message} action={<Button onClick={() => void users.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      )}
      {users.data && items.length === 0 && <p className="ui-note">{t("users.empty")}</p>}
      {items.length > 0 && (
        <div className="au-t" role="table" aria-label={t("users.title")}>
          <div className="au-r au-h" role="row">
            <span role="columnheader">
              <input
                type="checkbox"
                className="au-cb"
                aria-label={t("users.bulk.all")}
                checked={allPicked}
                onChange={() => setPicked(allPicked ? new Set() : new Set(pickable.map((u) => u.username)))}
              />
            </span>
            <span role="columnheader">{t("users.col.user")}</span>
            <span role="columnheader">{t("users.col.role")}</span>
            <span role="columnheader">{t("users.col.status")}</span>
            <span role="columnheader">{t("users.col.twoFactor")}</span>
            <span role="columnheader">{t("users.col.lastActive")}</span>
          </div>
          {items.map((u) => (
            <div key={u.id} role="row" className={picked.has(u.username) ? "au-r on" : "au-r"}>
              <span role="cell">
                {!u.isBot && (
                  <input type="checkbox" className="au-cb" aria-label={t("users.bulk.pick", { login: u.username })} checked={picked.has(u.username)} onChange={() => toggle(u.username)} />
                )}
              </span>
              <UserIdentity user={u} />
              <span role="cell"><span className={u.isBot ? "ui-chip" : roleChip[u.role]}>{u.isBot ? t("users.role.bot") : t(`users.role.${u.role}`)}</span></span>
              <span role="cell">
                <span className={u.suspendedAt ? "ui-chip ui-chip-err" : "ui-chip ui-chip-ok"}>{t(u.suspendedAt ? "users.status.suspended" : "users.status.active")}</span>
              </span>
              <span role="cell" className={u.isBot ? "ui-tm" : u.has2fa ? "ui-tm ui-st-open" : "ui-tm ui-st-warn"}>
                {u.isBot ? "—" : u.has2fa ? t("users.twoFactor.on") : t("users.twoFactor.off")}
              </span>
              <span role="cell" className="ui-tm">{u.lastActivity ? <When iso={u.lastActivity} /> : t("users.never")}</span>
            </div>
          ))}
        </div>
      )}
      {users.data && total > ADMIN_PAGE_SIZE && (
        <div className="acc-inline adm-pager">
          <span className="ui-note">{t("users.range", { from, to, total })}</span>
          <Button size="sm" disabled={page <= 1} onClick={() => onSearch({ ...search, page: page > 2 ? page - 1 : undefined })}>{t("users.prev")}</Button>
          <Button size="sm" disabled={to >= total} onClick={() => onSearch({ ...search, page: page + 1 })}>{t("users.next")}</Button>
        </div>
      )}
    </AdminPane>
  );
}
