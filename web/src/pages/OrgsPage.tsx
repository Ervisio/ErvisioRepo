import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useMyOrgs } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Badge, Button, Card, Page } from "../ui/index.ts";
import { Async } from "./account/common.tsx";
import { CreateOrgDialog } from "./account/CreateOrgDialog.tsx";

/** The organizations the signed-in user belongs to. */
export function OrgsPage() {
  const t = useT("account");
  const orgs = useMyOrgs();
  const [open, setOpen] = useState(false);
  return (
    <Page section="pr" title={t("orgs.title")}>
      <Card title={t("orgs.title")} actions={<Button variant="primary" icon="plus" onClick={() => setOpen(true)}>{t("orgs.create")}</Button>}>
        <Async query={orgs} empty={t("orgs.empty")}>
          {(rows) =>
            rows.map((o) => (
              <Link key={o.name} to="/orgs/$org" params={{ org: o.name }} className="ui-row">
                <span className="acc-grow">{o.displayName || o.name}</span>
                <span className="ui-mono">{o.name}</span>
                {o.role && <Badge>{t(`orgs.role.${o.role}`)}</Badge>}
              </Link>
            ))
          }
        </Async>
      </Card>
      <CreateOrgDialog open={open} onOpenChange={setOpen} />
    </Page>
  );
}
