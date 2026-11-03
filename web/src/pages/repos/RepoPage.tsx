import { Link } from "@tanstack/react-router";
import { useMeta } from "../../api/useMeta.ts";
import { useRepo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Badge, Button, Card, EmptyState, Page } from "../../ui/index.ts";
import { Secret } from "../account/common.tsx";

/** Clone URLs derived from the public URL; the SSH form is provisional until the server reports its SSH endpoint. */
export function cloneUrls(publicUrl: string, owner: string, name: string) {
  let host: string;
  try {
    host = new URL(publicUrl).hostname;
  } catch {
    host = "";
  }
  return {
    http: `${publicUrl.replace(/\/+$/, "")}/${owner}/${name}.git`,
    ssh: host ? `git@${host}:${owner}/${name}.git` : "",
  };
}

export function RepoPage({ owner, name }: { owner: string; name: string }) {
  const t = useT("repos");
  const tc = useT("common");
  const repo = useRepo(owner, name);
  const meta = useMeta();
  const title = `${owner}/${name}`;

  if (repo.isPending) {
    return (
      <Page title={title}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }
  if (repo.isError) {
    return (
      <Page title={title}>
        <div role="alert">
          <EmptyState icon="alert" title={t("error")} description={repo.error.message} action={<Button onClick={() => void repo.refetch()}>{tc("action.retry")}</Button>} />
        </div>
      </Page>
    );
  }
  const r = repo.data;
  const urls = cloneUrls(meta.data?.publicUrl ?? window.location.origin, r.owner, r.name);
  const canAdmin = r.permission === "admin";
  return (
    <Page
      section="code"
      title={r.fullName}
      subtitle={r.description || undefined}
      actions={
        <>
          <Badge tone={r.visibility === "public" ? "ok" : "neutral"}>{t(`visibility.${r.visibility}`)}</Badge>
          {canAdmin && (
            <Link to="/$owner/$name/settings" params={{ owner: r.owner, name: r.name }}>
              <Button icon="gear">{t("settings.link")}</Button>
            </Link>
          )}
        </>
      }
    >
      <Card title={t("empty.title")}>
        <p className="ui-note acc-pad">{t("empty.intro")}</p>
        <Secret label={t("empty.http")} value={urls.http} />
        {urls.ssh && <Secret label={t("empty.ssh")} value={urls.ssh} />}
        <Secret
          label={t("empty.init")}
          multiline
          value={[
            `git init -b ${r.defaultBranch}`,
            "git add .",
            'git commit -m "Initial commit"',
            `git remote add origin ${urls.http}`,
            `git push -u origin ${r.defaultBranch}`,
          ].join("\n")}
        />
        <p className="ui-note acc-pad">{t("empty.auth")}</p>
      </Card>
    </Page>
  );
}
