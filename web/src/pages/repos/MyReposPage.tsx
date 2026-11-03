import { useMyRepos } from "../../api/repos.ts";
import { useT } from "../../i18n/useT.ts";
import { Page } from "../../ui/index.ts";
import { MyRepos } from "./RepoList.tsx";

export function MyReposPage() {
  const t = useT("repos");
  const repos = useMyRepos();
  return (
    <Page section="code" title={t("page.title")}>
      <MyRepos repos={repos} />
    </Page>
  );
}
