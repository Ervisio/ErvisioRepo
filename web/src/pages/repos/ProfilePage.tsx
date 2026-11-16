import { Link } from "@tanstack/react-router";
import { useSession, useUserProfile } from "../../api/identity.ts";
import { ApiError } from "../../api/http.ts";
import { useOwnerRepos } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, EmptyState, Page } from "../../ui/index.ts";
import { formatDate } from "../account/common.tsx";
import { RepoRows } from "./RepoList.tsx";

/** Public profile of a user, GitHub style: avatar and name on the left, repositories on the right. Reachable signed out. */
export function ProfilePage({ username }: { username: string }) {
  const t = useT("repos");
  const tc = useT("common");
  const profile = useUserProfile(username);
  const repos = useOwnerRepos(username, "user");
  const me = useSession().data?.username;

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
  return (
    <Page section="code" title={u.displayName || u.username} quiet>
      <div className="up">
        <aside className="up-card">
          {u.avatarUrl ? <img className="up-av" src={u.avatarUrl} alt="" /> : <span className="up-av" aria-hidden="true">{u.username.slice(0, 1).toUpperCase()}</span>}
          <p className="up-name">{u.displayName || u.username}</p>
          <p className="up-login">{u.username}</p>
          {me === u.username && <Link to="/settings" className="ui-btn ui-btn-secondary">{t("profile.edit")}</Link>}
          <p className="up-meta">{t("profile.joined", { date: formatDate(u.createdAt) })}</p>
        </aside>
        <div>
          <h2 className="up-h2">{t("list.title")}</h2>
          <RepoRows query={repos} />
        </div>
      </div>
    </Page>
  );
}
