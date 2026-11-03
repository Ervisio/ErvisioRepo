import { useMeta } from "../api/useMeta.ts";
import { useT } from "../i18n/useT.ts";
import { Button, Card, EmptyState, Page } from "../ui/index.ts";

export function Home() {
  const t = useT("home");
  const tc = useT("common");
  const meta = useMeta();

  if (meta.isPending) {
    return (
      <Page title={t("title")}>
        <p role="status">{tc("state.loading")}</p>
      </Page>
    );
  }

  if (meta.isError) {
    return (
      <Page title={t("title")}>
        <div role="alert">
          <EmptyState
            icon="alert"
            title={t("error.title")}
            description={t("error", { message: meta.error.message })}
            action={<Button onClick={() => void meta.refetch()}>{tc("action.retry")}</Button>}
          />
        </div>
      </Page>
    );
  }

  const m = meta.data;
  const rows: [string, string][] = [
    [t("status"), t("status.online")],
    [t("name"), m.name],
    [t("version"), m.version],
    ...(m.commit ? [[t("commit"), m.commit] as [string, string]] : []),
    [t("instanceId"), m.instanceId],
    [t("publicUrl"), m.publicUrl],
    [t("registration"), t(`registration.${m.registration}`)],
  ];

  return (
    <Page title={t("title")}>
      <Card>
        <dl className="ui-kv">
          {rows.map(([label, value]) => (
            <div key={label} className="ui-kv-row">
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      </Card>
    </Page>
  );
}
