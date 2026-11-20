import { Close, Content, Overlay, Portal, Root, Title, Trigger } from "@radix-ui/react-dialog";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useCodeShares, type CodeShare } from "../../api/codeSharing.ts";
import { useRemoteSessions, type RemoteRecent, type RemoteSession } from "../../api/codeWorkspace.ts";
import { ApiError } from "../../api/http.ts";
import type { components } from "../../api/schema";
import { useT } from "../../i18n/useT.ts";
import { AvatarImg } from "../../ui/AvatarImg.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { When } from "../code/common.tsx";
import { CodeConversation, type CodeComposer } from "./CodeConversation.tsx";
import { CodeProposals } from "./CodeProposals.tsx";
import { CodeShareCreate } from "./CodeShareCreate.tsx";
import { CodeShareList } from "./CodeShareList.tsx";
import { NewCodeSession, type CreatedSession } from "./NewCodeSession.tsx";
import { SharedCodeConversation } from "./SharedCodeConversation.tsx";

type Account = { id: string; username: string; displayName?: string; avatarUrl?: string };
type Device = components["schemas"]["ErvisioCodeDevice"];
type Recent = RemoteRecent & { key: string; live?: RemoteSession };
type ListState = "loading" | "ready" | "error";
type SharingView = { kind: "list" } | { kind: "create"; session: RemoteSession } | { kind: "proposals"; shareId: string };

function sessionKey(session: Pick<RemoteSession, "deviceId" | "sessionId">) {
  return `${session.deviceId}:${session.sessionId}`;
}

export function CodeWorkspace({ account, hubAccountId, devices, manageDevices, authorizeDevice }: {
  account: Account;
  hubAccountId?: string;
  devices: readonly Device[];
  manageDevices: ReactNode;
  authorizeDevice?: ReactNode;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const remote = useRemoteSessions(account.id, hubAccountId);
  const scope = { accountId: account.id, hubAccountId };
  const shares = useCodeShares(scope);
  const [selectedKey, setSelectedKey] = useState("");
  const [sharedId, setSharedId] = useState("");
  const [sharingView, setSharingView] = useState<SharingView>();
  const [collapsed, setCollapsed] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const [newSession, setNewSession] = useState(false);
  const [opening, setOpening] = useState<{ key: string; projectLabel: string; expired: boolean }>();
  const desktopToggle = useRef<HTMLButtonElement>(null);
  const desktopCollapse = useRef<HTMLButtonElement>(null);
  const mobileToggle = useRef<HTMLButtonElement>(null);
  const mainTitle = useRef<HTMLHeadingElement>(null);
  const newTrigger = useRef<HTMLButtonElement>(null);
  const sharingTrigger = useRef<HTMLButtonElement>(null);
  const composer = useRef<CodeComposer>(null);
  const focusDraft = useRef(false);
  const entries = remote.data ? recentSessions(remote.data.sessions, remote.data.recent, remote.isSuccess) : [];
  const selected = entries.find((entry) => entry.key === selectedKey);
  const currentShares = shares.isSuccess ? shares.data.pages.flatMap((page) => page.shares) : [];
  const shared = currentShares.find((share) => share.id === sharedId && share.ownerId !== account.id && !share.revokedAt
    && (share.orgId || (share.recipientId === account.id && share.acceptedAt)));
  const proposalShare = sharingView?.kind === "proposals"
    ? currentShares.find((share) => share.id === sharingView.shareId && !share.revokedAt && (share.orgId || share.acceptedAt))
    : undefined;
  const pc = devices.find((device) => device.id === selected?.live?.deviceId)?.name;
  const authExpired = remote.error instanceof ApiError && remote.error.status === 401;
  const listState: ListState = remote.isError ? "error" : remote.isPending ? "loading" : "ready";
  const selectedTitle = shared ? shareTitle(shared) : selected && (selected.title || t("session.untitled"));
  const awaitingPublish = opening?.key === selectedKey && !selected?.live;

  useEffect(() => {
    if (!opening || opening.expired) return;
    const timer = window.setTimeout(() => setOpening({ ...opening, expired: true }), 15_000);
    return () => window.clearTimeout(timer);
  }, [opening]);

  useEffect(() => {
    if (!drawer) return;
    const mobile = window.matchMedia("(max-width: 700px)");
    const closeOnDesktop = () => { if (!mobile.matches) setDrawer(false); };
    mobile.addEventListener("change", closeOnDesktop);
    return () => mobile.removeEventListener("change", closeOnDesktop);
  }, [drawer]);

  function select(entry: Recent) {
    setSelectedKey(entry.key);
    setSharedId("");
    setOpening(undefined);
    setDrawer(false);
  }

  function openNew(event: MouseEvent<HTMLButtonElement>) {
    newTrigger.current = event.currentTarget;
    setDrawer(false);
    setNewSession(true);
  }

  function openCreated(session: CreatedSession) {
    const key = sessionKey(session);
    setSelectedKey(key);
    setSharedId("");
    setOpening({ key, projectLabel: session.projectLabel, expired: false });
    setNewSession(false);
    void remote.refetch();
  }

  function restoreFocus() {
    const trigger = newTrigger.current;
    (trigger?.isConnected && trigger.offsetParent ? trigger : mobileToggle.current)?.focus();
  }

  function openSharing(view: SharingView, trigger: HTMLButtonElement) {
    sharingTrigger.current = trigger;
    setDrawer(false);
    setSharingView(view);
  }

  function restoreSharingFocus() {
    if (focusDraft.current) {
      focusDraft.current = false;
      composer.current?.focus();
      return;
    }
    const trigger = sharingTrigger.current;
    (trigger?.isConnected && trigger.offsetParent ? trigger : mobileToggle.current?.offsetParent ? mobileToggle.current : mainTitle.current)?.focus();
  }

  function shareTitle(share: CodeShare) {
    const ownTitle = entries.find((entry) => entry.key === sessionKey(share))?.title;
    return ownTitle || t("share.sessionRef", { id: share.id.slice(0, 8) });
  }

  function readShare(share: CodeShare) {
    setSelectedKey("");
    setOpening(undefined);
    setSharedId(share.id);
    sharingTrigger.current = null;
    setSharingView(undefined);
  }

  function toggleSidebar() {
    setCollapsed(!collapsed);
    requestAnimationFrame(() => (collapsed ? desktopCollapse : desktopToggle).current?.focus());
  }

  const rail = { account, devices, entries: authExpired ? [] : entries, selectedKey, listState, onSelect: select, onNew: authExpired ? undefined : openNew,
    onSharing: authExpired ? undefined : (event: MouseEvent<HTMLButtonElement>) => openSharing({ kind: "list" }, event.currentTarget), manageDevices, authorizeDevice };
  const closeSharing = () => setSharingView(undefined);
  const reviewShare = (share: CodeShare) => setSharingView({ kind: "proposals", shareId: share.id });
  const live = selected?.live;
  const useProposalDraft = proposalShare && proposalShare.ownerId === account.id && selected?.live
    && sessionKey(proposalShare) === sessionKey(selected.live) ? (body: string) => {
      if (!composer.current?.adopt(proposalShare, body)) return false;
      focusDraft.current = true;
      closeSharing();
      return true;
    } : undefined;
  return (
    <div className="ec-workspace" data-sidebar={collapsed ? "closed" : "open"}>
      <aside className="ec-desktop-rail ec-rail" hidden={collapsed} aria-label={t("sidebar.label")}>
        <div className="ec-rail-head">
          <Brand home={!hubAccountId} />
          <button ref={desktopCollapse} type="button" className="ec-icon-button" onClick={toggleSidebar} aria-label={t("sidebar.close")}><Icon name="sidebar" /></button>
        </div>
        <SessionRail {...rail} />
      </aside>
      <main className="ec-main">
        <header className="ec-main-head">
          <Root open={drawer} onOpenChange={setDrawer}>
            <Trigger ref={mobileToggle} className="ec-icon-button ec-mobile-toggle" aria-label={t("sidebar.open")}><Icon name="sidebar" /></Trigger>
            <Portal>
              <Overlay className="ec-drawer-overlay" />
              <Content className="ec-drawer ec-rail" aria-describedby={undefined} onCloseAutoFocus={(event) => {
                if (newSession || sharingView) event.preventDefault();
              }}>
                <div className="ec-rail-head">
                  <Title className="ec-sr">{t("sidebar.label")}</Title>
                  <Brand home={!hubAccountId} />
                  <Close className="ec-icon-button" aria-label={t("sidebar.close")}><Icon name="x" /></Close>
                </div>
                <SessionRail {...rail} />
              </Content>
            </Portal>
          </Root>
          <button ref={desktopToggle} type="button" onClick={toggleSidebar} className="ec-icon-button ec-desktop-toggle" aria-label={t("sidebar.open")}><Icon name="sidebar" /></button>
          <h1 ref={mainTitle} tabIndex={-1} title={authExpired ? undefined : selectedTitle}>{(!authExpired && selectedTitle) || t("title")}</h1>
          {!authExpired && live && <button type="button" className="ec-icon-button ec-share-open" aria-label={t("share.create")}
            title={t("share.create")} onClick={(event) => openSharing({ kind: "create", session: live }, event.currentTarget)}><Icon name="users" /></button>}
        </header>
        {!authExpired && shared ? <SharedCodeConversation key={shared.id} scope={scope} share={shared}
          onProposals={(button) => openSharing({ kind: "proposals", shareId: shared.id }, button)} />
          : !authExpired && sharedId ? <div className="ec-transcript">
            <div className="ec-notice">
              <p role="status">{shares.isPending ? t("share.loading") : shares.isError ? t("share.listError") : t("share.unavailable")}</p>
              {shares.isError && <button type="button" className="ec-retry" onClick={() => void shares.refetch()}>{common("action.retry")}</button>}
            </div>
          </div> : <CodeConversation
          key={selectedKey}
          composer={composer}
          accountId={account.id}
          hubAccountId={hubAccountId}
          liveSession={authExpired ? undefined : selected?.live}
          pc={pc}
          project={opening?.key === selectedKey ? opening.projectLabel : undefined}
          view={authExpired ? "expired" : listState === "error" ? "error" : listState === "loading" ? "loading"
            : selected?.live ? "live" : awaitingPublish && !opening.expired ? "opening"
              : selected ? "offline" : awaitingPublish ? "notPublished"
                : remote.data?.sessions.length ? "select" : "empty"}
          retryList={() => void remote.refetch()}
        />}
      </main>
      {newSession && !authExpired && <NewCodeSession
        accountId={account.id}
        hubAccountId={hubAccountId}
        devices={devices}
        onClose={() => setNewSession(false)}
        onCreated={openCreated}
        onRefreshSessions={() => void remote.refetch()}
        restoreFocus={restoreFocus}
      />}
      {!authExpired && (sharingView?.kind === "list" || sharingView?.kind === "proposals" && !proposalShare) &&
        <CodeShareList scope={scope} query={shares} titleOf={shareTitle} onRead={readShare} onReview={reviewShare}
        onRestoreFocus={restoreSharingFocus} onClose={closeSharing} />}
      {!authExpired && sharingView?.kind === "create" && <CodeShareCreate scope={scope} session={sharingView.session}
        isLive={Boolean(live && sessionKey(live) === sessionKey(sharingView.session) && live.relayId === sharingView.session.relayId)}
        refresh={shares.refetch} onRestoreFocus={restoreSharingFocus} onClose={closeSharing} />}
      {!authExpired && proposalShare && <CodeProposals key={proposalShare.id} scope={scope} share={proposalShare}
        onRestoreFocus={restoreSharingFocus} onClose={closeSharing} onUseDraft={useProposalDraft} />}
    </div>
  );
}

function recentSessions(live: readonly RemoteSession[], offline: readonly RemoteRecent[], isCurrent: boolean): Recent[] {
  const entries = new Map<string, Recent>();
  for (const session of offline) {
    const key = sessionKey(session);
    entries.set(key, { ...session, key });
  }
  for (const session of live) {
    const { deviceId, sessionId, title, lastActivityAt } = session;
    const key = sessionKey(session);
    entries.set(key, { key, deviceId, sessionId, title, lastActivityAt, live: isCurrent ? session : undefined });
  }
  return Array.from(entries.values()).sort((a, b) => Date.parse(b.lastActivityAt) - Date.parse(a.lastActivityAt));
}

function Brand({ home }: { home: boolean }) {
  const t = useT("codehub");
  const content = <><span className="ec-brand-mark" aria-hidden="true">E</span><span>{t("sidebar.brand")}</span></>;
  return home
    ? <Link className="ec-brand" to="/" aria-label={t("sidebar.home")}>{content}</Link>
    : <div className="ec-brand">{content}</div>;
}

function SessionRail({ account, devices, entries, selectedKey, listState, onSelect, onNew, onSharing, manageDevices, authorizeDevice }: {
  account: Account;
  devices: readonly Device[];
  entries: Recent[];
  selectedKey: string;
  listState: ListState;
  onSelect: (entry: Recent) => void;
  onNew?: (event: MouseEvent<HTMLButtonElement>) => void;
  onSharing?: (event: MouseEvent<HTMLButtonElement>) => void;
  manageDevices: ReactNode;
  authorizeDevice?: ReactNode;
}) {
  const t = useT("codehub");
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const needle = query.trim().toLocaleLowerCase();
  const visible = needle ? entries.filter(({ title }) => title.toLocaleLowerCase().includes(needle)) : entries;

  function toggleSearch() {
    setSearching(!searching);
    if (searching) setQuery("");
    requestAnimationFrame(() => (searching ? toggle : input).current?.focus());
  }

  return (
    <>
      <div className="ec-rail-actions">
        {onNew && <button type="button" className="ec-rail-row" aria-label={t("new.title")} onClick={onNew}>
          <Icon name="plus" /> {t("new.nav")}
        </button>}
        {onSharing && <button type="button" className="ec-rail-row" onClick={onSharing}>
          <Icon name="users" /> {t("share.manage")}
        </button>}
        <button ref={toggle} type="button" className="ec-rail-row" aria-label={t("sidebar.search")} aria-expanded={searching} onClick={toggleSearch}>
          <Icon name="search" /> {t("sidebar.search")}
        </button>
        {searching && <input
          ref={input} className="ec-search" type="search" name="sessions" aria-label={t("sidebar.search")}
          placeholder={t("sidebar.searchPlaceholder")} value={query} onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Escape") return;
            event.preventDefault();
            event.stopPropagation();
            toggleSearch();
          }}
        />}
      </div>
      <nav className="ec-recent" aria-label={t("sidebar.recent")}>
        <h2>{t("sidebar.recent")}</h2>
        <ul>
          {visible.map((entry) => (
            <RecentRow key={entry.key} entry={entry} selected={selectedKey === entry.key} listState={listState}
              host={devices.find((device) => device.id === entry.live?.deviceId)?.name} onSelect={onSelect} />
          ))}
        </ul>
        {!visible.length && <p>{t(listState === "loading" ? "sidebar.loading" : listState === "error" ? "sidebar.unavailable" : needle ? "sidebar.noMatches" : "sidebar.noSessions")}</p>}
      </nav>
      <footer className="ec-rail-foot">
        {authorizeDevice}
        {manageDevices}
        <div className="ec-account">
          <AvatarImg className="ec-avatar" src={account.avatarUrl}>{account.username.slice(0, 2).toLocaleUpperCase()}</AvatarImg>
          <div className="ec-account-label">
            <b>{account.displayName || account.username}</b>
            <small>{t("sidebar.signedIn")}</small>
          </div>
        </div>
      </footer>
    </>
  );
}

function RecentRow({ entry, selected, listState, host, onSelect }: {
  entry: Recent; selected: boolean; listState: ListState; host?: string; onSelect: (entry: Recent) => void;
}) {
  const t = useT("codehub");
  const status = entry.live ? "live" : listState === "ready" ? "offline" : "unknown";
  return (
    <li>
      <button type="button" className="ec-rail-row ec-session-row" aria-current={selected || undefined} onClick={() => onSelect(entry)}>
        <span className="ec-dot" data-status={status} />
        <span className="ec-session-label">
          <span title={entry.title}>{entry.title || t("session.untitled")}</span>
          <small className="ec-session-meta" title={host}>
            <span>{t(`session.${status}`)}</span>
            {host && <span>{host}</span>}
            <span><When iso={entry.lastActivityAt} /></span>
          </small>
        </span>
      </button>
    </li>
  );
}
