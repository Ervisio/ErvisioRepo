import { useState } from "react";
import { useAudit } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { AdminPane } from "./admin/AdminFrame.tsx";
import { Badge, Button, Card, EmptyState, Input, Table, TableBody, TableHead, Td, Th, Tr } from "./../ui/index.ts";
import { formatDate } from "./account/common.tsx";

function tone(action: string) {
  if (action.endsWith("_failed")) return "err" as const;
  if (action.startsWith("auth.")) return "info" as const;
  return "neutral" as const;
}

/** Admin: the audit log, filtered by action prefix and actor, newest first. */
export function AdminAudit() {
  const t = useT("admin");
  const tc = useT("common");
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const [applied, setApplied] = useState({ action: "", actor: "" });
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.at(-1);
  const audit = useAudit(applied, cursor);

  return (
    <AdminPane title={t("audit.title")} subtitle={t("audit.subtitle")}>
      <Card>
        <form
          className="acc-inline acc-filter"
          onSubmit={(e) => {
            e.preventDefault();
            setCursors([]);
            setApplied({ action: action.trim(), actor: actor.trim() });
          }}
        >
          <Input label={t("audit.filter.action")} mono placeholder="auth.login" value={action} onChange={(e) => setAction(e.target.value)} />
          <Input label={t("audit.filter.actor")} mono value={actor} onChange={(e) => setActor(e.target.value)} />
          <Button type="submit" icon="search">{t("audit.filter.apply")}</Button>
        </form>
      </Card>
      <Card>
        {audit.isPending && <p role="status" className="ui-note acc-pad">{tc("state.loading")}</p>}
        {audit.isError && (
          <div role="alert">
            <EmptyState icon="alert" title={t("audit.error")} description={audit.error.message} action={<Button onClick={() => void audit.refetch()}>{tc("action.retry")}</Button>} />
          </div>
        )}
        {audit.data && audit.data.entries.length === 0 && <p className="ui-note acc-pad">{t("audit.empty")}</p>}
        {audit.data && audit.data.entries.length > 0 && (
          <>
            <Table>
              <TableHead>
                <Tr>
                  <Th>{t("audit.time")}</Th>
                  <Th>{t("audit.action")}</Th>
                  <Th>{t("audit.actor")}</Th>
                  <Th>{t("audit.target")}</Th>
                  <Th>{t("audit.ip")}</Th>
                </Tr>
              </TableHead>
              <TableBody>
                {audit.data.entries.map((e) => (
                  <Tr key={e.id}>
                    <Td>{formatDate(e.at)}</Td>
                    <Td><Badge tone={tone(e.action)}>{e.action}</Badge></Td>
                    <Td className="ui-mono">{e.actor ?? "-"}</Td>
                    <Td className="ui-mono">{e.targetType ? `${e.targetType}:${e.targetId}` : "-"}</Td>
                    <Td className="ui-mono">{e.ip || "-"}</Td>
                  </Tr>
                ))}
              </TableBody>
            </Table>
            <div className="acc-actions acc-pad">
              <Button size="sm" disabled={cursors.length === 0} onClick={() => setCursors((c) => c.slice(0, -1))}>{t("audit.newer")}</Button>
              <Button size="sm" disabled={!audit.data.next} onClick={() => {
                const next = audit.data.next;
                if (next) setCursors((c) => [...c, next]);
              }}>{t("audit.older")}</Button>
            </div>
          </>
        )}
      </Card>
    </AdminPane>
  );
}
