import { useMeta } from "../../api/useMeta.ts";
import type { Repo } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, Card, Dialog } from "../../ui/index.ts";
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

function useCloneUrls(r: Repo) {
  const meta = useMeta();
  return cloneUrls(meta.data?.publicUrl ?? window.location.origin, r.owner, r.name);
}

/** Shown in place of the tree while the repository has no commits. */
export function EmptyRepoCard({ repo: r }: { repo: Repo }) {
  const t = useT("repos");
  const urls = useCloneUrls(r);
  return (
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
  );
}

/** "Clone" button with the HTTP and SSH URLs. */
export function CloneDialog({ repo: r }: { repo: Repo }) {
  const t = useT("repos");
  const urls = useCloneUrls(r);
  return (
    <Dialog trigger={<Button icon="copy">{t("clone.button")}</Button>} title={t("clone.title")} description={t("empty.auth")}>
      <Secret label={t("empty.http")} value={urls.http} />
      {urls.ssh && <Secret label={t("empty.ssh")} value={urls.ssh} />}
    </Dialog>
  );
}
