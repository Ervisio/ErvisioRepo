import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useEditFile, useTree } from "../../api/code.ts";
import { useSession, useUserProfile } from "../../api/identity.ts";
import { ApiError } from "../../api/http.ts";
import { useCreateRepo, useOwnerRepos, useRepo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { BotMark, Button, EmptyState, Icon, Page } from "../../ui/index.ts";
import { formatDate, useNotify } from "../account/common.tsx";
import { Readme } from "../code/Readme.tsx";
import { RepoRows } from "./RepoList.tsx";

/** Public profile of a user, GitHub style: avatar and name on the left, repositories on the right. Reachable signed out. */
export function ProfilePage({ username }: { username: string }) {
  const t = useT("repos");
  const tc = useT("common");
  const profile = useUserProfile(username);
  const repos = useOwnerRepos(username, "user");
  const me = useSession().data?.username;
  const [broken, setBroken] = useState("");

  if (profile.isPending) {
    return (
      <Page section="code" title={username}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (profile.isError) {
    const missing = profile.error instanceof ApiError && profile.error.status === 404;
    return (
      <Page section="code" title={username}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={missing ? t("profile.notFound") : profile.error.message}
            action={missing ? <Link to="/orgs/$org" params={{ org: username }} className="ui-btn ui-btn-secondary">{t("profile.tryOrg")}</Link> : <Button onClick={() => void profile.refetch()}>{tc("action.retry")}</Button>}
          />
        </div>
      </Page>
    );
  }
  const u = profile.data;
  const bot = u.kind === "bot";
  return (
    <Page section="code" title={u.displayName || u.username} quiet>
      <div className="up">
        <aside className="up-card">
          {u.avatarUrl && broken !== u.avatarUrl ? (
            <img className="up-av" src={u.avatarUrl} alt="" onError={() => setBroken(u.avatarUrl)} />
          ) : (
            <span className="up-av" aria-hidden="true">{u.username.slice(0, 1).toUpperCase()}</span>
          )}
          <p className="up-name">{u.displayName || u.username}</p>
          <p className="up-login">{u.username} <BotMark login={u.username} /></p>
          {bot && u.description && <p className="up-meta">{u.description}</p>}
          {me === u.username && <Link to="/settings" className="ui-btn ui-btn-secondary">{t("profile.edit")}</Link>}
          <p className="up-meta">{t("profile.joined", { date: formatDate(u.createdAt) })}</p>
        </aside>
        {bot ? (
          <div>
            <EmptyState icon="info" title={t("profile.botTitle")} description={t("profile.botHint")} />
          </div>
        ) : (
          <div>
            <ProfileReadme username={u.username} displayName={u.displayName || u.username} own={me === u.username} />
            <h2 className="up-h2">{t("list.title")}</h2>
            <RepoRows query={repos} />
          </div>
        )}
      </div>
    </Page>
  );
}

const PROFILE_README = "README.md";

/**
 * Profile README (GitHub special repository): the README of the public repository
 * username/username, above the repositories. The owner gets an edit pencil, or a call to
 * create it when it is missing; other visitors see nothing in that case.
 */
function ProfileReadme({ username, displayName, own }: { username: string; displayName: string; own: boolean }) {
  const t = useT("repos");
  const repo = useRepo(username, username);
  const r = repo.data?.visibility === "public" ? repo.data : undefined;
  const tree = useTree(username, username, r?.defaultBranch ?? "", "", r !== undefined);
  if (repo.isPending || (r && tree.isPending && tree.fetchStatus !== "idle")) return null;
  const missing = repo.isError && repo.error instanceof ApiError && repo.error.status === 404;
  const readme = tree.data?.readme;
  if (r && readme) {
    return (
      <div className="up-readme">
        <Readme
          owner={username}
          name={username}
          refName={r.defaultBranch}
          path={readme}
          label={`${username} / ${readme}`}
          action={
            own && (
              <Link
                to="/$owner/$name/edit"
                params={{ owner: username, name: username }}
                search={{ ref: r.defaultBranch, path: readme }}
                className="ui-btn ui-btn-ghost ui-btn-sm rp-ib"
                aria-label={t("profile.readmeEdit")}
                title={t("profile.readmeEdit")}
              >
                <Icon name="edit" />
              </Link>
            )
          }
        />
      </div>
    );
  }
  // Only the owner is invited, and only when the special repository is missing or public without a README.
  if (!own || !(missing || (r && !readme))) return null;
  return <ReadmeInvite username={username} displayName={displayName} exists={!missing} branch={r?.defaultBranch} />;
}

function ReadmeInvite({ username, displayName, exists, branch }: { username: string; displayName: string; exists: boolean; branch?: string }) {
  const t = useT("repos");
  const notify = useNotify();
  const navigate = useNavigate();
  const create = useCreateRepo();
  const edit = useEditFile(username, username);
  const busy = create.isPending || edit.isPending;
  async function start() {
    try {
      const ref = exists ? branch : (await create.mutateAsync({ name: username, visibility: "public" })).defaultBranch;
      await edit.mutateAsync({ op: "create", path: PROFILE_README, content: t("profile.readmeSeed", { name: displayName }), encoding: "utf-8", branch: ref });
      await navigate({ to: "/$owner/$name/edit", params: { owner: username, name: username }, search: { ref, path: PROFILE_README } });
    } catch (e) {
      notify.fail(e);
    }
  }
  return (
    <div className="up-invite">
      <Icon name="book" />
      <div>
        <b>{t("profile.readmeTitle")}</b>
        <p>{t("profile.readmeHint", { repo: `${username}/${username}` })}</p>
        <Button variant="primary" size="sm" disabled={busy} onClick={() => void start()}>
          {t("profile.readmeCreate")}
        </Button>
      </div>
    </div>
  );
}
