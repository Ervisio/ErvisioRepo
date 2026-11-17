import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useMyOrgs } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Button, Page } from "../ui/index.ts";
import { Async } from "./account/common.tsx";
import { CreateOrgDialog } from "./account/CreateOrgDialog.tsx";

/** The organizations the signed-in user belongs to. */
export function OrgsPage() {
  const t = useT("account");
  const orgs = useMyOrgs();
  const [open, setOpen] = useState(false);
  return (
    <Page section="pr" title={t("orgs.title")} actions={<Button variant="primary" icon="plus" onClick={() => setOpen(true)}>{t("orgs.create")}</Button>}>
      <div className="ui-box">
        <Async query={orgs} empty={t("orgs.empty")}>
          {(rows) => (
            <ul className="ui-in px-orgs">
              {rows.map((o) => (
                <li key={o.name}>
                  <Link to="/orgs/$org" params={{ org: o.name }} className="ui-li">
                    <span className="px-org px-sm" aria-hidden="true">{(o.displayName || o.name).slice(0, 1).toUpperCase()}</span>
                    <span className="px-n">
                      <b>{o.displayName || o.name}</b>
                      {o.description && <span className="px-d">{o.description}</span>}
                    </span>
                    <span className="ui-mono ui-tm">{o.name}</span>
                    {o.role && <span className="ui-chip">{t(`orgs.role.${o.role}`)}</span>}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Async>
      </div>
      <CreateOrgDialog open={open} onOpenChange={setOpen} />
    </Page>
  );
}
