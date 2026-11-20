import { Close, Content, Description, Overlay, Portal, Root, Title } from "@radix-ui/react-dialog";
import { useId, useRef, useState, type FormEvent } from "react";
import {
  createRemoteSession, useRemoteProjects, useRemoteRepositoryCatalog, type RemoteProject, type RemoteRepositoryCatalog,
} from "../../api/codeWorkspace.ts";
import { ApiError } from "../../api/http.ts";
import type { components } from "../../api/schema";
import { useT } from "../../i18n/useT.ts";
import { Icon } from "../../ui/Icon.tsx";

type Device = Pick<components["schemas"]["ErvisioCodeDevice"], "id" | "name">;
export type CreatedSession = Awaited<ReturnType<typeof createRemoteSession>> & { projectLabel: string };

type Repository = NonNullable<RemoteProject["repository"]>;
type ProjectSource = "local" | Repository["provider"];
type ProjectChoice = { source: ProjectSource; label: string; projects: RemoteProject[] };
const repositoryProviders = ["ervisiorepo", "github"] as const satisfies readonly Repository["provider"][];
const projectSources = ["local", ...repositoryProviders] as const;

function projectKey(project: RemoteProject) {
  return `${project.deviceId}:${project.relayId}:${project.projectId}:${project.repository?.checkout ?? "local"}`;
}

function projectChoices(projects: readonly RemoteProject[], deviceId: string, catalog?: RemoteRepositoryCatalog) {
  const choices = new Map<string, ProjectChoice>();
  const live = new Map<string, RemoteProject>();
  for (const project of projects) {
    if (project.deviceId !== deviceId) continue;
    const key = projectKey(project);
    if (project.repository) live.set(key, project);
    else choices.set(key, { source: "local", label: project.label, projects: [project] });
  }
  if (!catalog) return choices;

  for (const provider of repositoryProviders) {
    for (const { name, checkouts } of catalog[provider].repositories) {
      const normalizedName = name.toLowerCase();
      const approved = checkouts.flatMap((checkout) => {
        const current = live.get(projectKey(checkout));
        if (current?.repository?.provider !== provider || current.repository.name.toLowerCase() !== normalizedName) return [];
        return [current];
      });
      choices.set(`${provider}:${normalizedName}`, { source: provider, label: name, projects: approved });
    }
  }
  return choices;
}

function failureKey(error: unknown, isClone: boolean) {
  const uncertain = isClone ? "new.cloneUncertain" : "new.uncertain";
  if (!(error instanceof ApiError)) return uncertain;
  switch (error.status) {
    case 401:
    case 403: return "new.denied";
    case 404: return "new.offline";
    case 429: return "new.rateLimited";
    case 502:
      if (error.detail === "the host did not accept the Remote action") {
        return isClone ? "new.cloneFailed" : "new.failed";
      }
      return uncertain;
    default: return uncertain;
  }
}

function isUncertain(problem: ReturnType<typeof failureKey> | undefined) {
  return problem === "new.uncertain" || problem === "new.cloneUncertain";
}

function CatalogStatus({ catalog, source, fetching, failed, locked, onRefresh }: {
  catalog?: RemoteRepositoryCatalog;
  source: ProjectSource | "";
  fetching: boolean;
  failed: boolean;
  locked: boolean;
  onRefresh: () => void;
}) {
  const t = useT("codehub");
  const providers = repositoryProviders.filter((provider) => !source || provider === source);
  return (
    <div className="ec-new-catalog">
      <div role="status">
        {fetching ? <p>{t("new.catalog.loading")}</p>
          : failed ? <p>{t("new.catalog.error")}</p>
          : catalog && providers.map((provider) => {
            const { status, repositories } = catalog[provider];
            const message = status === "ready" && !repositories.length ? "empty" : status;
            return <p key={provider}>{t("new.catalog.status", {
              provider: t(`new.source.${provider}`), status: t(`new.catalog.${message}`),
            })}</p>;
          })}
      </div>
      <button type="button" className="ec-retry" disabled={locked || fetching} onClick={onRefresh}>
        {t("new.catalog.refresh")}
      </button>
    </div>
  );
}

export function NewCodeSession({ accountId, hubAccountId, devices, onClose, onCreated, onRefreshSessions, restoreFocus }: {
  accountId: string;
  hubAccountId?: string;
  devices: readonly Device[];
  onClose: () => void;
  onCreated: (session: CreatedSession) => void;
  onRefreshSessions: () => void;
  restoreFocus: () => void;
}) {
  const t = useT("codehub");
  const common = useT("common");
  const projects = useRemoteProjects(accountId, hubAccountId);
  const catalogQuery = useRemoteRepositoryCatalog(accountId, hubAccountId);
  const catalog = catalogQuery.isSuccess ? catalogQuery.data : undefined;
  const [deviceId, setDeviceId] = useState("");
  const [source, setSource] = useState<ProjectSource | "">("");
  const [selection, setSelection] = useState<{ choice: string; project: string }>();
  const [pending, setPending] = useState<"clone" | "create">();
  const [problem, setProblem] = useState<ReturnType<typeof failureKey>>();
  const inFlight = useRef(false);
  const checkoutHint = useId();
  const options = projects.isSuccess ? projects.data : [];
  const hosts = new Map(options.map((project) => [
    project.deviceId, project.deviceName || devices.find((device) => device.id === project.deviceId)?.name,
  ]));
  const choices = projectChoices(options, deviceId, catalog);
  const visibleGroups = projectSources.filter((provider) => !source || provider === source).map((provider) => ({
    provider, entries: Array.from(choices).filter(([, choice]) => choice.source === provider),
  })).filter(({ entries }) => entries.length > 0);
  const chosen = selection && choices.get(selection.choice);
  const choice = chosen?.projects.length && (!source || chosen.source === source) ? chosen : undefined;
  const selected = choice?.projects.find((project) => projectKey(project) === selection?.project);
  const separate = choice?.projects.filter((project) => project.repository?.checkout === "separate") ?? [];
  const existing = choice?.projects.filter((project) => project.repository?.checkout === "existing") ?? [];
  const checkout = selected?.repository?.checkout;
  const locked = Boolean(pending) || isUncertain(problem);
  const canSubmit = !locked && (!checkout || !catalogQuery.isFetching);

  function refreshCatalog() {
    void projects.refetch({ cancelRefetch: false });
    void catalogQuery.refetch();
  }

  function pickProject(key: string) {
    const picked = choices.get(key);
    const [initial, another] = picked?.projects.filter((project) => !project.repository || project.repository.checkout === "separate") ?? [];
    setSelection(picked?.projects.length ? { choice: key, project: initial && !another ? projectKey(initial) : "" } : undefined);
    setProblem(undefined);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selected || !choice || !canSubmit || inFlight.current) return;
    inFlight.current = true;
    setPending(checkout === "separate" ? "clone" : "create");
    setProblem(undefined);
    try {
      const session = await createRemoteSession(selected, hubAccountId);
      onCreated({ ...session, projectLabel: choice.label });
    } catch (error) {
      const key = failureKey(error, checkout === "separate");
      setProblem(key);
      if (isUncertain(key) || key === "new.offline") onRefreshSessions();
      if (key === "new.offline") {
        setSelection(undefined);
        void projects.refetch();
      }
    } finally {
      inFlight.current = false;
      setPending(undefined);
    }
  }

  function checkoutOptions(projects: RemoteProject[], mode: Repository["checkout"]) {
    return projects.map((project) => {
      const modeLabel = t(`new.${mode}`);
      const label = projects.length > 1
        ? t("new.namedCheckout", { mode: modeLabel, label: project.label })
        : modeLabel;
      return <option key={projectKey(project)} value={projectKey(project)}>{label}</option>;
    });
  }

  return (
    <Root open onOpenChange={(open) => { if (!open && !inFlight.current) onClose(); }}>
      <Portal>
        <Overlay className="ec-drawer-overlay ec-new-overlay" />
        <Content className="ec-new" onCloseAutoFocus={(event) => {
          event.preventDefault();
          restoreFocus();
        }}>
          <div className="ec-new-head">
            <Title>{t("new.title")}</Title>
            <Close className="ec-icon-button" aria-label={t("new.close")} disabled={Boolean(pending)}><Icon name="x" /></Close>
          </div>
          <Description>{t("new.description")}</Description>
          <form className="ec-new-form" onSubmit={(event) => void submit(event)}>
            <label>
              {t("new.pc")}
              <select name="pc" value={hosts.has(deviceId) ? deviceId : ""} disabled={!hosts.size || locked} onChange={(event) => {
                setDeviceId(event.target.value);
                setSource("");
                setSelection(undefined);
                setProblem(undefined);
              }}>
                <option value="">{t("new.selectPC")}</option>
                {Array.from(hosts, ([id, label]) => <option key={id} value={id}>{label || t("new.unnamedPC", { id: id.slice(0, 8) })}</option>)}
              </select>
            </label>
            <label>
              {t("new.source")}
              <select name="source" value={source} disabled={locked} onChange={(event) => {
                setSource(projectSources.find((provider) => provider === event.target.value) ?? "");
                setSelection(undefined);
                setProblem(undefined);
              }}>
                <option value="">{t("new.source.all")}</option>
                {projectSources.map((provider) => <option key={provider} value={provider}>{t(`new.source.${provider}`)}</option>)}
              </select>
            </label>
            {source !== "local" && <CatalogStatus catalog={catalog} source={source} fetching={catalogQuery.isFetching}
              failed={catalogQuery.isError} locked={locked} onRefresh={refreshCatalog} />}
            <label>
              {t("new.project")}
              <select name="project" value={choice ? selection?.choice : ""} disabled={!visibleGroups.length || locked} onChange={(event) => pickProject(event.target.value)}>
                <option value="">{t("new.selectProject")}</option>
                {visibleGroups.map(({ provider, entries }) => (
                  <optgroup key={provider} label={t(`new.source.${provider}`)}>
                    {entries.map(([key, entry]) => (
                      <option key={key} value={key} disabled={!entry.projects.length}>
                        {hosts.has(deviceId) && !entry.projects.length
                          ? t("new.notApproved", { name: entry.label }) : entry.label}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </label>
            {Boolean(choice && choice.source !== "local") && <>
              <label>
                {t("new.checkout")}
                <select name="checkout" aria-describedby={checkoutHint} value={selected ? projectKey(selected) : ""} disabled={locked} onChange={(event) => {
                  if (!selection) return;
                  setSelection({ choice: selection.choice, project: event.target.value });
                  setProblem(undefined);
                }}>
                  <option value="">{t("new.selectCheckout")}</option>
                  {checkoutOptions(separate, "separate")}
                  {checkoutOptions(existing, "existing")}
                </select>
              </label>
              <p id={checkoutHint}>{t(checkout === "separate" || !existing.length ? "new.separateHint" : "new.existingHint")}</p>
            </>}
            {projects.isPending && <p role="status">{common("state.loading")}</p>}
            {!pending && !problem && projects.isSuccess && !options.length && <p role="status">{t("new.noProjects")}</p>}
            {!locked && projects.isError && <div role="alert">
              <p>{t("new.listError")}</p>
              <button type="button" className="ec-retry" onClick={() => void projects.refetch()}>{common("action.retry")}</button>
            </div>}
            {problem && <p role="alert">{t(problem)}</p>}
            <p role="status" className={pending ? undefined : "ec-sr"}>{pending && t(pending === "clone" ? "new.cloning" : "new.creating")}</p>
            <div className="ec-new-actions">
              <button type="submit" className="ec-new-submit" disabled={!selected || !canSubmit}>
                {t(checkout === "separate" ? "new.clone" : checkout === "existing" ? "new.useExisting" : "new.create")}
              </button>
            </div>
          </form>
        </Content>
      </Portal>
    </Root>
  );
}
