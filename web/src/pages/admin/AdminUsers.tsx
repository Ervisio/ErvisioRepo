import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { ADMIN_PAGE_SIZE, USER_ROLES, useAdminUsers, type AdminUser, type UserRole } from "../../api/admin.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Input, Select, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
import { formatDate } from "../account/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";

export type UsersSearch = { q?: string; role?: UserRole; suspended?: boolean; page?: number };

const ANY = "any";

export const roleTone = (role: UserRole) => (role === "admin" ? ("section" as const) : role === "restricted" ? ("warn" as const) : ("neutral" as const));

export function UserStatus({ user }: { user: AdminUser }) {
  const t = useT("admin");
  return user.suspendedAt ? <Badge tone="err">{t("users.status.suspended")}</Badge> : <Badge tone="ok">{t("users.status.active")}</Badge>;
}

/** Admin: searchable, filterable list of users. Filters live in the URL so a view can be shared. */
export function AdminUsers({ search, onSearch }: { search: UsersSearch; onSearch: (s: UsersSearch) => void }) {
  const t = useT("admin");
  const tc = useT("common");
  const page = search.page ?? 1;
  const users = useAdminUsers({ q: search.q, role: search.role, suspended: search.suspended, page });
  const [q, setQ] = useState(search.q ?? "");
  const status = search.suspended === undefined ? ANY : search.suspended ? "suspended" : "active";
  const total = users.data?.total ?? 0;
  const from = total === 0 ? 0 : (page - 1) * ADMIN_PAGE_SIZE + 1;
  const to = Math.min(page * ADMIN_PAGE_SIZE, total);

  return (
    <AdminPane title={t("users.title")} subtitle={t("users.subtitle")}>
      <Card>
        <form
          className="acc-inline acc-filter"
          role="search"
          onSubmit={(e) => {
            e.preventDefault();
            onSearch({ ...search, q: q.trim() || undefined, page: undefined });
          }}
        >
          <Input label={t("users.search")} type="search" placeholder={t("users.searchPlaceholder")} value={q} onChange={(e) => setQ(e.target.value)} />
          <Select
            label={t("users.filter.role")}
            value={search.role ?? ANY}
            onValueChange={(v) => onSearch({ ...search, role: USER_ROLES.find((r) => r === v), page: undefined })}
            options={[{ value: ANY, label: t("users.filter.any") }, ...USER_ROLES.map((r) => ({ value: r, label: t(`users.role.${r}`) }))]}
          />
          <Select
            label={t("users.filter.status")}
            value={status}
            onValueChange={(v) => onSearch({ ...search, suspended: v === ANY ? undefined : v === "suspended", page: undefined })}
            options={[
              { value: ANY, label: t("users.filter.any") },
              { value: "active", label: t("users.status.active") },
              { value: "suspended", label: t("users.status.suspended") },
            ]}
          />
          <Button type="submit" icon="search">{t("users.filter.apply")}</Button>
        </form>
      </Card>
      <Card>
        {users.isPending && <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>}
        {users.isError && (
          <div role="alert">
            <EmptyState icon="alert" title={t("users.error")} description={users.error.message} action={<Button onClick={() => void users.refetch()}>{tc("action.retry")}</Button>} />
          </div>
        )}
        {users.data && users.data.items.length === 0 && <p className="ui-note acc-pad">{t("users.empty")}</p>}
        {users.data && users.data.items.length > 0 && (
          <>
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("users.col.user")}</Th>
                  <Th>{t("users.col.role")}</Th>
                  <Th>{t("users.col.status")}</Th>
                  <Th>{t("users.col.twoFactor")}</Th>
                  <Th>{t("users.col.lastActive")}</Th>
                  <Th>{t("users.col.created")}</Th>
                </Tr>
              </TableHead>
              <TableBody>
                {users.data.items.map((u) => (
                  <Tr key={u.id}>
                    <Td>
                      <Link to="/admin/users/$login" params={{ login: u.username }} className="adm-user">
                        <b>{u.username}</b>
                        <span className="ui-note">{[u.displayName && u.displayName !== u.username ? u.displayName : "", u.email ?? ""].filter(Boolean).join(" · ")}</span>
                      </Link>
                    </Td>
                    <Td><Badge tone={roleTone(u.role)}>{t(`users.role.${u.role}`)}</Badge></Td>
                    <Td><UserStatus user={u} /></Td>
                    <Td>{u.has2fa ? t("users.twoFactor.on") : t("users.twoFactor.off")}</Td>
                    <Td>{u.lastActivity ? formatDate(u.lastActivity) : t("users.never")}</Td>
                    <Td>{formatDate(u.createdAt)}</Td>
                  </Tr>
                ))}
              </TableBody>
            </Table>
            <div className="acc-inline adm-pager">
              <span className="ui-note">{t("users.range", { from, to, total })}</span>
              <Button size="sm" disabled={page <= 1} onClick={() => onSearch({ ...search, page: page > 2 ? page - 1 : undefined })}>{t("users.prev")}</Button>
              <Button size="sm" disabled={to >= total} onClick={() => onSearch({ ...search, page: page + 1 })}>{t("users.next")}</Button>
            </div>
          </>
        )}
      </Card>
    </AdminPane>
  );
}
