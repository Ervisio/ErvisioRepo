import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { api } from "../api/client.ts";
import { useErvisioCodeDevices, useSession } from "../api/identity.ts";
import { useT } from "../i18n/useT.ts";
import { Icon } from "../ui/Icon.tsx";
import { Dialog } from "../ui/Dialog.tsx";
import { ErvisioCodeDeviceRows } from "./account/ErvisioCodeDevices.tsx";
import { CodeWorkspace } from "./code-workspace/CodeWorkspace.tsx";
import "../styles/code-hub.css";

export function CodeHub() {
  const t = useT("codehub");
  const session = useSession();
  const devices = useErvisioCodeDevices(true);
  if (!session.data) return null;
  return (
    <CodeWorkspace
      key={session.data.id}
      account={session.data}
      devices={devices.isSuccess ? devices.data : []}
      manageDevices={
        <Link to="/settings" search={{ section: "ervisiocode-devices" }} className="ec-rail-row">
          <Icon name="gear" /> {t("devices.manage")}
        </Link>
      }
      authorizeDevice={
        <Link to="/login/device" search={{ from: "code" }} className="ec-rail-row">
          <Icon name="link" /> {t("enterCode")}
        </Link>
      }
    />
  );
}

export function MobileCodeHub({ accountId }: { accountId?: string }) {
  const t = useT("codehub");
  const common = useT("common");
  const account = accountId?.toLowerCase() ?? "";
  const validAccount = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(account);
  const session = useQuery({
    queryKey: ["ervisiocode", "hub-session", account],
    enabled: validAccount,
    queryFn: async ({ signal }) => {
      const { data } = await api.GET("/user/ervisiocode/hub-session", {
        params: { query: { accountId: account } },
        signal,
        cache: "no-store",
        redirect: "error",
      });
      if (data?.accountId !== account) throw new Error("No matching ErvisioCode hub session");
      return data;
    },
    gcTime: 0,
    retry: false,
    refetchInterval: (query) => query.state.status === "error" ? false : 5_000,
  });
  if (!validAccount || session.isError || session.isPending) {
    return (
      <main className="ec-workspace ec-gate">
        <h1 translate="no">{t("title")}</h1>
        {validAccount && session.isPending
          ? <p role="status">{common("state.loading")}</p>
          : <p role="alert">{t("mobile.sessionUnavailable")}</p>}
      </main>
    );
  }
  const { accountId: id, username, displayName, avatarUrl, devices } = session.data;
  return (
    <CodeWorkspace
      key={id}
      account={{ id, username, displayName, avatarUrl }}
      hubAccountId={id}
      devices={devices}
      manageDevices={
        <Dialog
          trigger={<button type="button" className="ec-rail-row"><Icon name="gear" /> {t("devices.title")}</button>}
          title={t("devices.title")}
          description={t("devices.mobileReadonly")}
        >
          {devices.length ? <ErvisioCodeDeviceRows rows={devices} live /> : <p>{t("devices.empty")}</p>}
        </Dialog>
      }
    />
  );
}
