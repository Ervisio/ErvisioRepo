import { useState, type FormEvent, type ReactNode } from "react";
import { ApiError } from "../../api/http.ts";
import {
  MAX_CLIENT_SECRETS,
  useCreateOAuthApp,
  useCreateOAuthSecret,
  useDeleteOAuthApp,
  useDeleteOAuthSecret,
  useOAuthApp,
  useOAuthApps,
  useUpdateOAuthApp,
  type CreatedOAuthAppSecret,
  type OAuthApp,
  type OAuthAppCreate,
  type OAuthAppsOwner,
} from "../../api/oauth.ts";
import { useT } from "../../i18n/useT.ts";
import { Button, ConfirmDialog, DangerRow, DangerZone, EmptyState, Input, SettingRow, SettingsSection, Toggle } from "../../ui/index.ts";
import { Async, formatDate, Secret, useNotify } from "../account/common.tsx";
import { useProblemText } from "../auth/useProblemText.ts";
import { AppLogo } from "./common.tsx";

/** Where a link inside the OAuth app pages goes: the list, the registration form or one app. */
export type AppTarget = { kind: "list" } | { kind: "new" } | { kind: "app"; clientId: string };
/** The routes differ between account and organization settings, so the caller supplies links. */
export type AppsNav = {
  link: (to: AppTarget, props: { className?: string; children: ReactNode }) => ReactNode;
  go: (to: AppTarget) => void;
};

const MAX_CALLBACKS = 10;

/** OAuth apps of an account or organization: the list, the registration form (app = "new") or one app. */
export function OAuthAppsSection({ owner, app, nav, canRegister }: { owner: OAuthAppsOwner; app?: string; nav: AppsNav; canRegister: boolean }) {
  if (app === "new" && canRegister) return <CreateApp owner={owner} nav={nav} />;
  if (app && app !== "new") return <AppPage key={app} clientId={app} nav={nav} />;
  return <AppList owner={owner} nav={nav} canRegister={canRegister} />;
}

function AppList({ owner, nav, canRegister }: { owner: OAuthAppsOwner; nav: AppsNav; canRegister: boolean }) {
  const t = useT("oauth");
  const apps = useOAuthApps(owner);
  return (
    <SettingsSection
      title={t("apps.title")}
      description={canRegister ? t("apps.hint") : t("apps.notAllowed")}
      actions={canRegister ? nav.link({ kind: "new" }, { className: "ui-btn ui-btn-secondary", children: t("apps.new") }) : undefined}
    >
      <Async query={apps} empty={t("apps.empty")}>
        {(rows) => (
          <div className="st-list">
            {rows.map((a) => (
              <div key={a.clientId} className="st-item">
                <AppLogo name={a.name} url={a.logoUrl} />
                <div className="st-item-m">
                  {nav.link({ kind: "app", clientId: a.clientId }, { className: "oa-name", children: a.name })}
                  <div className="ui-note"><span className="ui-mono">{a.clientId}</span></div>
                </div>
                <span className="ui-note">{t(a.users === 1 ? "apps.users.one" : "apps.users.other", { count: a.users })}</span>
              </div>
            ))}
          </div>
        )}
      </Async>
    </SettingsSection>
  );
}

type FormValue = { name: string; description: string; homepageUrl: string; logoUrl: string; callbackUrls: string[]; deviceFlow: boolean };

const toForm = (a?: OAuthApp): FormValue => ({
  name: a?.name ?? "",
  description: a?.description ?? "",
  homepageUrl: a?.homepageUrl ?? "",
  logoUrl: a?.logoUrl ?? "",
  callbackUrls: a?.callbackUrls.length ? a.callbackUrls : [""],
  deviceFlow: a?.deviceFlow ?? false,
});

const toBody = (v: FormValue): OAuthAppCreate => ({
  name: v.name.trim(),
  description: v.description.trim(),
  homepageUrl: v.homepageUrl.trim(),
  logoUrl: v.logoUrl.trim(),
  callbackUrls: v.callbackUrls.map((u) => u.trim()).filter(Boolean),
  deviceFlow: v.deviceFlow,
});

/** Problem detail plus the per-field messages of a 422 answer. */
function useFormError() {
  const problem = useProblemText();
  return (e: unknown): string[] => {
    const fields = e instanceof ApiError && e.errors ? e.errors.map((x) => x.msg) : [];
    return [problem(e), ...fields];
  };
}

/** Registration and edit form: stacked fields grouped by titled sections, primary button at the end. */
function AppForm({ initial, title, description, submitLabel, pending, errors, onSubmit }: {
  initial: FormValue;
  title: string;
  description?: ReactNode;
  submitLabel: string;
  pending: boolean;
  errors: string[];
  onSubmit: (v: FormValue) => void;
}) {
  const t = useT("oauth");
  const [v, setV] = useState(initial);
  const set = <K extends keyof FormValue>(k: K, x: FormValue[K]) => setV((cur) => ({ ...cur, [k]: x }));
  const setUrl = (i: number, x: string) => set("callbackUrls", v.callbackUrls.map((u, j) => (j === i ? x : u)));

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(v);
  }

  return (
    <form className="oa-form" onSubmit={submit}>
      <SettingsSection title={title} description={description}>
        <div className="st-card">
          <SettingRow label={t("form.name")}>
            <Input label={t("form.name")} hideLabel required maxLength={80} value={v.name} onChange={(e) => set("name", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("form.homepage")} description={t("form.homepageHint")}>
            <Input label={t("form.homepage")} hideLabel required type="url" placeholder="https://" value={v.homepageUrl} onChange={(e) => set("homepageUrl", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("form.description")} description={t("form.descriptionHint")}>
            <textarea aria-label={t("form.description")} className="ui-input acc-area" rows={3} maxLength={400} value={v.description} onChange={(e) => set("description", e.target.value)} />
          </SettingRow>
          <SettingRow label={t("form.logo")} description={t("form.logoHint")}>
            <Input label={t("form.logo")} hideLabel type="url" placeholder="https://" value={v.logoUrl} onChange={(e) => set("logoUrl", e.target.value)} />
          </SettingRow>
        </div>
      </SettingsSection>
      <SettingsSection title={t("form.callbacks")} description={t("form.callbacksHint")}>
        <div className="st-card">
          {v.callbackUrls.map((u, i) => (
            <div key={i} className="st-add">
              <Input
                label={t("form.callbackN", { n: i + 1 })}
                hideLabel
                mono
                required={i === 0}
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
        </div>
      </SettingsSection>
      <SettingsSection title={t("form.deviceTitle")}>
        <div className="st-card">
          <SettingRow label={t("form.device")} description={t("form.deviceHint")}>
            <Toggle label={t("form.device")} checked={v.deviceFlow} onChange={(x) => set("deviceFlow", x)} />
          </SettingRow>
          {errors.length > 0 && (
            <div className="auth-error" role="alert">
              {errors.map((m, i) => <div key={i}>{m}</div>)}
            </div>
          )}
          <div className="acc-actions">
            <Button variant="primary" type="submit" disabled={pending}>{submitLabel}</Button>
          </div>
        </div>
      </SettingsSection>
    </form>
  );
}

function CreateApp({ owner, nav }: { owner: OAuthAppsOwner; nav: AppsNav }) {
  const t = useT("oauth");
  const notify = useNotify();
  const formError = useFormError();
  const create = useCreateOAuthApp(owner);
  const [errors, setErrors] = useState<string[]>([]);
  return (
    <AppForm
      initial={toForm()}
      title={t("new.title")}
      description={<>{nav.link({ kind: "list" }, { children: t("back") })}</>}
      submitLabel={t("new.submit")}
      pending={create.isPending}
      errors={errors}
      onSubmit={(v) => {
        setErrors([]);
        create.mutate(toBody(v), {
          onSuccess: (a) => {
            notify.ok(t("new.done"));
            nav.go({ kind: "app", clientId: a.clientId });
          },
          onError: (e) => setErrors(formError(e)),
        });
      }}
    />
  );
}

function AppPage({ clientId, nav }: { clientId: string; nav: AppsNav }) {
  const t = useT("oauth");
  const tc = useT("common");
  const app = useOAuthApp(clientId);
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

function AppDetail({ app, nav }: { app: OAuthApp; nav: AppsNav }) {
  const t = useT("oauth");
  const notify = useNotify();
  const formError = useFormError();
  const update = useUpdateOAuthApp(app.clientId);
  const del = useDeleteOAuthApp(app.clientId);
  const [errors, setErrors] = useState<string[]>([]);
  return (
    <>
      <SettingsSection title={app.name} description={nav.link({ kind: "list" }, { children: t("back") })}>
        <div className="oa-head">
          <AppLogo name={app.name} url={app.logoUrl} size="lg" />
          <p className="ui-note">
            {t(app.users === 1 ? "apps.users.one" : "apps.users.other", { count: app.users })} · {t("app.created", { date: formatDate(app.createdAt) })}
          </p>
        </div>
        <Secret label={t("app.clientId")} value={app.clientId} />
      </SettingsSection>
      <Secrets app={app} />
      <AppForm
        key={app.updatedAt}
        initial={toForm(app)}
        title={t("edit.title")}
        submitLabel={t("edit.submit")}
        pending={update.isPending}
        errors={errors}
        onSubmit={(v) => {
          setErrors([]);
          update.mutate(toBody(v), { onSuccess: () => notify.ok(t("edit.done")), onError: (e) => setErrors(formError(e)) });
        }}
      />
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
              typeToConfirm={app.name}
              pending={del.isPending}
              onConfirm={() => del.mutate(undefined, { onSuccess: () => { notify.ok(t("danger.deleted")); nav.go({ kind: "list" }); }, onError: notify.fail })}
            />
          }
        />
      </DangerZone>
    </>
  );
}

function Secrets({ app }: { app: OAuthApp }) {
  const t = useT("oauth");
  const notify = useNotify();
  const create = useCreateOAuthSecret(app.clientId);
  const del = useDeleteOAuthSecret(app.clientId);
  const [created, setCreated] = useState<CreatedOAuthAppSecret>();
  const secrets = app.secrets ?? [];
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
                  {s.createdBy ? t("secrets.addedBy", { user: s.createdBy, date: formatDate(s.createdAt) }) : t("secrets.added", { date: formatDate(s.createdAt) })}
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
