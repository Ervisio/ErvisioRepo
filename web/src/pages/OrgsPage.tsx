import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useMyOrgs, useOrgMembers, useOrgTeams, type Org } from "../api/identity.ts";
import { useOwnerRepos } from "../api/repos.ts";
import { useT } from "../i18n/useT.ts";
import { Button, Page } from "../ui/index.ts";
import { AvatarImg } from "../ui/AvatarImg.tsx";
import { Avatar } from "../ui/shell/ConsoleShell.tsx";
import { Async } from "./account/common.tsx";
import { CreateOrgDialog } from "./account/CreateOrgDialog.tsx";

/** How many member faces a card shows before the "+n" counter. */
const faces = 3;

function OrgCard({ org }: { org: Org }) {
  const t = useT("account");
  const members = useOrgMembers(org.name);
  const repos = useOwnerRepos(org.name, "org");
  const teams = useOrgTeams(org.name);
  const people = members.data ?? [];
  const label = org.displayName || org.name;
  return (
    <li>
      <Link to="/orgs/$org" params={{ org: org.name }} className="og-card">
        <span className="og-top">
          <AvatarImg src={org.avatarUrl} className="og-av">{label.slice(0, 1).toUpperCase()}</AvatarImg>
          <span className="og-id">
            <b>{label}</b>
            <span className="ui-mono ui-tm">@{org.name}</span>
          </span>
          {org.role && <span className={org.role === "owner" ? "ui-chip og-own" : "ui-chip"}>{t(`orgs.role.${org.role}`)}</span>}
        </span>
        <span className={org.description ? "og-desc" : "og-desc og-none"}>{org.description || t("orgs.noDescription")}</span>
        <span className="og-foot">
          {repos.data && <span>{t("orgs.repos", { count: repos.data.length })}</span>}
          {teams.data && <span>{t("orgs.teams", { count: teams.data.length })}</span>}
          {people.length > 0 && (
            <span className="og-faces" aria-label={t("orgs.members", { count: people.length })}>
              {people.slice(0, faces).map((m) => <Avatar key={m.username} user={{ username: m.username, avatarUrl: m.avatarUrl ?? "" }} />)}
              {people.length > faces && <span className="og-more">+{people.length - faces}</span>}
            </span>
          )}
        </span>
      </Link>
    </li>
  );
}

/** The organizations the signed-in user belongs to. */
export function OrgsPage() {
  const t = useT("account");
  const orgs = useMyOrgs();
  const [open, setOpen] = useState(false);
  return (
    <Page section="pr" title={t("orgs.title")} actions={<Button variant="primary" icon="plus" onClick={() => setOpen(true)}>{t("orgs.create")}</Button>}>
      <Async query={orgs} empty={t("orgs.empty")}>
        {(rows) => (
          <ul className="og-grid">
            {rows.map((o) => <OrgCard key={o.name} org={o} />)}
            <li className="og-new">
              <span>
                <b>{t("orgs.newTitle")}</b>
                <span className="og-desc">{t("orgs.newHint")}</span>
              </span>
              <Button icon="plus" onClick={() => setOpen(true)}>{t("orgs.create")}</Button>
            </li>
          </ul>
        )}
      </Async>
      <CreateOrgDialog open={open} onOpenChange={setOpen} />
    </Page>
  );
}
