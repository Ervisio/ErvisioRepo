import { useT } from "../i18n/useT.ts";
import { AdminPane } from "./admin/AdminFrame.tsx";
import { Runners } from "./actions/Settings.tsx";

/** Admin: runners available to every repository of the instance. */
export function AdminRunners() {
  const t = useT("actions");
  return (
    <AdminPane title={t("admin.title")} subtitle={t("admin.subtitle")}>
      <Runners scope={{ kind: "instance" }} />
    </AdminPane>
  );
}
