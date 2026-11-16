import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useApp, useInstallApp, useInstallTargets, type App, type AppInstallTarget, type RepositorySelection } from "../../api/apps.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Page } from "../../ui/index.ts";
import { Avatar } from "../../ui/shell/ConsoleShell.tsx";
import { useNotify } from "../account/common.tsx";
import { useProblemText } from "../auth/useProblemText.ts";
import { Gate } from "../code/common.tsx";
import { AppLogo, useOwnerLabel } from "../oauth/common.tsx";
import { PermissionSummary, RepoSelection } from "./common.tsx";

/** Where the settings of an installation live: account or organization settings. */
function InstallationLink({ target, id, children }: { target: AppInstallTarget; id: number; children: string }) {
  return target.kind === "org" ? (
    <Link to="/orgs/$org" params={{ org: target.name }} search={{ tab: "installations", installation: id }}>{children}</Link>
  ) : (
    <Link to="/settings" search={{ section: "installations", installation: id }}>{children}</Link>
  );
}

/** Install flow (/apps/:slug/installations/new): account, repositories, permissions, then install. */
export function InstallApp({ slug }: { slug: string }) {
  const t = useT("apps");
  const app = useApp(slug);
  return (
    <Page section="act" title={app.data ? t("install.title", { app: app.data.name }) : t("install.titleBare")} quiet>
      <div className="ap-install">
        <Gate query={app}>{(a) => <InstallForm app={a} />}</Gate>
      </div>
    </Page>
  );
}

function InstallForm({ app }: { app: App }) {
  const t = useT("apps");
  const notify = useNotify();
  const problem = useProblemText();
  const owner = useOwnerLabel();
  const navigate = useNavigate();
  const targets = useInstallTargets(app.slug);
  const install = useInstallApp(app.slug);
  const [picked, setPicked] = useState<string>();
  const [selection, setSelection] = useState<RepositorySelection>("all");
  const [repos, setRepos] = useState<string[]>([]);
  const [error, setError] = useState("");

  const free = (targets.data ?? []).filter((x) => x.installationId === undefined);
  const account = (targets.data ?? []).find((x) => x.name === picked && x.installationId === undefined) ?? free[0];
  const invalid = !account || (selection === "selected" && repos.length === 0);

  function submit() {
    if (!account) return;
    setError("");
    install.mutate(
      { account: account.name, repositorySelection: selection, ...(selection === "selected" ? { repositories: repos } : {}) },
      {
        onSuccess: (res) => {
          notify.ok(t("install.done"));
          if (res.redirectUrl) {
            window.location.assign(res.redirectUrl);
            return;
          }
          const id = res.installation.id;
          if (res.installation.account.kind === "org") void navigate({ to: "/orgs/$org", params: { org: res.installation.account.name }, search: { tab: "installations", installation: id } });
          else void navigate({ to: "/settings", search: { section: "installations", installation: id } });
        },
        onError: (e) => setError(problem(e)),
      },
    );
  }

  return (
    <div className="ap-card">
      <header className="oa-consent-h">
        <AppLogo name={app.name} url={app.logoUrl} size="xl" />
        <div>
          <h2 className="ap-install-h">{t("install.title", { app: app.name })}</h2>
          <p className="ui-note">{t("by", { owner: owner(app.owner) })}</p>
        </div>
      </header>
      {app.description && <p className="oa-consent-d">{app.description}</p>}

      <h3 className="oa-consent-t">{t("install.account")}</h3>
      <Gate query={targets}>
        {(list) => list.length === 0 ? (
          <p className="ui-note">{t("install.noTargets")}</p>
        ) : (
          <div className="ap-targets" role="radiogroup" aria-label={t("install.account")}>
            {list.map((x) => (
              <label key={x.name} className="ap-target" data-on={account?.name === x.name || undefined} data-off={x.installationId !== undefined || undefined}>
                <input
                  type="radio"
                  className="sr-only"
                  name="install-account"
                  checked={account?.name === x.name}
                  disabled={x.installationId !== undefined}
                  onChange={() => { setPicked(x.name); setRepos([]); }}
                />
                <Avatar user={{ username: x.name }} />
                <span className="acc-grow">
                  <b>{x.name}</b> <span className="ui-note">{t(`account.${x.kind}`)}</span>
                </span>
                {x.installationId !== undefined && (
                  <span className="ui-note">
                    {t("install.already")} <InstallationLink target={x} id={x.installationId}>{t("installed.configure")}</InstallationLink>
                  </span>
                )}
              </label>
            ))}
          </div>
        )}
      </Gate>

      {account && (
        <>
          <h3 className="oa-consent-t">{t("repos.title")}</h3>
          <RepoSelection key={account.name} account={account} selection={selection} repos={repos} onChange={(s, r) => { setSelection(s); setRepos(r); }} />
        </>
      )}

      <h3 className="oa-consent-t">{t("install.permissions")}</h3>
      <PermissionSummary perms={app.permissions} events={app.events} />

      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="acc-actions">
        <Button variant="primary" disabled={install.isPending || invalid} onClick={submit}>{t("install.submit")}</Button>
        <Button onClick={() => window.history.length > 1 ? window.history.back() : void navigate({ to: "/" })}>{t("install.cancel")}</Button>
      </div>
      {app.homepageUrl && (
        <p className="ui-note">
          <a href={app.homepageUrl} target="_blank" rel="noreferrer noopener">{app.homepageUrl}</a>
        </p>
      )}
    </div>
  );
}
