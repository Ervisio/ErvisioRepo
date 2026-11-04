import { useT } from "../i18n/useT.ts";
import { Page } from "../ui/index.ts";
import { Runners } from "./actions/Settings.tsx";

/** Admin: runners available to every repository of the instance. */
export function AdminRunners() {
  const t = useT("actions");
  return (
    <Page section="act" title={t("admin.title")} subtitle={t("admin.subtitle")}>
      <Runners scope={{ kind: "instance" }} />
    </Page>
  );
}
