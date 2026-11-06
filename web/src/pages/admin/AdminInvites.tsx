import { useState } from "react";
import { ADMIN_PAGE_SIZE, useAdminInvites, useCreateAdminInvite, useRevokeAdminInvite, type AdminInvite } from "../../api/admin.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, ConfirmDialog, EmptyState, Input, Table, TableBody, TableHead, Td, Th, Tr } from "../../ui/index.ts";
import { formatDate, Secret, useNotify } from "../account/common.tsx";
import { AdminPane } from "./AdminFrame.tsx";

type Created = { token: string; expiresAt: string };

const statusTone = (s: AdminInvite["status"]) => (s === "pending" ? ("info" as const) : s === "used" ? ("ok" as const) : ("neutral" as const));

/** Admin: create single-use sign-up links and revoke the ones still pending. */
export function AdminInvites({ page, onPage }: { page: number; onPage: (p: number) => void }) {
  const t = useT("admin");
  const tc = useT("common");
  const notify = useNotify();
  const list = useAdminInvites(page);
  const create = useCreateAdminInvite();
  const revoke = useRevokeAdminInvite();
  const [email, setEmail] = useState("");
  const [created, setCreated] = useState<Created | undefined>();
  const [revoking, setRevoking] = useState<string | undefined>();
  const total = list.data?.total ?? 0;
  const from = total === 0 ? 0 : (page - 1) * ADMIN_PAGE_SIZE + 1;
  const to = Math.min(page * ADMIN_PAGE_SIZE, total);

  return (
    <AdminPane title={t("invites.title")} subtitle={t("invites.subtitle")}>
      <Card>
        <form
          className="acc-form acc-pad"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate(email.trim() ? { email: email.trim() } : {}, {
              onSuccess: (inv) => { setCreated({ token: inv.token, expiresAt: inv.expiresAt }); setEmail(""); },
              onError: notify.fail,
            });
          }}
        >
          <h2>{t("invites.create.title")}</h2>
          <p className="ui-note">{t("invites.create.hint")}</p>
          <Input label={t("invites.create.email")} type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
          <div className="acc-actions">
            <Button type="submit" variant="primary" icon="plus" disabled={create.isPending}>{t("invites.create.button")}</Button>
          </div>
        </form>
        {created && (
          <div className="acc-pad" role="status">
            <h3>{t("invites.created.title")}</h3>
            <Secret label={t("invites.created.link")} value={`${window.location.origin}/register?invite=${encodeURIComponent(created.token)}`} />
            <p className="ui-note">{t("invites.created.expires", { date: formatDate(created.expiresAt) })}</p>
            <Button size="sm" onClick={() => setCreated(undefined)}>{t("invites.created.dismiss")}</Button>
          </div>
        )}
      </Card>
      <Card>
        <h2 className="acc-pad">{t("invites.list")}</h2>
        {list.isPending && <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>}
        {list.isError && (
          <div role="alert">
            <EmptyState icon="alert" title={t("invites.error")} description={list.error.message} action={<Button onClick={() => void list.refetch()}>{tc("action.retry")}</Button>} />
          </div>
        )}
        {list.data && list.data.items.length === 0 && <p className="ui-note acc-pad">{t("invites.empty")}</p>}
        {list.data && list.data.items.length > 0 && (
          <>
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("invites.col.email")}</Th>
                  <Th>{t("invites.col.status")}</Th>
                  <Th>{t("invites.col.createdBy")}</Th>
                  <Th>{t("invites.col.created")}</Th>
                  <Th>{t("invites.col.expires")}</Th>
                  <Th><span className="sr-only">{t("invites.revoke")}</span></Th>
                </Tr>
              </TableHead>
              <TableBody>
                {list.data.items.map((inv) => (
                  <Tr key={inv.id}>
                    <Td>{inv.email ?? t("invites.anyone")}</Td>
                    <Td>
                      <Badge tone={statusTone(inv.status)}>{t(`invites.status.${inv.status}`)}</Badge>
                      {inv.usedBy && <span className="ui-note"> {t("invites.usedBy", { user: inv.usedBy })}</span>}
                    </Td>
                    <Td>{inv.createdBy ?? ""}</Td>
                    <Td>{formatDate(inv.createdAt)}</Td>
                    <Td>{formatDate(inv.expiresAt)}</Td>
                    <Td>
                      {inv.status === "pending" && (
                        <ConfirmDialog
                          open={revoking === inv.id}
                          onOpenChange={(o) => setRevoking(o ? inv.id : undefined)}
                          trigger={<Button size="sm" variant="danger" icon="trash">{t("invites.revoke")}</Button>}
                          title={t("invites.revoke.title")}
                          description={t("invites.revoke.hint")}
                          confirmLabel={t("invites.revoke")}
                          pending={revoke.isPending}
                          onConfirm={() =>
                            revoke.mutate(inv.id, {
                              onSuccess: () => { setRevoking(undefined); notify.ok(t("invites.revoke.done")); },
                              onError: (e) => { setRevoking(undefined); notify.fail(e); },
                            })
                          }
                        />
                      )}
                    </Td>
                  </Tr>
                ))}
              </TableBody>
            </Table>
            <div className="acc-inline adm-pager">
              <span className="ui-note">{t("users.range", { from, to, total })}</span>
              <Button size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>{t("users.prev")}</Button>
              <Button size="sm" disabled={to >= total} onClick={() => onPage(page + 1)}>{t("users.next")}</Button>
            </div>
          </>
        )}
      </Card>
    </AdminPane>
  );
}
