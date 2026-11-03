import { useMeta } from "../api/useMeta.ts";
import { useT } from "../i18n/useT.ts";

export function Home() {
  const t = useT("home");
  const tc = useT("common");
  const meta = useMeta();

  if (meta.isPending) return <p role="status">{tc("state.loading")}</p>;

  if (meta.isError) {
    return (
      <div role="alert" className="card">
        <p>{t("error", { message: meta.error.message })}</p>
        <button type="button" onClick={() => void meta.refetch()}>
          {tc("action.retry")}
        </button>
      </div>
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
    <section className="card">
      <h2>{t("title")}</h2>
      <dl>
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
