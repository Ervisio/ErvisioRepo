import { Link } from "@tanstack/react-router";
import { useState, type FormEvent, type ReactNode } from "react";
import {
  appPath,
  MAX_APP_KEYS,
  slugify,
  useApp,
  useAppCatalog,
  useAppInstallationsOf,
  useApps,
  useCreateApp,
  useCreateAppKey,
  useCreateAppSecret,
  useDeleteApp,
  useDeleteAppKey,
  useDeleteAppSecret,
  useUpdateApp,
  type App,
  type AppCreate,
  type AppPermissions,
  type AppsOwner,
  type AppUpdate,
  type CreatedAppKey,
} from "../../api/apps.ts";
import { ApiError } from "../../api/http.ts";
import { MAX_CLIENT_SECRETS, type CreatedOAuthAppSecret } from "../../api/oauth.ts";
import { useT } from "../../i18n/useT.ts";
import {
  Badge,
  Button,
  ConfirmDialog,
  DangerRow,
  DangerZone,
  EmptyState,
  Input,
  SettingRow,
  SettingsSection,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Toggle,
} from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "../account/common.tsx";
import { useProblemText } from "../auth/useProblemText.ts";
import { Gate } from "../code/common.tsx";
import { AppLogo } from "../oauth/common.tsx";
import { Deliveries } from "../settings/Webhooks.tsx";
import { EventChips, PermissionMatrix } from "./common.tsx";

/** Where a link inside the App pages goes: the list, the registration form or one App. */
export type AppTarget = { kind: "list" } | { kind: "new" } | { kind: "app"; slug: string };
/** Account and organization settings use different routes, so the caller supplies links. */
export type AppsNav = {
  link: (to: AppTarget, props: { className?: string; children: ReactNode }) => ReactNode;
  go: (to: AppTarget) => void;
};

const MAX_CALLBACKS = 10;

/** Link to the install page of an App. */
export function InstallLink({ slug, className = "ui-btn ui-btn-secondary", children }: { slug: string; className?: string; children: ReactNode }) {
  return <Link to="/apps/$slug/installations/new" params={{ slug }} className={className}>{children}</Link>;
}

/** Apps of an account or organization: the list, the registration form (app = "new") or one App. */
export function AppsSection({ owner, app, nav, canRegister }: { owner: AppsOwner; app?: string; nav: AppsNav; canRegister: boolean }) {
  if (app === "new" && canRegister) return <CreateApp owner={owner} nav={nav} />;
  if (app && app !== "new") return <AppPage key={app} slug={app} nav={nav} />;
  return <AppList owner={owner} nav={nav} canRegister={canRegister} />;
}

function AppList({ owner, nav, canRegister }: { owner: AppsOwner; nav: AppsNav; canRegister: boolean }) {
  const t = useT("apps");
  const apps = useApps(owner);
  return (
    <SettingsSection
      title={t("list.title")}
      description={canRegister ? t("list.hint") : t("list.notAllowed")}
      actions={canRegister ? nav.link({ kind: "new" }, { className: "ui-btn ui-btn-secondary", children: t("list.new") }) : undefined}
    >
      <Async query={apps} empty={t("list.empty")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((a) => (
              <div key={a.slug} className="st-item">
                <AppLogo name={a.name} url={a.logoUrl} />
                <div className="st-item-m">
                  {nav.link({ kind: "app", slug: a.slug }, { className: "oa-name", children: a.name })}{" "}
                  {!a.public && <Badge>{t("visibility.private")}</Badge>}
                  <div className="ui-note"><span className="ui-mono">{a.botLogin}</span> · {t(a.installations === 1 ? "installs.one" : "installs.other", { count: a.installations })}</div>
                </div>
                <InstallLink slug={a.slug} className="ui-btn ui-btn-secondary ui-btn-sm">{t("install")}</InstallLink>
              </div>
            ))}
          </div>
        )}
      </Async>
    </SettingsSection>
  );
}

type FormValue = {
  name: string;
  slug: string;
  description: string;
  homepageUrl: string;
  logoUrl: string;
  public: boolean;
  setupUrl: string;
  callbackUrls: string[];
  webhookUrl: string;
  webhookSecret: string;
  removeSecret: boolean;
  webhookActive: boolean;
  permissions: AppPermissions;
  events: string[];
};

const toForm = (a?: App): FormValue => ({
  name: a?.name ?? "",
  slug: a?.slug ?? "",
  description: a?.description ?? "",
  homepageUrl: a?.homepageUrl ?? "",
  logoUrl: a?.logoUrl ?? "",
  public: a?.public ?? false,
  setupUrl: a?.setupUrl ?? "",
  callbackUrls: a?.callbackUrls?.length ? a.callbackUrls : [""],
  webhookUrl: a?.webhook?.url ?? "",
  webhookSecret: "",
  removeSecret: false,
  webhookActive: a?.webhook?.active ?? true,
  permissions: a?.permissions ?? { metadata: "read" },
  events: a?.events ?? [],
});

const general = (v: FormValue) => ({
  name: v.name.trim(),
  description: v.description.trim(),
  homepageUrl: v.homepageUrl.trim(),
  logoUrl: v.logoUrl.trim(),
  public: v.public,
  setupUrl: v.setupUrl.trim(),
  callbackUrls: v.callbackUrls.map((u) => u.trim()).filter(Boolean),
  webhookUrl: v.webhookUrl.trim(),
  webhookActive: v.webhookActive,
});

const createBody = (v: FormValue): AppCreate => ({
  ...general(v),
  ...(v.slug.trim() ? { slug: v.slug.trim() } : {}),
  ...(v.webhookSecret ? { webhookSecret: v.webhookSecret } : {}),
  permissions: { ...v.permissions, metadata: "read" },
  events: v.events,
});

// An absent webhookSecret keeps the stored one, "" removes it.
const generalBody = (v: FormValue): AppUpdate => ({
  ...general(v),
  ...(v.removeSecret ? { webhookSecret: "" } : v.webhookSecret ? { webhookSecret: v.webhookSecret } : {}),
});

/** Problem detail plus the per-field messages of a 422 answer. */
function useFormError() {
  const problem = useProblemText();
  return (e: unknown): string[] => {
    const fields = e instanceof ApiError && e.errors ? e.errors.map((x) => x.msg) : [];
    return [problem(e), ...fields];
  };
}

function Errors({ errors }: { errors: string[] }) {
  if (errors.length === 0) return null;
  return (
    <div className="auth-error" role="alert">
      {errors.map((m, i) => <div key={i}>{m}</div>)}
    </div>
  );
}

/**
 * Registration and edit form. "create" shows every part, "general" everything but permissions,
 * "permissions" only the matrix and the events.
 */
function AppForm({ mode, app, title, description, submitLabel, pending, errors, onSubmit }: {
  mode: "create" | "general" | "permissions";
  app?: App;
  title: string;
  description?: ReactNode;
  submitLabel: string;
  pending: boolean;
  errors: string[];
  onSubmit: (v: FormValue) => void;
}) {
  const t = useT("apps");
  const catalog = useAppCatalog();
  const [v, setV] = useState(() => toForm(app));
  const set = <K extends keyof FormValue>(k: K, x: FormValue[K]) => setV((cur) => ({ ...cur, [k]: x }));
  const setUrl = (i: number, x: string) => set("callbackUrls", v.callbackUrls.map((u, j) => (j === i ? x : u)));
  const slug = mode === "create" ? v.slug.trim() || slugify(v.name) : app?.slug ?? "";
  const hasSecret = app?.webhook?.hasSecret ?? false;

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(v);
  }

  const submitRow = (
    <>
      <Errors errors={errors} />
      <div className="acc-actions">
        <Button variant="primary" type="submit" disabled={pending}>{submitLabel}</Button>
      </div>
    </>
  );

  const perms = (
    <>
      <SettingsSection title={t("perm.title")} description={mode === "permissions" ? t("perm.editHint") : t("perm.hint")}>
        <Gate query={catalog}>
          {(c) => (
            <PermissionMatrix catalog={c} perms={v.permissions} events={v.events} onChange={(p, ev) => setV((cur) => ({ ...cur, permissions: p, events: ev }))} />
          )}
        </Gate>
      </SettingsSection>
      <SettingsSection title={t("events.title")} description={t("events.hint")}>
        <Gate query={catalog}>
          {(c) => <EventChips catalog={c} perms={v.permissions} events={v.events} onChange={(ev) => set("events", ev)} />}
        </Gate>
      </SettingsSection>
    </>
  );

  if (mode === "permissions") {
    return (
      <form className="oa-form" onSubmit={submit}>
        {perms}
        <div className="st-card">{submitRow}</div>
      </form>
    );
  }

  return (
    <form className="oa-form" onSubmit={submit}>
      <SettingsSection title={title} description={description}>
        <div className="st-card">
          <SettingRow label={t("form.name")}>
            <Input label={t("form.name")} hideLabel required maxLength={100} value={v.name} onChange={(e) => set("name", e.target.value)} />
          </SettingRow>
          {mode === "create" && (
            <SettingRow label={t("form.slug")} description={t("form.slugHint", { bot: slug ? `${slug}[bot]` : "…[bot]" })}>
              <Input label={t("form.slug")} hideLabel mono maxLength={34} placeholder={slugify(v.name)} value={v.slug} onChange={(e) => set("slug", e.target.value)} />
            </SettingRow>
          )}
          <SettingRow label={t("form.description")} description={t("form.descriptionHint")}>
            <textarea aria-label={t("form.description")} className="ui-input acc-area" rows={3} maxLength={1000} value={v.description} onChange={(e) => set("description", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("form.homepage")} description={t("form.homepageHint")}>
            <Input label={t("form.homepage")} hideLabel required type="url" placeholder="https://" value={v.homepageUrl} onChange={(e) => set("homepageUrl", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("form.logo")} description={t("form.logoHint")}>
            <Input label={t("form.logo")} hideLabel type="url" placeholder="https://" value={v.logoUrl} onChange={(e) => set("logoUrl", e.target.value)} />
          </SettingRow>
        </div>
      </SettingsSection>
      <SettingsSection title={t("form.userAuth")} description={t("form.userAuthHint")}>
        <div className="st-card">
          {v.callbackUrls.map((u, i) => (
            <div key={i} className="st-add">
              <Input
                label={t("form.callbackN", { n: i + 1 })}
                hideLabel
                mono
                type="url"
                placeholder="https://example.com/callback"
                value={u}
                onChange={(e) => setUrl(i, e.target.value)}
              />
              <Button
                variant="ghost"
                icon="trash"
                aria-label={t("form.removeCallback", { n: i + 1 })}
                disabled={v.callbackUrls.length === 1}
                onClick={() => set("callbackUrls", v.callbackUrls.filter((_, j) => j !== i))}
              />
            </div>
          ))}
          <div>
            <Button size="sm" icon="plus" disabled={v.callbackUrls.length >= MAX_CALLBACKS} onClick={() => set("callbackUrls", [...v.callbackUrls, ""])}>
              {t("form.addCallback")}
            </Button>
          </div>
          <SettingRow label={t("form.setup")} description={t("form.setupHint")}>
            <Input label={t("form.setup")} hideLabel type="url" placeholder="https://" value={v.setupUrl} onChange={(e) => set("setupUrl", e.target.value)} />
          </SettingRow>
        </div>
      </SettingsSection>
      <SettingsSection title={t("webhook.title")} description={t("webhook.hint")}>
        <div className="st-card">
          <SettingRow label={t("webhook.active")} description={t("webhook.activeHint")}>
            <Toggle label={t("webhook.active")} checked={v.webhookActive} onChange={(x) => set("webhookActive", x)} />
          </SettingRow>
          <SettingRow label={t("webhook.url")} description={t("webhook.urlHint")}>
            <Input label={t("webhook.url")} hideLabel mono type="url" placeholder="https://example.com/hook" value={v.webhookUrl} onChange={(e) => set("webhookUrl", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("webhook.secret")} description={hasSecret ? t("webhook.secretKeep") : t("webhook.secretHint")}>
            <Input
              label={t("webhook.secret")}
              hideLabel
              type="password"
              autoComplete="new-password"
              maxLength={256}
              disabled={v.removeSecret}
              value={v.webhookSecret}
              onChange={(e) => set("webhookSecret", e.target.value)}
            />
            {hasSecret && (
              <label className="acc-check ap-plain">
                <input type="checkbox" checked={v.removeSecret} onChange={(e) => setV((cur) => ({ ...cur, removeSecret: e.target.checked, webhookSecret: "" }))} />
                <span>{t("webhook.removeSecret")}</span>
              </label>
            )}
          </SettingRow>
        </div>
      </SettingsSection>
      {mode === "create" && perms}
      <SettingsSection title={t("visibility.title")}>
        <div className="st-card">
          <div className="ap-repos">
            <label className="ap-radio">
              <input type="radio" name="app-public" checked={!v.public} onChange={() => set("public", false)} />
              <span><b>{t("visibility.private")}</b><span className="ui-note">{t("visibility.privateHint")}</span></span>
            </label>
            <label className="ap-radio">
              <input type="radio" name="app-public" checked={v.public} onChange={() => set("public", true)} />
              <span><b>{t("visibility.public")}</b><span className="ui-note">{t("visibility.publicHint")}</span></span>
            </label>
          </div>
          {submitRow}
        </div>
      </SettingsSection>
    </form>
  );
}

function CreateApp({ owner, nav }: { owner: AppsOwner; nav: AppsNav }) {
  const t = useT("apps");
  const notify = useNotify();
  const formError = useFormError();
  const create = useCreateApp(owner);
  const [errors, setErrors] = useState<string[]>([]);
  return (
    <AppForm
      mode="create"
      title={t("new.title")}
      description={<>{t("new.hint")} {nav.link({ kind: "list" }, { children: t("back") })}</>}
      submitLabel={t("new.submit")}
      pending={create.isPending}
      errors={errors}
      onSubmit={(v) => {
        setErrors([]);
        create.mutate(createBody(v), {
          onSuccess: (a) => {
            notify.ok(t("new.done"));
            nav.go({ kind: "app", slug: a.slug });
          },
          onError: (e) => setErrors(formError(e)),
        });
      }}
    />
  );
}

function AppPage({ slug, nav }: { slug: string; nav: AppsNav }) {
  const t = useT("apps");
  const tc = useT("common");
  const app = useApp(slug);
  if (app.isPending) return <p role="status" className="ui-note">{tc("state.loading")}</p>;
  if (app.isError) {
    return (
      <div role="alert">
        <EmptyState
          icon="alert"
          title={t("app.loadError")}
          description={app.error.message}
          action={nav.link({ kind: "list" }, { className: "ui-btn ui-btn-secondary", children: t("back") })}
        />
      </div>
    );
  }
  return <AppDetail app={app.data} nav={nav} />;
}

type AppTab = "general" | "permissions" | "keys" | "installations" | "deliveries" | "advanced";
const appTabs: AppTab[] = ["general", "permissions", "keys", "installations", "deliveries", "advanced"];

function AppDetail({ app, nav }: { app: App; nav: AppsNav }) {
  const t = useT("apps");
  const notify = useNotify();
  const formError = useFormError();
  const update = useUpdateApp(app.slug);
  const [tab, setTab] = useState<AppTab>("general");
  const [errors, setErrors] = useState<string[]>([]);
  const save = (body: AppUpdate) => {
    setErrors([]);
    update.mutate(body, { onSuccess: () => notify.ok(t("edit.done")), onError: (e) => setErrors(formError(e)) });
  };
  return (
    <>
      <SettingsSection
        title={app.name}
        description={nav.link({ kind: "list" }, { children: t("back") })}
        actions={<InstallLink slug={app.slug}>{t("installApp")}</InstallLink>}
      >
        <div className="oa-head">
          <AppLogo name={app.name} url={app.logoUrl} size="lg" />
          <div>
            <p className="ui-note">
              <span className="ui-mono">{app.botLogin}</span> · {app.public ? t("visibility.public") : t("visibility.private")} ·{" "}
              {t(app.installations === 1 ? "installs.one" : "installs.other", { count: app.installations })} · {t("app.created", { date: formatDate(app.createdAt) })}
            </p>
          </div>
        </div>
        <div className="ap-ids">
          <Secret label={t("app.id")} value={String(app.id)} />
          <Secret label={t("app.clientId")} value={app.clientId} />
        </div>
      </SettingsSection>
      {!app.manageable ? (
        <p className="ui-note">{t("app.readOnly")}</p>
      ) : (
        <Tabs value={tab} onValueChange={(x) => { setErrors([]); setTab(x as AppTab); }}>
          <TabsList aria-label={t("app.sections")}>
            {appTabs.map((x) => <TabsTrigger key={x} value={x}>{t(`tab.${x}`)}</TabsTrigger>)}
          </TabsList>
          <TabsContent value="general">
            <AppForm
              key={app.updatedAt}
              mode="general"
              app={app}
              title={t("edit.title")}
              submitLabel={t("edit.submit")}
              pending={update.isPending}
              errors={errors}
              onSubmit={(v) => save(generalBody(v))}
            />
          </TabsContent>
          <TabsContent value="permissions">
            <AppForm
              key={app.updatedAt}
              mode="permissions"
              app={app}
              title={t("perm.title")}
              submitLabel={t("perm.submit")}
              pending={update.isPending}
              errors={errors}
              onSubmit={(v) => save({ permissions: { ...v.permissions, metadata: "read" }, events: v.events })}
            />
          </TabsContent>
          <TabsContent value="keys">
            <div className="oa-form">
              <PrivateKeys app={app} />
              <ClientSecrets app={app} />
            </div>
          </TabsContent>
          <TabsContent value="installations"><Installations app={app} /></TabsContent>
          <TabsContent value="deliveries">
            <SettingsSection title={t("deliveries.title")} description={app.webhook?.url ? t("deliveries.hint", { url: app.webhook.url }) : t("deliveries.noHook")}>
              <Deliveries root={appPath(app.slug)} also={["app", app.slug]} />
            </SettingsSection>
          </TabsContent>
          <TabsContent value="advanced"><Danger app={app} nav={nav} /></TabsContent>
        </Tabs>
      )}
    </>
  );
}

/** Saves the PEM as <slug>.<date>.private-key.pem. */
function downloadPem(slug: string, pem: string) {
  const url = URL.createObjectURL(new Blob([pem], { type: "application/x-pem-file" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${slug}.${new Date().toISOString().slice(0, 10)}.private-key.pem`;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function PrivateKeys({ app }: { app: App }) {
  const t = useT("apps");
  const notify = useNotify();
  const create = useCreateAppKey(app.slug);
  const del = useDeleteAppKey(app.slug);
  const [created, setCreated] = useState<CreatedAppKey>();
  const keys = app.keys ?? [];
  const full = keys.length >= MAX_APP_KEYS;
  return (
    <SettingsSection
      title={t("keys.title")}
      description={full ? t("keys.full", { max: MAX_APP_KEYS }) : t("keys.hint")}
      actions={
        <Button
          disabled={create.isPending || full}
          onClick={() => create.mutate(undefined, {
            onSuccess: (k) => {
              setCreated(k);
              downloadPem(app.slug, k.privateKey);
              notify.ok(t("keys.created"));
            },
            onError: notify.fail,
          })}
        >
          {t("keys.new")}
        </Button>
      }
    >
      {created && (
        <div className="acc-block" role="status">
          <p className="ui-note">{t("keys.once")}</p>
          <Secret label={t("keys.newLabel", { fingerprint: created.key.fingerprint })} value={created.privateKey} multiline />
          <div className="acc-actions">
            <Button size="sm" icon="download" onClick={() => downloadPem(app.slug, created.privateKey)}>{t("keys.download")}</Button>
          </div>
        </div>
      )}
      {keys.length === 0 ? (
        <p className="ui-note">{t("keys.empty")}</p>
      ) : (
        <div className="st-list">
          {keys.map((k) => (
            <div key={k.id} className="st-item">
              <div className="st-item-m">
                <span className="ui-mono">{k.fingerprint}</span>
                <div className="ui-note">
                  {k.createdBy ? t("keys.addedBy", { user: k.createdBy, date: formatDate(k.createdAt) }) : t("keys.added", { date: formatDate(k.createdAt) })}
                  {" · "}
                  {k.lastUsedAt ? t("lastUsed", { date: formatDate(k.lastUsedAt) }) : t("neverUsed")}
                </div>
              </div>
              <ConfirmDialog
                trigger={<Button size="sm" variant="danger">{t("keys.delete")}</Button>}
                title={t("keys.deleteTitle")}
                description={t("keys.deleteHint", { fingerprint: k.fingerprint })}
                confirmLabel={t("keys.delete")}
                pending={del.isPending}
                onConfirm={() => del.mutate(k.id, {
                  onSuccess: () => {
                    if (created?.key.id === k.id) setCreated(undefined);
                    notify.ok(t("keys.deleted"));
                  },
                  onError: notify.fail,
                })}
              />
            </div>
          ))}
        </div>
      )}
    </SettingsSection>
  );
}

function ClientSecrets({ app }: { app: App }) {
  const t = useT("apps");
  const notify = useNotify();
  const create = useCreateAppSecret(app.slug, app.clientId);
  const del = useDeleteAppSecret(app.slug, app.clientId);
  const [created, setCreated] = useState<CreatedOAuthAppSecret>();
  const secrets = app.clientSecrets ?? [];
  const full = secrets.length >= MAX_CLIENT_SECRETS;
  return (
    <SettingsSection
      title={t("secrets.title")}
      description={full ? t("secrets.full", { max: MAX_CLIENT_SECRETS }) : t("secrets.hint")}
      actions={
        <Button
          disabled={create.isPending || full}
          onClick={() => create.mutate(undefined, { onSuccess: (s) => { setCreated(s); notify.ok(t("secrets.created")); }, onError: notify.fail })}
        >
          {t("secrets.new")}
        </Button>
      }
    >
      {created && (
        <div className="acc-block" role="status">
          <p className="ui-note">{t("secrets.once")}</p>
          <Secret label={t("secrets.newLabel")} value={created.clientSecret} />
        </div>
      )}
      {secrets.length === 0 ? (
        <p className="ui-note">{t("secrets.empty")}</p>
      ) : (
        <div className="st-list">
          {secrets.map((s) => (
            <div key={s.id} className="st-item">
              <div className="st-item-m">
                <span className="ui-mono">*****{s.hint}</span>
                <div className="ui-note">
                  {s.createdBy ? t("keys.addedBy", { user: s.createdBy, date: formatDate(s.createdAt) }) : t("keys.added", { date: formatDate(s.createdAt) })}
                  {" · "}
                  {s.lastUsedAt ? t("lastUsed", { date: formatDate(s.lastUsedAt) }) : t("neverUsed")}
                </div>
              </div>
              <ConfirmDialog
                trigger={<Button size="sm" variant="danger">{t("secrets.delete")}</Button>}
                title={t("secrets.deleteTitle")}
                description={t("secrets.deleteHint", { hint: s.hint })}
                confirmLabel={t("secrets.delete")}
                pending={del.isPending}
                onConfirm={() => del.mutate(s.id, {
                  onSuccess: () => {
                    if (created?.secret.id === s.id) setCreated(undefined);
                    notify.ok(t("secrets.deleted"));
                  },
                  onError: notify.fail,
                })}
              />
            </div>
          ))}
        </div>
      )}
    </SettingsSection>
  );
}

function Installations({ app }: { app: App }) {
  const t = useT("apps");
  const list = useAppInstallationsOf(app.slug);
  return (
    <SettingsSection title={t("installations.title")} description={t("installations.hint")}>
      <Async query={list} empty={t("installations.empty")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((i) => (
              <div key={i.id} className="st-item">
                <div className="st-item-m">
                  <b>{i.account.name}</b>{" "}
                  <span className="ui-note">{t(`account.${i.account.kind}`)}</span>
                  <div className="ui-note">
                    {i.repositorySelection === "all" ? t("repos.all") : t("repos.selected")} · {t("installations.since", { date: formatDate(i.createdAt) })}
                  </div>
                </div>
                <span className="st-item-a">
                  {i.suspendedAt && <Badge tone="warn">{t("installed.suspended")}</Badge>}
                  {i.pending && <Badge tone="info">{t("installed.pending")}</Badge>}
                </span>
              </div>
            ))}
          </div>
        )}
      </Async>
    </SettingsSection>
  );
}

function Danger({ app, nav }: { app: App; nav: AppsNav }) {
  const t = useT("apps");
  const notify = useNotify();
  const del = useDeleteApp(app.slug);
  return (
    <DangerZone title={t("danger.title")}>
      <DangerRow
        title={t("danger.delete")}
        description={t("danger.deleteHint")}
        action={
          <ConfirmDialog
            trigger={<Button variant="danger">{t("danger.deleteButton")}</Button>}
            title={t("danger.deleteTitle", { name: app.name })}
            description={t("danger.deleteHint")}
            confirmLabel={t("danger.deleteConfirm")}
            typeToConfirm={app.slug}
            pending={del.isPending}
            onConfirm={() => del.mutate(undefined, { onSuccess: () => { notify.ok(t("danger.deleted")); nav.go({ kind: "list" }); }, onError: notify.fail })}
          />
        }
      />
    </DangerZone>
  );
}
