import { Link } from "@tanstack/react-router";
import { useT } from "../i18n/useT.ts";
import { Icon, Page } from "../ui/index.ts";
import { ErvisioCodeDeviceList } from "./account/ErvisioCodeDevices.tsx";
import "../styles/code-hub.css";

export function CodeHub() {
  const t = useT("codehub");
  return (
    <Page
      title={t("title")}
      subtitle={t("subtitle")}
      actions={
        <Link to="/login/device" search={{ from: "code" }} className="ui-btn ui-btn-primary code-hub-link">
          <Icon name="link" />
          {t("enterCode")}
        </Link>
      }
    >
      <div className="code-hub">
        <section className="ui-box" aria-labelledby="code-hub-devices">
          <header className="code-hub-header">
            <h2 id="code-hub-devices">{t("devices.title")}</h2>
            <p>{t("devices.description")}</p>
          </header>
          <div className="ui-in">
            <ErvisioCodeDeviceList live empty={t("devices.empty")} />
          </div>
        </section>
        <aside className="code-hub-guide" aria-labelledby="code-hub-setup">
          <h2 id="code-hub-setup"><Icon name="terminal" />{t("setup.title")}</h2>
          <ol>
            <li>
              <code>/account</code>
              <p>{t("setup.account")}</p>
            </li>
            <li>
              <code>/rc</code>
              <p>{t("setup.remote")}</p>
            </li>
          </ol>
          <p className="code-hub-limits">{t("setup.limits")}</p>
          <div className="code-hub-legacy">
            <h3>{t("legacy.title")}</h3>
            <p>{t("legacy.description")}</p>
          </div>
        </aside>
      </div>
    </Page>
  );
}
